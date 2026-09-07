// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCoverCommandWhileMovingIsNotDropped mirrors the reference's moving
// rule (cover.py CustomDpCover.is_state_change): while DIRECTION reports
// travel, the last confirmed level is not where the cover is — classic
// actuators re-report the old level when they start moving — so a command
// must not be compared against it. Here the cover is confirmed fully open,
// starts closing, and the operator reverses with Open: the write has to
// reach the wire.
func TestCoverCommandWhileMovingIsNotDropped(t *testing.T) {
	w := &stubWriter{}
	c, _, level := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	level.OnEvent(1.0)

	// Negative control: at rest and already open, Open is a no-op.
	if err := c.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != nil {
		t.Fatalf("Open on a resting, fully open cover wrote %v; want nothing", w.last)
	}

	// Travelling towards closed with the old level still confirmed at 1.0.
	c.OnDirection(DirectionDown)
	if err := c.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.last.(float64); got != 1.0 {
		t.Fatalf("Open while closing wrote %v; want LEVEL=1.0 to reverse the movement", w.last)
	}

	// The same for the position axis and for Blind, which delegates to Cover.
	w.last = nil
	if !c.IsStateChangeArgs(StateChangeArgs{Position: ptrFloat(1.0)}) {
		t.Fatal("IsStateChangeArgs(position=current) while moving must report a change")
	}
}

func ptrFloat(v float64) *float64 { return &v }
