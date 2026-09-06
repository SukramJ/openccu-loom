// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCancelAfterCloseDoesNotUnderflowInFlight drives the Close-then-cancel
// interleaving: Close drains a queued waiter WITHOUT reserving an inFlight
// permit, and the same waiter's context is cancelled, so its Acquire may
// take the ctx.Done branch into cancelWaiter. That path emulated a Release
// on an already-woken waiter — a permit Close never handed out — and drove
// the permit count negative, which lets the throttle admit more concurrent
// commands than its capacity allows.
func TestCancelAfterCloseDoesNotUnderflowInFlight(t *testing.T) {
	t.Parallel()

	// The waiter's context is cancelled first and Close drains it before it
	// is scheduled, so both select branches are ready when it finally runs
	// and the branch is chosen at random; repeat so the ctx.Done branch is
	// exercised.
	for range 200 {
		tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

		// Hold the sole permit so the next Acquire queues in the heap.
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("holder acquire: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- tt.Acquire(ctx, hmenum.CommandPriorityLow) }()

		deadline := time.Now().Add(2 * time.Second)
		for tt.Waiting() < 1 {
			if time.Now().After(deadline) {
				t.Fatal("waiter did not queue")
			}
			time.Sleep(time.Millisecond)
		}

		cancel()
		tt.Close()   // drains the waiter without reserving a permit
		tt.Release() // the holder returns its own permit → InFlight() == 0
		<-done

		if got := tt.InFlight(); got < 0 {
			t.Fatalf("InFlight() = %d after Close+cancel: a permit was released that was never held", got)
		}
	}
}
