// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package clock

import (
	"testing"
	"time"
)

// TestRealClockNewTimerFiresAndStops exercises Real.NewTimer / Timer.C / Timer.Stop.
func TestRealClockNewTimerFiresAndStops(t *testing.T) {
	c := New()

	// Stop a pending timer before it fires.
	tmr := c.NewTimer(10 * time.Second)
	if !tmr.Stop() {
		t.Fatal("Stop returned false on a live timer")
	}
	select {
	case <-tmr.C():
		t.Fatal("stopped timer should not fire")
	default:
	}
}

func TestRealClockNewTimerFires(t *testing.T) {
	c := New()
	tmr := c.NewTimer(1 * time.Millisecond)
	select {
	case <-tmr.C():
	case <-time.After(2 * time.Second):
		t.Fatal("timer did not fire within deadline")
	}
}

func TestRealClockAfter(t *testing.T) {
	c := New()
	ch := c.After(1 * time.Millisecond)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("After channel did not fire")
	}
}

func TestRealClockSleep(t *testing.T) {
	c := New()
	t0 := time.Now()
	c.Sleep(1 * time.Millisecond)
	if time.Since(t0) < time.Millisecond {
		t.Fatal("Sleep returned too early")
	}
}

// TestFakeSetForwardFiresTimers ensures Set triggers pending timers like Advance.
func TestFakeSetForwardFiresTimers(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(start)
	tmr := c.NewTimer(500 * time.Millisecond)
	c.Set(start.Add(time.Second))
	select {
	case <-tmr.C():
	case <-time.After(time.Second):
		t.Fatal("timer should fire when Set jumps past deadline")
	}
}

// TestFakeSetBackwardMovesTime verifies that Set with a past time changes now
// without firing timers that haven't been added yet.
func TestFakeSetBackwardMovesTime(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c := NewFake(start)
	past := start.Add(-time.Hour)
	c.Set(past)
	if !c.Now().Equal(past) {
		t.Fatalf("Now()=%v, want %v", c.Now(), past)
	}
}

// TestFakeTimerStopReturnsFalseWhenAlreadyRemovedFromPending exercises the
// branch in Stop where the timer is no longer in the pending slice.
func TestFakeTimerStopAfterDoubleStop(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	tmr := c.NewTimer(100 * time.Millisecond)
	first := tmr.Stop()
	second := tmr.Stop()
	if !first {
		t.Fatal("first Stop should return true")
	}
	if second {
		t.Fatal("second Stop should return false (already removed)")
	}
}

// TestFakeAfterChannel verifies After returns a channel that fires on Advance.
func TestFakeAfterChannel(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	ch := c.After(50 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("channel fired before Advance")
	default:
	}
	c.Advance(100 * time.Millisecond)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("After channel did not fire after Advance")
	}
}
