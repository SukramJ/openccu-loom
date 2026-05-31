// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// P1-2 follow-up: CommandThrottle honours the injected clock. Tests can
// advance virtual time instead of sleeping real burst windows — the entire
// burst-window cycle completes in microseconds.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestThrottleDefaultClockIsReal verifies that NewThrottle with a nil Clock
// field wires the production wall clock, not a fake.
func TestThrottleDefaultClockIsReal(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{})
	if tt.clk == nil {
		t.Fatal("expected clk to be non-nil after construction")
	}
	if _, ok := tt.clk.(clock.Real); !ok {
		t.Fatalf("expected clock.Real, got %T", tt.clk)
	}
}

// TestThrottleBurstWaitUsesInjectedClock verifies that burst-slot waiting
// uses the injected fake clock so the window can be unblocked by advancing
// virtual time rather than real sleep.
func TestThrottleBurstWaitUsesInjectedClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 2,
		BurstWindow:    100 * time.Millisecond,
		Clock:          fake,
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

	// Give the goroutine real time to park on the timer.
	time.Sleep(10 * time.Millisecond)
	if completed.Load() {
		t.Fatal("third Acquire should be blocked but returned immediately")
	}

	// Advance fake clock past the burst window — the oldest sample is now
	// outside the window; the goroutine should wake and complete.
	fake.Advance(150 * time.Millisecond)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("third Acquire: unexpected error: %v", err)
		}
		tt.Release()
	case <-time.After(time.Second):
		t.Fatal("third Acquire did not complete after advancing fake clock by 150ms")
	}
}

// TestThrottleCriticalBypassesBurstWindow verifies that a CRITICAL-priority
// Acquire is never delayed by the burst guard even when the window is fully
// saturated.
func TestThrottleCriticalBypassesBurstWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 2,
		BurstWindow:    5 * time.Second, // large window so it never expires on its own
		Clock:          fake,
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

	// A CRITICAL Acquire must not block. Verify with a real-time short
	// timeout: if it doesn't return within 50ms the burst guard kicked in.
	done := make(chan error, 1)
	go func() {
		done <- tt.Acquire(context.Background(), hmenum.CommandPriorityCritical)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CRITICAL Acquire returned error: %v", err)
		}
		tt.Release()
	case <-time.After(50 * time.Millisecond):
		t.Fatal("CRITICAL Acquire blocked — burst guard must not apply to CRITICAL priority")
	}
}

// TestThrottleBurstSamplePruningHonorsClock verifies that advancing the fake
// clock past the burst window prunes stale samples so a subsequent Acquire
// does not wait.
func TestThrottleBurstSamplePruningHonorsClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:    10,
		BurstThreshold: 2,
		BurstWindow:    100 * time.Millisecond,
		Clock:          fake,
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

	// Advance the fake clock past the burst window so both samples are stale.
	fake.Advance(200 * time.Millisecond)

	// Third Acquire must not block — the window was pruned by the clock advance.
	done := make(chan error, 1)
	go func() {
		done <- tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire after pruning: unexpected error: %v", err)
		}
		tt.Release()
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Acquire blocked after advancing clock past burst window — pruning did not honour fake clock")
	}
}
