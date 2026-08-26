// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// C28-Variant: Inter-Command-Delay tests for [CommandThrottle].

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestInterCommandDelayEnforcesGap verifies that a second non-critical
// Acquire must wait if it arrives before InterCommandDelay has elapsed
// since the first Acquire completed.
func TestInterCommandDelayEnforcesGap(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 100 * time.Millisecond,
	})

	// First Acquire should be instant.
	start := time.Now()
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	// Second Acquire must wait for the remainder of the delay.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	tt.Release()

	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("second acquire returned too soon: %v (want ≥ 80ms gap)", elapsed)
	}
}

// TestInterCommandDelayCriticalBypasses verifies that CRITICAL-priority
// Acquires are never delayed by the inter-command guard, even when a
// non-critical Acquire just completed.
func TestInterCommandDelayCriticalBypasses(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 5 * time.Second, // large delay so non-critical would block
	})

	// Non-critical Acquire to set lastCommandAt.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	// CRITICAL Acquire must not block.
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- tt.Acquire(context.Background(), hmenum.CommandPriorityCritical)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CRITICAL acquire returned error: %v", err)
		}
		tt.Release()
	case <-time.After(50 * time.Millisecond):
		t.Fatalf("CRITICAL acquire blocked (elapsed %v) — must bypass inter-command delay", time.Since(start))
	}
}

// TestInterCommandDelayZeroDisables verifies that when InterCommandDelay
// is zero the guard is disabled and consecutive Acquires complete with
// no artificial wait.
func TestInterCommandDelayZeroDisables(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 0,
	})

	start := time.Now()
	for i := range 20 {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		tt.Release()
	}
	// With InterCommandDelay=0 no acquire should wait; a generous upper
	// bound still catches an accidental delay regression without flaking
	// on race-detector overhead across 20 iterations on a loaded runner.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("20 acquires with no delay took %v (want no per-command wait)", elapsed)
	}
}

// TestInterCommandDelayContextCancels verifies that a blocked inter-command
// waiter respects ctx cancellation.
func TestInterCommandDelayContextCancels(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 5 * time.Second,
	})

	// Trigger lastCommandAt.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := tt.Acquire(ctx, hmenum.CommandPriorityHigh)
	if err == nil {
		tt.Release()
		t.Fatal("expected cancellation, got nil")
	}
}

// TestInterCommandDelayFakeClock verifies that the delay uses the
// injected clock for time measurements, allowing deterministic testing.
func TestInterCommandDelayFakeClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 200 * time.Millisecond,
		Clock:             fake,
	})

	// First Acquire records lastCommandAt at now.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	// Second Acquire must block (fake clock hasn't advanced).
	var completed atomic.Bool
	errCh := make(chan error, 1)
	go func() {
		err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
		completed.Store(true)
		errCh <- err
	}()

	// Give the goroutine real time to park on the fake timer.
	time.Sleep(15 * time.Millisecond)
	if completed.Load() {
		t.Fatal("second acquire must block while fake clock has not advanced")
	}

	// Advance fake clock past the delay — the goroutine should wake.
	fake.Advance(300 * time.Millisecond)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		tt.Release()
	case <-time.After(time.Second):
		t.Fatal("second acquire did not complete after advancing fake clock")
	}
}

// TestInterCommandDelayTelemetry verifies that WaitedForCommandDelay
// increments exactly once per Acquire that actually blocked.
func TestInterCommandDelayTelemetry(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 80 * time.Millisecond,
	})

	if tt.WaitedForCommandDelay() != 0 {
		t.Fatalf("initial WaitedForCommandDelay=%d, want 0", tt.WaitedForCommandDelay())
	}

	// First acquire: no delay (lastCommandAt is zero).
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	if got := tt.WaitedForCommandDelay(); got != 0 {
		t.Fatalf("after first acquire WaitedForCommandDelay=%d, want 0", got)
	}

	// Second acquire: must block ⇒ counter bumps.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	tt.Release()

	if got := tt.WaitedForCommandDelay(); got != 1 {
		t.Fatalf("after second acquire WaitedForCommandDelay=%d, want 1", got)
	}
}

// TestInterCommandDelayCloseUnblocks verifies that Close() wakes a
// goroutine blocked on the inter-command delay.
func TestInterCommandDelayCloseUnblocks(t *testing.T) {
	t.Parallel()
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:       10,
		InterCommandDelay: 5 * time.Second,
	})

	// Trigger lastCommandAt.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	tt.Release()

	errCh := make(chan error, 1)
	go func() {
		errCh <- tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
	}()

	time.Sleep(15 * time.Millisecond)
	tt.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrThrottleClosed) {
			t.Fatalf("expected ErrThrottleClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked goroutine did not unblock after Close()")
	}
}
