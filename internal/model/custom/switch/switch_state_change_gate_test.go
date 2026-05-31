// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── L7: Switch.TurnOn / TurnOff gate + ResetTimerOnTime ─────────────────────

// TestSwitchTurnOnSkipsWhenAlreadyOn verifies that TurnOn does not issue
// a wire write when the switch is already on.
func TestSwitchTurnOnSkipsWhenAlreadyOn(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(true)

	w.lastVal = nil
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned error: %v", err)
	}
	if w.lastVal != nil {
		t.Error("TurnOn wrote to wire when switch was already on; want no write")
	}
}

// TestSwitchTurnOnPassesWhenOff verifies that TurnOn issues a wire write
// when the switch is currently off.
func TestSwitchTurnOnPassesWhenOff(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(false)

	w.lastVal = nil
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned error: %v", err)
	}
	if w.lastVal == nil {
		t.Error("TurnOn issued no write when switch was off; want 1 write")
	}
}

// TestSwitchTurnOffSkipsWhenAlreadyOff verifies that TurnOff suppresses
// wire writes when the switch is already off.
func TestSwitchTurnOffSkipsWhenAlreadyOff(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(false)

	w.lastVal = nil
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff returned error: %v", err)
	}
	if w.lastVal != nil {
		t.Error("TurnOff wrote to wire when switch was already off; want no write")
	}
}

// TestSwitchTurnOffPassesWhenOn verifies that TurnOff issues a wire write
// when the switch is currently on.
func TestSwitchTurnOffPassesWhenOn(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(true)

	w.lastVal = nil
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff returned error: %v", err)
	}
	if w.lastVal == nil {
		t.Error("TurnOff issued no write when switch was on; want 1 write")
	}
}

// TestSwitchTurnOffClearsPendingTimer verifies that TurnOff clears a deferred
// ON_TIME timer so a pending SetTimerOnTime call cannot re-open the output.
func TestSwitchTurnOffClearsPendingTimer(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(true)
	// Stage a pending on-time timer.
	s.SetTimerOnTime(30 * time.Second)

	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff returned error: %v", err)
	}
	// After TurnOff the timer must be cleared; IsTimerStateChange returns false.
	if s.IsTimerStateChange() {
		t.Error("TurnOff did not clear the pending ON_TIME timer")
	}
}

// TestSwitchTurnOnPassesWhenTimerPending verifies that TurnOn always writes
// when an ON_TIME timer is pending (IsStateChange returns true for timer changes
// even if state appears unchanged).
func TestSwitchTurnOnPassesWhenTimerPending(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "VCU:4", "", w)
	s.OnState(true)
	// Stage a pending on-time timer — should force a write even though already on.
	s.SetTimerOnTime(30 * time.Second)

	w.lastVal = nil
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned error: %v", err)
	}
	if w.lastVal == nil {
		t.Error("TurnOn issued no write when timer was pending; want 1 write")
	}
}
