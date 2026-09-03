// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// FixRSSI normalises a raw RSSI integer reading into the canonical "negative
// dBm" form. All rejected inputs return (0, false) so callers suppress the
// reading; the returned value is in dBm.
//
// The two encodings the bands straddle are the CCU's own, and both are
// negations of a single wire byte — which is why the boundaries sit at
// |128| (the byte 0x80) rather than anywhere else:
//
//   - BidCoS negates an UNSIGNED byte, so it publishes [-255, 0]:
//     ../OpenCCU-Base/src/rfd/RFDevice.cpp:502 `int rssi=-1*frame.GetByteData(13);`
//     with `unsigned char StructuredFrame::GetByteData(int)` at
//     ../OpenCCU-Base/src/libhsscomm/StructuredFrame.cpp:85; the same shape in
//     ../OpenCCU-Base/src/rfd/RFCommMessage.cpp:174 and in multimacd's
//     SerialFrame/HmLegacyFrameBidcos/HmLegacyFrameBidcosRxTelegram.cpp:38-40
//     and SerialFrame/LowLevelMacFrame/LowLevelMacFrameRxTelegram.cpp:52-54.
//   - HmIP negates a SIGNED byte, so it publishes [-127, +128]:
//     HMIPServer de.eq3.cbcs.server.core.vertx.IncomingHMIPFrameHandler#handleIncomingFrame.
//
// Bands, and what each rests on:
//
//	-128 < v <    0 →  v        already-correct dBm (BidCoS bytes 1..127)
//	   0 < v <  128 → -v        sign-flipped (HmIP bytes -1..-127)
//	-256 < v < -128 → -v - 256  BidCoS bytes 129..255, reinterpreted signed
//	 129 < v <  256 →  v - 256  no firmware stack produces this — see below
//
// Rejected, and why:
//
//   - ±128 is the raw byte 0x80, the CCU's own no-signal marker. HmIP tests
//     it explicitly before publishing RSSI_PEER (HMIPServer
//     de.eq3.cbcs.server.core.framehandling.HMIPApplicationHandler#handleStatusFrame,
//     `(getRssiValue() & 0xFF) != 128`) and publishes RSSI_DEVICE with no such
//     test, so the marker does reach us and has to be dropped here. That the
//     BidCoS sign of the same byte (-128) is a marker rather than a -128 dBm
//     reading is UNVERIFIED — BidCoS's own "never measured" marker is
//     INVALID_RSSI_VALUE = -0xffff (../OpenCCU-Base/src/rfd/BidcosFrame.h:214),
//     which the fall-through drops.
//   - 0 is load-bearing. RFDevice.cpp:505 `if(receiver && rssi)` suppresses it
//     for RSSI_DEVICE, but the two RSSI_PEER sites guard only against
//     INVALID_RSSI_VALUE (../OpenCCU-Base/src/rfd/BidcosInterfaceConcentrator.cpp:382
//     and :854), so a frame byte of 0 does arrive as RSSI_PEER = 0.
//
// The fourth band is UNVERIFIED and NOT PERFORMABLE from the CCU sources:
// neither stack can publish 130..255 (BidCoS tops out at 0, HmIP at 128), and
// the transformation `v - 256` — like band three's `-v - 256` — is a signed
// reinterpretation the firmware performs nowhere. Both rest on the Python
// port. What would settle them is a CUxD or Homegear parameter description
// for RSSI_DEVICE / RSSI_PEER; the shipped HomeMatic device XMLs cannot
// (e.g. ../OpenCCU-Base/src/devicetypes/rftypes/rf_wds_v1_1.xml:79-88 declares
// both as a bare `<logical type="integer"/>` with no min, max or
// special_value), so a runtime MIN/MAX read carries nothing here.
func FixRSSI(v int32) (int32, bool) {
	switch {
	case v > -128 && v < 0:
		return v, true
	case v > 0 && v < 128:
		return -v, true
	case v > -256 && v < -128:
		return -v - 256, true
	case v > 129 && v < 256:
		return v - 256, true
	}
	return 0, false
}

// IsRSSIParameter reports whether p is one of the two RSSI wire
// parameters that carry the encoding [FixRSSI] handles.
func IsRSSIParameter(p hmenum.Parameter) bool {
	return p == hmenum.ParameterRSSIDevice || p == hmenum.ParameterRSSIPeer
}

// SensorValue is every numeric-or-text payload the generic sensor
// layer supports. The typed [Sensor[T]] enforces one concrete shape
// per data point.
type SensorValue interface {
	~int32 | ~int64 | ~float64 | ~string
}

// Sensor is a read-only typed data point. T is the sensor's concrete
// Go type (int32, int64, float64, string). Values arrive via
// [DataPoint.OnEvent] only.
type Sensor[T SensorValue] struct {
	*DataPoint[T]
}

// NewSensor constructs a typed Sensor.
func NewSensor[T SensorValue](cfg Spec) *Sensor[T] {
	return &Sensor[T]{DataPoint: NewDataPoint[T](cfg)}
}

// NewFloatSensor is a convenience constructor for the common
// float-valued sensor.
func NewFloatSensor(cfg Spec) *Sensor[float64] { return NewSensor[float64](cfg) }

// NewIntegerSensor is a convenience constructor for int32 sensors.
func NewIntegerSensor(cfg Spec) *Sensor[int32] { return NewSensor[int32](cfg) }

// NewStringSensor is a convenience constructor for string sensors.
func NewStringSensor(cfg Spec) *Sensor[string] { return NewSensor[string](cfg) }

// normalizeRSSIWire coerces an arbitrary numeric wire value into the
// canonical dBm reading via [FixRSSI]. handled reports whether the data
// point is an RSSI parameter (so the caller must NOT fall through to the
// raw store); valid reports whether FixRSSI accepted the reading.
//
// The wire value may arrive as any numeric shape: int / int32 from the
// XML-RPC decode, but also int64 or — critically — float64 from the Rega
// `fetch_all_device_data` JSON seed, which decodes every number as
// float64 regardless of the descriptor's INTEGER type. Coercing through
// [toInt64] first means the 128/0x80 HmIP "no signal" sentinel is dropped
// on every ingestion path, not just the int/int32 ones. A non-numeric
// payload for an RSSI parameter cannot be a valid reading, so it is
// reported handled-but-invalid (dropped) rather than stored verbatim.
func (s *Sensor[T]) normalizeRSSIWire(v any) (fixed int32, handled, valid bool) { //nolint:nonamedreturns // documents the tri-state result
	if !IsRSSIParameter(s.Parameter()) {
		return 0, false, false
	}
	raw, ok := toInt64(v)
	if !ok || raw < math.MinInt32 || raw > math.MaxInt32 {
		return 0, true, false
	}
	f, valid := FixRSSI(int32(raw)) //nolint:gosec // bounds-checked above
	return f, true, valid
}

// OnEvent is the entry point for CCU-driven updates. For RSSI parameters
// (RSSI_DEVICE, RSSI_PEER) the value is normalised via [FixRSSI] before
// storage — invalid readings (the 127/128/129 "no signal" sentinels) are
// silently discarded so that previously stored values are preserved. All
// other parameter / type combinations delegate to [DataPoint.OnEvent]
// unchanged.
//
// This matches
// `set_state` path (model/generic/sensor.py:99–109).
func (s *Sensor[T]) OnEvent(v T) {
	if fixed, handled, valid := s.normalizeRSSIWire(any(v)); handled {
		if !valid {
			return // discard invalid reading, keep previous value
		}
		if normalized, ok := coerceWire[T](fixed); ok {
			s.DataPoint.OnEvent(normalized)
		}
		return
	}
	s.DataPoint.OnEvent(v)
}

// OnWireValue is the untyped-wire entry point. For RSSI parameters the
// raw wire number — int, int32, int64, or the float64 that the Rega
// `fetch_all_device_data` JSON seed always produces — is normalised via
// [FixRSSI] before being forwarded. An invalid reading (the HmIP
// 127/128/129 "no signal" sentinels) is intentionally discarded WITHOUT
// updating the stored value, but still reported as handled (returns
// true): it is a well-formed wire message the sensor chose to drop, not a
// type-coercion failure. Returning false here would make the callback
// handler log a misleading "coerce_failed" and fire a pointless getValue
// self-reload on every invalid RSSI push. Non-RSSI parameters are
// forwarded unchanged.
func (s *Sensor[T]) OnWireValue(v any) bool {
	if fixed, handled, valid := s.normalizeRSSIWire(v); handled {
		if !valid {
			// Invalid sentinel — handled by dropping it; no reload needed.
			return true
		}
		normalized, ok := coerceWire[T](fixed)
		if !ok {
			return false
		}
		s.DataPoint.OnEvent(normalized)
		return true
	}
	return s.DataPoint.OnWireValue(v)
}

// RestoreCachedValue applies a persisted values-cache snapshot. RSSI
// parameters are re-normalised through [FixRSSI] before restore, so a raw
// sentinel (e.g. 128) persisted by an older build — before the ingestion
// paths dropped it — is discarded on restore instead of resurfacing as a
// bogus reading after a daemon restart. Non-RSSI sensors delegate to
// [DataPoint.RestoreCachedValue] unchanged.
func (s *Sensor[T]) RestoreCachedValue(v any, lastSeen, lastChanged time.Time) bool {
	if fixed, handled, valid := s.normalizeRSSIWire(v); handled {
		if !valid {
			return false // drop sentinel; the values-cache GC pass reclaims the row
		}
		return s.DataPoint.RestoreCachedValue(fixed, lastSeen, lastChanged)
	}
	return s.DataPoint.RestoreCachedValue(v, lastSeen, lastChanged)
}

// TransformSensorValue resolves a raw wire value against the parameter
// descriptor's VALUE_LIST.
//
// if value_list and isinstance(value, int): return value_list[value] if 0 <=
// value < len(value_list) else value
//
// If the descriptor has no VALUE_LIST, or if v is not an integer type, v is
// returned unchanged. This lets callers call TransformSensorValue
// unconditionally and only receive label resolution when applicable.
func TransformSensorValue(desc hmproto.ParameterData, v any) any {
	if len(desc.ValueList) == 0 {
		return v
	}
	var idx int
	switch x := v.(type) {
	case int:
		idx = x
	case int32:
		idx = int(x)
	case int64:
		idx = int(x)
	default:
		return v
	}
	if idx < 0 || idx >= len(desc.ValueList) {
		return v
	}
	return desc.ValueList[idx]
}
