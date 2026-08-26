// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// Tests for the WaitedForBurstSlot and Suspended telemetry counters (C28 ext).
//
// These tests are in the internal reliability package so they can use the
// fake clock helpers already wired into throttle_clock_test.go.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// WaitedForBurstSlot counter
// ---------------------------------------------------------------------------

// TestThrottleWaitedForBurstSlotIncrements verifies that WaitedForBurstSlot()
// grows by exactly one when a LOW-priority Acquire actually blocks inside
// waitForBurstSlot. The fake clock is used so the test does not wall-clock
// sleep.
func TestThrottleWaitedForBurstSlotIncrements(t *testing.T) {
	fc := clock.NewFake(time.Now())
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 2,
		BurstWindow:    500 * time.Millisecond,
		Clock:          fc,
	})

	// Fill the burst window with two HIGH-priority Acquires (recorded as
	// non-critical samples by recordBurstLocked).
	for i := range 2 {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("fill Acquire %d: %v", i, err)
		}
		tt.Release()
		// Advance clock slightly so samples are not deduped (same instant).
		fc.Advance(1 * time.Millisecond)
	}

	// WaitedForBurstSlot must be zero before any blocking call.
	if got := tt.WaitedForBurstSlot(); got != 0 {
		t.Fatalf("WaitedForBurstSlot()=%d before any blocking call, want 0", got)
	}

	// Now send a LOW-priority Acquire while the window is still full.
	// The call must block in waitForBurstSlot; we release it by advancing
	// the fake clock past the window.
	var wg sync.WaitGroup
	acquireErr := make(chan error, 1)
	wg.Go(func() {
		acquireErr <- tt.Acquire(context.Background(), hmenum.CommandPriorityLow)
	})

	// Give the goroutine time to enter waitForBurstSlot and start the timer.
	time.Sleep(10 * time.Millisecond)

	// Advance the fake clock to drain the burst window.
	fc.Advance(600 * time.Millisecond)

	wg.Wait()
	if err := <-acquireErr; err != nil {
		t.Fatalf("LOW Acquire: %v", err)
	}
	tt.Release()

	if got := tt.WaitedForBurstSlot(); got != 1 {
		t.Fatalf("WaitedForBurstSlot()=%d, want 1", got)
	}
}

// TestThrottleWaitedForBurstSlotNotIncrementedWhenNoWait asserts that
// WaitedForBurstSlot stays zero when the burst window is not full and
// Acquires pass through immediately.
func TestThrottleWaitedForBurstSlotNotIncrementedWhenNoWait(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 10,
		BurstWindow:    500 * time.Millisecond,
	})

	for i := range 5 {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityLow); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		tt.Release()
	}

	if got := tt.WaitedForBurstSlot(); got != 0 {
		t.Fatalf("WaitedForBurstSlot()=%d, want 0 (window not full)", got)
	}
}

// TestThrottleWaitedForBurstSlotCountedOncePerAcquire verifies that a single
// Acquire that blocks counts as exactly one wait even if it iterates through
// the waitForBurstSlot loop multiple times.
func TestThrottleWaitedForBurstSlotCountedOncePerAcquire(t *testing.T) {
	fc := clock.NewFake(time.Now())
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 1,
		BurstWindow:    200 * time.Millisecond,
		Clock:          fc,
	})

	// Saturate the burst window.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("saturate: %v", err)
	}
	tt.Release()

	acquireErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		acquireErr <- tt.Acquire(context.Background(), hmenum.CommandPriorityLow)
	})

	time.Sleep(10 * time.Millisecond)
	fc.Advance(300 * time.Millisecond)
	wg.Wait()

	if err := <-acquireErr; err != nil {
		t.Fatalf("LOW Acquire: %v", err)
	}
	tt.Release()

	if got := tt.WaitedForBurstSlot(); got != 1 {
		t.Fatalf("WaitedForBurstSlot()=%d, want exactly 1", got)
	}
}

// ---------------------------------------------------------------------------
// Suspended counter
// ---------------------------------------------------------------------------

// TestThrottleSuspendedIncrementedOnClose verifies that Suspended() is
// incremented once per queued waiter that is forcibly released when
// Close() is called.
func TestThrottleSuspendedIncrementedOnClose(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

	// Hold the single permit so subsequent Acquires queue.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("hold Acquire: %v", err)
	}

	const waiters = 3
	var wg sync.WaitGroup
	for range waiters {
		wg.Go(func() {
			// These will queue; Close() will wake them all.
			tt.Acquire(context.Background(), hmenum.CommandPriorityLow) //nolint:errcheck // we expect ErrThrottleClosed or nil
		})
	}

	// Give the goroutines time to reach the waiter heap.
	time.Sleep(20 * time.Millisecond)

	tt.Release() // free the held permit
	tt.Close()   // drain all remaining waiters

	wg.Wait()

	if got := tt.Suspended(); got < 1 {
		t.Fatalf("Suspended()=%d after Close(), want ≥1", got)
	}
}

// TestThrottleSuspendedIncrementedByWaitForBurstSlotOnClose verifies that
// Suspended() grows when a caller blocks in waitForBurstSlot and the
// throttle is closed while it waits.
func TestThrottleSuspendedIncrementedByWaitForBurstSlotOnClose(t *testing.T) {
	fc := clock.NewFake(time.Now())
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 1,
		BurstWindow:    10 * time.Second, // very long window so we never drain it
		Clock:          fc,
	})

	// Saturate the burst window.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("saturate: %v", err)
	}
	tt.Release()

	acquireErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		acquireErr <- tt.Acquire(context.Background(), hmenum.CommandPriorityLow)
	})

	// Give the goroutine time to enter waitForBurstSlot and start the timer.
	time.Sleep(10 * time.Millisecond)

	// Close the throttle; the goroutine must wake and return ErrThrottleClosed.
	tt.Close()

	// Advance the clock so the goroutine's timer also fires (defence in depth).
	fc.Advance(20 * time.Second)

	wg.Wait()

	err := <-acquireErr
	if err == nil {
		t.Fatal("expected ErrThrottleClosed or context error after Close, got nil")
	}

	if got := tt.Suspended(); got < 1 {
		t.Fatalf("Suspended()=%d after Close() with queued waiter, want ≥1", got)
	}
}

// TestThrottleSuspendedZeroWithNoClose asserts that Suspended() stays zero
// when the throttle is never closed and no caller is forcibly ejected.
func TestThrottleSuspendedZeroWithNoClose(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 5})

	for i := range 10 {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityLow); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		tt.Release()
	}

	if got := tt.Suspended(); got != 0 {
		t.Fatalf("Suspended()=%d without Close, want 0", got)
	}
}
