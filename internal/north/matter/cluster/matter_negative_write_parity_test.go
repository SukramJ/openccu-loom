// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package cluster_test — consolidated negative-write / negative-invoke
// parity suite.
//
// # Purpose
//
// matter.js schema-parity tests (TestParity_*) lock cluster IDs and
// revisions but NOT write-constraint enforcement.  This file closes that
// gap: every row asserts "a write or invoke that matter.js REJECTS is
// also rejected by Loom with the expected IM status code."
//
// # How to add a row
//
//  1. Read the relevant matter.js source (cluster element or behavior).
//  2. Add a negativeWriteCase (or negativeInvokeCase) row to the table
//     inside TestNegativeWriteParity / TestNegativeInvokeParity.
//  3. Set wantStatus to the IM status matter.js raises (StatusConstraintError,
//     StatusInvalidCommand, …).
//  4. Cite the matter.js path:line in the row comment.
//  5. Add a complementary positive-control row (accepted boundary value)
//     to TestPositiveWriteControl / TestPositiveInvokeControl to prove
//     the suite is not rejecting everything.
package cluster_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/cover"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/thermo"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// statusCoder is a local alias for the im.StatusCodeError interface so this
// file needs no direct knowledge of unexported error types.
type statusCoder interface {
	MatterStatusCode() im.StatusCode
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newHeatOnlyServer() *thermo.ThermostatServer {
	return thermo.NewThermostatServer(thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureHEAT,
		AbsMinHeatSetpointLimit: 700,
		AbsMaxHeatSetpointLimit: 3000,
		InitialHeatingSetpoint:  2000,
	})
}

func newCoolOnlyServer() *thermo.ThermostatServer {
	return thermo.NewThermostatServer(thermo.ThermostatConfig{
		Features:                thermo.ThermostatFeatureCOOL,
		AbsMinCoolSetpointLimit: 1600,
		AbsMaxCoolSetpointLimit: 3200,
		InitialCoolingSetpoint:  2600,
	})
}

func newWindowCoveringServer() *cover.WindowCoveringServer {
	return cover.NewWindowCoveringServer(cover.Config{
		Type:           0,
		EndProductType: 0,
		FeatureMap:     0x05, // LF (bit 0) + PA_LF (bit 2)
	})
}

// ── negative write cases ─────────────────────────────────────────────────────

type negativeWriteCase struct {
	name  string
	build func() interface {
		MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
	}
	attrID     uint32
	value      any
	wantStatus im.StatusCode
}

// TestNegativeWriteParity is the consolidated suite that asserts every
// attribute write that matter.js rejects is rejected by Loom with the
// correct IM status code.
//
// Each row cites the matter.js source path and line number where the
// rejection is defined.  Positive-control rows live in
// TestPositiveWriteControl.
func TestNegativeWriteParity(t *testing.T) {
	t.Parallel()

	cases := []negativeWriteCase{
		{
			// Mirrors matter.js packages/node/src/behaviors/thermostat/ThermostatServer.ts:911
			// #assertSetpointWithinLimits — rejects values above MaxHeatSetpointLimit.
			name: "Thermostat/OccupiedHeatingSetpoint above maxHeat → ConstraintError",
			build: func() interface {
				MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
			} {
				return newHeatOnlyServer()
			},
			attrID:     0x0012, // OccupiedHeatingSetpoint
			value:      int16(3001),
			wantStatus: im.StatusConstraintError,
		},
		{
			// Mirrors matter.js packages/node/src/behaviors/thermostat/ThermostatServer.ts:911
			// #assertSetpointWithinLimits — rejects values below MinHeatSetpointLimit.
			name: "Thermostat/OccupiedHeatingSetpoint below minHeat → ConstraintError",
			build: func() interface {
				MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
			} {
				return newHeatOnlyServer()
			},
			attrID:     0x0012, // OccupiedHeatingSetpoint
			value:      int16(699),
			wantStatus: im.StatusConstraintError,
		},
		{
			// Mirrors matter.js packages/node/src/behaviors/thermostat/ThermostatServer.ts:615-634
			// #assertSystemModeChanging — CoolingOnly sequence forbids SystemMode=Heat(4).
			name: "Thermostat/SystemMode=Heat(4) on CoolingOnly server → ConstraintError",
			build: func() interface {
				MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
			} {
				return newCoolOnlyServer()
			},
			attrID:     0x001C, // SystemMode
			value:      uint8(4),
			wantStatus: im.StatusConstraintError,
		},
		{
			// Mirrors matter.js packages/node/src/standard/elements/window-covering-cluster.element.ts:72
			// liftPercent100thsValue constraint "max 10000".
			name: "WindowCovering/GoToLiftPercentage liftPercent100ths > 10000 → ConstraintError",
			build: func() interface {
				MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
			} {
				// WindowCovering has no direct MatterWrite path for GoToLiftPercentage;
				// the constraint is enforced inside MatterInvoke via extractPercent100ths.
				// This row is intentionally omitted from TestNegativeWriteParity and
				// covered in TestNegativeInvokeParity instead (see note there).
				return newWindowCoveringServer()
			},
			attrID:     0xFFFF, // sentinel — skip: constraint is on invoke, not write
			value:      nil,
			wantStatus: im.StatusConstraintError,
		},
		{
			// Mirrors matter.js packages/node/src/standard/elements/window-covering-cluster.element.ts:79
			// Mode attribute constraint "max 15".
			name: "WindowCovering/Mode write > 15 → ConstraintError",
			build: func() interface {
				MatterWrite(context.Context, uint32, any, hmenum.CommandPriority) error
			} {
				return newWindowCoveringServer()
			},
			attrID:     wire.WindowCoveringAttrMode, // 0x0017
			value:      uint8(16),
			wantStatus: im.StatusConstraintError,
		},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Skip rows that delegate to TestNegativeInvokeParity.
			if tc.attrID == 0xFFFF {
				t.Skip("constraint is on invoke path — see TestNegativeInvokeParity")
			}

			srv := tc.build()
			err := srv.MatterWrite(ctx, tc.attrID, tc.value, hmenum.CommandPriorityHigh)
			if err == nil {
				t.Fatalf("MatterWrite: expected error with status %s, got nil", tc.wantStatus)
			}
			sc, ok := err.(statusCoder)
			if !ok {
				t.Fatalf("MatterWrite error %v does not implement MatterStatusCode()", err)
			}
			if got := sc.MatterStatusCode(); got != tc.wantStatus {
				t.Errorf("MatterStatusCode() = %s (0x%02X), want %s (0x%02X)", got, uint8(got), tc.wantStatus, uint8(tc.wantStatus))
			}
		})
	}
}

// ── negative invoke cases ─────────────────────────────────────────────────────

type negativeInvokeCase struct {
	name  string
	build func() interface {
		MatterInvoke(context.Context, uint32, any, hmenum.CommandPriority) (any, error)
	}
	cmdID      uint32
	fields     any
	wantStatus im.StatusCode
}

// TestNegativeInvokeParity asserts that Matter commands matter.js rejects
// are also rejected by Loom with the correct IM status code.
func TestNegativeInvokeParity(t *testing.T) {
	t.Parallel()

	cases := []negativeInvokeCase{
		{
			// Mirrors matter.js packages/node/src/behaviors/thermostat/ThermostatServer.ts:158-166
			// setpointRaiseLower — mode=Heat(1) without HEAT feature → InvalidCommand.
			name: "Thermostat/SetpointRaiseLower mode=Heat without HEAT feature → InvalidCommand",
			build: func() interface {
				MatterInvoke(context.Context, uint32, any, hmenum.CommandPriority) (any, error)
			} {
				return newCoolOnlyServer()
			},
			cmdID:      0x00, // SetpointRaiseLower
			fields:     map[string]any{"mode": uint8(1), "amount": int8(5)},
			wantStatus: im.StatusInvalidCommand,
		},
		{
			// Mirrors matter.js packages/node/src/standard/elements/window-covering-cluster.element.ts:72
			// GoToLiftPercentage liftPercent100thsValue constraint "max 10000".
			name: "WindowCovering/GoToLiftPercentage liftPercent100ths > 10000 → ConstraintError",
			build: func() interface {
				MatterInvoke(context.Context, uint32, any, hmenum.CommandPriority) (any, error)
			} {
				return newWindowCoveringServer()
			},
			cmdID: wire.WindowCoveringCmdGoToLiftPercentage, // 0x05
			fields: map[string]any{
				"percent": uint16(10001),
			},
			wantStatus: im.StatusConstraintError,
		},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := tc.build()
			_, err := srv.MatterInvoke(ctx, tc.cmdID, tc.fields, hmenum.CommandPriorityHigh)
			if err == nil {
				t.Fatalf("MatterInvoke: expected error with status %s, got nil", tc.wantStatus)
			}
			sc, ok := err.(statusCoder)
			if !ok {
				t.Fatalf("MatterInvoke error %v does not implement MatterStatusCode()", err)
			}
			if got := sc.MatterStatusCode(); got != tc.wantStatus {
				t.Errorf("MatterStatusCode() = %s (0x%02X), want %s (0x%02X)", got, uint8(got), tc.wantStatus, uint8(tc.wantStatus))
			}
		})
	}
}

// ── positive control cases ────────────────────────────────────────────────────

// TestPositiveWriteControl verifies that boundary values matter.js ACCEPTS are
// not rejected by Loom.  These rows guard against over-rejection: if any of
// these fail, the negative-write suite is broken and rejecting valid writes.
func TestPositiveWriteControl(t *testing.T) {
	t.Parallel()

	t.Run("Thermostat/OccupiedHeatingSetpoint == maxHeat accepted", func(t *testing.T) {
		t.Parallel()
		// maxHeat = 3000; write exactly 3000 → must succeed.
		srv := newHeatOnlyServer()
		if err := srv.MatterWrite(context.Background(), 0x0012, int16(3000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("write at maxHeat boundary: %v", err)
		}
		v, ok := srv.MatterRead(0x0012)
		if !ok {
			t.Fatal("OccupiedHeatingSetpoint read after boundary write: ok=false")
		}
		if v.(int16) != 3000 {
			t.Errorf("OccupiedHeatingSetpoint = %d, want 3000", v.(int16))
		}
	})

	t.Run("WindowCovering/Mode write == 15 accepted", func(t *testing.T) {
		t.Parallel()
		// Mode constraint max 15; write exactly 15 → must succeed.
		srv := newWindowCoveringServer()
		if err := srv.MatterWrite(context.Background(), wire.WindowCoveringAttrMode, uint8(15), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("write Mode=15 (boundary): %v", err)
		}
		v, ok := srv.MatterRead(wire.WindowCoveringAttrMode)
		if !ok {
			t.Fatal("Mode read after boundary write: ok=false")
		}
		if v.(uint8) != 15 {
			t.Errorf("Mode = %d, want 15", v.(uint8))
		}
	})
}

// TestPositiveInvokeControl verifies that boundary command arguments
// matter.js accepts are not rejected by Loom.
func TestPositiveInvokeControl(t *testing.T) {
	t.Parallel()

	t.Run("WindowCovering/GoToLiftPercentage == 10000 accepted", func(t *testing.T) {
		t.Parallel()
		// liftPercent100ths == 10000 is the maximum valid value (fully closed).
		srv := newWindowCoveringServer()
		_, err := srv.MatterInvoke(
			context.Background(),
			wire.WindowCoveringCmdGoToLiftPercentage,
			map[string]any{"percent": uint16(10000)},
			hmenum.CommandPriorityHigh,
		)
		if err != nil {
			t.Fatalf("GoToLiftPercentage(10000) boundary: %v", err)
		}
		v, ok := srv.MatterRead(wire.WindowCoveringAttrCurrentPositionLiftPercent100ths)
		if !ok {
			t.Fatal("CurrentPositionLiftPercent100ths read after invoke: ok=false")
		}
		if v.(uint16) != 10000 {
			t.Errorf("CurrentPositionLiftPercent100ths = %d, want 10000", v.(uint16))
		}
	})
}
