// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The reference climate model reports OFF whenever the target
// temperature sits at or below the 4.5 °C sentinel, before it maps
// CONTROL_MODE / SET_POINT_MODE to a mode (climate.py CustomDpRfThermostat.mode,
// CustomDpIpThermostat.mode). The simple RF thermostat (HM-CC-TC) carries
// no mode parameter at all: its mode is always HEAT and set_mode is a no-op
// (climate.py BaseCustomDpClimate.mode / set_mode).

func TestModeDerivesOffFromSetpointSentinelIP(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsHeat: true, SupportsOff: true, MinTemperature: 4.5, MaxTemperature: 30.5})
	r.climate.OnSetPointMode(1) // MANU echoed by the CCU after an OFF write
	r.setpoint.OnEvent(4.5)
	if m, ok := r.climate.Mode(); !ok || m != ModeOff {
		t.Fatalf("Mode() with MANU + setpoint 4.5 = (%v,%v), want (off,true)", m, ok)
	}
	// Leaving OFF via the mode surface must reach the wire.
	before := callCount(w)
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if callCount(w) == before {
		t.Fatal("SetMode(heat) on an OFF thermostat wrote nothing")
	}
	r.setpoint.OnEvent(21.0)
	if m, ok := r.climate.Mode(); !ok || m != ModeHeat {
		t.Fatalf("Mode() with MANU + setpoint 21 = (%v,%v), want (heat,true)", m, ok)
	}
}

func TestModeDerivesOffFromSetpointSentinelRF(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{SupportsHeat: true, SupportsOff: true, MinTemperature: 4.5, MaxTemperature: 30.5})
	r.climate.OnControlMode("MANU-MODE")
	r.setpoint.OnEvent(4.0)
	if m, ok := r.climate.Mode(); !ok || m != ModeOff {
		t.Fatalf("Mode() with MANU-MODE + setpoint 4.0 = (%v,%v), want (off,true)", m, ok)
	}
	r.setpoint.OnEvent(5.0)
	if m, ok := r.climate.Mode(); !ok || m != ModeHeat {
		t.Fatalf("Mode() with MANU-MODE + setpoint 5.0 = (%v,%v), want (heat,true)", m, ok)
	}
}

func TestSimpleRFModeIsAlwaysHeatAndSetModeWritesNothing(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := newRig(t, "x", KindSimpleRF, w, simpleRfCapabilities)
	if m, ok := r.climate.Mode(); !ok || m != ModeHeat {
		t.Fatalf("SimpleRF Mode() = (%v,%v), want (heat,true)", m, ok)
	}
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SimpleRF SetMode(heat): %v", err)
	}
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
		t.Fatalf("SimpleRF SetMode(off) = %v, want ErrModeNotSupported", err)
	}
	if n := callCount(w); n != 0 {
		t.Fatalf("SimpleRF SetMode must not touch the wire, got %d writes", n)
	}
	// The setpoint sentinel rule does not apply: HM-CC-TC has no OFF.
	r.setpoint.OnEvent(4.5)
	if m, ok := r.climate.Mode(); !ok || m != ModeHeat {
		t.Fatalf("SimpleRF Mode() at 4.5 °C = (%v,%v), want (heat,true)", m, ok)
	}
}

func callCount(w *stubWriter) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}
