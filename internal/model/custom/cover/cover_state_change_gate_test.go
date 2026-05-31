// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── countWriter records how many SetValue calls reached the wire ─────────────

type countWriter struct {
	count int
	last  any
}

func (w *countWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.count++
	w.last = value
	return nil
}

// ─── L3: Cover.Open / Close / SetPosition gates ───────────────────────────────

func TestCoverOpenSkipsWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	w := &countWriter{}
	c, _, level := newRig(t, "x", w, custom.CoverCapabilities{})
	level.OnEvent(1.0)

	before := w.count
	if err := c.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("Open wrote %d time(s) when already open; want 0 writes", w.count-before)
	}
}

func TestCoverOpenPassesWhenNotOpen(t *testing.T) {
	t.Parallel()

	w := &countWriter{}
	c, _, level := newRig(t, "x", w, custom.CoverCapabilities{})
	level.OnEvent(0.0)

	before := w.count
	if err := c.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if w.count == before {
		t.Error("Open issued no write when position was 0; want 1 write")
	}
}

func TestCoverCloseSkipsWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	w := &countWriter{}
	c, _, level := newRig(t, "x", w, custom.CoverCapabilities{})
	level.OnEvent(0.0)

	before := w.count
	if err := c.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("Close wrote %d time(s) when already closed; want 0 writes", w.count-before)
	}
}

func TestCoverSetPositionSkipsWhenUnchanged(t *testing.T) {
	t.Parallel()

	w := &countWriter{}
	c, _, level := newRig(t, "x", w, custom.CoverCapabilities{})
	level.OnEvent(0.5)

	before := w.count
	if err := c.SetPosition(context.Background(), 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("SetPosition wrote %d time(s) when position unchanged; want 0 writes", w.count-before)
	}
}

func TestCoverSetPositionPassesWhenChanged(t *testing.T) {
	t.Parallel()

	w := &countWriter{}
	c, _, level := newRig(t, "x", w, custom.CoverCapabilities{})
	level.OnEvent(0.5)

	before := w.count
	if err := c.SetPosition(context.Background(), 0.75, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition returned error: %v", err)
	}
	if w.count == before {
		t.Error("SetPosition issued no write when position changed; want 1 write")
	}
}

// ─── L4: Blind.SetPosition / SetTilt / OpenTilt / CloseTilt gates ────────────

func TestBlindSetPositionSkipsWhenUnchanged(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
	b.OnLevel(0.5)

	before := len(w.calls)
	if err := b.SetPosition(context.Background(), 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Blind.SetPosition wrote %d time(s) when position unchanged; want 0", len(w.calls)-before)
	}
}

func TestBlindSetPositionPassesWhenChanged(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
	b.OnLevel(0.5)

	before := len(w.calls)
	if err := b.SetPosition(context.Background(), 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetPosition returned error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("Blind.SetPosition issued no write when position changed; want 1 write")
	}
}

func TestBlindSetTiltSkipsWhenUnchanged(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
	if b.level2 != nil {
		b.level2.OnEvent(0.3)
	}

	before := len(w.calls)
	if err := b.SetTilt(context.Background(), 0.3, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTilt returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Blind.SetTilt wrote %d time(s) when tilt unchanged; want 0", len(w.calls)-before)
	}
}

func TestBlindOpenTiltSkipsWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
	if b.level2 != nil {
		b.level2.OnEvent(1.0)
	}

	before := len(w.calls)
	if err := b.OpenTilt(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OpenTilt returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Blind.OpenTilt wrote %d time(s) when tilt already open; want 0", len(w.calls)-before)
	}
}

func TestBlindCloseTiltSkipsWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	b := newBlindRig(t, "VCU:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindIP)
	if b.level2 != nil {
		b.level2.OnEvent(0.0)
	}

	before := len(w.calls)
	if err := b.CloseTilt(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("CloseTilt returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Blind.CloseTilt wrote %d time(s) when tilt already closed; want 0", len(w.calls)-before)
	}
}

// ─── L5: Garage.Open / Close / Vent gates ────────────────────────────────────

type garageCountWriter struct{ count int }

func (w *garageCountWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	w.count++
	return nil
}

func TestGarageOpenSkipsWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	w := &garageCountWriter{}
	g := NewGarage(GarageConfig{Writer: w})
	g.OnState(DoorStateOpen)

	before := w.count
	if err := g.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("Garage.Open wrote %d time(s) when door already open; want 0", w.count-before)
	}
}

func TestGarageOpenPassesWhenClosed(t *testing.T) {
	t.Parallel()

	w := &garageCountWriter{}
	g := NewGarage(GarageConfig{Writer: w})
	g.OnState(DoorStateClosed)

	before := w.count
	if err := g.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if w.count == before {
		t.Error("Garage.Open issued no write when door was closed; want 1 write")
	}
}

func TestGarageCloseSkipsWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	w := &garageCountWriter{}
	g := NewGarage(GarageConfig{Writer: w})
	g.OnState(DoorStateClosed)

	before := w.count
	if err := g.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("Garage.Close wrote %d time(s) when door already closed; want 0", w.count-before)
	}
}

func TestGarageVentSkipsWhenAlreadyVentilation(t *testing.T) {
	t.Parallel()

	w := &garageCountWriter{}
	g := NewGarage(GarageConfig{Writer: w})
	g.OnState(DoorStateVentilation)

	before := w.count
	if err := g.Vent(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Vent returned error: %v", err)
	}
	if w.count != before {
		t.Errorf("Garage.Vent wrote %d time(s) when door already vented; want 0", w.count-before)
	}
}

func TestGarageVentPassesWhenClosed(t *testing.T) {
	t.Parallel()

	w := &garageCountWriter{}
	g := NewGarage(GarageConfig{Writer: w})
	g.OnState(DoorStateClosed)

	before := w.count
	if err := g.Vent(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Vent returned error: %v", err)
	}
	if w.count == before {
		t.Error("Garage.Vent issued no write when door was closed; want 1 write")
	}
}
