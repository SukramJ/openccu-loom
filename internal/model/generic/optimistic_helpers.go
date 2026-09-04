// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package-level helpers that support the optimistic-update logic in
// DataPoint[T]. These were previously bundled with the inline
// optimisticTracker[T] implementation; they are kept here so the
// generic package's public surface (RollbackReason, valuesClose,
// OptimisticDefaultTimeout) remains stable after the tracker was
// consolidated into internal/model/optimistic.

package generic

import (
	"math"
	"time"
)

// OptimisticDefaultTimeout is the rollback grace period applied when no
// per-data-point timeout was configured.
const OptimisticDefaultTimeout = 30 * time.Second

// RollbackReason identifies why an optimistic state was reverted.
// String values are stable so they can travel through events / logs
// to clients (REST, MQTT, audit) without further translation.
type RollbackReason string

const (
	// RollbackReasonTimeout — no CCU confirmation arrived within
	// the configured optimistic-update-timeout window.
	RollbackReasonTimeout RollbackReason = "timeout"
	// RollbackReasonSendError — the wire-level SetValue call failed
	// outright. The optimistic value is rolled back so the user-
	// visible state stays truthful.
	RollbackReasonSendError RollbackReason = "send_error"
	// RollbackReasonValueMismatch — a confirmation event arrived but
	// carried a different value (CCU rounded / clamped / refused).
	// Emitted as DEBUG only by the canonical reference; we keep it
	// for telemetry parity even though the rollback is logical only
	// (the CCU value is authoritative).
	RollbackReasonValueMismatch RollbackReason = "mismatch"
)

// valuesClose reports whether two values of type T are equivalent
// for confirmation purposes. Floats compare after rounding to two
// decimals — a fixed ±0.005 window; everything else uses Go's ==
// (which works because T is constrained to comparable).
//
// The fixed window is NOT the CCU's rule, and it is not a conservative
// approximation of it either. The CCU quantises every scaled FLOAT
// parameter to 1/factor on write and echoes back the round-tripped
// quantised value
// (../OpenCCU-Base/src/libhsscomm/HSSTypeConversionFloatInteger.cpp:56-66
// and :69-77, half away from zero), and the factor is per parameter:
// SET_TEMPERATURE is factor 2, i.e. a step of 0.5 °C
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2392-2401);
// ON_TIME and RAMP_TIME are factor 10; LEVEL on some dimmers is factor
// 200, i.e. a step of 0.005. So the window is far too tight for the
// coarse parameters — a written 20.3 comes back as 20.5 and reads as a
// mismatch — and, on the callers that use it to decide whether to send
// at all, too loose for the fine ones, because it swallows a step the
// device can actually execute.
//
// The right driver is that per-parameter step, and it is NOT PERFORMABLE
// at runtime: getParamsetDescription exports ID, OPERATIONS, FLAGS,
// TAB_ORDER, CONTROL, TYPE, MIN, MAX, DEFAULT, SPECIAL and UNIT and
// nothing about the physical scaling
// (../OpenCCU-Base/src/libhsscomm/HSSParameter.cpp:205-218,
// HSSLogicalTypeFloat.cpp:105-119, HSSLogicalType.cpp:53-61) on both the
// BidCos and the HmIP legacy surface. Deriving a step from MIN/MAX or
// from the unit string would be a guess. What would settle it is a
// per-parameter step table sourced from the device XMLs' `factor`
// attributes; until that exists the constant stays, unverified and
// deliberately unchanged rather than replaced by an invented one.
func valuesClose[T comparable](a, b T) bool {
	if af, aok := any(a).(float64); aok {
		if bf, bok := any(b).(float64); bok {
			return math.Abs(roundToTwoDecimals(af)-roundToTwoDecimals(bf)) < 1e-9
		}
	}
	if af, aok := any(a).(float32); aok {
		if bf, bok := any(b).(float32); bok {
			return math.Abs(roundToTwoDecimals(float64(af))-roundToTwoDecimals(float64(bf))) < 1e-9
		}
	}
	return a == b
}

// roundToTwoDecimals rounds v half away from zero to two decimal
// places. It is the quantisation [valuesClose] compares on; the
// authority for that window, and its limits, are documented there.
func roundToTwoDecimals(v float64) float64 {
	if v >= 0 {
		return math.Floor(v*100+0.5) / 100
	}
	return math.Ceil(v*100-0.5) / 100
}
