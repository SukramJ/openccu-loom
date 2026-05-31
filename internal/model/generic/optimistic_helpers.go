// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// for confirmation purposes. Floats compare with two-decimal
// tolerance (matching python's `round(v, 2)`); everything else
// uses Go's == (which works because T is constrained to
// comparable).
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

// roundToTwoDecimals rounds v half-up to two decimal places —
// Matches the precision
// vs. confirmed CCU values (`round(x, 2)`).
func roundToTwoDecimals(v float64) float64 {
	if v >= 0 {
		return math.Floor(v*100+0.5) / 100
	}
	return math.Ceil(v*100-0.5) / 100
}
