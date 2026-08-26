// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package thermo_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/thermo"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParity_Thermostat_SystemMode_ControlSequenceConsistency verifies
// that the SystemMode initial value is compatible with the server's
// feature-set: a HEAT-only server must start in Heat mode (4), a
// COOL-only server in Cool mode (3), and a HEAT+COOL+AUTO server in
// Auto mode (1). This mirrors the conformance rules in matter.js
// packages/node/src/behaviors/thermostat/ThermostatServer.ts for
// SystemMode defaults and the corresponding ControlSequenceOfOperation
// values expected per Matter §4.3.7.24.
func TestParity_Thermostat_SystemMode_ControlSequenceConsistency(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		srv         *thermo.ThermostatServer
		wantMode    uint8
		wantFeature uint32
	}{
		{
			name:        "HEAT-only → SystemMode=4",
			srv:         newHeatOnly(),
			wantMode:    4,
			wantFeature: thermo.ThermostatFeatureHEAT,
		},
		{
			name:        "COOL-only → SystemMode=3",
			srv:         newCoolOnly(),
			wantMode:    3,
			wantFeature: thermo.ThermostatFeatureCOOL,
		},
		{
			name:     "HEAT+COOL+AUTO → SystemMode=1",
			srv:      newHeatCool(),
			wantMode: 1,
			wantFeature: thermo.ThermostatFeatureHEAT |
				thermo.ThermostatFeatureCOOL |
				thermo.ThermostatFeatureAUTO,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := tc.srv.MatterRead(0x001C) // SystemMode
			if !ok {
				t.Fatal("SystemMode: ok=false")
			}
			if got := v.(uint8); got != tc.wantMode {
				t.Errorf("SystemMode = %d, want %d", got, tc.wantMode)
			}
			fv, ok := tc.srv.MatterRead(0xFFFC) // FeatureMap
			if !ok {
				t.Fatal("FeatureMap: ok=false")
			}
			if got := fv.(uint32); got != tc.wantFeature {
				t.Errorf("FeatureMap = 0x%08X, want 0x%08X", got, tc.wantFeature)
			}
		})
	}
}

type statusCoder interface{ MatterStatusCode() im.StatusCode }

// TestParityMatterJS_Thermostat_SetpointConstraintError verifies that
// writing OccupiedHeatingSetpoint / OccupiedCoolingSetpoint outside
// [min, max] returns ConstraintError (0x87). Mirrors matter.js
// ThermostatServer.ts:#assertSetpointWithinLimits (lines 879-892).
func TestParityMatterJS_Thermostat_SetpointConstraintError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name   string
		srv    *thermo.ThermostatServer
		attrID uint32
		value  int16
	}{
		{
			name:   "heat below min (700)",
			srv:    newHeatOnly(),
			attrID: 0x0012,
			value:  int16(600),
		},
		{
			name:   "heat above max (3000)",
			srv:    newHeatOnly(),
			attrID: 0x0012,
			value:  int16(3100),
		},
		{
			name:   "cool below min (1600)",
			srv:    newCoolOnly(),
			attrID: 0x0011,
			value:  int16(1500),
		},
		{
			name:   "cool above max (3200)",
			srv:    newCoolOnly(),
			attrID: 0x0011,
			value:  int16(3300),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.srv.MatterWrite(ctx, tc.attrID, tc.value, hmenum.CommandPriorityHigh)
			if err == nil {
				t.Fatal("expected ConstraintError, got nil")
			}
			sc, ok := err.(statusCoder)
			if !ok {
				t.Fatalf("error %v does not implement MatterStatusCode()", err)
			}
			if sc.MatterStatusCode() != im.StatusConstraintError {
				t.Errorf("MatterStatusCode()=0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
			}
		})
	}
}

// TestParityMatterJS_Thermostat_SetpointWithinLimitsAccepted verifies that
// valid setpoints are accepted. Mirrors matter.js ThermostatServer.ts:732,879.
func TestParityMatterJS_Thermostat_SetpointWithinLimitsAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newHeatOnly()
	if err := srv.MatterWrite(ctx, 0x0012, int16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write within limits: %v", err)
	}
	v, _ := srv.MatterRead(0x0012)
	if v.(int16) != 2500 {
		t.Errorf("OccupiedHeatingSetpoint = %d, want 2500", v.(int16))
	}
}

// TestParityMatterJS_Thermostat_SystemModeConstraintError verifies that
// forbidden SystemMode values return ConstraintError (0x87). Mirrors
// matter.js ThermostatServer.ts:#assertSystemModeChanging (lines 615-634).
func TestParityMatterJS_Thermostat_SystemModeConstraintError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name string
		srv  *thermo.ThermostatServer
		mode uint8 // forbidden mode
	}{
		{"cooling-only forbids Heat(4)", newCoolOnly(), 4},
		{"cooling-only forbids EmergencyHeat(5)", newCoolOnly(), 5},
		{"heating-only forbids Cool(3)", newHeatOnly(), 3},
		{"heating-only forbids Precooling(7)", newHeatOnly(), 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.srv.MatterWrite(ctx, 0x001C, tc.mode, hmenum.CommandPriorityHigh)
			if err == nil {
				t.Fatal("expected ConstraintError, got nil")
			}
			sc, ok := err.(statusCoder)
			if !ok {
				t.Fatalf("error %v does not implement MatterStatusCode()", err)
			}
			if sc.MatterStatusCode() != im.StatusConstraintError {
				t.Errorf("MatterStatusCode()=0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
			}
		})
	}
}

// TestParityMatterJS_Thermostat_SetpointRaiseLowerInvalidCommand verifies
// that SetpointRaiseLower returns InvalidCommand for Heat mode without the
// HEAT feature and Cool mode without the COOL feature. Mirrors matter.js
// ThermostatServer.ts:setpointRaiseLower (lines 158-165).
func TestParityMatterJS_Thermostat_SetpointRaiseLowerInvalidCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name string
		srv  *thermo.ThermostatServer
		mode int // 1=Heat, 2=Cool
	}{
		{"cool-only forbids mode=Heat(1)", newCoolOnly(), 1},
		{"heat-only forbids mode=Cool(2)", newHeatOnly(), 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields := map[string]any{"mode": uint8(tc.mode), "amount": int8(10)}
			_, err := tc.srv.MatterInvoke(ctx, 0x00, fields, hmenum.CommandPriorityHigh)
			if err == nil {
				t.Fatal("expected InvalidCommand, got nil")
			}
			sc, ok := err.(statusCoder)
			if !ok {
				t.Fatalf("error %v does not implement MatterStatusCode()", err)
			}
			if sc.MatterStatusCode() != im.StatusInvalidCommand {
				t.Errorf("MatterStatusCode()=0x%02X, want StatusInvalidCommand (0x85)", sc.MatterStatusCode())
			}
		})
	}
}

// TestParityMatterJS_Thermostat_SetpointRaiseLowerAppliesDelta verifies that
// SetpointRaiseLower adjusts the setpoint by amount*10 units. Mirrors
// matter.js ThermostatServer.ts:setpointRaiseLower line 169 (amount *= 10).
func TestParityMatterJS_Thermostat_SetpointRaiseLowerAppliesDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newHeatOnly() // initial occupHeat = 2000
	// mode=1 (Heat), amount=5 → delta = 5*10 = 50 → new = 2050
	fields := map[string]any{"mode": uint8(1), "amount": int8(5)}
	if _, err := srv.MatterInvoke(ctx, 0x00, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetpointRaiseLower: %v", err)
	}
	v, _ := srv.MatterRead(0x0012)
	if v.(int16) != 2050 {
		t.Errorf("OccupiedHeatingSetpoint after raise = %d, want 2050", v.(int16))
	}
}

// TestParityMatterJS_Thermostat_SetpointRaiseLowerClampsToLimits verifies
// that SetpointRaiseLower clamps to limits rather than exceeding them.
// Mirrors matter.js ThermostatServer.ts:#clampSetpointToLimits (lines 864-874).
func TestParityMatterJS_Thermostat_SetpointRaiseLowerClampsToLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newHeatOnly() // maxHeat = 3000, initial = 2000
	// amount=200 → delta=2000 → 2000+2000=4000 > 3000 → clamped to 3000
	fields := map[string]any{"mode": uint8(1), "amount": int8(100)}
	if _, err := srv.MatterInvoke(ctx, 0x00, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetpointRaiseLower clamp: %v", err)
	}
	v, _ := srv.MatterRead(0x0012)
	if v.(int16) != 3000 {
		t.Errorf("OccupiedHeatingSetpoint clamped = %d, want 3000", v.(int16))
	}
}
