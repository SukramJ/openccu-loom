// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordWriter captures the last (parameter, value) pair written, so a
// test can assert which wire parameter an operation actually reached.
type recordWriter struct {
	mu    sync.Mutex
	param hmenum.Parameter
	value any
}

func (w *recordWriter) SetValue(
	_ context.Context, _ string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.param, w.value = parameter, value
	return nil
}

func (w *recordWriter) lastWrite() (parameter hmenum.Parameter, value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.param, w.value
}

// doorModeOf returns the garage channel's attached door-mode select, or
// nil when the drive has none.
func doorModeOf(t *testing.T, g *Garage) *combined.EnumSelect {
	t.Helper()
	if g == nil {
		return nil
	}
	return g.doorMode
}

// TestGarageAttachesADoorModeSelectWhenVentCapable pins that a vent-capable
// drive materialises the mode select on its channel.
//
// The attachment is what makes the ventilation position reachable at all:
// before it, the only route was a cover position between two thresholds,
// which no north-bound surface can discover, label, or read back.
func TestGarageAttachesADoorModeSelectWhenVentCapable(t *testing.T) {
	t.Parallel()
	w := &recordWriter{}
	ch := newGarageChannel(t, "MOD0001:1", w)
	g := NewGarage(GarageConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
	})

	if doorModeOf(t, g) == nil {
		t.Fatal("a vent-capable garage must carry a door-mode select")
	}

	// It must reach the channel's combined set: that collection is what
	// the event bridge walks, so a select the constructor built but never
	// attached would publish nothing and look identical to a working one.
	var found bool
	for _, cdp := range ch.CombinedDataPoints() {
		if cdp.DataPointKey().Parameter == string(parameterDoorMode) {
			found = true
		}
	}
	if !found {
		t.Fatal("the door-mode select is not in CombinedDataPoints(); the event bridge would never see it")
	}
}

// TestGarageWithoutVentHasNoDoorModeSelect is the negative control for the
// test above: without the capability the select must be absent, otherwise
// that test would pass no matter what the capability said.
func TestGarageWithoutVentHasNoDoorModeSelect(t *testing.T) {
	t.Parallel()
	w := &recordWriter{}
	ch := newGarageChannel(t, "MOD0002:1", w)
	g := NewGarage(GarageConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsVent: false, SupportsStop: true},
	})

	if doorModeOf(t, g) != nil {
		t.Fatal("a drive without a ventilation position must not offer a mode select")
	}
	for _, cdp := range ch.CombinedDataPoints() {
		if cdp.DataPointKey().Parameter == string(parameterDoorMode) {
			t.Fatal("a drive without a ventilation position must not attach a mode select")
		}
	}
}

// TestGarageCoverOperationsUpdateTheDoorModeSelect pins the reporting seam
// between the cover half and the mode select.
//
// Every cover operation writes DOOR_COMMAND through Garage.command, which
// is the one place that sees them all. Without the report back, the
// select keeps showing the mode of the last command it issued itself.
// After a Stop that is permanent: the drive is left reporting a non-mode
// state and no further event arrives to correct it.
func TestGarageCoverOperationsUpdateTheDoorModeSelect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name     string
		op       func(g *Garage) error
		wantMode string
		wantAny  bool
	}{
		{
			name:     "open",
			op:       func(g *Garage) error { return g.Open(ctx, hmenum.CommandPriorityHigh) },
			wantMode: string(DoorStateOpen),
			wantAny:  true,
		},
		{
			name:     "close",
			op:       func(g *Garage) error { return g.Close(ctx, hmenum.CommandPriorityHigh) },
			wantMode: string(DoorStateClosed),
			wantAny:  true,
		},
		{
			name:     "vent",
			op:       func(g *Garage) error { return g.Vent(ctx, hmenum.CommandPriorityHigh) },
			wantMode: string(DoorStateVentilation),
			wantAny:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &recordWriter{}
			ch := newGarageChannel(t, "MOD0003:1", w)
			g := NewGarage(GarageConfig{
				Channel:      ch,
				Writer:       w,
				Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
			})
			mode := doorModeOf(t, g)
			if mode == nil {
				t.Fatal("fixture bug: no door-mode select on a vent-capable drive")
			}
			if err := tc.op(g); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got, ok := mode.Value()
			if ok != tc.wantAny {
				t.Fatalf("Value() reported observed=%v, want %v — the cover operation did not reach the select", ok, tc.wantAny)
			}
			if tc.wantAny && got != tc.wantMode {
				t.Fatalf("Value() = %q, want %q", got, tc.wantMode)
			}
		})
	}
}

// TestGarageStopClearsTheHeldDoorMode pins the case the optimistic hold
// gets wrong if nobody thinks about it.
//
// A stop reaches no mode. The drive is left reporting a non-mode state
// with no further event coming, so a hold left in place would show a mode
// the door is not in — permanently, because nothing arrives to correct
// it.
//
// The vent first is what makes this measure anything: after a bare Stop
// the select reports no value whether or not the operation reached it, so
// the assertion would hold with the reporting seam deleted.
func TestGarageStopClearsTheHeldDoorMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordWriter{}
	ch := newGarageChannel(t, "MOD0005:1", w)
	g := NewGarage(GarageConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
	})
	mode := doorModeOf(t, g)
	if mode == nil {
		t.Fatal("fixture bug: no door-mode select on a vent-capable drive")
	}

	if err := g.Vent(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Vent: %v", err)
	}
	if v, ok := mode.Value(); !ok || v != string(DoorStateVentilation) {
		t.Fatalf("setup: Value() = (%q, %v) after Vent, want VENTILATION_POSITION — "+
			"the cover operation did not reach the select", v, ok)
	}

	if err := g.Stop(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if v, ok := mode.Value(); ok {
		t.Fatalf("Value() = (%q, true) after Stop, want no value — a stop reaches no mode", v)
	}
}

// TestGarageDoorModeWriteReachesTheDevice pins the write half end to end:
// selecting the ventilation mode must put PARTIAL_OPEN on DOOR_COMMAND.
func TestGarageDoorModeWriteReachesTheDevice(t *testing.T) {
	t.Parallel()
	w := &recordWriter{}
	ch := newGarageChannel(t, "MOD0004:1", w)
	g := NewGarage(GarageConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
	})
	mode := doorModeOf(t, g)
	if mode == nil {
		t.Fatal("fixture bug: no door-mode select on a vent-capable drive")
	}
	if err := mode.SetMode(context.Background(), string(DoorStateVentilation), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	param, value := w.lastWrite()
	if param != hmenum.ParameterDoorCommand {
		t.Fatalf("wrote %q, want DOOR_COMMAND", param)
	}
	if value != string(DoorCommandPartialOpen) {
		t.Fatalf("wrote %v, want PARTIAL_OPEN", value)
	}
}
