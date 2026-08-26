// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

// connection_recovery_edge_test.go — gap closure for Connection-Recovery.
//
// Covers four clusters not exercised by the existing 7 test files:
//
// A. CircuitBreakerResetter — called on success, not on failure, nil-safe. B.
// InRecovery(id) — per-interface active-recovery predicate. C.
// SetRecorder(nil) — nil-safety guard on the recovery coordinator. D. Backoff
// extreme values — very large consecutive-failure count must cap at maxDelay
// without integer overflow.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// spyCBResetter is a CircuitBreakerResetter spy that records every
// ResetForInterface call.
type spyCBResetter struct {
	calls atomic.Int32
	last  atomic.Value // stores the last interfaceID string
}

func (s *spyCBResetter) ResetForInterface(interfaceID string) {
	s.calls.Add(1)
	s.last.Store(interfaceID)
}

// lastInterface returns the last interfaceID passed to ResetForInterface,
// or the empty string if the resetter was never called.
func (s *spyCBResetter) lastInterface() string {
	v := s.last.Load()
	if v == nil {
		return ""
	}
	return v.(string) //nolint:forcetypeassert // atomic.Value always holds a string here
}

// newEdgeCoord returns a fresh coordinator scoped to "edge-central" with the
// attempt cap disabled (0 == unlimited) so individual test cases are
// independent of each other's run counts.
func newEdgeCoord(t *testing.T) *ConnectionRecoveryCoordinator {
	t.Helper()
	return NewConnectionRecoveryCoordinatorWithLimit("edge-central", events.NewBus(), 0)
}

// ─── Cluster A: CircuitBreakerResetter ────────────────────────────────────────

// TestCBResetterCalledOnSuccess verifies that WithCircuitBreakerResetter
// wires the resetter and that it is called exactly once per successful Run,
// with the correct interfaceID.
func TestCBResetterCalledOnSuccess(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	spy := &spyCBResetter{}
	c.WithCircuitBreakerResetter(spy)

	result := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})

	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %v, want success", result)
	}
	if got := spy.calls.Load(); got != 1 {
		t.Errorf("ResetForInterface called %d times, want 1", got)
	}
	if got := spy.lastInterface(); got != "HmIP-RF" {
		t.Errorf("last interfaceID = %q, want HmIP-RF", got)
	}
}

// TestCBResetterNotCalledOnFailure verifies that the CB resetter is NOT called
// when a pipeline step returns an error (only success triggers a reset).
func TestCBResetterNotCalledOnFailure(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	spy := &spyCBResetter{}
	c.WithCircuitBreakerResetter(spy)

	result := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return errors.New("rpc down") },
	}})

	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("Run = %v, want failed", result)
	}
	if got := spy.calls.Load(); got != 0 {
		t.Errorf("ResetForInterface called %d times on failure, want 0", got)
	}
}

// TestCBResetterNilSafe verifies that a nil resetter (never calling
// WithCircuitBreakerResetter) does not cause a panic on a successful run.
func TestCBResetterNilSafe(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t) // no WithCircuitBreakerResetter call
	result := c.Run(context.Background(), "BidCos-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})

	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %v, want success (nil resetter must not panic)", result)
	}
}

// TestCBResetterReplacedByNilReverts verifies that passing nil to
// WithCircuitBreakerResetter after a non-nil resetter disables future calls.
func TestCBResetterReplacedByNilReverts(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	spy := &spyCBResetter{}
	c.WithCircuitBreakerResetter(spy)

	// First run — resetter in place, should be called.
	c.Run(context.Background(), "CUxD", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})
	if spy.calls.Load() != 1 {
		t.Fatalf("setup: first run must call resetter once, got %d", spy.calls.Load())
	}

	// Replace with nil — subsequent success must NOT trigger the old spy.
	c.WithCircuitBreakerResetter(nil)
	c.Run(context.Background(), "CUxD", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})

	// Spy still at 1 — nil replacement must have suppressed the second call.
	if spy.calls.Load() != 1 {
		t.Errorf("resetter called after nil replacement: calls=%d, want 1", spy.calls.Load())
	}
}

// ─── Cluster B: InRecovery(interfaceID) ──────────────────────────────────────

// TestInRecoveryFalseBeforeAnyRun verifies that InRecovery returns false for
// any interface before any recovery has been started.
func TestInRecoveryFalseBeforeAnyRun(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	if c.InRecoveryFor("HmIP-RF") {
		t.Fatal("InRecovery must be false before any Run")
	}
	// Unknown interface — also false.
	if c.InRecoveryFor("does-not-exist") {
		t.Fatal("InRecovery must be false for unknown interface")
	}
}

// TestInRecoveryTrueWhileRunning verifies that InRecovery(id) returns true
// while a Run call is in progress for that interface, and false after it
// completes.
func TestInRecoveryTrueWhileRunning(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)

	reached := make(chan struct{})
	proceed := make(chan struct{})

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			close(reached)
			<-proceed
			return nil
		},
	}}

	done := make(chan hmenum.RecoveryResult, 1)
	go func() {
		done <- c.Run(context.Background(), "HmIP-RF", pipeline)
	}()

	// Wait until the stage is entered.
	select {
	case <-reached:
	case <-time.After(eventWaitTimeout):
		t.Fatal("pipeline did not enter stage")
	}

	if !c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecovery must be true while a Run is in progress")
	}
	// A different interface must still be false.
	if c.InRecoveryFor("BidCos-RF") {
		t.Error("InRecovery must be false for a different interface")
	}

	close(proceed)

	select {
	case res := <-done:
		if res != hmenum.RecoveryResultSuccess {
			t.Fatalf("Run = %v, want success", res)
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("Run did not complete")
	}

	// After completion, must be false again.
	if c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecovery must be false after Run completes")
	}
}

// TestInRecoveryRemainsAccurateAfterFailure verifies that InRecovery returns
// false after a failed run (the active-recovery entry is cleaned up on
// failure, not only on success).
func TestInRecoveryRemainsAccurateAfterFailure(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return errors.New("down") },
	}})

	if c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecovery must be false after a failed Run (defer cleans up active map)")
	}
}

// ─── Cluster C: SetRecorder(nil) nil-safety ──────────────────────────────────

// TestRecoveryCoordinatorSetRecorderNilFallsBackToNoop verifies that passing
// nil to SetRecorder on the ConnectionRecoveryCoordinator does not panic and
// that the coordinator keeps running normally (using a NoopRecorder
// internally).
//
// Mirrors the same nil-guard contract tested for HubCoordinator and
// LinkCoordinator in instrument_test.go (SetRecorder(nil)).
func TestRecoveryCoordinatorSetRecorderNilFallsBackToNoop(t *testing.T) {
	t.Parallel()

	c := newEdgeCoord(t)
	// Must not panic.
	c.SetRecorder(nil)

	result := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})

	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run after SetRecorder(nil) = %v, want success", result)
	}
}

// ─── Cluster D: Backoff extreme values ───────────────────────────────────────

// TestBackoffVeryLargeConsecutiveFailuresCapped verifies that with a very
// large number of consecutive failures (e.g. 100) the computed delay is
// exactly maxDelay and does not overflow.
func TestBackoffVeryLargeConsecutiveFailuresCapped(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("backoff-cap", events.NewBus(), 0)
	base := 5 * time.Second
	capMax := 60 * time.Second
	c.SetBackoff(base, capMax)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("offline") },
	}}

	// Drive 100 consecutive failures.
	for range 100 {
		c.Run(context.Background(), "iface-cap", failing)
	}

	got := c.NextRetryDelay("iface-cap")
	if got != capMax {
		t.Fatalf("NextRetryDelay after 100 failures = %v, want %v (cap); possible overflow", got, capMax)
	}
}

// TestBackoffDoesNotOverflowDuration verifies that the exponential
// doubling loop never produces a negative duration (which would indicate
// signed-integer overflow on large shift counts).
func TestBackoffDoesNotOverflowDuration(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("overflow", events.NewBus(), 0)
	// Use a very small base with the default max. The doubling loop must
	// saturate, never wrap around to a negative value.
	c.SetBackoff(time.Nanosecond, defaultMaxRetryDelay)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("down") },
	}}

	// 63 failures would overflow a signed int64 if the loop does not cap.
	for range 63 {
		c.Run(context.Background(), "overflow-iface", failing)
	}

	got := c.NextRetryDelay("overflow-iface")
	if got < 0 {
		t.Fatalf("NextRetryDelay is negative (%v) — integer overflow in backoff loop", got)
	}
	if got != defaultMaxRetryDelay {
		t.Fatalf("NextRetryDelay = %v, want %v (saturated at max)", got, defaultMaxRetryDelay)
	}
}
