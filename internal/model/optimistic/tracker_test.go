// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package optimistic

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// P1-4: Public Tracker mirrors
// The contract is: pin the rollback anchor on the first Apply, drain
// PendingSends on Confirm events, and either Clear or Rollback at the
// end. Bursts must collapse to a single anchor.

func TestApplyMakesValueVisible(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(42, 0, true)
	snap := tr.Snapshot()
	if !snap.Active || snap.Value != 42 {
		t.Fatalf("snap=%+v", snap)
	}
	if snap.PendingSends != 1 {
		t.Fatalf("pending=%d", snap.PendingSends)
	}
}

func TestBurstApplyPinsFirstAnchorOnly(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(1, 100, true) // first send pins anchor=100
	tr.Apply(2, 999, true) // burst — anchor must NOT move to 999
	tr.Apply(3, 999, true)
	snap := tr.Snapshot()
	if snap.PreviousValue != 100 {
		t.Fatalf("anchor moved during burst: %d", snap.PreviousValue)
	}
	if snap.PendingSends != 3 {
		t.Fatalf("pending=%d, want 3", snap.PendingSends)
	}
}

func TestConfirmOneDrainsCounter(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(7, 0, true)
	tr.Apply(7, 0, true)
	if drained := tr.ConfirmOne(); drained {
		t.Fatal("first confirm must not fully drain (2 → 1)")
	}
	if drained := tr.ConfirmOne(); !drained {
		t.Fatal("second confirm must drain (1 → 0)")
	}
}

func TestConfirmOneOnInactiveTrackerReturnsFalse(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	if drained := tr.ConfirmOne(); drained {
		t.Fatal("inactive tracker must return false")
	}
}

func TestRollbackReturnsAnchorAndResets(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(99, 50, true)
	rolled, restored, restoredSet, ok := tr.Rollback()
	if !ok {
		t.Fatal("Rollback returned ok=false on active tracker")
	}
	if rolled != 99 || restored != 50 || !restoredSet {
		t.Fatalf("rolled=%d restored=%d set=%v", rolled, restored, restoredSet)
	}
	if tr.IsActive() {
		t.Fatal("Rollback did not reset")
	}
}

func TestRollbackOnInactiveReturnsOkFalse(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	if _, _, _, ok := tr.Rollback(); ok {
		t.Fatal("inactive Rollback must return ok=false")
	}
}

func TestClearResetsWithoutRollbackTuple(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(1, 2, true)
	tr.Clear()
	if tr.IsActive() {
		t.Fatal("Clear did not reset")
	}
}

func TestDoneClosesOnRollback(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(1, 0, true)
	done := tr.Done()
	if done == nil {
		t.Fatal("Done returned nil for active tracker")
	}
	tr.Rollback()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Done channel did not close after Rollback")
	}
}

func TestSnapshotAgeUsesInjectedClock(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tr := New[int](fake)
	tr.Apply(1, 0, true)
	fake.Advance(5 * time.Second)
	snap := tr.Snapshot()
	if snap.Age != 5*time.Second {
		t.Fatalf("age=%v want 5s", snap.Age)
	}
}

func TestScheduleRollbackFiresOnTimeoutWithFakeClock(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Now())
	tr := New[int](fake)
	tr.Apply(1, 0, true)

	var fired atomic.Int32
	tr.ScheduleRollback(100*time.Millisecond, func() { fired.Add(1) })

	// Before advancing the fake, the timer must NOT have fired.
	time.Sleep(10 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatalf("timer fired prematurely: %d", fired.Load())
	}
	fake.Advance(150 * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for fired.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatalf("expected 1 fire, got %d", fired.Load())
	}
}

func TestScheduleRollbackCancelledByClear(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Now())
	tr := New[int](fake)
	tr.Apply(1, 0, true)

	var fired atomic.Int32
	tr.ScheduleRollback(100*time.Millisecond, func() { fired.Add(1) })
	tr.Clear()
	fake.Advance(time.Second)
	time.Sleep(20 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatalf("Clear did not cancel timer: fired=%d", fired.Load())
	}
}

func TestApplyBurst_BumpsExpectedEchoes(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	// 3 echoes expected: 1 (initial Apply) + 2 (additionalPending).
	tr.ApplyBurst(7, 0, true, 2)
	if snap := tr.Snapshot(); snap.PendingSends != 3 {
		t.Fatalf("PendingSends after ApplyBurst(.,2) = %d, want 3", snap.PendingSends)
	}
	// 2 of 3 echoes: not yet final.
	if final := tr.ConfirmOne(); final {
		t.Fatal("ConfirmOne returned final after 1/3 echoes")
	}
	if final := tr.ConfirmOne(); final {
		t.Fatal("ConfirmOne returned final after 2/3 echoes")
	}
	// 3. echo: final.
	if final := tr.ConfirmOne(); !final {
		t.Fatal("ConfirmOne did not return final after 3/3 echoes")
	}
}

func TestApplyBurst_ZeroAdditionalEqualsApply(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.ApplyBurst(7, 0, true, 0)
	if snap := tr.Snapshot(); snap.PendingSends != 1 {
		t.Fatalf("PendingSends = %d, want 1", snap.PendingSends)
	}
}
