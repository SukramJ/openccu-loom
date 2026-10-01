// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestRetrierBackoffRunsOnTheBubbleClock verifies that the backoff between
// attempts is a timer on the bubble's clock: the retry schedule advances only
// when the clock does, and a multi-second schedule completes without real
// waiting.
func TestRetrierBackoffRunsOnTheBubbleClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := NewRetrier(RetryConfig{
			MaxAttempts: 3,
			Initial:     100 * time.Millisecond,
			Max:         time.Second,
			Multiplier:  2,
		})

		var attempts atomic.Int32
		done := make(chan error, 1)
		go func() {
			done <- r.Do(context.Background(), func(_ context.Context, _ int) error {
				attempts.Add(1)
				return errors.New("transient")
			})
		}()

		// The first attempt fired and the retrier parked on its backoff
		// timer; the clock has not moved, so no second attempt yet.
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatalf("expected exactly the first attempt before the clock advances, got %d", attempts.Load())
		}

		// Advance past the first backoff (100ms plus at most 20% jitter) →
		// the second attempt fires.
		synctest.Sleep(150 * time.Millisecond)
		if attempts.Load() != 2 {
			t.Fatalf("second attempt did not fire after the first backoff: %d", attempts.Load())
		}

		// Advance past the second backoff (200ms plus at most 20% jitter) →
		// the third attempt fires and exhausts the schedule.
		synctest.Sleep(300 * time.Millisecond)

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected exhaustion error, got nil")
			}
		default:
			t.Fatal("Retrier.Do did not return after exhausting attempts")
		}
		if attempts.Load() != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts.Load())
		}
	})
}
