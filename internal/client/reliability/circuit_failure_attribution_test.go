// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestCallerCancellationDoesNotTripTheBreaker pins that the breaker only
// reacts to what the wire told it.
//
// A read pool runs one command in flight, so a view that reads several
// paramsets parks the rest in the throttle queue. When the browser tab that
// asked for them goes away, every parked call unwinds at once with the
// caller's own cancellation — five in a row on the default threshold. None
// of them touched the CCU, so counting them trips the interface OPEN for
// the full reset timeout against a healthy CCU: reads and writes are shed,
// an incident is recorded, and connection recovery re-registers the
// callback.
func TestCallerCancellationDoesNotTripTheBreaker(t *testing.T) {
	t.Parallel()

	const queued = 5
	c := NewCircuit(CircuitConfig{FailureThreshold: queued, ResetTimeout: time.Hour})
	throttle := NewThrottle(ThrottleConfig{MaxInFlight: 1, MaxQueueDepth: queued})
	t.Cleanup(throttle.Close)
	retrier := NewRetrier(RetryConfig{MaxAttempts: 3, Initial: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	holding := make(chan struct{})
	var holdOnce sync.Once

	// The production nesting: the breaker wraps the retrier, the retrier
	// takes a throttle permit per attempt, and the wire call happens under
	// that permit.
	call := func() error {
		return c.DoWithPriority(ctx, "getParamset", hmenum.CommandPriorityLow, func(ctx context.Context) error {
			return retrier.Do(ctx, func(ctx context.Context, _ int) error {
				if err := throttle.Acquire(ctx, hmenum.CommandPriorityLow); err != nil {
					return err
				}
				defer throttle.Release()
				// Whoever wins the single permit keeps it until the
				// context dies, which is what parks the others.
				holdOnce.Do(func() { close(holding) })
				<-ctx.Done()
				return ctx.Err()
			})
		})
	}

	var wg sync.WaitGroup
	errs := make([]error, queued)
	for i := range queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = call()
		}()
	}

	<-holding
	waitForThrottleWaiters(t, throttle, queued-1)
	cancel()
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Fatalf("call %d returned nil, want the caller's cancellation", i)
		}
		if errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
			t.Fatalf("call %d was shed by the breaker: %v", i, err)
		}
	}
	if got := c.State(); got != hmenum.CircuitStateClosed {
		t.Fatalf("state = %v after %d cancelled calls, want CLOSED — no call reached the CCU",
			got, queued)
	}
}

// TestThrottleBackpressureDoesNotTripTheBreaker pins the same rule for the
// throttle's own fail-fast paths. A full queue is the daemon shedding its
// own load and a closed throttle is the daemon shutting down; neither
// observed the wire, so neither may be attributed to the CCU.
func TestThrottleBackpressureDoesNotTripTheBreaker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "queue full", err: ErrThrottleQueueFull},
		{name: "throttle closed", err: ErrThrottleClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := NewCircuit(CircuitConfig{FailureThreshold: 2, ResetTimeout: time.Hour})
			for range 3 {
				//nolint:errcheck // the returned error is the injected one.
				c.Do(context.Background(), "getParamset", func(context.Context) error { return tc.err })
			}
			if got := c.State(); got != hmenum.CircuitStateClosed {
				t.Fatalf("state = %v, want CLOSED — %v is internal backpressure, not a wire failure", got, tc.err)
			}
		})
	}
}

// TestTransportDeadlineStillTripsTheBreaker is the other half of the rule:
// a per-attempt deadline the transport sets on its own derived context is a
// genuine "the CCU did not answer in time", and the breaker must still
// react to it even though the error is a context error.
func TestTransportDeadlineStillTripsTheBreaker(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{FailureThreshold: 2, ResetTimeout: time.Hour})
	for range 2 {
		err := c.Do(context.Background(), "getParamset", func(ctx context.Context) error {
			attempt, cancel := context.WithTimeout(ctx, time.Millisecond)
			defer cancel()
			<-attempt.Done()
			return attempt.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the transport deadline", err)
		}
	}
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("state = %v, want OPEN — a CCU that never answers is a wire failure", got)
	}
}

// waitForThrottleWaiters blocks until want callers are parked in the
// throttle queue, so the cancellation lands on a real backlog rather than
// on a race.
func waitForThrottleWaiters(t *testing.T, throttle *CommandThrottle, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if throttle.Waiting() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("throttle queue depth = %d, want %d parked callers", throttle.Waiting(), want)
}
