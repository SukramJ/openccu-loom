// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// rssiCfg builds a Config wired to an RSSI parameter so that the
// Sensor.OnEvent override exercises FixRSSI. ChannelAddress and
// ParamsetKey are kept minimal — the test only needs the parameter
// name for IsRSSIParameter.
func rssiCfg(p hmenum.Parameter) Spec {
	return Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
}

// TestFixRSSITable verifies all four encoding regions defined in
// FixRSSI. Mirrors the boundary documentation from sensor.go.
func TestFixRSSITable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input int32
		want  int32
		valid bool
	}{
		// Already-correct negative dBm (passthrough).
		{name: "already_negative_low", input: -100, want: -100, valid: true},
		{name: "already_negative_high", input: -1, want: -1, valid: true},
		// Sign-flipped positive (1 < v < 127 → -v).
		{name: "sign_flipped_low", input: 2, want: -2, valid: true},
		{name: "sign_flipped_high", input: 126, want: -126, valid: true},
		// Off-by-256 in negative range (-256 < v < -129 → -v - 256).
		{name: "off256_neg_low", input: -255, want: -1, valid: true},
		{name: "off256_neg_high", input: -130, want: -126, valid: true},
		// Off-by-256 in positive range (129 < v < 256 → v - 256).
		{name: "off256_pos_low", input: 130, want: -126, valid: true},
		{name: "off256_pos_high", input: 255, want: -1, valid: true},
		// Out-of-range inputs that FixRSSI must reject.
		{name: "zero", input: 0, want: 0, valid: false},
		{name: "boundary_127", input: 127, want: 0, valid: false},
		{name: "boundary_neg127", input: -127, want: 0, valid: false},
		{name: "boundary_129", input: 129, want: 0, valid: false},
		{name: "boundary_neg129", input: -129, want: 0, valid: false},
		{name: "boundary_256", input: 256, want: 0, valid: false},
		{name: "boundary_neg256", input: -256, want: 0, valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FixRSSI(tc.input)
			if ok != tc.valid {
				t.Fatalf("FixRSSI(%d) valid=%v want %v", tc.input, ok, tc.valid)
			}
			if ok && got != tc.want {
				t.Fatalf("FixRSSI(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// TestSensorRSSIOnEventAppliesFixRSSI verifies that Sensor[int32].OnEvent
// for RSSI_DEVICE / RSSI_PEER passes the wire value through FixRSSI
// before storing it. A sign-flipped raw value (+50) must be stored as
// -50; an out-of-range value (0) must be silently discarded (no-op).
func TestSensorRSSIOnEventAppliesFixRSSI(t *testing.T) {
	t.Parallel()

	for _, param := range []hmenum.Parameter{hmenum.ParameterRSSIDevice, hmenum.ParameterRSSIPeer} {
		t.Run(string(param), func(t *testing.T) {
			t.Parallel()
			s := NewIntegerSensor(rssiCfg(param))

			// Raw +50 → sign-flipped to -50.
			s.OnEvent(50)
			v, ok := s.Value()
			if !ok || v != -50 {
				t.Fatalf("OnEvent(50): got (%d, %v), want (-50, true)", v, ok)
			}

			// Out-of-range 0 → discard; value must remain -50.
			s.OnEvent(0)
			v, ok = s.Value()
			if !ok || v != -50 {
				t.Fatalf("OnEvent(0) must be a no-op: got (%d, %v), want (-50, true)", v, ok)
			}

			// Already-correct -80 → stored as-is.
			s.OnEvent(-80)
			v, ok = s.Value()
			if !ok || v != -80 {
				t.Fatalf("OnEvent(-80): got (%d, %v), want (-80, true)", v, ok)
			}
		})
	}
}

// TestSensorNonRSSIOnEventPassesThrough verifies that Sensor.OnEvent
// does NOT apply FixRSSI for non-RSSI parameters, even when the
// value happens to be in a range that FixRSSI would transform.
func TestSensorNonRSSIOnEventPassesThrough(t *testing.T) {
	t.Parallel()
	// ParameterTemperature is not an RSSI parameter.
	cfg := rssiCfg(hmenum.ParameterTemperature)
	s := NewIntegerSensor(cfg)

	// Value +50 for a non-RSSI param must be stored verbatim.
	s.OnEvent(50)
	v, ok := s.Value()
	if !ok || v != 50 {
		t.Fatalf("non-RSSI OnEvent(50): got (%d, %v), want (50, true)", v, ok)
	}
}

// TestSensorOnWireValue_RSSI_ValidInt verifies that OnWireValue with a
// valid int RSSI value returns true for RSSI_DEVICE sensors.
func TestSensorOnWireValue_RSSI_ValidInt(t *testing.T) {
	t.Parallel()
	s := NewIntegerSensor(baseCfg(hmenum.ParameterRSSIDevice, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent))
	// RSSI -65 is a valid value.
	ok := s.OnWireValue(int(-65))
	if !ok {
		t.Error("valid RSSI int(-65) should return true")
	}
}

// TestSensorOnWireValue_RSSI_ValidInt32 verifies that OnWireValue with a
// valid int32 RSSI value returns true for RSSI_DEVICE sensors.
func TestSensorOnWireValue_RSSI_ValidInt32(t *testing.T) {
	t.Parallel()
	s := NewIntegerSensor(baseCfg(hmenum.ParameterRSSIDevice, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent))
	ok := s.OnWireValue(int32(-80))
	if !ok {
		t.Error("valid RSSI int32(-80) should return true")
	}
}

// TestSensorOnWireValue_RSSI_InvalidValue verifies that the canonical
// "no reading" sentinel is dropped WITHOUT updating the stored value,
// yet reported as handled (true): a well-formed sentinel the sensor
// chose to discard is not a coercion failure, so the callback handler
// must not log "coerce_failed" or fire a pointless getValue reload.
func TestSensorOnWireValue_RSSI_InvalidValue(t *testing.T) {
	t.Parallel()
	s := NewIntegerSensor(baseCfg(hmenum.ParameterRSSIDevice, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent))
	// 0 is the canonical "no reading" sentinel → FixRSSI returns invalid.
	if !s.OnWireValue(int(0)) {
		t.Error("invalid RSSI sentinel should be reported as handled (true), not coerce-failed")
	}
	// The invalid reading must NOT have been stored.
	if _, ok := s.Value(); ok {
		t.Error("invalid RSSI sentinel must not update the stored value")
	}
}

// TestSensorOnWireValue_RSSI_FloatNormalized verifies that a float64 wire
// value (the Rega fetch_all_device_data JSON-seed shape) is normalised
// through FixRSSI rather than stored verbatim — even on a float-typed
// RSSI sensor. An already-correct -70 restores as-is.
func TestSensorOnWireValue_RSSI_FloatNormalized(t *testing.T) {
	t.Parallel()
	s := NewFloatSensor(baseCfg(hmenum.ParameterRSSIDevice, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	// float64 → coerced to int32, FixRSSI applied, stored back as float64.
	ok := s.OnWireValue(float64(-70))
	if !ok {
		t.Error("valid float64 RSSI should be handled (true)")
	}
	if v, got := s.Value(); !got || v != -70 {
		t.Errorf("float64(-70) must normalise to -70, got (%v, %v)", v, got)
	}
}

// TestSensorRSSIOnWireValueNormalizesFloatSeed reproduces the SPA
// "RSSI 128 dBm" bug. The Rega fetch_all_device_data seed decodes JSON
// numbers as float64, so an RSSI_DEVICE reading is delivered to
// OnWireValue as float64 even though the descriptor is INTEGER. The
// float64 (and any other numeric) wire shape must still route through
// FixRSSI: the 128 (0x80) HmIP "no signal" sentinel must be dropped,
// never stored as a bogus reading.
func TestSensorRSSIOnWireValueNormalizesFloatSeed(t *testing.T) {
	t.Parallel()
	for _, param := range []hmenum.Parameter{hmenum.ParameterRSSIDevice, hmenum.ParameterRSSIPeer} {
		t.Run(string(param), func(t *testing.T) {
			t.Parallel()
			s := NewIntegerSensor(rssiCfg(param))

			// float64(128) is the JSON-seed shape of the HmIP no-signal sentinel.
			if !s.OnWireValue(float64(128)) {
				t.Fatal("OnWireValue(float64(128)) must be handled (true), not coerce-failed")
			}
			if v, ok := s.Value(); ok {
				t.Fatalf("sentinel 128 must be dropped; got stored value %d", v)
			}

			// A valid sign-flipped reading from the JSON seed must be fixed.
			if !s.OnWireValue(float64(60)) {
				t.Fatal("OnWireValue(float64(60)) must return true")
			}
			if v, ok := s.Value(); !ok || v != -60 {
				t.Fatalf("float64(60) must normalise to -60, got (%d, %v)", v, ok)
			}

			// int64 (another wire decode shape) must normalise the same way.
			if !s.OnWireValue(int64(0)) {
				t.Fatal("OnWireValue(int64(0)) must return true (handled-by-discard)")
			}
			if v, ok := s.Value(); !ok || v != -60 {
				t.Fatalf("int64(0) sentinel must leave -60 unchanged, got (%d, %v)", v, ok)
			}
		})
	}
}

// TestSensorRSSIRestoreCachedValueNormalizes verifies the values-cache
// restore path also passes RSSI through FixRSSI. A sentinel (128)
// persisted by an older build must be dropped on restore instead of
// resurfacing as a bogus "128 dBm" reading after a daemon restart.
func TestSensorRSSIRestoreCachedValueNormalizes(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, param := range []hmenum.Parameter{hmenum.ParameterRSSIDevice, hmenum.ParameterRSSIPeer} {
		t.Run(string(param), func(t *testing.T) {
			t.Parallel()
			s := NewIntegerSensor(rssiCfg(param))

			// Persisted sentinel (128) must be dropped on restore.
			if s.RestoreCachedValue(int(128), now, now) {
				t.Fatal("RestoreCachedValue(128) sentinel must return false (dropped)")
			}
			if v, ok := s.Value(); ok {
				t.Fatalf("restored sentinel must not be stored, got %d", v)
			}

			// A valid persisted reading restores normally (already-correct -70).
			if !s.RestoreCachedValue(int(-70), now, now) {
				t.Fatal("RestoreCachedValue(-70) must return true")
			}
			if v, ok := s.Value(); !ok || v != -70 {
				t.Fatalf("valid restore: got (%d, %v), want (-70, true)", v, ok)
			}
		})
	}
}

// TestSensorRSSIOnWireValueAppliesFixRSSI verifies that the
// OnWireValue path (untyped wire feed) also passes through FixRSSI
// for RSSI parameters.
func TestSensorRSSIOnWireValueAppliesFixRSSI(t *testing.T) {
	t.Parallel()
	s := NewIntegerSensor(rssiCfg(hmenum.ParameterRSSIDevice))

	// Wire delivers int (standard xml-rpc decode result) → must fix.
	if !s.OnWireValue(int(60)) {
		t.Fatal("OnWireValue(int(60)) must return true for a valid RSSI")
	}
	v, ok := s.Value()
	if !ok || v != -60 {
		t.Fatalf("OnWireValue(int(60)): got (%d, %v), want (-60, true)", v, ok)
	}

	// An invalid sentinel is handled by dropping it: OnWireValue reports
	// true (not a coercion failure) but the stored value stays unchanged.
	if !s.OnWireValue(int(0)) {
		t.Fatal("OnWireValue(int(0)) must return true (handled-by-discard) for invalid RSSI")
	}
	v, ok = s.Value()
	if !ok || v != -60 {
		t.Fatalf("after invalid wire value: got (%d, %v), want (-60, true) — value must be unchanged", v, ok)
	}
}
