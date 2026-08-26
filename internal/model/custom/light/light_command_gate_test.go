// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── countingWriter counts wire writes for gate tests ─────────────────────────

type countingWriter struct {
	count int
}

func (w *countingWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	w.count++
	return nil
}

// ─── Light.TurnOn / TurnOff / SetLevel write-gate ────────────────────────────

// TestLightTurnOnSkipsWhenAlreadyOn verifies that TurnOn suppresses the wire
// write when the light is already on.
func TestLightTurnOnSkipsWhenAlreadyOn(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.8)

	before := w.count
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("TurnOn wrote %d time(s) when light was already on; want 0", w.count-before)
	}
}

// TestLightTurnOnPassesWhenOff verifies that TurnOn writes when the light is off.
func TestLightTurnOnPassesWhenOff(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.0)

	before := w.count
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned error: %v", err)
	}
	if w.count == before {
		t.Error("TurnOn issued no write when light was off; want 1 write")
	}
}

// TestLightTurnOffSkipsWhenAlreadyOff verifies that TurnOff suppresses the
// wire write when the light is already off.
func TestLightTurnOffSkipsWhenAlreadyOff(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.0)

	before := w.count
	if err := l.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("TurnOff wrote %d time(s) when light was already off; want 0", w.count-before)
	}
}

// TestLightTurnOffPassesWhenOn verifies that TurnOff issues a write when the
// light is currently on.
func TestLightTurnOffPassesWhenOn(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.8)

	before := w.count
	if err := l.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff returned error: %v", err)
	}
	if w.count == before {
		t.Error("TurnOff issued no write when light was on; want 1 write")
	}
}

// TestLightSetLevelSkipsWhenBrightnessUnchanged verifies that SetLevel
// suppresses the write when the requested level maps to the same brightness
// byte as the current level.
func TestLightSetLevelSkipsWhenBrightnessUnchanged(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.5)

	before := w.count
	if err := l.SetLevel(context.Background(), 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetLevel returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("SetLevel wrote %d time(s) when brightness unchanged; want 0", w.count-before)
	}
}

// TestLightSetLevelPassesWhenBrightnessChanged verifies that SetLevel writes
// when the requested level differs from the current level.
func TestLightSetLevelPassesWhenBrightnessChanged(t *testing.T) {
	t.Parallel()

	w := &countingWriter{}
	l, level := newLightRig(t, "x", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.5)

	before := w.count
	if err := l.SetLevel(context.Background(), 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetLevel returned error: %v", err)
	}
	if w.count == before {
		t.Error("SetLevel issued no write when brightness changed; want 1 write")
	}
}
