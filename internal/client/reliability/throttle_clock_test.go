// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// The burst window is driven by the bubble's fake clock: each test runs in
// a synctest bubble, so a 100ms window elapses instantly and a goroutine
// parked on the throttle's timer is observed with synctest.Wait instead of
// a real sleep.

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestThrottleBurstWaitBlocksUntilTheWindowPasses verifies that an Acquire
// over the burst threshold parks until the oldest sample leaves the window,
// and completes once the window has elapsed.
func TestThrottleBurstWaitBlocksUntilTheWindowPasses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tt := NewThrottle(ThrottleConfig{
			MaxInFlight:    10,
			BurstThreshold: 2,
			BurstWindow:    100 * time.Millisecond,
		})

		// First two Acquires succeed immediately — below threshold.
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		defer tt.Release()
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		defer tt.Release()

		// Third Acquire must block because the burst window is saturated.
		var completed atomic.Bool
		errCh := make(chan error, 1)
		go func() {
			err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
			completed.Store(true)
			errCh <- err
		}()

		synctest.Wait()
		if completed.Load() {
			t.Fatal("third Acquire should be blocked but returned immediately")
		}

		// Just short of the window the oldest sample is still inside it.
		synctest.Sleep(100*time.Millisecond - time.Nanosecond)
		if completed.Load() {
			t.Fatal("third Acquire returned before the burst window elapsed")
		}

		// At the window boundary the oldest sample leaves; the waiter wakes.
		synctest.Sleep(time.Nanosecond)
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("third Acquire: unexpected error: %v", err)
			}
			tt.Release()
		default:
			t.Fatal("third Acquire did not complete once the 100ms burst window elapsed")
		}
	})
}

// TestThrottleCriticalBypassesBurstWindow verifies that a CRITICAL-priority
// Acquire is never delayed by the burst guard even when the window is fully
// saturated.
func TestThrottleCriticalBypassesBurstWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tt := NewThrottle(ThrottleConfig{
			MaxInFlight:    10,
			BurstThreshold: 2,
			BurstWindow:    5 * time.Second,
		})

		// Saturate the burst window with two non-critical Acquires.
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		defer tt.Release()
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		defer tt.Release()

		// A CRITICAL Acquire must return without the clock moving at all.
		start := time.Now()
		done := make(chan error, 1)
		go func() {
			done <- tt.Acquire(context.Background(), hmenum.CommandPriorityCritical)
		}()
		synctest.Wait()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("CRITICAL Acquire returned error: %v", err)
			}
			tt.Release()
		default:
			t.Fatal("CRITICAL Acquire blocked — burst guard must not apply to CRITICAL priority")
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("CRITICAL Acquire waited %v, want 0", waited)
		}
	})
}

// TestThrottleBurstSamplePruningAfterTheWindow verifies that once the burst
// window has elapsed the stale samples are pruned, so a subsequent Acquire
// does not wait.
func TestThrottleBurstSamplePruningAfterTheWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tt := NewThrottle(ThrottleConfig{
			MaxInFlight:    10,
			BurstThreshold: 2,
			BurstWindow:    100 * time.Millisecond,
		})

		// Record 2 burst samples — fills the window.
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		tt.Release()
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		tt.Release()

		// Let the window elapse so both samples are stale.
		time.Sleep(200 * time.Millisecond)

		// Third Acquire must not block — the samples are pruned on entry.
		start := time.Now()
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("Acquire after pruning: unexpected error: %v", err)
		}
		tt.Release()
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("Acquire after the window waited %v, want 0 — stale samples were not pruned", waited)
		}
	})
}
