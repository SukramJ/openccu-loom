// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// Item A — Retry+Throttle-Stack-Integration tests.
//
// Each test instanciates a Retrier whose fn internally calls Acquire on a
// CommandThrottle. The four scenarios test that context-cancellation,
// throttle-close, successful acquire, and throttle-caused retry-eligible errors
// all interact correctly between the two layers.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── A1. Context-Cancellation during Throttle-Wait ───────────────────────────

// TestRetryThrottleContextCancelDuringThrottleWait verifies that when a
// context is cancelled while the fn is blocked on Throttle.Acquire the
// retrier does not attempt another retry — it propagates context.Canceled and
// returns cleanly.
//
// Design: MaxInFlight=1 is already held by a goroutine so every Acquire call
// in fn parks in the waiter queue. Cancelling ctx wakes the queued Acquire
// with ctx.Err(); fn returns context.Canceled; the retrier sees ctx.Err() !=
// nil and short-circuits.
func TestRetryThrottleContextCancelDuringThrottleWait(t *testing.T) {
	t.Parallel()

	// MaxInFlight=1 — the holder goroutine occupies the only permit.
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})

	// Hold the sole permit so fn will always have to wait.
	if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	// Release when the test ends so we don't leak goroutines.
	defer tt.Release()

	var attempts atomic.Int32
	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     10 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  2,
	})

	ctx, cancel := context.WithCancel(context.Background())

	// fn will park in Throttle.Acquire; cancel from outside will unblock it.
	fnBlocking := make(chan struct{})
	go func() {
		// Signal the main goroutine that fn is about to call Acquire.
		close(fnBlocking)
		cancel()
	}()

	<-fnBlocking

	err := r.Do(ctx, func(innerCtx context.Context, _ int) error {
		attempts.Add(1)
		return tt.Acquire(innerCtx, hmenum.CommandPriorityHigh)
	})

	// Must propagate context.Canceled — not an "exhausted" wrapper.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// The retrier must not have retried after the ctx error.
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts=%d, want 1 (no retry after ctx cancel)", n)
	}
}

// ─── A2. Throttle-Close during Wait propagates ErrThrottleClosed ─────────────

// TestRetryThrottleClosePropagatesToRetrier verifies that when Close() is
// called on the throttle while fn is blocked in Acquire, the retrier receives
// ErrThrottleClosed and — because that error is not in the non-retryable set —
// retries until MaxAttempts is exhausted.
//
// The test uses a freshly-closed throttle so every Acquire call in fn returns
// ErrThrottleClosed immediately; this lets us count exactly MaxAttempts calls.
func TestRetryThrottleClosePropagatesToRetrier(t *testing.T) {
	t.Parallel()

	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	tt.Close() // Close immediately — every Acquire returns ErrThrottleClosed.

	const maxAttempts = 3
	var attempts atomic.Int32
	r := NewRetrier(RetryConfig{
		MaxAttempts: maxAttempts,
		Initial:     time.Millisecond,
		Max:         10 * time.Millisecond,
		Multiplier:  2,
	})

	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		attempts.Add(1)
		return tt.Acquire(context.Background(), hmenum.CommandPriorityHigh)
	})

	// Must be an exhaustion wrapper that wraps ErrThrottleClosed.
	if err == nil {
		t.Fatal("expected error after exhaustion, got nil")
	}
	if !errors.Is(err, ErrThrottleClosed) {
		t.Errorf("expected error chain to contain ErrThrottleClosed, got %v", err)
	}
	if n := attempts.Load(); n != maxAttempts {
		t.Errorf("attempts=%d, want %d (all MaxAttempts used)", n, maxAttempts)
	}
}

// ─── A3. Successful Acquire — fn runs, Retry-Counter at 1 ────────────────────

// TestRetryThrottleSuccessfulAcquire verifies the happy path: when the throttle
// has a free permit, fn's Acquire succeeds on the first attempt, fn returns nil,
// and the retrier records exactly one attempt.
func TestRetryThrottleSuccessfulAcquire(t *testing.T) {
	t.Parallel()

	tt := NewThrottle(ThrottleConfig{MaxInFlight: 2})

	var attempts atomic.Int32
	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     10 * time.Millisecond,
		Max:         100 * time.Millisecond,
	})

	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		attempts.Add(1)
		if acqErr := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); acqErr != nil {
			return acqErr
		}
		tt.Release()
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error on successful acquire, got %v", err)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts=%d, want 1", n)
	}
}

// ─── A4. Throttle-ErrSuperseded triggers a retry ─────────────────────────────

// TestRetryThrottleSupersededErrorCausesRetry verifies that when Acquire
// returns ErrSuperseded (a non-terminal, retry-eligible error) the Retrier
// re-calls fn. The test uses a fabricated first-call purge: the first fn call
// sets up a queued waiter and immediately purges it (simulating a newer command
// for the same address), then the second call finds a free permit and succeeds.
//
// This covers the case where the throttle signals "a newer command superseded
// this queued Acquire" which the retrier should treat as transient and retry.
func TestRetryThrottleSupersededErrorCausesRetry(t *testing.T) {
	t.Parallel()

	// MaxInFlight=1 — first Acquire succeeds; second will queue.
	tt := NewThrottle(ThrottleConfig{MaxInFlight: 1})
	const addr = "BidCos-RF.ABC123:1"

	var attempts atomic.Int32
	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     time.Millisecond,
		Max:         10 * time.Millisecond,
		Multiplier:  2,
	})

	// Holder goroutine: occupy the only permit, release after the first fn call
	// queued its waiter.
	holderAcquired := make(chan struct{})
	holderRelease := make(chan struct{})
	go func() {
		if err := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); err != nil {
			return
		}
		close(holderAcquired)
		<-holderRelease
		tt.Release()
	}()
	<-holderAcquired

	// fn on attempt 1: call AcquireFor (queues behind holder), then immediately
	// purge the address so the waiter receives ErrSuperseded.
	// fn on attempt 2+: holder released; free permit available; succeed.
	firstAttempt := true
	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		n := attempts.Add(1)
		if firstAttempt {
			firstAttempt = false
			// Start AcquireFor in a goroutine so we can purge while it waits.
			acquireDone := make(chan error, 1)
			go func() {
				acquireDone <- tt.AcquireFor(context.Background(), hmenum.CommandPriorityHigh, addr)
			}()
			// Wait for the waiter to enter the queue.
			deadline := time.Now().Add(500 * time.Millisecond)
			for tt.Waiting() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			// Purge the address — triggers ErrSuperseded for the waiter.
			tt.Purge(addr)
			acqErr := <-acquireDone
			if !errors.Is(acqErr, ErrSuperseded) {
				return errors.New("unexpected: AcquireFor did not return ErrSuperseded")
			}
			// Release the holder so attempt 2 can succeed.
			close(holderRelease)
			return ErrSuperseded // return the supersede error so the retrier retries
		}
		// Subsequent attempts: permit now free.
		if acqErr := tt.Acquire(context.Background(), hmenum.CommandPriorityHigh); acqErr != nil {
			return acqErr
		}
		tt.Release()
		_ = n
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil on second attempt, got %v", err)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("attempts=%d, want 2 (1 superseded + 1 success)", n)
	}
}
