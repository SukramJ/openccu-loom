// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package optimistic

import (
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// TestSnapshotInactiveTracker verifies Snapshot returns a zero-value when inactive.
func TestSnapshotInactiveTracker(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	snap := tr.Snapshot()
	if snap.Active {
		t.Error("inactive tracker Snapshot.Active must be false")
	}
	if snap.Value != 0 {
		t.Errorf("inactive tracker Snapshot.Value = %d, want 0", snap.Value)
	}
	if snap.PendingSends != 0 {
		t.Errorf("inactive tracker Snapshot.PendingSends = %d, want 0", snap.PendingSends)
	}
}

// TestSnapshotActiveTrackerAge verifies Snapshot.Age is positive when sentAt is set.
func TestSnapshotActiveTrackerAge(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Now())
	tr := New[int](fake)
	tr.Apply(1, 0, false)
	// Advance virtual time deterministically instead of sleeping.
	fake.Advance(2 * time.Millisecond)
	snap := tr.Snapshot()
	if !snap.Active {
		t.Error("should be active after Apply")
	}
	if snap.Age <= 0 {
		t.Errorf("age = %v, want > 0", snap.Age)
	}
}

// TestScheduleRollbackFires verifies ScheduleRollback fires after the timeout.
func TestScheduleRollbackFires(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(10, 0, false)

	done := make(chan struct{})
	tr.ScheduleRollback(20*time.Millisecond, func() {
		close(done)
	})
	select {
	case <-done:
		// callback fired as expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ScheduleRollback: callback did not fire in time")
	}
}

// TestScheduleRollbackCancelledBySecondCall verifies re-arming cancels the first.
func TestScheduleRollbackCancelledBySecondCall(t *testing.T) {
	t.Parallel()
	tr := New[int](nil)
	tr.Apply(10, 0, false)

	var (
		mu             sync.Mutex
		fired1, fired2 bool
	)
	// Arm a 50ms timer that would set fired1.
	tr.ScheduleRollback(50*time.Millisecond, func() {
		mu.Lock()
		fired1 = true
		mu.Unlock()
	})
	// Immediately re-arm with a shorter 20ms timer that sets fired2.
	tr.ScheduleRollback(20*time.Millisecond, func() {
		mu.Lock()
		fired2 = true
		mu.Unlock()
	})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if fired1 {
		t.Error("first ScheduleRollback callback should have been cancelled")
	}
	if !fired2 {
		t.Error("second ScheduleRollback callback should have fired")
	}
}
