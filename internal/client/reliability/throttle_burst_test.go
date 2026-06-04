// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestThrottleBurstAllowsThresholdInsideWindow(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 5, BurstWindow: 200 * time.Millisecond})

	for i := range 5 {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		tt.Release()
	}
	// Five calls at threshold five never exceed the window, so none waits
	// for a burst slot. Asserting the counter is deterministic, unlike a
	// wall-clock "should be fast" bound that flakes on loaded CI runners.
	if w := tt.WaitedForBurstSlot(); w != 0 {
		t.Fatalf("first burst should not throttle, WaitedForBurstSlot=%d want 0", w)
	}
}

func TestThrottleBurstThrottlesAfterThresholdInsideWindow(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 5, BurstWindow: 100 * time.Millisecond})

	for range 5 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}

	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	tt.Release()
	// The 6th call exceeds the threshold inside the window, so it must
	// block in the burst guard. Asserting the counter is deterministic,
	// unlike a wall-clock lower bound.
	if w := tt.WaitedForBurstSlot(); w < 1 {
		t.Fatalf("6th acquire must wait for a burst slot, WaitedForBurstSlot=%d want >= 1", w)
	}
}

func TestThrottleBurstAllowsCriticalToBypass(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 3, BurstWindow: 200 * time.Millisecond})

	// Saturate the burst window with HIGH priority calls.
	for range 3 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}

	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("critical acquire: %v", err)
	}
	tt.Release()
	// CRITICAL bypasses the burst guard, and the three HIGH calls sat at
	// (not above) the threshold, so nothing waited. Deterministic counter
	// check instead of a wall-clock bound.
	if w := tt.WaitedForBurstSlot(); w != 0 {
		t.Fatalf("CRITICAL must bypass burst, WaitedForBurstSlot=%d want 0", w)
	}
}

func TestThrottleBurstNotConfigured(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10})

	for range 50 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}
	// With no burst threshold configured the guard is a no-op, so no call
	// ever waits. Deterministic counter check instead of a wall-clock
	// bound that -race overhead on 50 iterations could blow past.
	if w := tt.WaitedForBurstSlot(); w != 0 {
		t.Fatalf("no burst config = no wait, WaitedForBurstSlot=%d want 0", w)
	}
}

func TestThrottleBurstWindowDrainsOverTime(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 3, BurstWindow: 50 * time.Millisecond})

	for range 3 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}

	// Wait for the window to fully drain.
	time.Sleep(80 * time.Millisecond)

	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("acquire after drain: %v", err)
	}
	tt.Release()
	// The three pre-drain calls sat at the threshold (no wait) and the
	// window has since drained, so the post-drain call does not wait
	// either. Deterministic counter check instead of a tight wall-clock
	// bound.
	if w := tt.WaitedForBurstSlot(); w != 0 {
		t.Fatalf("after window-drain, no wait expected, WaitedForBurstSlot=%d want 0", w)
	}
}

func TestThrottleBurstCancelsOnContext(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 2, BurstWindow: 5 * time.Second})

	for range 2 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := tt.Acquire(ctx, hmenum.CommandPriorityHigh)
	if err == nil {
		tt.Release()
		t.Fatal("expected cancellation")
	}
}

// TestThrottleBurstDowngradedCounter verifies that the BurstDowngraded counter
// is incremented only for HIGH-priority acquires that were blocked by the
// burst window (not for LOW, not for CRITICAL).
func TestThrottleBurstDowngradedCounter(t *testing.T) {
	t.Parallel()

	tt := NewThrottle(ThrottleConfig{MaxInFlight: 10, BurstThreshold: 2, BurstWindow: 200 * time.Millisecond})

	// Fill the burst window with HIGH-priority calls.
	for range 2 {
		_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		tt.Release()
	}

	if got := tt.BurstDowngraded(); got != 0 {
		t.Fatalf("before throttle trigger: BurstDowngraded = %d, want 0", got)
	}

	// Let one more HIGH call be cancelled — it must block.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = tt.Acquire(ctx, hmenum.CommandPriorityHigh)

	if got := tt.BurstDowngraded(); got != 1 {
		t.Fatalf("after burst-blocked HIGH: BurstDowngraded = %d, want 1", got)
	}

	// A CRITICAL call must not increment the counter.
	_ = tt.Acquire(context.Background(), hmenum.CommandPriorityCritical)
	tt.Release()
	if got := tt.BurstDowngraded(); got != 1 {
		t.Fatalf("CRITICAL must not increment BurstDowngraded: got %d, want 1", got)
	}
}

func TestThrottleBurstLoadStress(t *testing.T) {
	// 50 concurrent HIGH-prio Acquires through a 5/100ms burst window: the
	// burst guard must throttle the vast majority of them.
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 5, BurstThreshold: 5, BurstWindow: 100 * time.Millisecond})

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
			tt.Release()
		})
	}
	wg.Wait()
	// Assert the burst guard actually throttled the load via the throttle's
	// own counter rather than wall-clock duration. The old timing bound
	// (>= 500ms) flaked on loaded CI runners that completed in ~400ms.
	// Calibration over 80 race-enabled runs never dropped below 34 burst
	// waits; 20 is a safe, meaningful floor (40% of the load throttled).
	if w := tt.WaitedForBurstSlot(); w < 20 {
		t.Fatalf("50 calls through 5/100ms must be heavily throttled, WaitedForBurstSlot=%d want >= 20", w)
	}
}
