// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_deep_test.go — edge cases for §6.7 / P0-2.
//
// Existing tests already cover:
// TestHistoryRingCaps, TestPipelineClassifyOverridesFailureReason,
// TestExponentialBackoff, TestExponentialBackoffSaturates,
// TestSuccessAfterFailuresResetsConsecutive, TestFailedRunBumpsConsecutiveFailures,
// TestStateZeroValueForUnknownInterface, TestNextRetryAfterIsZeroBeforeFirstAttempt,
// TestResetAttemptsZeroesConsecutive, TestRecoveryAttemptCounter*,
// TestRecoveryAttemptCap*, TestRecoveryUnlimitedAttempts*,
// TestRecoveryPipeline*, TestRecoveryConcurrent*.
//
// This file adds the missing classification, history, and backoff edge cases.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// newDeepCoord builds a coordinator with a high cap so individual tests
// can run many pipelines without hitting the exhaustion gate.
func newDeepCoord(t *testing.T) *ConnectionRecoveryCoordinator {
	t.Helper()
	return NewConnectionRecoveryCoordinatorWithLimit("deep", events.NewBus(), 0)
}

// classifyByErrors is a typical caller-supplied Classify closure: it maps
// hmerr.ErrAuthFailure → FailureReasonAuth, hmerr.ErrNoConnection →
// FailureReasonNetwork, context.DeadlineExceeded → FailureReasonTimeout,
// context.Canceled → nil (keep default / no override), and anything
// else → FailureReasonUnknown. This mirrors the pattern a real backend
// would wire.
func classifyByErrors(err error) *hmenum.FailureReason {
	if errors.Is(err, context.Canceled) {
		return nil // signal: "keep default"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return new(hmenum.FailureReasonTimeout)
	}
	if errors.Is(err, hmerr.ErrAuthFailure) {
		return new(hmenum.FailureReasonAuth)
	}
	if errors.Is(err, hmerr.ErrNoConnection) {
		return new(hmenum.FailureReasonNetwork)
	}
	return new(hmenum.FailureReasonUnknown)
}

// ─── Cluster A: Pipeline.Classify edge cases ──────────────────────────────────

// TestClassifyDistinguishesAuthFailureFromTransport verifies that a
// caller-supplied Classify closure can map hmerr.ErrAuthFailure to
// FailureReasonAuth and hmerr.ErrNoConnection to FailureReasonNetwork,
// keeping the two distinct in the history ring.
func TestClassifyDistinguishesAuthFailureFromTransport(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	makeIface := func(name string, cause error) {
		pipeline := []Pipeline{{
			Stage:    hmenum.RecoveryStageDetecting,
			Run:      func(_ context.Context) error { return cause },
			Classify: classifyByErrors,
		}}
		c.Run(context.Background(), name, pipeline)
	}

	makeIface("auth-iface", hmerr.ErrAuthFailure)
	makeIface("net-iface", hmerr.ErrNoConnection)

	authHist := c.History("auth-iface")
	if len(authHist) != 1 {
		t.Fatalf("auth-iface history len=%d", len(authHist))
	}
	if authHist[0].Reason != hmenum.FailureReasonAuth {
		t.Fatalf("auth-iface reason=%v, want %v", authHist[0].Reason, hmenum.FailureReasonAuth)
	}

	netHist := c.History("net-iface")
	if len(netHist) != 1 {
		t.Fatalf("net-iface history len=%d", len(netHist))
	}
	if netHist[0].Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("net-iface reason=%v, want %v", netHist[0].Reason, hmenum.FailureReasonNetwork)
	}
	if authHist[0].Reason == netHist[0].Reason {
		t.Fatal("auth and network reasons must be distinct")
	}
}

// TestClassifyTimeoutMapsToTimeoutReason verifies that context.DeadlineExceeded
// surfaced through a caller's Classify closure records FailureReasonTimeout in
// the history ring.
func TestClassifyTimeoutMapsToTimeoutReason(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	pipeline := []Pipeline{{
		Stage:    hmenum.RecoveryStageDetecting,
		Run:      func(_ context.Context) error { return context.DeadlineExceeded },
		Classify: classifyByErrors,
	}}
	c.Run(context.Background(), "timeout-iface", pipeline)

	hist := c.History("timeout-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonTimeout {
		t.Fatalf("reason=%v, want %v", hist[0].Reason, hmenum.FailureReasonTimeout)
	}
}

// TestClassifyContextCanceledFallsBackToInternal verifies the production
// policy: when Classify returns nil (e.g., because the caller treats
// context.Canceled as "not a real failure reason"), the coordinator falls
// back to FailureReasonInternal. This matches the guard in runInternal:
// `if r := step.Classify(err); r != nil { reason = *r }`.
func TestClassifyContextCanceledFallsBackToInternal(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		// Step returns context.Canceled as a hard error (caller decided it
		// should count as failure, not a silent skip).
		Run: func(_ context.Context) error { return context.Canceled },
		// Classify returns nil for Canceled → coordinator uses FailureReasonInternal.
		Classify: func(err error) *hmenum.FailureReason {
			if errors.Is(err, context.Canceled) {
				return nil // signal: "keep default"
			}
			return new(hmenum.FailureReasonUnknown)
		},
	}}
	c.Run(context.Background(), "cancel-iface", pipeline)

	hist := c.History("cancel-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonInternal {
		t.Fatalf("reason=%v, want %v (fallback to internal)", hist[0].Reason, hmenum.FailureReasonInternal)
	}
}

// TestClassifyUnknownErrorMapsToGenericReason verifies that an arbitrary
// error not matching any known sentinel is classified as FailureReasonUnknown
// by a standard Classify closure, and that the coordinator stores it
// faithfully.
func TestClassifyUnknownErrorMapsToGenericReason(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	pipeline := []Pipeline{{
		Stage:    hmenum.RecoveryStageDetecting,
		Run:      func(_ context.Context) error { return errors.New("some completely unknown error") },
		Classify: classifyByErrors,
	}}
	c.Run(context.Background(), "unknown-iface", pipeline)

	hist := c.History("unknown-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonUnknown {
		t.Fatalf("reason=%v, want %v", hist[0].Reason, hmenum.FailureReasonUnknown)
	}
}

// TestClassifyNilClassifyKeepsInternalDefault verifies that when Pipeline.Classify
// is nil the coordinator assigns FailureReasonInternal regardless of the
// specific error returned by Run.
func TestClassifyNilClassifyKeepsInternalDefault(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	pipeline := []Pipeline{{
		Stage:    hmenum.RecoveryStageDetecting,
		Run:      func(_ context.Context) error { return hmerr.ErrAuthFailure },
		Classify: nil, // intentionally nil
	}}
	c.Run(context.Background(), "nil-classify-iface", pipeline)

	hist := c.History("nil-classify-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonInternal {
		t.Fatalf("reason=%v, want %v (nil Classify must use default)", hist[0].Reason, hmenum.FailureReasonInternal)
	}
}

// ─── Cluster B: InterfaceRecoveryState evolution ──────────────────────────────

// TestRecoveryStateAttemptsAndConsecutiveIncrementTogether verifies that 5
// consecutive failures leave Attempts == 5, ConsecutiveFailures == 5, and
// LastSuccess as the zero time.
func TestRecoveryStateAttemptsAndConsecutiveIncrementTogether(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("down") },
	}}
	for range 5 {
		c.Run(context.Background(), "iface5", failing)
	}

	state := c.State("iface5")
	if state.Attempts != 5 {
		t.Fatalf("Attempts=%d want 5", state.Attempts)
	}
	if state.ConsecutiveFailures != 5 {
		t.Fatalf("ConsecutiveFailures=%d want 5", state.ConsecutiveFailures)
	}
	if !state.LastSuccess.IsZero() {
		t.Fatalf("LastSuccess must be zero after only failures, got %v", state.LastSuccess)
	}
}

// TestRecoveryStateConsecutiveResetsOnSuccessAttemptsKeepCumulative verifies
// that 3 failures followed by 1 success leaves Consecutive == 0, Attempts ==
// 4 (cumulative, counts the successful run too), and LastSuccess non-zero.
func TestRecoveryStateConsecutiveResetsOnSuccessAttemptsKeepCumulative(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("nope") },
	}}
	success := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return nil },
	}}

	for range 3 {
		c.Run(context.Background(), "iface-mix", failing)
	}
	c.Run(context.Background(), "iface-mix", success)

	state := c.State("iface-mix")
	if state.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures=%d want 0 after success", state.ConsecutiveFailures)
	}
	if state.Attempts != 4 {
		t.Fatalf("Attempts=%d want 4 (cumulative including success)", state.Attempts)
	}
	if state.LastSuccess.IsZero() {
		t.Fatal("LastSuccess must be non-zero after a successful run")
	}
}

// TestRecoveryStateNextRetryAfterIsInTheFuture verifies that after a failure
// the NextRetryAfter field is strictly after now (uses real time, which is
// fine because baseDelay is 5 s and the test runs in well under 1 s).
func TestRecoveryStateNextRetryAfterIsInTheFuture(t *testing.T) {
	t.Parallel()
	c := newDeepCoord(t)

	c.Run(context.Background(), "future-iface", []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("offline") },
	}})

	before := time.Now()
	state := c.State("future-iface")
	if !before.Before(state.NextRetryAfter) {
		t.Fatalf("NextRetryAfter=%v is not after now=%v", state.NextRetryAfter, before)
	}
}

// ─── Cluster C: Backoff schedule ─────────────────────────────────────────────

// TestNextRetryDelayMonotonicallyNonDecreasing verifies that the delay is
// non-decreasing as consecutive failures grow, and that it eventually
// saturates at the configured cap.
func TestNextRetryDelayMonotonicallyNonDecreasing(t *testing.T) {
	t.Parallel()

	base := 100 * time.Millisecond
	capMax := 3200 * time.Millisecond

	c := NewConnectionRecoveryCoordinatorWithLimit("mono", events.NewBus(), 0)
	c.SetBackoff(base, capMax)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("x") },
	}}

	var prev time.Duration
	for i := range 20 {
		c.Run(context.Background(), "mono-iface", failing)
		d := c.NextRetryDelay("mono-iface")
		if d < prev {
			t.Fatalf("delay decreased at failure %d: prev=%v now=%v", i+1, prev, d)
		}
		prev = d
	}
	// After 20 failures the delay must be at the cap.
	if prev != capMax {
		t.Fatalf("delay did not saturate at cap: got=%v want=%v", prev, capMax)
	}
}

// TestNextRetryDelayFirstAttemptEqualsBase verifies that for one failure the
// returned delay equals the configured base delay (no doubling yet).
func TestNextRetryDelayFirstAttemptEqualsBase(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("base", events.NewBus(), 0)
	c.SetBackoff(7*time.Second, 5*time.Minute)

	c.Run(context.Background(), "base-iface", []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("x") },
	}})

	if got := c.NextRetryDelay("base-iface"); got != 7*time.Second {
		t.Fatalf("after 1 failure delay=%v want 7s (base)", got)
	}
}

// TestNextRetryDelayIsDeterministic verifies that two back-to-back calls to
// NextRetryDelay with the same state return the same value (no random jitter
// in the production formula). If jitter is ever added, this test documents
// the expected bounds so it must be updated together with the production code.
func TestNextRetryDelayIsDeterministic(t *testing.T) {
	t.Parallel()

	c := newDeepCoord(t)
	c.SetBackoff(time.Second, time.Minute)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("x") },
	}}
	for range 3 {
		c.Run(context.Background(), "det-iface", failing)
	}

	d1 := c.NextRetryDelay("det-iface")
	d2 := c.NextRetryDelay("det-iface")
	if d1 != d2 {
		t.Fatalf("NextRetryDelay is non-deterministic: first=%v second=%v", d1, d2)
	}
}

// ─── Cluster D: HistoryEntry ring buffer ─────────────────────────────────────

// TestHistoryRingBufferKeepsMostRecent20 records 30 failures and asserts that
// History returns exactly 20 entries — the most recent ones.
func TestHistoryRingBufferKeepsMostRecent20(t *testing.T) {
	t.Parallel()

	c := newDeepCoord(t)

	// Use a counter so each pipeline entry carries a unique marker via a
	// different Classify return value. We distinguish the most-recent batch
	// by the FailureReason stored in the history entry.
	const total = 30

	// 10 runs with FailureReasonNetwork (will be dropped).
	for range 10 {
		c.Run(context.Background(), "ring30", []Pipeline{{
			Stage:    hmenum.RecoveryStageDetecting,
			Run:      func(_ context.Context) error { return errors.New("old") },
			Classify: func(_ error) *hmenum.FailureReason { return new(hmenum.FailureReasonNetwork) },
		}})
	}
	// 20 runs with FailureReasonAuth (kept).
	for range 20 {
		c.Run(context.Background(), "ring30", []Pipeline{{
			Stage:    hmenum.RecoveryStageDetecting,
			Run:      func(_ context.Context) error { return errors.New("new") },
			Classify: func(_ error) *hmenum.FailureReason { return new(hmenum.FailureReasonAuth) },
		}})
	}

	_ = total
	hist := c.History("ring30")
	if len(hist) != historySize {
		t.Fatalf("history len=%d want %d (ring capped)", len(hist), historySize)
	}
	// All 20 retained entries must be from the Auth phase (most recent).
	for i, h := range hist {
		if h.Reason != hmenum.FailureReasonAuth {
			t.Fatalf("entry[%d].Reason=%v, want Auth (oldest entries should be dropped)", i, h.Reason)
		}
	}
}

// TestHistoryEntryCarriesFailureReasonFromClassify records one failure with a
// specific FailureReason and verifies that the entry in History carries it.
func TestHistoryEntryCarriesFailureReasonFromClassify(t *testing.T) {
	t.Parallel()

	c := newDeepCoord(t)
	want := hmenum.FailureReasonCircuitBreaker

	c.Run(context.Background(), "reason-iface", []Pipeline{{
		Stage:    hmenum.RecoveryStageDetecting,
		Run:      func(_ context.Context) error { return errors.New("cb open") },
		Classify: func(_ error) *hmenum.FailureReason { return new(want) },
	}})

	hist := c.History("reason-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d want 1", len(hist))
	}
	if hist[0].Reason != want {
		t.Fatalf("Reason=%v want %v", hist[0].Reason, want)
	}
}

// TestHistoryEmptyAfterConstruction verifies that a fresh coordinator returns
// an empty (nil or zero-length) slice from History before any Run call.
func TestHistoryEmptyAfterConstruction(t *testing.T) {
	t.Parallel()

	c := newDeepCoord(t)
	hist := c.History("brand-new")
	if len(hist) != 0 {
		t.Fatalf("History should be empty on a fresh coordinator, got %d entries", len(hist))
	}
}

// ─── Cluster E: Combined integration ─────────────────────────────────────────

// TestRecoveryRunPropagatesProbeErrorAndRecordsState verifies the end-to-end
// contract: a pipeline whose probe step returns an error must (a) return
// RecoveryResultFailed, (b) increment state Attempts and ConsecutiveFailures,
// and (c) append a HistoryEntry with Result == RecoveryResultFailed and a
// non-empty Reason.
func TestRecoveryRunPropagatesProbeErrorAndRecordsState(t *testing.T) {
	t.Parallel()

	c := newDeepCoord(t)
	probeErr := fmt.Errorf("probe: %w", hmerr.ErrNoConnection)

	pipeline := []Pipeline{{
		Stage:    hmenum.RecoveryStageRPCChecking,
		Run:      func(_ context.Context) error { return probeErr },
		Classify: classifyByErrors,
	}}

	result := c.Run(context.Background(), "probe-iface", pipeline)
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("Run returned %v, want RecoveryResultFailed", result)
	}

	state := c.State("probe-iface")
	if state.Attempts != 1 {
		t.Fatalf("Attempts=%d want 1", state.Attempts)
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures=%d want 1", state.ConsecutiveFailures)
	}

	hist := c.History("probe-iface")
	if len(hist) != 1 {
		t.Fatalf("history len=%d want 1", len(hist))
	}
	h := hist[0]
	if h.Result != hmenum.RecoveryResultFailed {
		t.Fatalf("HistoryEntry.Result=%v want RecoveryResultFailed", h.Result)
	}
	if h.Reason == "" {
		t.Fatal("HistoryEntry.Reason must not be empty after a classified failure")
	}
	// The classify closure maps ErrNoConnection → FailureReasonNetwork.
	if h.Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("HistoryEntry.Reason=%v want %v", h.Reason, hmenum.FailureReasonNetwork)
	}
}

// TestRecoveryExhaustedReasonInHistory verifies that when the attempt cap
// is reached the coordinator records a HistoryEntry with
// FailureReasonExhausted (the exhaustion guard fires before any step runs).
func TestRecoveryExhaustedReasonInHistory(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("exhaust", bus, 2)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return errors.New("boom") },
	}}
	// Burn through the cap.
	c.Run(context.Background(), "ex-iface", failing)
	c.Run(context.Background(), "ex-iface", failing)

	// Third call hits the cap — step must NOT run; coordinator records
	// exhaustion in history.
	called := false
	c.Run(context.Background(), "ex-iface", []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run: func(_ context.Context) error {
			called = true
			return nil
		},
	}})

	if called {
		t.Fatal("step must not run when cap is exhausted")
	}

	hist := c.History("ex-iface")
	last := hist[len(hist)-1]
	if last.Reason != hmenum.FailureReasonExhausted {
		t.Fatalf("last history Reason=%v want %v", last.Reason, hmenum.FailureReasonExhausted)
	}
}
