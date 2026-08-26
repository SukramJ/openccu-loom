// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// priority_parity_test.go pins the CommandThrottle priority-queue ordering
// Invariants from py)
// - CRITICAL Acquire bypasses the in-flight semaphore and increments
// the critical-count counter.
// - FIFO ordering within the same priority level.
// - Purge cancels waiters for a given address with ErrSuperseded.
// - CommandPriorityCritical == 0 (per CLAUDE.md critical rules).
// - CriticalCount / ThrottledCount / PurgedCount counters.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Test 1: CommandPriorityCritical is zero ──────────────────────────────────

func TestCommandPriorityCriticalIsZeroValue(t *testing.T) {
	t.Parallel()
	// CLAUDE.md: "CommandPriority.Critical = 0". Never check != 0.
	if hmenum.CommandPriorityCritical != 0 {
		t.Fatalf("CommandPriorityCritical must be 0, got %d", hmenum.CommandPriorityCritical)
	}
}

// ─── Test 2: CRITICAL Acquire bypasses burst guard and increments counter ─────

func TestCriticalAcquireBypassesBurstAndIncrementsCounter(t *testing.T) {
	t.Parallel()
	// Burst threshold = 1, window = 10 s — non-critical acquires would
	// block after the first one.
	th := NewThrottle(ThrottleConfig{
		MaxInFlight:    2,
		BurstThreshold: 1,
		BurstWindow:    10 * time.Second,
	})
	defer th.Close()

	ctx := context.Background()

	// First non-critical exhausts the burst window.
	if err := th.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("non-critical Acquire: %v", err)
	}
	th.Release()

	// A CRITICAL acquire must complete immediately (bypasses burst guard).
	done := make(chan error, 1)
	go func() {
		done <- th.Acquire(ctx, hmenum.CommandPriorityCritical)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CRITICAL Acquire must bypass burst, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CRITICAL Acquire blocked by burst guard (should bypass)")
	}
	th.Release()

	// CriticalCount must be 1.
	if got := th.CriticalCount(); got != 1 {
		t.Errorf("CriticalCount=%d, want 1", got)
	}
}

// ─── Test 3: FIFO ordering within same priority ───────────────────────────────

func TestFIFOWithinSamePriority(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer th.Close()

	ctx := context.Background()
	// Hold the single permit so all waiters queue up.
	if err := th.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	var mu sync.Mutex
	var order []int
	const n = 3
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {

		go func() {
			defer wg.Done()
			if err := th.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			th.Release()
		}()
		// Ensure goroutines queue in order.
		time.Sleep(5 * time.Millisecond)
	}

	// Release the held permit to drain the waiters.
	th.Release()
	wg.Wait()

	// All three goroutines should have acquired in FIFO order (0, 1, 2).
	mu.Lock()
	defer mu.Unlock()
	for i, v := range order {
		if v != i {
			t.Errorf("order[%d]=%d, expected FIFO order %d", i, v, i)
		}
	}
}

// ─── Test 4: Purge cancels waiters and increments PurgedCount ────────────────

func TestPurgeCancelsWaitersForAddress(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer th.Close()

	ctx := context.Background()
	// Hold permit so next Acquire blocks.
	if err := th.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("hold Acquire: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- th.AcquireFor(ctx, hmenum.CommandPriorityLow, "chan:1")
	}()
	time.Sleep(20 * time.Millisecond) // ensure waiter is queued

	purged := th.Purge("chan:1")
	if purged != 1 {
		t.Fatalf("Purge returned %d, want 1", purged)
	}
	if got := th.PurgedCount(); got != 1 {
		t.Errorf("PurgedCount=%d, want 1", got)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSuperseded) {
			t.Fatalf("purged waiter error=%v, want ErrSuperseded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for purged waiter to return")
	}
}

// ─── Test 5: ThrottledCount increments for non-critical queued waiters ────────

func TestThrottledCountIncrements(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer th.Close()

	ctx := context.Background()
	// Hold permit.
	if err := th.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("hold Acquire: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = th.Acquire(ctx, hmenum.CommandPriorityLow) // will queue
		th.Release()
	}()
	time.Sleep(20 * time.Millisecond)
	th.Release()
	<-done

	if got := th.ThrottledCount(); got < 1 {
		t.Errorf("ThrottledCount=%d, want >= 1", got)
	}
}

// ─── Test 6: CRITICAL priority ordering beats LOW waiters ────────────────────

func TestCriticalBeatsLowWaiter(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	defer th.Close()

	ctx := context.Background()
	// Hold permit.
	if err := th.Acquire(ctx, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("hold Acquire: %v", err)
	}

	// Queue a LOW waiter.
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup

	wg.Go(func() {
		if err := th.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
			return
		}
		mu.Lock()
		order = append(order, "low")
		mu.Unlock()
		th.Release()
	})
	time.Sleep(5 * time.Millisecond)

	// Queue a CRITICAL waiter after LOW is already in queue.
	wg.Go(func() {
		if err := th.Acquire(ctx, hmenum.CommandPriorityCritical); err != nil {
			return
		}
		mu.Lock()
		order = append(order, "critical")
		mu.Unlock()
		th.Release()
	})
	time.Sleep(5 * time.Millisecond)

	th.Release()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	// CRITICAL should have been served before LOW.
	if len(order) < 2 {
		t.Fatalf("expected 2 completions, got %d: %v", len(order), order)
	}
	if order[0] != "critical" {
		t.Errorf("first served=%q, want critical (priority ordering broken)", order[0])
	}
}

// ─── Test 7: Closed throttle returns ErrThrottleClosed ───────────────────────

func TestClosedThrottleReturnsError(t *testing.T) {
	t.Parallel()
	th := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	th.Close()
	err := th.Acquire(context.Background(), hmenum.CommandPriorityLow)
	if !errors.Is(err, ErrThrottleClosed) {
		t.Fatalf("Acquire on closed throttle=%v, want ErrThrottleClosed", err)
	}
}
