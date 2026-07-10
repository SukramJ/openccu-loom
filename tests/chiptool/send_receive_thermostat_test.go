// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_Thermostat is the vertical-slice acceptance test for the
// chip-tool <-> daemon <-> godevccu send/receive matrix. It exercises the
// whole machinery the rest of the matrix clones:
//
//   - SEND: a north-bound WriteAttr that must actually land on the simulated
//     CCU (asserted via GetDPValue ground truth, not a second Matter read).
//   - RECEIVE: a simulated device-originated push that must reach the
//     controller through a PROACTIVE Subscribe report (via AwaitProactiveReport
//     — subscribe first, then fire, so a broken change-notifier is caught
//     rather than masked by the subscribe's initial read).
//   - INVARIANT: a LocalTemperature-only push must not corrupt the operator's
//     OccupiedHeatingSetpoint — the regression guard for the field-observed
//     "the setpoint I set reverts a few seconds later" symptom.
//
// HmIP-BWTH (climate.Climate KindIP) is the representative Thermostat (0x0201)
// device in godevccu's fleet. Writes go against the in-process simulator; the
// live CCU is never touched.
func TestSendReceive_Thermostat(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0201, 1)
	if len(eps) == 0 {
		t.Skip("no Thermostat endpoint — godevccu fleet lacks a climate device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0201)
	if !ok {
		t.Fatalf("could not resolve CCU address for thermostat endpoint %d", ep)
	}

	// SEND — a controller write of OccupiedHeatingSetpoint (centi-degrees C)
	// must reach the CCU as SET_POINT_TEMPERATURE in degrees C.
	t.Run("send/occupied-heating-setpoint", func(t *testing.T) {
		if _, err := b.SharedCtl.WriteAttr(ctx, t, "thermostat", "occupied-heating-setpoint", "2100", ep); err != nil {
			t.Fatalf("write occupied-heating-setpoint: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "SET_POINT_TEMPERATURE")
		if !ok {
			t.Fatalf("SET_POINT_TEMPERATURE absent on CCU after write")
		}
		if !valueNear(got, 21.0, 0.05) {
			t.Fatalf("CCU SET_POINT_TEMPERATURE = %v, want ~21.0", got)
		}
	})

	// RECEIVE — an external setpoint change (device dial / CCU program) must
	// reach the controller as a proactive OccupiedHeatingSetpoint report.
	t.Run("receive/occupied-heating-setpoint", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"thermostat", "occupied-heating-setpoint", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "SET_POINT_TEMPERATURE", 19.5) },
			func(out string) bool {
				v, ok := harness.FindAttrInt(out, "OccupiedHeatingSetpoint")
				return ok && v == 1950
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive OccupiedHeatingSetpoint=1950: %v\n%s", err, out)
		}
	})

	// RECEIVE — an external LocalTemperature push must reach the controller.
	t.Run("receive/local-temperature", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"thermostat", "local-temperature", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "ACTUAL_TEMPERATURE", 22.5) },
			func(out string) bool { v, ok := harness.FindAttrInt(out, "LocalTemperature"); return ok && v == 2250 },
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive LocalTemperature=2250: %v\n%s", err, out)
		}
	})

	// INVARIANT — after the operator sets a setpoint, a LocalTemperature-only
	// device push must not revert it. The Climate change-notifier dirty-marks
	// the whole endpoint on any DP change, so a frequent ACTUAL_TEMPERATURE
	// push re-reads OccupiedHeatingSetpoint; that re-read must return the
	// commanded value, never a stale/default one.
	t.Run("receive/temperature-push-preserves-setpoint", func(t *testing.T) {
		if _, err := b.SharedCtl.WriteAttr(ctx, t, "thermostat", "occupied-heating-setpoint", "2100", ep); err != nil {
			t.Fatalf("seed setpoint: %v", err)
		}
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"thermostat", "local-temperature", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "ACTUAL_TEMPERATURE", 21.5) },
			func(out string) bool { v, ok := harness.FindAttrInt(out, "LocalTemperature"); return ok && v == 2150 },
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive LocalTemperature=2150: %v\n%s", err, out)
		}
		read, err := b.SharedCtl.ReadAttr(ctx, t, "thermostat", "occupied-heating-setpoint", ep)
		if err != nil {
			t.Fatalf("read setpoint after temperature push: %v", err)
		}
		if v, ok := harness.FindAttrInt(read, "OccupiedHeatingSetpoint"); !ok || v != 2100 {
			t.Fatalf("OccupiedHeatingSetpoint reverted after LocalTemperature push: got %v (ok=%v), want 2100\n%s", v, ok, read)
		}
	})
}

// valueNear reports whether a godevccu-side numeric value (returned as any by
// GetDPValue) is within tol of want.
func valueNear(v any, want, tol float64) bool {
	f, ok := asFloat64(v)
	return ok && f >= want-tol && f <= want+tol
}

func asFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
