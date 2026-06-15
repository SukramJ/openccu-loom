// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package clock

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// P1-2: Fake clock needs deterministic timer semantics so the
// reliability layer's timing tests stop being wall-clock-bound.

func TestRealClockAdvancesWithWallTime(t *testing.T) {
	c := New()
	t0 := c.Now()
	time.Sleep(2 * time.Millisecond)
	if c.Now().Sub(t0) < time.Millisecond {
		t.Fatal("real clock did not advance with wall time")
	}
}

func TestFakeNowDoesNotAdvanceOnItsOwn(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(start)
	time.Sleep(2 * time.Millisecond)
	if !c.Now().Equal(start) {
		t.Fatalf("fake advanced without Advance: %v", c.Now())
	}
}

func TestFakeAdvanceFiresPendingTimers(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	timer := c.NewTimer(100 * time.Millisecond)
	select {
	case <-timer.C():
		t.Fatal("timer fired prematurely")
	default:
	}
	c.Advance(150 * time.Millisecond)
	select {
	case <-timer.C():
	case <-time.After(time.Second):
		t.Fatal("timer did not fire after Advance")
	}
}

func TestFakeAdvanceFiresMultipleTimersInOrder(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	t1 := c.NewTimer(50 * time.Millisecond)
	t2 := c.NewTimer(30 * time.Millisecond)
	t3 := c.NewTimer(80 * time.Millisecond)

	c.Advance(60 * time.Millisecond)

	// t2 (30ms) and t1 (50ms) should have fired; t3 (80ms) should not.
	select {
	case <-t1.C():
	default:
		t.Fatal("t1 did not fire")
	}
	select {
	case <-t2.C():
	default:
		t.Fatal("t2 did not fire")
	}
	select {
	case <-t3.C():
		t.Fatal("t3 fired too early")
	default:
	}
}

func TestFakeStopRemovesPendingTimer(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	timer := c.NewTimer(50 * time.Millisecond)
	if !timer.Stop() {
		t.Fatal("Stop returned false on a pending timer")
	}
	if c.PendingCount() != 0 {
		t.Fatalf("pending=%d after Stop", c.PendingCount())
	}
	c.Advance(time.Second)
	select {
	case <-timer.C():
		t.Fatal("stopped timer fired")
	default:
	}
}

func TestFakeStopReturnsFalseAfterFire(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	timer := c.NewTimer(10 * time.Millisecond)
	c.Advance(20 * time.Millisecond)
	<-timer.C()
	if timer.Stop() {
		t.Fatal("Stop returned true on a fired timer")
	}
}

func TestFakeNonPositiveTimerFiresImmediately(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	timer := c.NewTimer(0)
	select {
	case <-timer.C():
	case <-time.After(time.Second):
		t.Fatal("zero-duration timer did not fire")
	}
	timer = c.NewTimer(-1 * time.Second)
	select {
	case <-timer.C():
	case <-time.After(time.Second):
		t.Fatal("negative-duration timer did not fire")
	}
}

func TestFakeNegativeAdvanceIsNoop(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(start)
	c.Advance(-time.Hour)
	if !c.Now().Equal(start) {
		t.Fatalf("clock moved on negative advance: %v", c.Now())
	}
}

func TestFakeSleepUnblocksOnAdvance(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	done := make(chan struct{})
	go func() {
		c.Sleep(100 * time.Millisecond)
		close(done)
	}()
	// Wait until the sleeper has actually registered its timer before
	// advancing. A fixed real-time sleep races goroutine startup — on a
	// loaded runner the Advance can fire before Sleep inserts its timer,
	// leaving the timer permanently un-fired (observed as a macOS CI flake).
	// PendingCount becomes 1 the moment Sleep's NewTimer inserts the timer.
	for c.PendingCount() == 0 {
		runtime.Gosched()
	}
	c.Advance(150 * time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Sleep did not unblock on Advance")
	}
}

func TestFakeConcurrentAdvanceAndScheduleSafe(t *testing.T) {
	t.Parallel()
	c := NewFake(time.Now())
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_ = c.NewTimer(time.Millisecond)
		})
	}
	for range 32 {
		wg.Go(func() {
			c.Advance(time.Microsecond)
		})
	}
	wg.Wait()
	// Smoke: no panic / data race when running with -race.
}
