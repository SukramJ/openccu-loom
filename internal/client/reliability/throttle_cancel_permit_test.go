// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCancelRacingPurgeOrCloseKeepsTheHoldersPermit drives a queued waiter
// whose context is cancelled while Purge or Close wakes it. Neither path
// reserves an inFlight permit for the woken waiter, so when its Acquire takes
// the ctx.Done branch, cancelWaiter must not give a permit back: the only
// permit outstanding belongs to the live holder, and returning it would let
// the throttle admit one command more than its capacity. Both select branches
// are ready when the waiter runs and the branch is chosen at random, so the
// interleaving is repeated until the ctx.Done branch is exercised.
func TestCancelRacingPurgeOrCloseKeepsTheHoldersPermit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wake func(*CommandThrottle)
	}{
		{"purge", func(tt *CommandThrottle) { tt.Purge("dev:1") }},
		{"close", func(tt *CommandThrottle) { tt.Close() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				for range 200 {
					tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

					// The holder takes the sole permit and keeps it throughout.
					if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
						t.Fatalf("holder acquire: %v", err)
					}

					ctx, cancel := context.WithCancel(context.Background())
					done := make(chan error, 1)
					go func() { done <- tt.AcquireFor(ctx, hmenum.CommandPriorityLow, "dev:1") }()

					synctest.Wait()
					if tt.Waiting() < 1 {
						t.Fatal("waiter did not queue")
					}

					cancel()
					tc.wake(tt)
					<-done

					if got := tt.InFlight(); got != 1 {
						t.Fatalf("InFlight() = %d after cancel racing %s, want 1: the live holder's permit was given back",
							got, tc.name)
					}
				}
			})
		})
	}
}
