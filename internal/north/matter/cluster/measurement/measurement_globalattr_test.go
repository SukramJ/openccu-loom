// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for global attribute (FeatureMap 0xFFFC, ClusterRevision 0xFFFD),
// OccupancySensing server, Pressure/BooleanState extras, luxToMatter edge cases,
// concentration server MatterAttributes, and PowerSourceServer unobserved path.

package measurement_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Global attribute IDs from cluster/globals.go.
const (
	attrGlobalFeatureMap      uint32 = 0xFFFC
	attrGlobalClusterRevision uint32 = 0xFFFD
)

// ── TemperatureServer global attributes ────────────────────────────────────────

func TestTemperatureServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 22, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: want ok=true")
	}
	if v == nil {
		t.Fatal("FeatureMap: want non-nil value")
	}
}

func TestTemperatureServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 22, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: want ok=true")
	}
	if v == nil {
		t.Fatal("ClusterRevision: want non-nil value")
	}
}

// ── HumidityServer global attributes + min/max/tolerance ──────────────────────

func TestHumidityServer_MatterRead_MinMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	v, ok := s.MatterRead(0x0001) // attrMinMeasuredValue
	if !ok || v == nil {
		t.Fatalf("MinMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestHumidityServer_MatterRead_MaxMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	v, ok := s.MatterRead(0x0002) // attrMaxMeasuredValue
	if !ok || v == nil {
		t.Fatalf("MaxMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestHumidityServer_MatterRead_Tolerance(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	v, ok := s.MatterRead(0x0003) // attrTolerance
	if !ok || v == nil {
		t.Fatalf("Tolerance: got (%v, %v)", v, ok)
	}
}

func TestHumidityServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok || v == nil {
		t.Fatalf("FeatureMap: got (%v, %v)", v, ok)
	}
}

func TestHumidityServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok || v == nil {
		t.Fatalf("ClusterRevision: got (%v, %v)", v, ok)
	}
}

// ── IlluminanceServer global attributes ─────────────────────────────────────

func TestIlluminanceServer_MatterRead_MinMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100, obs: true})
	v, ok := s.MatterRead(0x0001)
	if !ok || v == nil {
		t.Fatalf("MinMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestIlluminanceServer_MatterRead_MaxMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100, obs: true})
	v, ok := s.MatterRead(0x0002)
	if !ok || v == nil {
		t.Fatalf("MaxMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestIlluminanceServer_MatterRead_Tolerance(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100, obs: true})
	v, ok := s.MatterRead(0x0003) // attrTolerance
	if !ok || v == nil {
		t.Fatalf("Tolerance: got (%v, %v)", v, ok)
	}
}

func TestIlluminanceServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok || v == nil {
		t.Fatalf("FeatureMap: got (%v, %v)", v, ok)
	}
}

func TestIlluminanceServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok || v == nil {
		t.Fatalf("ClusterRevision: got (%v, %v)", v, ok)
	}
}

// ── luxToMatter: v < 1 path (near-zero illuminance) ───────────────────────────
// A very small positive lux value gives log10(~0) which may produce v < 1 in
// luxToMatter; ensure MatterRead doesn't panic.
func TestIlluminanceServer_MatterRead_NearZeroLux(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 0.0001, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("near-zero lux: want ok=true")
	}
	_ = v // just verify no panic
}

// ── PressureServer global attributes ─────────────────────────────────────────

func TestPressureServer_MatterRead_MinMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	v, ok := s.MatterRead(0x0001)
	if !ok || v == nil {
		t.Fatalf("MinMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestPressureServer_MatterRead_MaxMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	v, ok := s.MatterRead(0x0002)
	if !ok || v == nil {
		t.Fatalf("MaxMeasuredValue: got (%v, %v)", v, ok)
	}
}

func TestPressureServer_MatterRead_Tolerance(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	v, ok := s.MatterRead(0x0003) // attrTolerance
	if !ok || v == nil {
		t.Fatalf("Tolerance: got (%v, %v)", v, ok)
	}
}

func TestPressureServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok || v == nil {
		t.Fatalf("FeatureMap: got (%v, %v)", v, ok)
	}
}

func TestPressureServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok || v == nil {
		t.Fatalf("ClusterRevision: got (%v, %v)", v, ok)
	}
}

func TestPressureServer_MatterRead_Unknown(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Fatal("unknown attr: want ok=false")
	}
}

// TestPressureServer_MatterRead_Unobserved verifies that an unobserved source
// returns (nil, true) from MatterRead for the measured value attribute.
func TestPressureServer_MatterRead_Unobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 0, obs: false})
	v, ok := s.MatterRead(0x0000) // attrMeasuredValue
	if !ok || v != nil {
		t.Fatalf("unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

// hPaToMatter: overflow/underflow paths via extreme pressures.
func TestPressureServer_MatterRead_VeryHighPressure(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 400000, obs: true}) // way above int16 max
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("very high pressure: want ok=true")
	}
	_ = v
}

func TestPressureServer_MatterRead_VeryLowPressure(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: -400000, obs: true}) // way below int16 min
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("very low pressure: want ok=true")
	}
	_ = v
}

// ── BooleanStateServer global attributes ────────────────────────────────────

func TestBooleanStateServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{class: interfaces.MatterMeasurementContact, val: true, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok || v == nil {
		t.Fatalf("FeatureMap: got (%v, %v)", v, ok)
	}
}

func TestBooleanStateServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{class: interfaces.MatterMeasurementContact, val: true, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok || v == nil {
		t.Fatalf("ClusterRevision: got (%v, %v)", v, ok)
	}
}

func TestBooleanStateServer_MatterRead_Unknown(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{class: interfaces.MatterMeasurementContact, val: true, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Fatal("unknown attr: want ok=false")
	}
}

// ── OccupancySensingServer ──────────────────────────────────────────────────

func TestOccupancySensingServer_MatterRead_Unobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: false, obs: false})
	v, ok := s.MatterRead(0x0000) // attrOccupancy
	if !ok || v != nil {
		t.Fatalf("unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

func TestOccupancySensingServer_MatterRead_OccupancySensorType(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	v, ok := s.MatterRead(0x0001) // attrOccupancySensorType
	if !ok || v == nil {
		t.Fatalf("OccupancySensorType: got (%v, %v)", v, ok)
	}
}

func TestOccupancySensingServer_MatterRead_OccupancySensorBmp(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	v, ok := s.MatterRead(0x0002) // attrOccupancySensorBmp
	if !ok || v == nil {
		t.Fatalf("OccupancySensorBmp: got (%v, %v)", v, ok)
	}
}

func TestOccupancySensingServer_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	v, ok := s.MatterRead(attrGlobalClusterRevision)
	if !ok || v == nil {
		t.Fatalf("ClusterRevision: got (%v, %v)", v, ok)
	}
}

func TestOccupancySensingServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestOccupancySensingServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestOccupancySensingServer_MatterReportable_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	if len(s.MatterReportable()) == 0 {
		t.Error("MatterReportable: want non-empty")
	}
}

func TestOccupancySensingServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ── concentrationServer MatterAttributes ─────────────────────────────────────

func TestCO2ConcentrationServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 400, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("CO2ConcentrationServer.MatterAttributes: want non-empty")
	}
}

func TestPM25ConcentrationServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewPM25ConcentrationServer(fakeFloat{val: 10, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("PM25ConcentrationServer.MatterAttributes: want non-empty")
	}
}

// ── PowerSourceServer unobserved ──────────────────────────────────────────────

// TestPowerSourceServer_MatterRead_BatReplacementNeeded_Unobserved verifies
// the attrPwrBatReplacementNeeded path when the source has not been observed:
// the returned value is false (default safe state) with ok=true.
func TestPowerSourceServer_MatterRead_BatReplacementNeeded_Unobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: false})
	v, ok := s.MatterRead(0x000F) // attrPwrBatReplacementNeeded
	if !ok {
		t.Fatal("BatReplacementNeeded unobserved: want ok=true")
	}
	// When unobserved, MatterBoolValue returns (false, false), so the !ok branch
	// returns (false, true). The value must be false.
	if b, isBool := v.(bool); !isBool || b {
		t.Fatalf("BatReplacementNeeded unobserved = %v, want false", v)
	}
}

// ── OccupancySensingServer: FeatureMap attribute ──────────────────────────────

func TestOccupancySensingServer_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true})
	v, ok := s.MatterRead(attrGlobalFeatureMap)
	if !ok || v == nil {
		t.Fatalf("OccupancySensing FeatureMap: got (%v, %v)", v, ok)
	}
}

// ── ElectricalPowerServer: NumberOfMeasurementTypes attribute ─────────────────

func TestElectricalPowerServer_MatterRead_NumberOfMeasurementTypes(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	v, ok := s.MatterRead(0x0001) // attrElPwrNumberOfMeasurementTypes
	if !ok || v == nil {
		t.Fatalf("NumberOfMeasurementTypes: got (%v, %v)", v, ok)
	}
}

// ── ElectricalPowerServer global attributes ─────────────────────────────────

func TestElectricalPowerServer_MatterRead_Unknown(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Fatal("unknown attr: want ok=false")
	}
}

func TestElectricalEnergyServer_MatterRead_Unknown(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 100, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Fatal("unknown attr: want ok=false")
	}
}
