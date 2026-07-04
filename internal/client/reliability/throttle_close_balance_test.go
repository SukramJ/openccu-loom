// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCloseDoesNotUnderflowInFlight verifies that Close() draining queued
// waiters keeps the permit accounting balanced.
//
// Regression: Close() woke each queued waiter by closing its ready channel
// without reserving an inFlight permit (unlike wakeNextLocked). The woken
// goroutine, not flagged as purged/closed, ran the burst re-check, saw the
// throttle closed, and called releaseAdmittedSlot() — decrementing an
// inFlight permit it was never handed. With a live permit still held by
// another caller that underflows the count and steals the live slot.
func TestCloseDoesNotUnderflowInFlight(t *testing.T) {
	t.Parallel()

	// Capacity 1, no burst / inter-command delay so waiters land straight
	// in the heap and are woken by Close's drain loop.
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

	// Hold the sole permit so every further Acquire queues in the heap.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	if got := tt.InFlight(); got != 1 {
		t.Fatalf("InFlight after holder acquire = %d, want 1", got)
	}

	const queued = 2
	var wg sync.WaitGroup
	errs := make(chan error, queued)
	for range queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- tt.Acquire(context.Background(), hmenum.CommandPriorityLow)
		}()
	}

	// Wait until both goroutines have parked in the waiter heap.
	deadline := time.Now().Add(2 * time.Second)
	for tt.Waiting() < queued {
		if time.Now().After(deadline) {
			t.Fatalf("waiters did not queue: Waiting()=%d, want %d", tt.Waiting(), queued)
		}
		time.Sleep(time.Millisecond)
	}

	tt.Close()

	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrThrottleClosed) {
			t.Errorf("queued Acquire returned %v, want ErrThrottleClosed", err)
		}
	}

	// The holder's permit must still be counted: Close must NOT have released
	// permits it never handed out. Before the fix this read 0 (underflowed).
	if got := tt.InFlight(); got != 1 {
		t.Fatalf("InFlight after Close = %d, want 1 (holder permit intact)", got)
	}

	// Both drained waiters are accounted as suspended.
	if got := tt.Suspended(); got != queued {
		t.Errorf("Suspended after Close = %d, want %d", got, queued)
	}

	// Releasing the holder returns to a clean zero — accounting is balanced.
	tt.Release()
	if got := tt.InFlight(); got != 0 {
		t.Errorf("InFlight after holder release = %d, want 0", got)
	}
}
