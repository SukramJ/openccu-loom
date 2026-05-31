// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Tests for the MaxQueueDepth backpressure gate (audit O9 / R5).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestThrottleMaxQueueDepthRejectsNonCritical verifies that with the
// queue full, a new non-critical Acquire fails fast with
// [ErrThrottleQueueFull] instead of blocking. The first Acquire holds
// the only in-flight permit; the next 2 fill the queue cap; the 4th
// must be rejected.
func TestThrottleMaxQueueDepthRejectsNonCritical(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:   1,
		MaxQueueDepth: 2,
	})
	defer tt.Close()
	ctx := context.Background()

	// Holder permit — never released until we trigger it explicitly.
	if err := tt.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("seed Acquire failed: %v", err)
	}

	// Two queued waiters — these block but do NOT return immediately.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			// We don't care whether they succeed; we only need them
			// occupying queue slots when the assertion below runs.
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
			_ = tt.Acquire(waitCtx, hmenum.CommandPriorityLow)
		}()
	}

	// Give the queued goroutines time to enqueue.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if tt.Waiting() != 2 {
		t.Fatalf("Waiting=%d, want 2 before depth-cap test", tt.Waiting())
	}

	// 4th non-critical Acquire — queue full, must fail fast.
	err := tt.Acquire(ctx, hmenum.CommandPriorityLow)
	if !errors.Is(err, ErrThrottleQueueFull) {
		t.Fatalf("Acquire over cap: got %v, want ErrThrottleQueueFull", err)
	}
	if got := tt.QueueRejectedCount(); got != 1 {
		t.Errorf("QueueRejectedCount=%d, want 1", got)
	}

	// Cleanup: drain queued waiters.
	tt.Release()
	wg.Wait()
}

// TestThrottleMaxQueueDepthBypassesCritical verifies CRITICAL-priority
// commands ignore the depth gate even when the queue is full. The
// depth check is a non-critical-only backpressure mechanism.
func TestThrottleMaxQueueDepthBypassesCritical(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{
		MaxInFlight:   1,
		MaxQueueDepth: 1,
	})
	defer tt.Close()
	ctx := context.Background()

	if err := tt.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fill the queue with one LOW waiter.
	go func() {
		waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		_ = tt.Acquire(waitCtx, hmenum.CommandPriorityLow)
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if tt.Waiting() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if tt.Waiting() != 1 {
		t.Fatalf("Waiting=%d, want 1", tt.Waiting())
	}

	// CRITICAL must NOT fail with ErrThrottleQueueFull. It joins the
	// queue (because the in-flight permit is held), and gets woken
	// when we Release the seed below.
	gotErr := make(chan error, 1)
	go func() {
		critCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		gotErr <- tt.Acquire(critCtx, hmenum.CommandPriorityCritical)
	}()
	// Give it time to land in the queue.
	time.Sleep(50 * time.Millisecond)

	tt.Release() // wake the highest-priority waiter (CRITICAL)

	select {
	case err := <-gotErr:
		if errors.Is(err, ErrThrottleQueueFull) {
			t.Fatal("CRITICAL got ErrThrottleQueueFull — bypass broken")
		}
		if err != nil {
			t.Fatalf("CRITICAL Acquire: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CRITICAL Acquire never returned")
	}
}

// TestThrottleMaxQueueDepthDisabledWhenZero is a guard against
// regressions: with MaxQueueDepth = 0 the depth gate is a no-op (the
// historical unbounded behaviour) regardless of how many waiters
// queue up.
func TestThrottleMaxQueueDepthDisabledWhenZero(t *testing.T) {
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1 /* MaxQueueDepth: 0 */})
	defer tt.Close()
	ctx := context.Background()
	if err := tt.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Pile waiters up — none of them must be rejected.
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			err := tt.Acquire(waitCtx, hmenum.CommandPriorityLow)
			if errors.Is(err, ErrThrottleQueueFull) {
				t.Errorf("unexpected ErrThrottleQueueFull with depth=0")
			}
		}()
	}
	// Let queue build.
	time.Sleep(50 * time.Millisecond)
	if tt.Waiting() == 0 {
		t.Fatal("queue never grew")
	}
	if got := tt.QueueRejectedCount(); got != 0 {
		t.Errorf("QueueRejectedCount=%d with depth disabled, want 0", got)
	}
	tt.Release()
	wg.Wait()
}
