// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// forceOpen trips a fresh CircuitBreaker into OPEN state by exhausting
// the failure threshold. It uses a non-bypass method name so the bypass
// logic does not interfere.
func forceOpen(t *testing.T, cb *CircuitBreaker) {
	t.Helper()
	boom := errors.New("boom")
	for range cb.cfg.FailureThreshold {
		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return boom })
	}
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("forceOpen: state=%s, want OPEN", cb.State())
	}
}

// newOpenCircuit builds a CircuitBreaker and immediately drives it into
// OPEN state.
func newOpenCircuit(t *testing.T) *CircuitBreaker {
	t.Helper()
	cb := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     10 * time.Minute, // never auto-transition during test
		HalfOpenSuccess:  1,
		Clock:            func() time.Time { return time.Unix(0, 0) },
	})
	forceOpen(t, cb)
	return cb
}

// TestCircuitBypassesInit verifies that "init" is executed even when
// the CircuitBreaker is OPEN, and that the function's return value
// propagates to the caller.
func TestCircuitBypassesInit(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "init", func(_ context.Context) error {
		called = true
		return nil
	})
	if !called {
		t.Fatal("init fn was not called while CB is OPEN — bypass missing")
	}
	if err != nil {
		t.Fatalf("init bypass returned unexpected error: %v", err)
	}
}

// TestCircuitBypassesPing verifies that "ping" is executed even when
// the CircuitBreaker is OPEN.
func TestCircuitBypassesPing(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "ping", func(_ context.Context) error {
		called = true
		return nil
	})
	if !called {
		t.Fatal("ping fn was not called while CB is OPEN — bypass missing")
	}
	if err != nil {
		t.Fatalf("ping bypass returned unexpected error: %v", err)
	}
}

// TestCircuitBypassGetVersionAndSystemListMethods checks that
// getVersion and system.listMethods are also bypassed.
func TestCircuitBypassGetVersionAndSystemListMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"getVersion", "system.listMethods", "system.methodHelp"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			cb := newOpenCircuit(t)
			called := false
			err := cb.Do(context.Background(), method, func(_ context.Context) error {
				called = true
				return nil
			})
			if !called {
				t.Fatalf("%s fn was not called while CB is OPEN — bypass missing", method)
			}
			if err != nil {
				t.Fatalf("%s bypass returned unexpected error: %v", method, err)
			}
		})
	}
}

// TestCircuitBypassPropagatesError checks that an error returned by a
// bypassed fn is passed through to the caller unchanged.
func TestCircuitBypassPropagatesError(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	want := errors.New("init transport failure")
	got := cb.Do(context.Background(), "init", func(_ context.Context) error { return want })
	if !errors.Is(got, want) {
		t.Fatalf("bypass error propagation: got %v, want %v", got, want)
	}
}

// TestCircuitBypassDoesNotResetState asserts that a successful bypass
// call does NOT change the breaker state: it must remain OPEN after
// "init" or "ping" returns nil.
func TestCircuitBypassDoesNotResetState(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	_ = cb.Do(context.Background(), "init", func(_ context.Context) error { return nil })
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state after bypass call = %s, want OPEN — bypass must not affect state", cb.State())
	}

	_ = cb.Do(context.Background(), "ping", func(_ context.Context) error { return nil })
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state after ping bypass = %s, want OPEN", cb.State())
	}
}

// TestCircuitBypassFailureDoesNotResetState asserts that a failing
// bypass call also does NOT change the breaker state: it must remain
// OPEN even when "init" returns an error.
func TestCircuitBypassFailureDoesNotResetState(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	_ = cb.Do(context.Background(), "init", func(_ context.Context) error {
		return errors.New("still unreachable")
	})
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state after failing bypass call = %s, want OPEN", cb.State())
	}
}

// TestCircuitNonBypassedStillBlocks asserts that a regular method name
// (e.g. "setValue") is still blocked when the CircuitBreaker is OPEN.
func TestCircuitNonBypassedStillBlocks(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "setValue", func(_ context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Fatal("setValue fn must not be called while CB is OPEN")
	}
	if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("setValue while OPEN: got %v, want ErrCircuitBreakerOpen", err)
	}
}

// ---------------------------------------------------------------------------
// TotalRequests counter (CB total_requests telemetry)
// ---------------------------------------------------------------------------

// TestCircuitTotalRequestsStartsZero asserts the counter is zero on a
// fresh CircuitBreaker.
func TestCircuitTotalRequestsStartsZero(t *testing.T) {
	t.Parallel()
	cb := NewCircuit(CircuitConfig{})
	if n := cb.TotalRequests(); n != 0 {
		t.Fatalf("TotalRequests on new breaker = %d, want 0", n)
	}
}

// TestCircuitTotalRequestsCountsNonBypassed asserts that every
// non-bypassed Do increments TotalRequests regardless of outcome.
func TestCircuitTotalRequestsCountsNonBypassed(t *testing.T) {
	t.Parallel()
	cb := NewCircuit(CircuitConfig{FailureThreshold: 10})

	// Two successes.
	_ = cb.Do(context.Background(), "getValue", func(_ context.Context) error { return nil })
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil })
	if n := cb.TotalRequests(); n != 2 {
		t.Fatalf("after 2 successes: TotalRequests=%d, want 2", n)
	}

	// One failure.
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
	if n := cb.TotalRequests(); n != 3 {
		t.Fatalf("after 1 failure: TotalRequests=%d, want 3", n)
	}
}

// TestCircuitTotalRequestsBypassedNotCounted asserts that bypass
// methods (init, ping, …) do NOT increment TotalRequests.
func TestCircuitTotalRequestsBypassedNotCounted(t *testing.T) {
	t.Parallel()
	cb := NewCircuit(CircuitConfig{FailureThreshold: 1})

	// Bypass methods must not increment.
	for _, m := range []string{"init", "ping", "getVersion", "system.listMethods", "system.methodHelp"} {
		_ = cb.Do(context.Background(), m, func(_ context.Context) error { return nil })
	}
	if n := cb.TotalRequests(); n != 0 {
		t.Fatalf("after 5 bypass calls: TotalRequests=%d, want 0", n)
	}
}

// TestCircuitTotalRequestsCountsRejected asserts that calls rejected
// because the breaker is OPEN also increment TotalRequests.
func TestCircuitTotalRequestsCountsRejected(t *testing.T) {
	t.Parallel()
	cb := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     10 * time.Minute,
		Clock:            func() time.Time { return time.Unix(0, 0) },
	})
	// Trip to OPEN (1 non-bypassed call).
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("x") })
	// Rejected call (OPEN state).
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil })
	if n := cb.TotalRequests(); n != 2 {
		t.Fatalf("after trip + rejected: TotalRequests=%d, want 2", n)
	}
}

// TestCircuitTotalRequestsNotResetByReset asserts that Reset() does
// not clear TotalRequests — it is a lifetime counter.
func TestCircuitTotalRequestsNotResetByReset(t *testing.T) {
	t.Parallel()
	cb := NewCircuit(CircuitConfig{FailureThreshold: 10})
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil })
	_ = cb.Do(context.Background(), "getValue", func(_ context.Context) error { return nil })
	cb.Reset()
	if n := cb.TotalRequests(); n != 2 {
		t.Fatalf("after Reset: TotalRequests=%d, want 2 (lifetime counter must not reset)", n)
	}
}
