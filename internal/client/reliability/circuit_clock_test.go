// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Fake-clock timing tests for [CircuitBreaker].
//
// Goal: assert the OPEN→HALF_OPEN transition happens exactly at the
// ResetTimeout boundary — not a nanosecond before (just under the timeout
// must stay OPEN), and at/after it the breaker must allow a half-open probe.
//
// The Clock field on CircuitConfig is a plain func() time.Time, so these
// tests use a mutable tick variable (same pattern as circuit_half_open_test.go
// and circuit_default_test.go) rather than clock.Fake.

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCircuitOpenToHalfOpenExactBoundaryNotBefore verifies that the breaker
// stays OPEN when the clock advances to just under ResetTimeout and switches
// to HALF_OPEN once the clock reaches exactly ResetTimeout.
func TestCircuitOpenToHalfOpenExactBoundaryNotBefore(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	const resetTimeout = 30 * time.Second

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     resetTimeout,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Trip the breaker OPEN.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errBoom
	})
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("setup: expected OPEN after failure, got %s", c.State())
	}
	openedAt := tick

	// Just under the boundary: ResetTimeout - 1 ns. Must stay OPEN.
	tick = openedAt.Add(resetTimeout - 1)
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("1 ns before ResetTimeout: expected OPEN, got %s", got)
	}

	// Exactly at the boundary. Must flip to HALF_OPEN.
	tick = openedAt.Add(resetTimeout)
	if got := c.State(); got != hmenum.CircuitStateHalfOpen {
		t.Fatalf("at ResetTimeout boundary: expected HALF_OPEN, got %s", got)
	}
}

// TestCircuitHalfOpenProbeAllowedAtBoundary verifies that the very first
// Do call after the clock reaches ResetTimeout actually executes fn (i.e.
// the circuit is HALF_OPEN and admits a probe) rather than returning
// ErrCircuitBreakerOpen.
func TestCircuitHalfOpenProbeAllowedAtBoundary(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	const resetTimeout = 30 * time.Second

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     resetTimeout,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Trip the breaker.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errBoom
	})

	// Advance to exactly the timeout boundary.
	tick = tick.Add(resetTimeout)

	// A Do call must go through (HALF_OPEN admits one probe).
	var fnCalled bool
	err := c.Do(context.Background(), "setValue", func(_ context.Context) error {
		fnCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("probe at boundary: unexpected error %v", err)
	}
	if !fnCalled {
		t.Fatal("probe fn was not called — breaker did not enter HALF_OPEN at boundary")
	}
}

// TestCircuitOpenStaysOpenBeforeBoundary verifies that Do calls are rejected
// with ErrCircuitBreakerOpen while the clock sits below ResetTimeout, even
// approaching the boundary asymptotically.
func TestCircuitOpenStaysOpenBeforeBoundary(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	const resetTimeout = 30 * time.Second

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     resetTimeout,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Trip the breaker.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errBoom
	})
	openedAt := tick

	// Sample several sub-boundary offsets.
	subBoundaryOffsets := []time.Duration{
		0,
		1,
		resetTimeout / 2,
		resetTimeout - 1,
	}

	for _, offset := range subBoundaryOffsets {
		tick = openedAt.Add(offset)
		var fnCalled bool
		err := c.Do(context.Background(), "setValue", func(_ context.Context) error {
			fnCalled = true
			return nil
		})
		if fnCalled {
			t.Fatalf("offset=%v (< ResetTimeout): fn was called — breaker should be OPEN", offset)
		}
		if err == nil {
			t.Fatalf("offset=%v: expected error, got nil — breaker should reject", offset)
		}
	}
}

// TestCircuitStateChangeCallbackFiredOnHalfOpen verifies that the OnStateChange
// callback is invoked with (OPEN→HALF_OPEN) when the clock advances past
// ResetTimeout and State() is queried.
func TestCircuitStateChangeCallbackFiredOnHalfOpen(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	const resetTimeout = 30 * time.Second

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     resetTimeout,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Track state transitions fired by the callback.
	type transition struct{ from, to hmenum.CircuitState }
	transitions := make(chan transition, 4)

	c.OnStateChange(func(from, to hmenum.CircuitState) {
		transitions <- transition{from, to}
	})

	// Trip → OPEN (from CLOSED).
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errBoom
	})

	// Drain the CLOSED→OPEN transition.
	select {
	case tr := <-transitions:
		if tr.from != hmenum.CircuitStateClosed || tr.to != hmenum.CircuitStateOpen {
			t.Fatalf("expected CLOSED→OPEN, got %s→%s", tr.from, tr.to)
		}
	case <-time.After(time.Second):
		t.Fatal("CLOSED→OPEN callback did not fire within 1s")
	}

	// Advance past ResetTimeout and trigger the refresh.
	tick = tick.Add(resetTimeout)
	if got := c.State(); got != hmenum.CircuitStateHalfOpen {
		t.Fatalf("expected HALF_OPEN after Advance, got %s", got)
	}

	select {
	case tr := <-transitions:
		if tr.from != hmenum.CircuitStateOpen || tr.to != hmenum.CircuitStateHalfOpen {
			t.Fatalf("expected OPEN→HALF_OPEN callback, got %s→%s", tr.from, tr.to)
		}
	case <-time.After(time.Second):
		t.Fatal("OPEN→HALF_OPEN callback did not fire")
	}
}

// TestCircuitMultipleOpenCyclesResetTimerCorrectly verifies that the
// ResetTimeout is measured from the most recent OPEN time, not from the
// original construction time. After a HALF_OPEN probe fails (re-opens the
// breaker) the new open time must govern the next OPEN→HALF_OPEN transition.
func TestCircuitMultipleOpenCyclesResetTimerCorrectly(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	const resetTimeout = 10 * time.Second

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     resetTimeout,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Cycle 1: trip OPEN.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error { return errBoom })
	openedAt1 := tick

	// Advance past timeout → HALF_OPEN, probe fails → re-OPEN.
	tick = openedAt1.Add(resetTimeout)
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error { return errBoom })
	// The re-open time is now the current tick (the moment the probe failed).
	openedAt2 := tick

	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("after failed probe: expected OPEN, got %s", got)
	}

	// Just under the second reset window (from openedAt2): must stay OPEN.
	tick = openedAt2.Add(resetTimeout - 1)
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("1 ns before second ResetTimeout: expected OPEN, got %s", got)
	}

	// Exactly at the second reset window: must transition to HALF_OPEN.
	tick = openedAt2.Add(resetTimeout)
	if got := c.State(); got != hmenum.CircuitStateHalfOpen {
		t.Fatalf("at second ResetTimeout: expected HALF_OPEN, got %s", got)
	}
}
