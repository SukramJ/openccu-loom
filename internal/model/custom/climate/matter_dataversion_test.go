// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityMatterJS_ClimateDataVersionBumpsOnSetpointWrite verifies
// that a successful OccupiedHeatingSetpoint write via
// climateThermostatServer increments Climate.MatterDataVersion.
// Controllers rely on this counter for DataVersionFilter evaluation.
func TestParityMatterJS_ClimateDataVersionBumpsOnSetpointWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	before := r.climate.MatterDataVersion()

	srv := findCluster(t, r.climate, matterClusterThermostat)
	if err := srv.MatterWrite(context.Background(), matterAttrThermOccupiedHeatSp, int16(2200), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(setpoint): %v", err)
	}
	if after := r.climate.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after setpoint write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_ClimateDataVersionBumpsOnSystemModeWrite verifies
// that a successful SystemMode write via climateThermostatServer
// increments MatterDataVersion.
func TestParityMatterJS_ClimateDataVersionBumpsOnSystemModeWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	before := r.climate.MatterDataVersion()

	srv := findCluster(t, r.climate, matterClusterThermostat)
	if err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(SystemMode): %v", err)
	}
	if after := r.climate.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after SystemMode write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_ClimateDataVersionBumpsOnSetpointRaiseLower verifies
// that a successful SetpointRaiseLower invoke increments MatterDataVersion.
func TestParityMatterJS_ClimateDataVersionBumpsOnSetpointRaiseLower(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0) // must have an observed baseline for the delta command
	before := r.climate.MatterDataVersion()

	srv := findCluster(t, r.climate, matterClusterThermostat)
	fields := map[string]any{"mode": uint8(0), "amount": int8(10)} // +1.0 °C
	if _, err := srv.MatterInvoke(context.Background(), matterCmdSetpointRaiseLower, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetpointRaiseLower: %v", err)
	}
	if after := r.climate.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after SetpointRaiseLower: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_ClimateDataVersionMonotonicallyRises verifies that
// alternating setpoint and mode writes each increment the counter strictly.
func TestParityMatterJS_ClimateDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := findCluster(t, r.climate, matterClusterThermostat)

	ops := []func() error{
		func() error {
			return srv.MatterWrite(context.Background(), matterAttrThermOccupiedHeatSp, int16(2100), hmenum.CommandPriorityHigh)
		},
		func() error {
			return srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeHeat, hmenum.CommandPriorityHigh)
		},
		func() error {
			return srv.MatterWrite(context.Background(), matterAttrThermOccupiedHeatSp, int16(2200), hmenum.CommandPriorityHigh)
		},
	}
	for i, op := range ops {
		prev := r.climate.MatterDataVersion()
		if err := op(); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		if next := r.climate.MatterDataVersion(); next <= prev {
			t.Fatalf("op %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_ClimateDataVersionStableOnRead verifies that
// MatterRead on any cluster server does not alter MatterDataVersion.
func TestParityMatterJS_ClimateDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	before := r.climate.MatterDataVersion()

	for _, id := range []uint32{matterClusterThermostat, matterClusterThermostatUI} {
		srv := findCluster(t, r.climate, id)
		srv.MatterRead(0x0000)
		srv.MatterRead(matterAttrClusterRevision)
	}
	if after := r.climate.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_ClimateDataVersionStableOnUnknownAttrWrite verifies
// that a write to an unsupported attribute ID does not increment
// MatterDataVersion.
func TestParityMatterJS_ClimateDataVersionStableOnUnknownAttrWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	before := r.climate.MatterDataVersion()

	srv := findCluster(t, r.climate, matterClusterThermostat)
	_ = srv.MatterWrite(context.Background(), 0x4001, int16(0), hmenum.CommandPriorityHigh)

	if after := r.climate.MatterDataVersion(); after != before {
		t.Fatalf("failed write bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_ClimateDataVersionStableOnReadOnlyClusterWrite
// verifies that a write to the read-only
// ThermostatUserInterfaceConfiguration cluster server does not alter
// MatterDataVersion.
func TestParityMatterJS_ClimateDataVersionStableOnReadOnlyClusterWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	before := r.climate.MatterDataVersion()

	for _, id := range []uint32{matterClusterThermostatUI} {
		srv := findCluster(t, r.climate, id)
		_ = srv.MatterWrite(context.Background(), 0x0000, int16(0), hmenum.CommandPriorityHigh)
	}
	if after := r.climate.MatterDataVersion(); after != before {
		t.Fatalf("read-only cluster writes bumped DataVersion: before=%d after=%d", before, after)
	}
}
