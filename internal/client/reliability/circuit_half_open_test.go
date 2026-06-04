// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// P2 Part C: Half-Open single-probe tests for [CircuitBreaker].
//
// The second concurrent caller receives ErrCircuitBreakerOpen until the probe
// settles (success → CLOSED, failure → OPEN).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// errBoom is a test sentinel for simulating backend failures.
var errBoom = errors.New("boom")

// halfOpenBreaker returns a CircuitBreaker already in HALF_OPEN state.
// failure threshold = 1 so a single Do(error) trips it; after advancing
// the clock past resetTimeout the next State()/Do call flips to HALF_OPEN.
func halfOpenBreaker(t *testing.T) *CircuitBreaker {
	t.Helper()
	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     1 * time.Second,
		HalfOpenSuccess:  2,
		Clock:            clk,
	})
	// Trip the breaker.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errBoom
	})
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("setup: expected OPEN, got %s", c.State())
	}
	// Advance past reset window to move to HALF_OPEN on next access.
	tick = tick.Add(2 * time.Second)
	return c
}

// TestHalfOpenAllowsOneProbe verifies that exactly one caller can
// execute its function while the circuit is HALF_OPEN.
func TestHalfOpenAllowsOneProbe(t *testing.T) {
	t.Parallel()
	c := halfOpenBreaker(t)

	// Probe: Do advances state to HALF_OPEN and runs fn.
	// The function blocks on a gate so we can observe concurrency.
	gate := make(chan struct{})
	var probeStarted atomic.Bool
	probeDone := make(chan error, 1)

	go func() {
		probeDone <- c.Do(context.Background(), "setValue", func(_ context.Context) error {
			probeStarted.Store(true)
			<-gate
			return nil
		})
	}()

	// Wait for the probe to start executing inside fn.
	deadline := time.Now().Add(time.Second)
	for !probeStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !probeStarted.Load() {
		t.Fatal("probe goroutine did not start within 1s")
	}

	// A second concurrent caller must be rejected.
	secondErr := c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return nil
	})
	if !errors.Is(secondErr, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("second caller: expected ErrCircuitBreakerOpen, got %v", secondErr)
	}

	// Release the probe — it succeeds.
	close(gate)
	if err := <-probeDone; err != nil {
		t.Fatalf("probe: unexpected error: %v", err)
	}
}

// TestHalfOpenProbeSlotReleasedAfterSuccess verifies that after the
// probe succeeds (and the circuit stays HALF_OPEN waiting for a second
// success), the slot is released so a new probe can enter.
func TestHalfOpenProbeSlotReleasedAfterSuccess(t *testing.T) {
	t.Parallel()
	c := halfOpenBreaker(t)

	// First probe succeeds.
	if err := c.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	// Circuit should still be HALF_OPEN (needs 2 successes).
	if c.State() != hmenum.CircuitStateHalfOpen {
		t.Fatalf("after 1 success expected HALF_OPEN, got %s", c.State())
	}

	// Second probe must be allowed (slot was released after first probe).
	if err := c.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("second probe: %v (expected success)", err)
	}
	// Two consecutive successes → CLOSED.
	if c.State() != hmenum.CircuitStateClosed {
		t.Fatalf("after 2 successes expected CLOSED, got %s", c.State())
	}
}

// TestHalfOpenProbeSlotReleasedAfterFailure verifies that after the
// probe fails (circuit re-opens), the slot is released so the next
// HALF_OPEN cycle can admit a new probe.
func TestHalfOpenProbeSlotReleasedAfterFailure(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clk := func() time.Time { return tick }
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     100 * time.Millisecond,
		HalfOpenSuccess:  1,
		Clock:            clk,
	})

	// Trip the breaker.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error { return errBoom })

	// Advance past reset → next Do flips to HALF_OPEN.
	tick = tick.Add(200 * time.Millisecond)

	// First HALF_OPEN probe fails → back to OPEN.
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error { return errBoom })
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("after probe failure expected OPEN, got %s", c.State())
	}

	// Advance past reset again → second HALF_OPEN.
	tick = tick.Add(200 * time.Millisecond)

	// New probe must succeed (slot was released after failure).
	if err := c.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("second-cycle probe: %v", err)
	}
}

// TestHalfOpenParallelContention verifies under concurrent load that
// exactly one probe runs at a time in HALF_OPEN while all other callers
// receive ErrCircuitBreakerOpen.
func TestHalfOpenParallelContention(t *testing.T) {
	t.Parallel()
	c := halfOpenBreaker(t)

	const goroutines = 20
	gate := make(chan struct{})
	var probeCount atomic.Int32
	var rejectedCount atomic.Int32
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			err := c.Do(context.Background(), "setValue", func(_ context.Context) error {
				probeCount.Add(1)
				<-gate
				return nil
			})
			if errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
				rejectedCount.Add(1)
			}
		}()
	}

	// Wait for probe goroutines to settle.
	time.Sleep(20 * time.Millisecond)

	// Exactly one probe should be in flight; all others rejected.
	if n := probeCount.Load(); n != 1 {
		close(gate)
		t.Fatalf("expected exactly 1 probe in flight, got %d", n)
	}

	close(gate)
	wg.Wait()

	if n := probeCount.Load(); n != 1 {
		t.Fatalf("expected exactly 1 probe total, got %d", n)
	}
	if n := rejectedCount.Load(); n != goroutines-1 {
		t.Fatalf("expected %d rejections, got %d", goroutines-1, n)
	}
}

// TestHalfOpenBypassOpsIgnoreSingleProbeGate verifies that bypass
// operations (init, ping, getVersion) are never subject to the single-
// probe gate, even when a probe is in flight.
func TestHalfOpenBypassOpsIgnoreSingleProbeGate(t *testing.T) {
	t.Parallel()
	c := halfOpenBreaker(t)

	gate := make(chan struct{})
	var probeStarted atomic.Bool
	probeDone := make(chan error, 1)

	go func() {
		probeDone <- c.Do(context.Background(), "setValue", func(_ context.Context) error {
			probeStarted.Store(true)
			<-gate
			return nil
		})
	}()

	deadline := time.Now().Add(time.Second)
	for !probeStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Bypass operations must get through while the probe is in-flight.
	for _, op := range []string{"init", "ping", "getVersion", "system.listMethods", "system.methodHelp"} {
		if err := c.Do(context.Background(), op, func(_ context.Context) error { return nil }); err != nil {
			close(gate)
			<-probeDone
			t.Fatalf("bypass op %q blocked: %v", op, err)
		}
	}

	close(gate)
	<-probeDone
}

// TestHalfOpenResetClearsProbeSlot verifies that [Reset] frees the
// in-flight probe counter so a subsequent probe can enter immediately.
func TestHalfOpenResetClearsProbeSlot(t *testing.T) {
	t.Parallel()
	c := halfOpenBreaker(t)

	// Simulate a probe in flight by advancing the CAS manually.
	// We do this by triggering a real probe and holding the gate.
	gate := make(chan struct{})
	probeDone := make(chan error, 1)
	var probeStarted atomic.Bool
	go func() {
		probeDone <- c.Do(context.Background(), "setValue", func(_ context.Context) error {
			probeStarted.Store(true)
			<-gate
			return nil
		})
	}()

	deadline := time.Now().Add(time.Second)
	for !probeStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Verify that a second caller is blocked.
	secondErr := c.Do(context.Background(), "setValue", func(_ context.Context) error { return nil })
	if !errors.Is(secondErr, hmerr.ErrCircuitBreakerOpen) {
		close(gate)
		<-probeDone
		t.Fatalf("expected ErrCircuitBreakerOpen before Reset, got %v", secondErr)
	}

	// Reset clears the probe slot and forces CLOSED.
	c.Reset()
	if c.State() != hmenum.CircuitStateClosed {
		close(gate)
		<-probeDone
		t.Fatalf("after Reset expected CLOSED, got %s", c.State())
	}

	// Now a new call must go through (circuit is CLOSED after Reset).
	if err := c.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		close(gate)
		<-probeDone
		t.Fatalf("after Reset a normal call must succeed, got %v", err)
	}

	close(gate)
	<-probeDone
}
