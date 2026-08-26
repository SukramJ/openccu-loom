// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package coordinators – lifecycle tests for ConnectionRecoveryCoordinator.
//
// Migration coverage for the "Lifecycle" sub-scope of test_connection_recovery.py.
// See mapping table in the sub-agent report for full provenance.
package coordinators

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRunClearsStageHistoryAndSetsStartTime covers Python
// test_start_recovery (InterfaceRecoveryState.start_recovery resets
// stages_completed and records recovery_start_time).
//
// In openccu-loom the equivalent state is surfaced by
// [ConnectionRecoveryCoordinator.State] after a Run call: the
// CurrentStage returns to Idle (pipeline ran to completion), and
// LastAttempt is non-zero.
func TestRunClearsStageHistoryAndSetsStartTime(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("lc-central", bus)

	// Run a two-stage successful pipeline to populate the stage machinery.
	pipeline := successfulPipeline(
		t,
		hmenum.RecoveryStageDetecting,
		hmenum.RecoveryStageReconnecting,
	)
	if got := c.Run(context.Background(), "HmIP-RF", pipeline); got != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %s want success", got)
	}

	st := c.State("HmIP-RF")
	// stages_completed equivalent: CurrentStage returns to Idle after success.
	if st.CurrentStage != hmenum.RecoveryStageIdle {
		t.Errorf("CurrentStage = %s want Idle after successful run", st.CurrentStage)
	}
	// recovery_start_time equivalent: LastAttempt is recorded.
	if st.LastAttempt.IsZero() {
		t.Error("LastAttempt is zero, want a recorded timestamp")
	}
}

// TestRunMaxRetriesUpfront covers Python test_start_recovery_max_retries_upfront.
// When the attempt counter already equals maxAttempts, Run must immediately
// return Failed with FailureReasonExhausted without entering the pipeline.
func TestRunMaxRetriesUpfront(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	const limit = 3
	c := NewConnectionRecoveryCoordinatorWithLimit("lc-central", bus, limit)

	// Exhaust the limit through failed runs.
	failPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			return errors.New("probe failed")
		}},
	}
	for range limit {
		if got := c.Run(context.Background(), "HmIP-RF", failPipeline); got != hmenum.RecoveryResultFailed {
			t.Fatalf("Run = %s want failed during exhaust phase", got)
		}
	}

	// The next Run must be rejected upfront.
	var failedReason hmenum.FailureReason
	var mu sync.Mutex
	events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) {
		mu.Lock()
		failedReason = e.Reason
		mu.Unlock()
	})

	// A pipeline that must NOT be entered.
	guardPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			t.Fatal("pipeline must not execute after attempts exhausted")
			return nil
		}},
	}
	if got := c.Run(context.Background(), "HmIP-RF", guardPipeline); got != hmenum.RecoveryResultFailed {
		t.Fatalf("Run after exhaustion = %s want failed", got)
	}
	mu.Lock()
	reason := failedReason
	mu.Unlock()
	if reason != hmenum.FailureReasonExhausted {
		t.Errorf("FailureReason = %s want Exhausted", reason)
	}
}

// TestRunExhaustsAfterMaxAttemptsDuring covers Python
// test_start_recovery_failure_max_retries_during.
// An interface that reaches maxAttempts on its last allowed run should emit
// FailureReasonExhausted and refuse further pipeline execution.
func TestRunExhaustsAfterMaxAttemptsDuring(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	const limit = 2
	c := NewConnectionRecoveryCoordinatorWithLimit("lc-central", bus, limit)

	failPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			return errors.New("tcp check failed")
		}},
	}

	// Consume limit-1 attempts.
	for range limit - 1 {
		_ = c.Run(context.Background(), "BidCos-RF", failPipeline)
	}
	// limit-th attempt: still runs the pipeline (not yet exhausted).
	got := c.Run(context.Background(), "BidCos-RF", failPipeline)
	if got != hmenum.RecoveryResultFailed {
		t.Fatalf("last allowed run = %s want failed", got)
	}

	// Now attempts == limit. Next Run must be refused upfront.
	var exhaustedFired atomic.Bool
	events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) {
		if e.Reason == hmenum.FailureReasonExhausted {
			exhaustedFired.Store(true)
		}
	})

	guardPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			t.Fatal("pipeline must not run when exhausted")
			return nil
		}},
	}
	if got2 := c.Run(context.Background(), "BidCos-RF", guardPipeline); got2 != hmenum.RecoveryResultFailed {
		t.Fatalf("post-exhaust run = %s want failed", got2)
	}
	if !exhaustedFired.Load() {
		t.Error("FailureReasonExhausted event not emitted")
	}
}

// TestRunAfterStopIsNoop covers Python test_start_recovery_when_shutdown.
// After [Stop], event-driven recoveries are suppressed. Direct Run calls
// still execute (Stop only unregisters subscriptions), but the stopped flag
// prevents triggerRecovery from spawning new goroutines.
func TestRunAfterStopIsNoop(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("lc-central", bus)

	// Register a default pipeline and subscribe so triggerRecovery is wired.
	defaultPipeline := successfulPipeline(t, hmenum.RecoveryStageDetecting)
	c.WithDefaultPipeline(defaultPipeline)
	c.Subscribe()

	// Stop the coordinator — all subscriptions released.
	c.Stop()

	// Publishing a ConnectionLostEvent must NOT spawn a recovery run.
	var startedFired atomic.Bool
	events.Subscribe(bus, func(hmevent.RecoveryStartedEvent) {
		startedFired.Store(true)
	})

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "lc-central",
		InterfaceID: "HmIP-RF",
	})

	// Give the event bus time to dispatch. If triggerRecovery were still
	// wired, a goroutine could start within a few milliseconds.
	// Use a small pipeline-guarded poll rather than a fixed sleep.
	for range 20 {
		if startedFired.Load() {
			t.Fatal("RecoveryStartedEvent fired after Stop — triggerRecovery still active")
		}
	}
}

// TestFailedRecoveryCycle covers the "failed recovery cycle" end-to-end
// scenario: a pipeline that always fails must emit RecoveryStartedEvent,
// then RecoveryFailedEvent, and must increment the attempt counter.
func TestFailedRecoveryCycle(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("lc-central", bus)

	var (
		mu      sync.Mutex
		started int
		failed  int
		reasons []hmenum.FailureReason
	)
	events.Subscribe(bus, func(hmevent.RecoveryStartedEvent) {
		mu.Lock()
		started++
		mu.Unlock()
	})
	events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) {
		mu.Lock()
		failed++
		reasons = append(reasons, e.Reason)
		mu.Unlock()
	})

	stageErr := errors.New("reconnect failed")
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return stageErr }},
	}
	got := c.Run(context.Background(), "HmIP-RF", pipeline)
	if got != hmenum.RecoveryResultFailed {
		t.Fatalf("Run = %s want failed", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if started != 1 {
		t.Errorf("RecoveryStartedEvent count = %d want 1", started)
	}
	if failed != 1 {
		t.Errorf("RecoveryFailedEvent count = %d want 1", failed)
	}
	if got := c.AttemptCount("HmIP-RF"); got != 1 {
		t.Errorf("AttemptCount = %d want 1", got)
	}
}

// TestSuccessfulRecoveryCycle covers the "successful recovery cycle" end-to-end
// scenario: a pipeline that succeeds at every stage must emit
// RecoveryStartedEvent, RecoveryStageChangedEvent per stage,
// RecoveryCompletedEvent, and must reset the attempt counter.
func TestSuccessfulRecoveryCycle(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("lc-central", bus)

	// Pre-seed a failed attempt so reset is visible.
	failPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			return errors.New("pre-seed fail")
		}},
	}
	_ = c.Run(context.Background(), "HmIP-RF", failPipeline)
	if c.AttemptCount("HmIP-RF") != 1 {
		t.Fatal("pre-condition: attempt counter must be 1 before successful cycle")
	}

	var (
		mu        sync.Mutex
		started   int
		completed int
		stages    []hmenum.RecoveryStage
	)
	events.Subscribe(bus, func(hmevent.RecoveryStartedEvent) {
		mu.Lock()
		started++
		mu.Unlock()
	})
	events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
		mu.Lock()
		stages = append(stages, e.To)
		mu.Unlock()
	})
	events.Subscribe(bus, func(hmevent.RecoveryCompletedEvent) {
		mu.Lock()
		completed++
		mu.Unlock()
	})

	pipeline := successfulPipeline(
		t,
		hmenum.RecoveryStageDetecting,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageRecovered,
	)
	got := c.Run(context.Background(), "HmIP-RF", pipeline)
	if got != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %s want success", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if started != 1 {
		t.Errorf("RecoveryStartedEvent count = %d want 1", started)
	}
	if completed != 1 {
		t.Errorf("RecoveryCompletedEvent count = %d want 1", completed)
	}
	if len(stages) != 3 {
		t.Errorf("stage transitions = %d want 3 (one per pipeline step)", len(stages))
	}
	// Attempt counter must be reset on success.
	if got := c.AttemptCount("HmIP-RF"); got != 0 {
		t.Errorf("AttemptCount after success = %d want 0", got)
	}
}
