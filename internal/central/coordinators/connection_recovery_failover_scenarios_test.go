// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// connection_recovery_failover_scenarios_test.go — coordinator recovery /
// failover scenario tests that close genuine gaps in the existing suite.
//
// Gap investigation summary (production code read before writing each test):
//
// 1. Circuit-recovery → device-refresh chain:
//    The DATA_LOADING stage in the production recovery pipeline (ccu_wiring.go)
//    calls unit.Recovery.RefreshHubDataAfterRecovery(), which reloads hub
//    entities (sysvars / programs / system-update) but NOT device descriptions.
//    RefreshDeviceDescriptionsAndCreateMissingDevices is only invoked during
//    initial bring-up (DevicePipeline.IngestFromBackend) and via the
//    DeviceReloaderAdapter WebSocket command path. It is NOT wired into the
//    recovery pipeline. Tests for this chain would be aspirational — not
//    testing wired production behaviour — so they are skipped per the task
//    instructions. The gap is noted here as a design observation.
//
// 2. Readiness-gate re-entry after mid-life reconnect:
//    The production RECONNECTING stage (ccu_wiring.go:743-754) calls
//    WaitForCCUReady before invoking backend.Reconnect. This means a
//    mid-life reconnect re-enters the same readiness gate used at boot time.
//    The coordinator models this as an injectable RecoveryStep so tests can
//    verify the step is invoked and gates correctly without a live CCU.
//
// 3. Multi-interface concurrent failure + classification change:
//    Existing tests cover (a) concurrent recoveries on different interfaces,
//    and (b) classification of individual failure reasons. The gap is (c)
//    two interfaces failing concurrently with different reasons, and (d) the
//    same interface's classified reason changing between attempts. Both are
//    now tested here.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ─── Gap 2: Readiness-gate re-entry after mid-life reconnect ─────────────────

// TestReconnectStageGatesOnReadinessProbe verifies that when the RECONNECTING
// pipeline stage is built around a WaitForCCUReady-like probe, the reconnect
// step is not invoked until the probe returns ready, and that a cancelled
// context aborts the wait cleanly.
//
// Production evidence: ccu_wiring.go's Reconnect closure calls
// WaitForCCUReady(rctx, ccForRecovery, …) before backend.Reconnect. This
// test exercises the coordinator's handling of that gating step in isolation
// without a live CCU.
func TestReconnectStageGatesOnReadinessProbe(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("gate-test", bus, 0)

	// ready is closed once the probe should report "CCU ready".
	ready := make(chan struct{})
	var reconnectCalled atomic.Int32

	pipeline := []Pipeline{
		{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(ctx context.Context) error {
				// Gate on readiness — mirrors WaitForCCUReady semantics.
				select {
				case <-ready:
				case <-ctx.Done():
					return ctx.Err()
				}
				reconnectCalled.Add(1)
				return nil
			},
		},
	}

	// Launch recovery in background.
	resultCh := make(chan hmenum.RecoveryResult, 1)
	go func() {
		resultCh <- c.Run(context.Background(), "HmIP-RF", pipeline)
	}()

	// Confirm reconnect is not called immediately (gate blocks).
	time.Sleep(30 * time.Millisecond)
	if reconnectCalled.Load() != 0 {
		t.Fatal("reconnect must not be called before CCU reports ready")
	}

	// Signal readiness — reconnect must now run.
	close(ready)

	select {
	case result := <-resultCh:
		if result != hmenum.RecoveryResultSuccess {
			t.Fatalf("Run = %v, want Success", result)
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("Run did not complete after readiness signal")
	}

	if reconnectCalled.Load() != 1 {
		t.Fatalf("reconnectCalled = %d, want 1", reconnectCalled.Load())
	}
}

// TestReconnectStageContextCancelAbortsReadinessWait verifies that cancelling
// the run context while the RECONNECTING stage is waiting for CCU readiness
// causes the stage to return the ctx error and the pipeline to fail cleanly.
//
// Production evidence: ccu_wiring.go passes rctx to WaitForCCUReady, which
// returns false (cancellation) when rctx is cancelled. The step then returns
// an error so the pipeline transitions to FAILED.
func TestReconnectStageContextCancelAbortsReadinessWait(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("gate-cancel", bus, 0)

	neverReady := make(chan struct{}) // never closed — simulates CCU never becoming ready
	var reconnectCalled atomic.Int32

	pipeline := []Pipeline{
		{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(ctx context.Context) error {
				select {
				case <-neverReady:
					reconnectCalled.Add(1)
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan hmenum.RecoveryResult, 1)
	go func() {
		resultCh <- c.Run(ctx, "BidCos-RF", pipeline)
	}()

	// Confirm recovery is blocked.
	time.Sleep(20 * time.Millisecond)

	// Cancel the context.
	cancel()

	select {
	case result := <-resultCh:
		if result == hmenum.RecoveryResultSuccess {
			t.Fatal("Run must not succeed when CCU never became ready and ctx was cancelled")
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("Run did not return after ctx cancel")
	}

	if reconnectCalled.Load() != 0 {
		t.Fatal("reconnect must not be called when CCU never became ready")
	}
}

// TestReadinessGateRetriableWhenGateRejectsFirstAttempt verifies the
// multi-attempt scenario: the readiness gate rejects the first reconnect
// (CCU not ready), the pipeline fails, and the next recovery attempt
// succeeds once the CCU is ready. Mirrors the re-gate behaviour in
// gatedCentralBringUp where each back-to-waiting cycle re-probes.
func TestReadinessGateRetriableWhenGateRejectsFirstAttempt(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("gate-retry", bus, 0)

	// First call returns "not ready", subsequent calls return "ready".
	var probeCount atomic.Int32
	var reconnectCount atomic.Int32

	pipeline := []Pipeline{
		{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(_ context.Context) error {
				n := probeCount.Add(1)
				if n == 1 {
					// First attempt: CCU not yet ready.
					return errors.New("reconnect: CCU not ready (checkrega.cgi != OK)")
				}
				reconnectCount.Add(1)
				return nil
			},
		},
	}

	// First run fails (CCU not ready).
	result := c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("first run = %v, want Failed", result)
	}
	if reconnectCount.Load() != 0 {
		t.Fatal("reconnect must not have succeeded on first attempt")
	}

	// Second run succeeds (CCU now ready).
	result = c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("second run = %v, want Success", result)
	}
	if reconnectCount.Load() != 1 {
		t.Fatalf("reconnectCount = %d, want 1", reconnectCount.Load())
	}

	// Attempt counter must have been reset by the successful run.
	if got := c.AttemptCount("HmIP-RF"); got != 0 {
		t.Fatalf("AttemptCount = %d after success, want 0", got)
	}
}

// ─── Gap 3a: Two interfaces fail concurrently with different failure reasons ──

// TestTwoInterfacesFailConcurrentlyWithDistinctReasons verifies that two
// interfaces undergoing concurrent recovery, each failing with a different
// error and a distinct Classify closure, record independent FailureReasons in
// their history rings. This is the multi-interface concurrent failure scenario.
func TestTwoInterfacesFailConcurrentlyWithDistinctReasons(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("multi-iface", bus, 0)

	// barrier ensures both recoveries are in-flight simultaneously.
	barrier := make(chan struct{})
	var inFlight atomic.Int32

	makePipeline := func(cause error, classify func(error) *hmenum.FailureReason) []Pipeline {
		return []Pipeline{{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(_ context.Context) error {
				if inFlight.Add(1) == 2 {
					close(barrier)
				}
				<-barrier
				return cause
			},
			Classify: classify,
		}}
	}

	classifyAsAuth := func(_ error) *hmenum.FailureReason {
		r := hmenum.FailureReasonAuth
		return &r
	}
	classifyAsNetwork := func(_ error) *hmenum.FailureReason {
		r := hmenum.FailureReasonNetwork
		return &r
	}

	pipelineRF := makePipeline(hmerr.ErrAuthFailure, classifyAsAuth)
	pipelineBidCos := makePipeline(hmerr.ErrNoConnection, classifyAsNetwork)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.Run(context.Background(), "HmIP-RF", pipelineRF)
	}()
	go func() {
		defer wg.Done()
		c.Run(context.Background(), "BidCos-RF", pipelineBidCos)
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("concurrent recoveries did not complete in time")
	}

	rfHist := c.History("HmIP-RF")
	if len(rfHist) != 1 {
		t.Fatalf("HmIP-RF history len=%d, want 1", len(rfHist))
	}
	if rfHist[0].Reason != hmenum.FailureReasonAuth {
		t.Fatalf("HmIP-RF reason=%v, want Auth", rfHist[0].Reason)
	}

	bidHist := c.History("BidCos-RF")
	if len(bidHist) != 1 {
		t.Fatalf("BidCos-RF history len=%d, want 1", len(bidHist))
	}
	if bidHist[0].Reason != hmenum.FailureReasonNetwork {
		t.Fatalf("BidCos-RF reason=%v, want Network", bidHist[0].Reason)
	}

	// Reasons must be distinct.
	if rfHist[0].Reason == bidHist[0].Reason {
		t.Fatal("concurrent interfaces must record distinct FailureReasons")
	}
}

// TestTwoInterfacesRecoverConcurrentlyOneBeatOneFails verifies the mixed-
// outcome case: two interfaces concurrently, one succeeds, one fails. The
// success clears attempts; the failure increments them. State is independent.
func TestTwoInterfacesRecoverConcurrentlyOneBeatOneFails(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("mixed-outcome", bus, 0)

	barrier := make(chan struct{})
	var entered atomic.Int32

	successPipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			if entered.Add(1) == 2 {
				close(barrier)
			}
			<-barrier
			return nil
		},
	}}
	failPipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			if entered.Add(1) == 2 {
				close(barrier)
			}
			<-barrier
			return errors.New("offline")
		},
	}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.Run(context.Background(), "HmIP-RF", successPipeline)
	}()
	go func() {
		defer wg.Done()
		c.Run(context.Background(), "BidCos-RF", failPipeline)
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("concurrent recoveries did not complete in time")
	}

	// Successful interface: attempt counter cleared.
	if got := c.AttemptCount("HmIP-RF"); got != 0 {
		t.Fatalf("HmIP-RF AttemptCount = %d, want 0 after success", got)
	}
	// Failed interface: attempt counter incremented.
	if got := c.AttemptCount("BidCos-RF"); got != 1 {
		t.Fatalf("BidCos-RF AttemptCount = %d, want 1 after failure", got)
	}
}

// ─── Gap 3b: Classified reason changes between attempts ──────────────────────

// TestClassifiedReasonChangeBetweenAttempts verifies that each recovery
// attempt independently classifies its failure reason and that a changing
// reason (e.g. Timeout on attempt 1, Auth on attempt 2) is faithfully
// recorded in the history ring — one entry per run, with the reason each
// classifier returned at that point.
//
// This tests the scenario where the underlying failure mode shifts between
// recovery cycles (e.g. a CCU that was unreachable by timeout later
// responds with a 401 because it has rebooted with a new session).
func TestClassifiedReasonChangeBetweenAttempts(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("reason-change", bus, 0)

	// Attempt 1: error classifies as Timeout.
	result1 := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return context.DeadlineExceeded },
		Classify: func(err error) *hmenum.FailureReason {
			if errors.Is(err, context.DeadlineExceeded) {
				r := hmenum.FailureReasonTimeout
				return &r
			}
			return nil
		},
	}})
	if result1 != hmenum.RecoveryResultFailed {
		t.Fatalf("attempt 1 result = %v, want Failed", result1)
	}

	// Attempt 2: error classifies as Auth.
	result2 := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return hmerr.ErrAuthFailure },
		Classify: func(err error) *hmenum.FailureReason {
			if errors.Is(err, hmerr.ErrAuthFailure) {
				r := hmenum.FailureReasonAuth
				return &r
			}
			return nil
		},
	}})
	if result2 != hmenum.RecoveryResultFailed {
		t.Fatalf("attempt 2 result = %v, want Failed", result2)
	}

	hist := c.History("HmIP-RF")
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (one entry per run)", len(hist))
	}
	if hist[0].Reason != hmenum.FailureReasonTimeout {
		t.Fatalf("hist[0].Reason = %v, want Timeout", hist[0].Reason)
	}
	if hist[1].Reason != hmenum.FailureReasonAuth {
		t.Fatalf("hist[1].Reason = %v, want Auth", hist[1].Reason)
	}
}

// TestClassifiedReasonTimeoutThenSuccessResetsState verifies the full
// lifecycle: Timeout failure, then Auth failure, then success — consecutive
// failures reset to 0 and last-success timestamp is set.
func TestClassifiedReasonTimeoutThenSuccessResetsState(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("reason-reset", bus, 0)

	failWith := func(iface string, cause error, reason hmenum.FailureReason) {
		c.Run(context.Background(), iface, []Pipeline{{
			Stage: hmenum.RecoveryStageReconnecting,
			Run:   func(_ context.Context) error { return cause },
			Classify: func(_ error) *hmenum.FailureReason {
				r := reason
				return &r
			},
		}})
	}

	failWith("HmIP-RF", context.DeadlineExceeded, hmenum.FailureReasonTimeout)
	failWith("HmIP-RF", hmerr.ErrAuthFailure, hmenum.FailureReasonAuth)

	// Consecutive failures must be 2.
	if s := c.State("HmIP-RF"); s.ConsecutiveFailures != 2 {
		t.Fatalf("ConsecutiveFailures = %d, want 2", s.ConsecutiveFailures)
	}

	// Successful recovery.
	result := c.Run(context.Background(), "HmIP-RF", []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}})
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("success run = %v, want Success", result)
	}

	s := c.State("HmIP-RF")
	if s.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d after success, want 0", s.ConsecutiveFailures)
	}
	if s.LastSuccess.IsZero() {
		t.Fatal("LastSuccess must be non-zero after recovery")
	}
}

// ─── Subscribe: multi-interface concurrent event-driven recovery ──────────────

// TestSubscribeTwoInterfacesFailConcurrentlyViaEvents verifies that when
// ConnectionLostEvents are published for two different interfaces in quick
// succession, both recoveries are triggered and run concurrently.
func TestSubscribeTwoInterfacesFailConcurrentlyViaEvents(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c-multiif", bus, 0)

	var (
		mu        sync.Mutex
		recovered []string
	)

	// Register per-interface pipelines so each interface runs independently.
	for _, id := range []string{"HmIP-RF", "BidCos-RF"} {
		iid := id
		c.WithPipelineFor(iid, []Pipeline{{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(_ context.Context) error {
				mu.Lock()
				recovered = append(recovered, iid)
				mu.Unlock()
				return nil
			},
		}})
	}
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c-multiif",
		InterfaceID: "HmIP-RF",
	})
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c-multiif",
		InterfaceID: "BidCos-RF",
	})

	if !waitFor(t, func() bool {
		mu.Lock()
		n := len(recovered)
		mu.Unlock()
		return n >= 2
	}, eventWaitTimeout) {
		mu.Lock()
		got := recovered
		mu.Unlock()
		t.Fatalf("both recoveries did not complete; got %v", got)
	}

	mu.Lock()
	defer mu.Unlock()
	seen := make(map[string]bool, len(recovered))
	for _, id := range recovered {
		seen[id] = true
	}
	for _, want := range []string{"HmIP-RF", "BidCos-RF"} {
		if !seen[want] {
			t.Errorf("interface %q was not recovered; got %v", want, recovered)
		}
	}
}

// TestSubscribeCentralStateFailed_TriggersAllInterfaces verifies that
// publishing CentralStateChangedEvent with To==Failed fires recovery for
// every interface registered via WithPipelineFor.
func TestSubscribeCentralStateFailed_TriggersAllInterfaces(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c-statefail", bus, 0)

	var mu sync.Mutex
	triggered := make(map[string]int)

	for _, id := range []string{"HmIP-RF", "BidCos-RF", "CUxD"} {
		iid := id
		c.WithPipelineFor(iid, []Pipeline{{
			Stage: hmenum.RecoveryStageReconnecting,
			Run: func(_ context.Context) error {
				mu.Lock()
				triggered[iid]++
				mu.Unlock()
				return nil
			},
		}})
	}
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CentralStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c-statefail",
		To:          hmenum.CentralStateFailed,
	})

	if !waitFor(t, func() bool {
		mu.Lock()
		n := len(triggered)
		mu.Unlock()
		return n >= 3
	}, eventWaitTimeout) {
		mu.Lock()
		got := triggered
		mu.Unlock()
		t.Fatalf("CentralStateFailed did not trigger all interfaces; got %v", got)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"HmIP-RF", "BidCos-RF", "CUxD"} {
		if triggered[id] < 1 {
			t.Errorf("interface %q was not triggered; triggered=%v", id, triggered)
		}
	}
}

// TestSubscribeCentralStateFailedIgnoresOtherCentral verifies that a
// CentralStateChangedEvent for a different central does not trigger any
// recovery on the coordinator.
func TestSubscribeCentralStateFailedIgnoresOtherCentral(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c-mine", bus, 0)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { runCount.Add(1); return nil },
	}})
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CentralStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c-other", // different central
		To:          hmenum.CentralStateFailed,
	})

	time.Sleep(40 * time.Millisecond)
	if runCount.Load() != 0 {
		t.Fatalf("runCount = %d, want 0 (event for wrong central)", runCount.Load())
	}
}

// TestSubscribeCentralStateRunningDoesNotTriggerRecovery ensures that
// CentralStateChangedEvent with To==Running does not fire the recovery
// pipeline (only To==Failed should).
func TestSubscribeCentralStateRunningDoesNotTriggerRecovery(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c-running", bus, 0)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { runCount.Add(1); return nil },
	}})
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CentralStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "c-running",
		To:          hmenum.CentralStateRunning,
	})

	time.Sleep(40 * time.Millisecond)
	if runCount.Load() != 0 {
		t.Fatalf("runCount = %d, want 0 (Running state must not trigger recovery)", runCount.Load())
	}
}
