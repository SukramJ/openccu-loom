// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package measurement — TemperatureMeasurement cluster-server parity
// tests against matter.js HEAD.
//
// The closest matter.js reference for temperature measurement is
// packages/model/src/standard/elements/temperature-measurement.element.ts
// and packages/node/src/behaviors/temperature-measurement/ (no dedicated
// unit test file exists for TemperatureMeasurementServer in matter.js node
// test/behaviors as of HEAD — verified via find ../matter.js).
// The cases below are derived from the cluster element definition and
// the chip reference implementation
// src/app/clusters/temperature-measurement-server/.
//
// Conversion pattern:
//   - Header cites the matter.js or chip source + line where the
//     invariant originates.
//   - Untranslatable TS constructs → t.Skip("FixMe: ...").

package measurement_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// All tests use fakeFloat from measurement_test.go (same package, visible
// without redeclaration).

// TestParityMatterJS_TempServer_ClusterID locks the cluster ID 0x0402.
//
// Mirrors matter.js packages/model/src/standard/elements/
// temperature-measurement.element.ts:5 (id: 0x0402, classification
// "application").
func TestParityMatterJS_TempServer_ClusterID(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	const wantID uint32 = 0x0402
	if got := s.MatterClusterID(); got != wantID {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, wantID)
	}
}

// TestParityMatterJS_TempServer_Revision5 pins the cluster revision at 5.
// matter.js HEAD temperature-measurement.element.ts:5 ships revision 5;
// chip TemperatureMeasurementCluster.cpp also declares revision 5 in
// CHIP_CONFIG_DATA_MODEL_SPEC_REVISION.
//
// Mirrors matter.js packages/model/src/standard/elements/
// temperature-measurement.element.ts:5 (revision: 5).
func TestParityMatterJS_TempServer_Revision5(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	v, ok := s.MatterRead(0xFFFD) // ClusterRevision
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if got := v.(uint16); got != 5 {
		t.Errorf("ClusterRevision = %d, want 5 (matter.js HEAD)", got)
	}
}

// TestParityMatterJS_TempServer_MeasuredValueEncoding verifies the
// Celsius → int16 (×0.01 °C) wire encoding. 21.5 °C → 2150.
//
// Mirrors matter.js packages/model/src/standard/elements/
// temperature-measurement.element.ts:30 (MeasuredValue type int16s,
// constraint "-27315 to 32767") and chip
// src/app/clusters/temperature-measurement-server/
// TemperatureMeasurementCluster.cpp:27 (kMaxMeasuredValueRange=32766).
func TestParityMatterJS_TempServer_MeasuredValueEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		celsius float64
		want    int16
	}{
		{0.0, 0},
		{21.5, 2150},
		{-10.0, -1000},
		{100.0, 10000},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := measurement.NewTemperatureServer(fakeFloat{val: tc.celsius, obs: true})
			v, ok := s.MatterRead(0x0000)
			if !ok {
				t.Fatal("MeasuredValue: ok=false")
			}
			if got := v.(int16); got != tc.want {
				t.Errorf("%.1f °C → wire %d, want %d", tc.celsius, got, tc.want)
			}
		})
	}
}

// TestParityMatterJS_TempServer_NullOnUnavailable verifies that (nil, true)
// is returned when the source has no observed value. matter.js's
// TemperatureMeasurementServer maps an absent value to the nullable
// attribute's null encoding (TLV null-typed).
//
// Mirrors matter.js packages/node/src/behaviors/temperature-measurement/
// TemperatureMeasurementServer.ts — nullable MeasuredValue.
func TestParityMatterJS_TempServer_NullOnUnavailable(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 0, obs: false}) // obs=false → unavailable
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MeasuredValue unavailable: ok=false, want true (null-capable attribute)")
	}
	if v != nil {
		t.Errorf("MeasuredValue unavailable: got %v, want nil", v)
	}
}

// TestParityMatterJS_TempServer_SaturationHighAtSpecCeiling verifies that
// extreme temperatures clamp at 32766 (chip kMaxMeasuredValueRange).
// 32767 is the Matter NULL sentinel and must never be emitted as a real value.
//
// Mirrors chip src/app/clusters/temperature-measurement-server/
// TemperatureMeasurementCluster.cpp:27-28 kMaxMeasuredValueRange=32766.
func TestParityMatterJS_TempServer_SaturationHighAtSpecCeiling(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 5000.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok=false for saturated high")
	}
	const wantMax int16 = 32766
	if got := v.(int16); got != wantMax {
		t.Errorf("saturation high = %d, want %d (32767 is TLV-null sentinel)", got, wantMax)
	}
}

// TestParityMatterJS_TempServer_SaturationLowAtAbsoluteZero verifies that
// below-absolute-zero temperatures clamp at -27315 (−273.15 °C).
//
// Mirrors chip src/app/clusters/temperature-measurement-server/
// TemperatureMeasurementCluster.cpp:26 kMinMeasuredValueRange=-27315.
func TestParityMatterJS_TempServer_SaturationLowAtAbsoluteZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: -500.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok=false for saturated low")
	}
	const wantMin int16 = -27315
	if got := v.(int16); got != wantMin {
		t.Errorf("saturation low = %d, want %d (absolute zero floor)", got, wantMin)
	}
}

// TestParityMatterJS_TempServer_MinMeasuredValue verifies the static
// MinMeasuredValue attribute (-27315 = -273.15 °C).
//
// Mirrors matter.js temperature-measurement.element.ts MinMeasuredValue
// constraint "-27315 to max 32766".
func TestParityMatterJS_TempServer_MinMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	v, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MinMeasuredValue: ok=false")
	}
	if got := v.(int16); got != -27315 {
		t.Errorf("MinMeasuredValue = %d, want -27315", got)
	}
}

// TestParityMatterJS_TempServer_MaxMeasuredValue verifies the static
// MaxMeasuredValue attribute (32766 per chip ceiling).
//
// Mirrors matter.js temperature-measurement.element.ts MaxMeasuredValue
// constraint "min -27314 to 32766".
func TestParityMatterJS_TempServer_MaxMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	v, ok := s.MatterRead(0x0002)
	if !ok {
		t.Fatal("MaxMeasuredValue: ok=false")
	}
	if got := v.(int16); got != 32766 {
		t.Errorf("MaxMeasuredValue = %d, want 32766", got)
	}
}

// TestParityMatterJS_TempServer_WriteAlwaysErrors verifies that
// TemperatureMeasurement is a read-only cluster — write must return an error.
//
// Mirrors matter.js temperature-measurement.element.ts — all attributes
// are R (read) with no W (write) access.
func TestParityMatterJS_TempServer_WriteAlwaysErrors(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, int16(2000), hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite on read-only cluster: expected error, got nil")
	}
}

// TestParityMatterJS_TempServer_InvokeAlwaysErrors verifies that
// TemperatureMeasurement has no commands.
//
// Mirrors matter.js temperature-measurement.element.ts — no commands
// defined (accepted command list empty).
func TestParityMatterJS_TempServer_InvokeAlwaysErrors(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	if _, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterInvoke on cluster with no commands: expected error, got nil")
	}
}

// TestParityMatterJS_TempServer_Tolerance verifies the Tolerance attribute
// (0x0003) returns uint16(0) — openccu-loom has no sensor-specific
// tolerance data, so we advertise 0 (exactly correct).
//
// Mirrors matter.js temperature-measurement.element.ts Tolerance type
// uint16 (default 0).
func TestParityMatterJS_TempServer_Tolerance(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 20.0, obs: true})
	v, ok := s.MatterRead(0x0003)
	if !ok {
		t.Fatal("Tolerance: ok=false")
	}
	if got := v.(uint16); got != 0 {
		t.Errorf("Tolerance = %d, want 0", got)
	}
}

// TestParityMatterJS_TempServer_NullableInt16_WireContract pins the wire
// contract for the Nullable<int16> MeasuredValue attribute. When the source
// reports no value (obs=false), MatterRead returns (nil, true). The
// dispatcher must encode this as a TLV null element (anonymous-tag null,
// control byte 0x14), NOT as a missing attribute (ok=false) and NOT as a
// valid int16 payload. Encoding it as ok=false would cause the dispatcher
// to respond with Status::UnsupportedAttribute, which is incorrect for a
// nullable attribute — the spec allows (and requires) null for transiently
// unavailable sensors. chip encodes the null sentinel as a dedicated
// TypeNull element (control 0x14) per §6.6.4; matter.js uses
// TlvNullable.encodeInternal which emits the same single-byte 0x14.
//
// This test locks the (nil, true) semantic contract at the cluster layer.
// Wire-level encoding (0x14 control byte) is enforced by
// TestCodecParity_NullableString_Null in internal/north/matter/tlv/codec_parity_test.go
// and TestCodecParity_AnyNull there; together they form the full null-wire
// regression guard for nullable int16 wire shape.
//
// Source-Origin: derived from matter.js packages/model/src/standard/
// elements/temperature-measurement.element.ts:30 (MeasuredValue type int16s,
// quality "X N" — nullable) and
// packages/node/src/behaviors/temperature-measurement/
// TemperatureMeasurementServer.ts — absent sensor value maps to null.
func TestParityMatterJS_TempServer_NullableInt16_WireContract(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 0, obs: false})

	// Contract part 1: (nil, true) must be returned — not (nil, false).
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MeasuredValue unavailable: ok=false — nullable attribute must return (nil, true), not (nil, false); dispatcher encodes ok=false as UnsupportedAttribute which is incorrect for a null-capable attribute")
	}
	if v != nil {
		t.Errorf("MeasuredValue unavailable: got %v (%T), want nil — must signal null to dispatcher", v, v)
	}

	// Contract part 2: the value must NOT be type-assertable to int16 — it
	// is nil, not a zero int16. A zero int16 would wire as 0x21 0x00 0x00
	// (TypeSignedInt2 payload = 0.00 °C), which is a valid temperature and
	// would silently hide sensor-unavailability from Apple Home.
	if _, isInt16 := v.(int16); isInt16 {
		t.Error("MeasuredValue unavailable returns int16(0) instead of nil — would hide sensor unavailability as 0 °C on the wire")
	}
}
