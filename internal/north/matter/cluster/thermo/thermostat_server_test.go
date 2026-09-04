// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package thermo_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/thermo"
)

func newHeatCool() *thermo.ThermostatServer {
	return thermo.NewThermostatServer(thermo.DefaultThermostatConfig())
}

func newHeatOnly() *thermo.ThermostatServer {
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		InitialHeatingSetpoint:  2000,
	}
	return thermo.NewThermostatServer(cfg)
}

func newCoolOnly() *thermo.ThermostatServer {
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureCOOL,
		AbsMinCoolSetpointLimit: 1600,
		AbsMaxCoolSetpointLimit: 3200,
		InitialCoolingSetpoint:  2600,
	}
	return thermo.NewThermostatServer(cfg)
}

func TestThermostatServer_ClusterID(t *testing.T) {
	t.Parallel()
	srv := newHeatCool()
	if got := srv.MatterClusterID(); got != 0x0201 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0201", got)
	}
}

// TestThermostatServer_AUTO_ClearedWithoutHeatCool verifies that the AUTO
// feature bit is silently cleared when HEAT+COOL are not both present.
func TestThermostatServer_AUTO_ClearedWithoutHeatCool(t *testing.T) {
	t.Parallel()
	// Try to create AUTO with HEAT-only — AUTO must be stripped.
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT | thermo.ThermostatFeatureAUTO,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		InitialHeatingSetpoint:  2000,
	}
	srv := thermo.NewThermostatServer(cfg)
	raw, ok := srv.MatterRead(0xFFFC) // FeatureMap
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	fm := raw.(uint32)
	if fm&thermo.ThermostatFeatureAUTO != 0 {
		t.Errorf("FeatureMap has AUTO bit set (0x%08X) but COOL is absent — AUTO requires HEAT+COOL", fm)
	}
}

// TestThermostatServer_COOL_AttributesGatedByCOOLFeature verifies that
// OccupiedCoolingSetpoint and COOL setpoint limits are absent in HEAT-only mode.
func TestThermostatServer_COOL_AttributesGatedByCOOLFeature(t *testing.T) {
	t.Parallel()
	srv := newHeatOnly()

	attrList := make(map[uint32]bool)
	for _, id := range srv.MatterAttributes() {
		attrList[id] = true
	}
	coolAttrs := []uint32{
		0x0011, // OccupiedCoolingSetpoint
		0x0005, // AbsMinCoolSetpointLimit
		0x0006, // AbsMaxCoolSetpointLimit
		0x0017, // MinCoolSetpointLimit
		0x0018, // MaxCoolSetpointLimit
	}
	for _, id := range coolAttrs {
		if attrList[id] {
			t.Errorf("MatterAttributes() contains COOL attr 0x%04X in HEAT-only mode", id)
		}
		_, ok := srv.MatterRead(id)
		if ok {
			t.Errorf("MatterRead(0x%04X) returned ok=true in HEAT-only mode", id)
		}
	}
}

// TestThermostatServer_HEAT_AttributesGatedByHEATFeature verifies that
// OccupiedHeatingSetpoint and HEAT setpoint limits are absent in COOL-only mode.
func TestThermostatServer_HEAT_AttributesGatedByHEATFeature(t *testing.T) {
	t.Parallel()
	srv := newCoolOnly()

	attrList := make(map[uint32]bool)
	for _, id := range srv.MatterAttributes() {
		attrList[id] = true
	}
	heatAttrs := []uint32{
		0x0012, // OccupiedHeatingSetpoint
		0x0003, // AbsMinHeatSetpointLimit
		0x0004, // AbsMaxHeatSetpointLimit
		0x0015, // MinHeatSetpointLimit
		0x0016, // MaxHeatSetpointLimit
	}
	for _, id := range heatAttrs {
		if attrList[id] {
			t.Errorf("MatterAttributes() contains HEAT attr 0x%04X in COOL-only mode", id)
		}
		_, ok := srv.MatterRead(id)
		if ok {
			t.Errorf("MatterRead(0x%04X) returned ok=true in COOL-only mode", id)
		}
	}
}

// TestThermostatServer_MinSetpointDeadBand_RequiresAUTO verifies that
// MinSetpointDeadBand (0x0019) is absent without the AUTO feature.
func TestThermostatServer_MinSetpointDeadBand_RequiresAUTO(t *testing.T) {
	t.Parallel()
	srv := newHeatOnly()
	_, ok := srv.MatterRead(0x0019)
	if ok {
		t.Error("MatterRead(MinSetpointDeadBand 0x0019) returned ok=true in HEAT-only (no AUTO feature)")
	}
	for _, id := range srv.MatterAttributes() {
		if id == 0x0019 {
			t.Error("MatterAttributes() contains MinSetpointDeadBand (0x0019) without AUTO feature")
		}
	}
}

// TestThermostatServer_LTNE_HidesLocalTemperatureCalibration verifies that
// LocalTemperatureCalibration is hidden when the LTNE feature is set.
func TestThermostatServer_LTNE_HidesLocalTemperatureCalibration(t *testing.T) {
	t.Parallel()
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT | thermo.ThermostatFeatureLTNE,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		InitialHeatingSetpoint:  2000,
	}
	srv := thermo.NewThermostatServer(cfg)
	_, ok := srv.MatterRead(0x0010) // LocalTemperatureCalibration
	if ok {
		t.Error("MatterRead(LocalTemperatureCalibration 0x0010) returned ok=true when LTNE feature is set")
	}
}

// invokeSetpointRaiseLower is a helper that calls SetpointRaiseLower
// via MatterInvoke and fails the test on error.
func invokeSetpointRaiseLower(t *testing.T, srv *thermo.ThermostatServer, mode uint8, amount int8) {
	t.Helper()
	_, err := srv.MatterInvoke(
		context.Background(),
		0x00,
		map[string]any{"mode": mode, "amount": amount},
	)
	if err != nil {
		t.Fatalf("SetpointRaiseLower(mode=%d, amount=%d): %v", mode, amount, err)
	}
}

// readSetpoint reads an int16 attribute (e.g. occupHeat 0x0012 or
// occupCool 0x0011) and fails the test if absent.
func readSetpoint(t *testing.T, srv *thermo.ThermostatServer, attrID uint32, name string) int16 {
	t.Helper()
	raw, ok := srv.MatterRead(attrID)
	if !ok {
		t.Fatalf("MatterRead(0x%04X %s): ok=false", attrID, name)
	}
	return raw.(int16)
}

// TestSetpointRaiseLower_Both_NoClamp verifies the no-clamp path: when
// both setpoints are mid-range and the delta keeps them within limits,
// both shift by exactly amount*10. Mirrors matter.js
// ThermostatServer.ts:170-189.
func TestSetpointRaiseLower_Both_NoClamp(t *testing.T) {
	t.Parallel()
	// HEAT+COOL+AUTO, minHeat=700 maxHeat=3000, minCool=1600 maxCool=3200
	// initialHeat=2000 initialCool=2600.
	srv := newHeatCool()

	// amount=3 → delta=30; 2000+30=2030 (within 700-3000), 2600+30=2630 (within 1600-3200)
	invokeSetpointRaiseLower(t, srv, 0, 3)

	gotHeat := readSetpoint(t, srv, 0x0012, "OccupiedHeatingSetpoint")
	gotCool := readSetpoint(t, srv, 0x0011, "OccupiedCoolingSetpoint")
	if gotHeat != 2030 {
		t.Errorf("OccupiedHeatingSetpoint = %d, want 2030 (no clamp)", gotHeat)
	}
	if gotCool != 2630 {
		t.Errorf("OccupiedCoolingSetpoint = %d, want 2630 (no clamp)", gotCool)
	}
}

// TestSetpointRaiseLower_Both_HeatingLimited verifies the coordinated
// clamp when the heating setpoint overshoots its maxHeat limit but the
// cooling setpoint does not overshoot. The more-limiting overshoot
// (heat) must be subtracted from BOTH setpoints to preserve their spacing.
//
// Setup (values chosen to be easy to verify by hand):
//
//	minHeat=700  maxHeat=2100  initialHeat=2000  → room until maxHeat: 100
//	minCool=1600 maxCool=3200  initialCool=2600  → room until maxCool: 600
//	amount=+12 → delta=+120
//
// Desired:
//
//	desiredHeat = 2000+120 = 2120 → clamp(2120, 700, 2100) = 2100 → heatLimit = 2120-2100 = 20
//	desiredCool = 2600+120 = 2720 → clamp(2720, 1600, 3200) = 2720 → coolLimit = 2720-2720 = 0
//
// abs(coolLimit)=0 <= abs(heatLimit)=20 → subtract heatLimit (20) from both:
//
//	finalHeat = 2120 - 20 = 2100  (== maxHeat, as expected)
//	finalCool = 2720 - 20 = 2700
//
// Expected: occupHeat=2100, occupCool=2700.
func TestSetpointRaiseLower_Both_HeatingLimited(t *testing.T) {
	t.Parallel()
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT | thermo.ThermostatFeatureCOOL | thermo.ThermostatFeatureAUTO,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 2100, // tight ceiling so heat overshoots
		AbsMinCoolSetpointLimit: 1600,
		AbsMaxCoolSetpointLimit: 3200,
		InitialHeatingSetpoint:  2000,
		InitialCoolingSetpoint:  2600,
	}
	srv := thermo.NewThermostatServer(cfg)

	// amount=+12 → delta=+120; heating overshoots by 20, cooling has room
	invokeSetpointRaiseLower(t, srv, 0, 12)

	gotHeat := readSetpoint(t, srv, 0x0012, "OccupiedHeatingSetpoint")
	gotCool := readSetpoint(t, srv, 0x0011, "OccupiedCoolingSetpoint")

	// Hand-computed expected values (see function doc):
	const wantHeat int16 = 2100 // maxHeat — heat was the limiting factor
	const wantCool int16 = 2700 // 2600 + 120 - 20 (the heat overshoot subtracted from both)
	if gotHeat != wantHeat {
		t.Errorf("OccupiedHeatingSetpoint = %d, want %d (heating-limited coordinated clamp)", gotHeat, wantHeat)
	}
	if gotCool != wantCool {
		t.Errorf("OccupiedCoolingSetpoint = %d, want %d (heating-limited coordinated clamp)", gotCool, wantCool)
	}
}

// TestSetpointRaiseLower_Both_CoolingLimited verifies the symmetric case
// where cooling overshoots its maxCool limit and the heat does not.
//
// Setup:
//
//	minHeat=700  maxHeat=3000  initialHeat=2000
//	minCool=1600 maxCool=2700  initialCool=2600  → room until maxCool: 100
//	amount=+12 → delta=+120
//
// Desired:
//
//	desiredCool = 2600+120 = 2720 → clamp(2720, 1600, 2700) = 2700 → coolLimit = 2720-2700 = 20
//	desiredHeat = 2000+120 = 2120 → clamp(2120, 700, 3000) = 2120 → heatLimit = 0
//
// abs(coolLimit)=20 > abs(heatLimit)=0 → subtract coolLimit (20) from both:
//
//	finalHeat = 2120 - 20 = 2100
//	finalCool = 2720 - 20 = 2700  (== maxCool)
//
// Expected: occupHeat=2100, occupCool=2700.
func TestSetpointRaiseLower_Both_CoolingLimited(t *testing.T) {
	t.Parallel()
	cfg := thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT | thermo.ThermostatFeatureCOOL | thermo.ThermostatFeatureAUTO,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		AbsMinCoolSetpointLimit: 1600,
		AbsMaxCoolSetpointLimit: 2700, // tight ceiling so cool overshoots
		InitialHeatingSetpoint:  2000,
		InitialCoolingSetpoint:  2600,
	}
	srv := thermo.NewThermostatServer(cfg)

	// amount=+12 → delta=+120; cooling overshoots by 20, heating has room
	invokeSetpointRaiseLower(t, srv, 0, 12)

	gotHeat := readSetpoint(t, srv, 0x0012, "OccupiedHeatingSetpoint")
	gotCool := readSetpoint(t, srv, 0x0011, "OccupiedCoolingSetpoint")

	// Hand-computed expected values (see function doc):
	const wantHeat int16 = 2100 // 2000 + 120 - 20 (cool overshoot subtracted from both)
	const wantCool int16 = 2700 // maxCool — cool was the limiting factor
	if gotHeat != wantHeat {
		t.Errorf("OccupiedHeatingSetpoint = %d, want %d (cooling-limited coordinated clamp)", gotHeat, wantHeat)
	}
	if gotCool != wantCool {
		t.Errorf("OccupiedCoolingSetpoint = %d, want %d (cooling-limited coordinated clamp)", gotCool, wantCool)
	}
}

// TestThermostatServer_Write_OccupiedHeatingSetpoint verifies that writing
// OccupiedHeatingSetpoint works when the HEAT feature is present.
func TestThermostatServer_Write_OccupiedHeatingSetpoint(t *testing.T) {
	t.Parallel()
	srv := newHeatOnly()
	if err := srv.MatterWrite(context.Background(), 0x0012, int16(2200)); err != nil {
		t.Fatalf("MatterWrite OccupiedHeatingSetpoint: %v", err)
	}
	raw, ok := srv.MatterRead(0x0012)
	if !ok {
		t.Fatal("MatterRead OccupiedHeatingSetpoint after write: ok=false")
	}
	if raw.(int16) != 2200 {
		t.Errorf("OccupiedHeatingSetpoint after write = %v, want 2200", raw)
	}
}

// TestMatterWriteAcceptsDecoderWidths locks the fix for the live
// "matter.tx.im.write_status … cluster=513 attribute=28 status=Failure"
// bug: the bridge's TLV attribute decoder surfaces a written enum8/int16
// as uint64/int64, so MatterWrite must accept those widths and narrow —
// a strict value.(uint8)/value.(int16) rejected the decoded value and
// failed the whole Write.
func TestMatterWriteAcceptsDecoderWidths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		attrSystemMode              = 0x001C
		attrOccupiedHeatingSetpoint = 0x0012
	)

	// SystemMode (enum8) arrives as uint64 from the decoder.
	srv := newHeatCool()
	if err := srv.MatterWrite(ctx, attrSystemMode, uint64(4)); err != nil {
		t.Fatalf("SystemMode write with uint64(4) must succeed, got: %v", err)
	}

	// Heating setpoint (int16) arrives as int64 from the decoder.
	if err := srv.MatterWrite(ctx, attrOccupiedHeatingSetpoint, int64(2100)); err != nil {
		t.Fatalf("OccupiedHeatingSetpoint write with int64(2100) must succeed, got: %v", err)
	}
}
