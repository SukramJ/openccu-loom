// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package optimistic

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// TestSnapshotInactiveTracker verifies Snapshot returns a zero-value when inactive.
func TestSnapshotInactiveTracker(t *testing.T) {
	t.Parallel()
	tr := New[int]()
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
	synctest.Test(t, func(t *testing.T) {
		tr := New[int]()
		tr.Apply(1, 0, false)
		time.Sleep(2 * time.Millisecond)
		snap := tr.Snapshot()
		if !snap.Active {
			t.Error("should be active after Apply")
		}
		if snap.Age != 2*time.Millisecond {
			t.Errorf("age = %v, want 2ms", snap.Age)
		}
	})
}

// TestScheduleRollbackFires verifies ScheduleRollback fires after the timeout.
func TestScheduleRollbackFires(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := New[int]()
		tr.Apply(10, 0, false)

		done := make(chan struct{})
		tr.ScheduleRollback(20*time.Millisecond, func() {
			close(done)
		})
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("ScheduleRollback: callback did not fire at the deadline")
		}
	})
}

// TestScheduleRollbackCancelledBySecondCall verifies re-arming cancels the first.
func TestScheduleRollbackCancelledBySecondCall(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := New[int]()
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
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if fired1 {
			t.Error("first ScheduleRollback callback should have been cancelled")
		}
		if !fired2 {
			t.Error("second ScheduleRollback callback should have fired")
		}
	})
}
