// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_concurrent_cancel_test.go — concurrent-cancellation
// and heartbeat-subscriber behaviour for ConnectionRecoveryCoordinator.
//
// Two clusters:
//
// A. Concurrent-Cancellation + Queued-Goroutine
// Two concurrent Run calls on the same interface: first holds the
// per-interface lock, its context is cancelled, the second goroutine
// unblocks and either succeeds or is also cleanly cancelled.
// Verified with -race; no goroutine leaks.
//
// B. Heartbeat _in_failed_state Audit (spec-pin test)
// Python.py) keeps a boolean
// `_in_failed_state` that guards the heartbeat loop. In Go the
// coordinator is purely event-driven: it subscribes to
// HeartbeatTimerFiredEvent and calls triggerRecovery per interface
// regardless of a separate "failed state" flag. The Go equivalent
// is that triggerRecovery is a no-op while recovery is already
// active (alreadyActive guard) or the coordinator is stopped — so
// the heartbeat delivers idempotent signals without needing an extra
// flag. These spec-pin tests document the current Go behaviour and
// explain the deliberate architectural divergence.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ─── Cluster A: Concurrent-Cancellation + Queued-Goroutine ───────────────────

// TestConcurrentCancelFirstRunLetsQueuedRunProceed verifies the full
// lifecycle when two goroutines call Run for the same interface:
//
// 1. Goroutine-1 enters the pipeline stage and holds the per-interface lock.
// 2. Goroutine-2 calls Run for the same interface and blocks on the lock.
// 3. Goroutine-1's context is cancelled while it is inside the pipeline.
// Run returns RecoveryResultCancelled (or RecoveryResultFailed if the
// step returns the ctx error).
// 4. The defer in runInternal closes the done channel, releasing Goroutine-2.
// 5. Goroutine-2 acquires the lock, enters its own pipeline stage, and
// returns RecoveryResultSuccess.
//
// The test uses channels instead of time.Sleep to avoid wallclock coupling
// and verifies correct results without races via the race detector.
func TestConcurrentCancelFirstRunLetsQueuedRunProceed(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("concurrent-cancel", events.NewBus(), 0)

	// firstEntered is closed once Goroutine-1 is inside its pipeline stage.
	firstEntered := make(chan struct{})
	// cancelFirst is used to signal Goroutine-1 to return from its stage.
	var ctxCancelFirst context.CancelFunc

	// Goroutine-1: blocks until its context is cancelled.
	ctxFirst, cancel := context.WithCancel(context.Background())
	ctxCancelFirst = cancel

	var firstResult hmenum.RecoveryResult
	var firstDone sync.WaitGroup
	firstDone.Add(1)

	pipelineFirst := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(ctx context.Context) error {
			// Signal that Goroutine-1 is now inside the stage.
			close(firstEntered)
			// Block until the context is cancelled.
			<-ctx.Done()
			return ctx.Err()
		},
	}}

	go func() {
		defer firstDone.Done()
		firstResult = c.Run(ctxFirst, "HmIP-RF", pipelineFirst)
	}()

	// Wait until Goroutine-1 is inside its stage (holds the per-interface lock
	// through the entire execution of runInternal).
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("Goroutine-1 did not enter its stage within 2 s")
	}

	// Verify that InRecovery reports true for the interface while G1 is active.
	if !c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecovery must be true while Goroutine-1 is inside Run")
	}

	// Goroutine-2: queues up behind Goroutine-1 on the same interface.
	var secondResult hmenum.RecoveryResult
	var secondDone sync.WaitGroup
	secondDone.Add(1)

	pipelineSecond := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil }, // succeeds immediately
	}}

	go func() {
		defer secondDone.Done()
		secondResult = c.Run(context.Background(), "HmIP-RF", pipelineSecond)
	}()

	// Give Goroutine-2 a moment to reach the blocking wait on the existing
	// done channel (the <-existing line in runInternal). A 20 ms pause is
	// sufficient because G1 is still blocked inside its stage.
	time.Sleep(20 * time.Millisecond)

	// Now cancel Goroutine-1's context. This unblocks G1's stage step,
	// which returns ctx.Err(). runInternal records the failure, the defer
	// fires (delete active entry + close done), and G2 unblocks.
	ctxCancelFirst()

	// Wait for both goroutines to finish.
	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() { firstDone.Wait(); close(done1) }()
	go func() { secondDone.Wait(); close(done2) }()

	timeout := time.After(4 * time.Second)
	for done1 != nil || done2 != nil {
		select {
		case <-done1:
			done1 = nil
		case <-done2:
			done2 = nil
		case <-timeout:
			t.Fatal("goroutines did not finish within 4 s — possible goroutine leak")
		}
	}

	// Goroutine-1 must have been cancelled (or failed because ctx.Err() is non-nil).
	if firstResult != hmenum.RecoveryResultCancelled && firstResult != hmenum.RecoveryResultFailed {
		t.Errorf("Goroutine-1 result = %v, want Cancelled or Failed", firstResult)
	}

	// Goroutine-2 ran its own pipeline with a fresh Background context and must succeed.
	if secondResult != hmenum.RecoveryResultSuccess {
		t.Errorf("Goroutine-2 result = %v, want Success", secondResult)
	}

	// After both finish, InRecovery must be false.
	if c.InRecoveryFor("HmIP-RF") {
		t.Error("InRecovery must be false after both goroutines finished")
	}
}

// TestConcurrentCancelBothRunsCleanup verifies that when BOTH goroutines
// are cancelled, there is no goroutine leak and InRecovery returns false.
//
// Goroutine-1 holds the lock. Goroutine-2 waits. Both contexts are
// cancelled before G2 runs its pipeline. G2 must detect ctx.Done() either
// through the stage or through the ctx.Err() check after each stage and
// return a non-success result cleanly.
func TestConcurrentCancelBothRunsCleanup(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("concurrent-cancel-both", events.NewBus(), 0)

	// firstEntered signals that G1 is inside its blocking stage.
	firstEntered := make(chan struct{})

	ctxFirst, cancelFirst := context.WithCancel(context.Background())
	ctxSecond, cancelSecond := context.WithCancel(context.Background())
	defer cancelFirst()
	defer cancelSecond()

	pipelineBlocking := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(ctx context.Context) error {
			select {
			case <-firstEntered:
			default:
				close(firstEntered)
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}}

	// Goroutine-2's pipeline: context is already cancelled when G2 acquires
	// the lock, so the ctx.Err() check after the stage fires even if Run
	// returns nil; OR the stage itself sees ctx.Done().
	pipelineCancelledCtx := []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run: func(ctx context.Context) error {
			// If context is already cancelled, return immediately.
			return ctx.Err()
		},
	}}

	var (
		res1 hmenum.RecoveryResult
		res2 hmenum.RecoveryResult
		wg   sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		res1 = c.Run(ctxFirst, "BidCos-RF", pipelineBlocking)
	}()

	// Wait until G1 is inside its stage before launching G2.
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("G1 did not enter its stage within 2 s")
	}

	go func() {
		defer wg.Done()
		res2 = c.Run(ctxSecond, "BidCos-RF", pipelineCancelledCtx)
	}()

	// Give G2 time to reach the <-existing block.
	time.Sleep(20 * time.Millisecond)

	// Cancel G2 first, while it is still parked on <-existing waiting for
	// G1's done channel. G2 cannot enter its stage until G1 releases the
	// lock, and G1 only releases once cancelFirst fires below. Cancelling
	// G2 first therefore guarantees its context is already done by the time
	// it runs its stage — without this ordering, cancelSecond races the
	// G1-unblock chain and G2's stage can observe a still-live context and
	// return Success.
	cancelSecond()
	// Now unblock G1; its defer closes the done channel and lets G2 proceed
	// into its already-cancelled stage.
	cancelFirst()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("goroutines did not finish within 4 s — possible goroutine leak")
	}

	// Both must be non-success.
	if res1 == hmenum.RecoveryResultSuccess {
		t.Errorf("G1 result = Success, want Cancelled or Failed")
	}
	if res2 == hmenum.RecoveryResultSuccess {
		t.Errorf("G2 result = Success, want Cancelled or Failed")
	}

	// Active map must be clean.
	if c.InRecoveryFor("BidCos-RF") {
		t.Error("InRecovery must be false after both goroutines finished")
	}
}

// ─── Cluster B: Heartbeat _in_failed_state Audit
// ─────────────────────────────
//
// It is set True by _handle_max_retries_reached() (called when attempt count
// is exhausted) and by _on_central_state_changed() for the
// INITIALIZING→FAILED transition (startup failure).
// _on_heartbeat_timer_fired() checks `self._in_failed_state` and returns
// early if it is False — so heartbeat retries only fire in the FAILED state.
//
// Go architectural decision: openccu-loom does NOT maintain an equivalent
// `_in_failed_state` flag. The Go coordinator is purely event-driven: -
// triggerRecovery() is called by the HeartbeatTimerFiredEvent handler. -
// triggerRecovery() already guards against duplicate recoveries via the
// `alreadyActive` check and the `stopped` flag. - The production caller
// (Unit) is responsible for only emitting HeartbeatTimerFiredEvent
// when a failed/degraded recovery is needed. This is intentional: Go avoids a
// mutable boolean that must be kept in sync across concurrent event handlers,
// leaning instead on the existing active-map and stop-flag guards. The
// contract that "heartbeat only triggers recovery in the right state" is
// enforced at the emitter, not at the receiver.

// TestHeartbeatTriggerIsIdempotentWhileActiveRecovery pins the Go
// behaviour: a HeartbeatTimerFiredEvent for an interface that is already
// undergoing recovery is silently dropped (alreadyActive guard).
// This is the Go equivalent of `_in_failed_state` filtering in Python.
func TestHeartbeatTriggerIsIdempotentWhileActiveRecovery(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hb-guard", bus, 0)

	// reached signals that the first recovery is inside its stage.
	reached := make(chan struct{})
	release := make(chan struct{})
	var runCount atomic.Int32

	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			runCount.Add(1)
			select {
			case <-reached:
			default:
				close(reached)
			}
			<-release
			return nil
		},
	}})
	c.Subscribe()
	defer c.Stop()

	// Trigger an initial recovery via ConnectionLostEvent.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-guard",
		InterfaceID: "HmIP-RF",
	})

	// Wait until the first recovery is inside its stage.
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("first recovery did not start within 2 s")
	}

	// Now fire a HeartbeatTimerFiredEvent while the first recovery is still active.
	// This must be silently dropped (alreadyActive guard in triggerRecovery).
	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "hb-guard",
		InterfaceIDs: []string{"HmIP-RF"},
	})

	// Small pause to allow any spurious duplicate goroutine to attempt entry.
	time.Sleep(30 * time.Millisecond)

	// runCount must still be exactly 1: the heartbeat did not start a second run.
	if runCount.Load() != 1 {
		t.Errorf("runCount = %d, want 1 (heartbeat must not duplicate active recovery)", runCount.Load())
	}

	// Release the first recovery and let it finish.
	close(release)
	if !waitFor(t, func() bool { return !c.MetricsInRecovery() }, eventWaitTimeout) {
		t.Fatal("recovery did not finish after release")
	}
}

// TestHeartbeatTriggerStartsRecoveryAfterPreviousCompleted pins the
// positive case: a HeartbeatTimerFiredEvent that arrives after a previous
// recovery has completed triggers a new recovery for the interface.
// This demonstrates that the Go coordinator does NOT require an explicit
// "failed state" flag to allow heartbeat-driven recovery — it is always
// ready to accept a new run once the active slot is free.
func TestHeartbeatTriggerStartsRecoveryAfterPreviousCompleted(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hb-new", bus, 0)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			runCount.Add(1)
			return nil
		},
	}})
	c.Subscribe()
	defer c.Stop()

	// Fire a ConnectionLostEvent to kick off the first recovery.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-new",
		InterfaceID: "CUxD",
	})

	// Wait for it to complete.
	if !waitFor(t, func() bool { return runCount.Load() >= 1 }, eventWaitTimeout) {
		t.Fatal("first recovery did not complete within 2 s")
	}

	// Now fire a HeartbeatTimerFiredEvent — this simulates the CCU heartbeat
	// Retry that
	// FAILED state. In Go the coordinator accepts it unconditionally.
	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "hb-new",
		InterfaceIDs: []string{"CUxD"},
	})

	// A second recovery must start and complete.
	if !waitFor(t, func() bool { return runCount.Load() >= 2 }, eventWaitTimeout) {
		t.Fatalf("heartbeat did not trigger a second recovery; runCount=%d", runCount.Load())
	}
}

// TestHeartbeatIgnoredAfterStop pins the behaviour that heartbeat events
// after coordinator.Stop() do not trigger any recovery, regardless of
// whether `_in_failed_state` would have been true in Python.
//
// In Python this is guarded by `if self._shutdown: return` in
// _on_heartbeat_timer_fired. In Go it is the `stopped` flag in
// triggerRecovery. Both approaches have the same observable effect.
func TestHeartbeatIgnoredAfterStop(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hb-stop", bus, 0)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { runCount.Add(1); return nil },
	}})
	c.Subscribe()

	// Stop before any event arrives.
	c.Stop()

	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "hb-stop",
		InterfaceIDs: []string{"HmIP-RF"},
	})

	time.Sleep(40 * time.Millisecond)
	if runCount.Load() != 0 {
		t.Errorf("runCount = %d after Stop, want 0", runCount.Load())
	}
}

// TestHeartbeatRevivesExhaustedInterface is the D-10 regression tripwire.
// Mirrors `recovery_state.attempt_count = MAX_RECOVERY_ATTEMPTS - 1` in the
// reference _heartbeat_loop: once a lane is fully exhausted, a subsequent
// HeartbeatTimerFiredEvent must grant exactly one additional run so the
// coordinator can climb back out of FAILED when the CCU finally comes back.
// Without the floor at maxAttempts-1, the exhausted-guard inside
// triggerRecovery rejects every heartbeat tick and the lane is stuck in
// FAILED forever.
func TestHeartbeatRevivesExhaustedInterface(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hb-revive", bus, 2)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			runCount.Add(1)
			return errors.New("simulated")
		},
	}})
	c.Subscribe()
	defer c.Stop()

	// Two failing attempts exhaust the lane.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-revive",
		InterfaceID: "HmIP-RF",
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 1 }, eventWaitTimeout) {
		t.Fatal("first attempt did not run")
	}
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-revive",
		InterfaceID: "HmIP-RF",
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 2 }, eventWaitTimeout) {
		t.Fatal("second attempt did not run")
	}

	// Lane is now exhausted. A vanilla ConnectionLostEvent would be
	// rejected by the exhausted-guard. The heartbeat path must lift
	// the cap by one so the next run lands.
	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "hb-revive",
		InterfaceIDs: []string{"HmIP-RF"},
	})

	if !waitFor(t, func() bool { return runCount.Load() >= 3 }, eventWaitTimeout) {
		t.Fatalf("heartbeat must revive an exhausted lane: runCount=%d, want >=3", runCount.Load())
	}
}

// TestHeartbeatDoesNotResetProgressOnHealthyLane locks the other half of the
// floor-at-cap-minus-one rule: heartbeat on a lane mid-progress must NOT
// erase its accumulated attempt count. A full reset would grant maxAttempts
// fresh tries per tick, which hammers the CCU. The floor only revives an
// already-exhausted lane.
func TestHeartbeatDoesNotResetProgressOnHealthyLane(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("hb-floor", bus, 4)

	var runCount atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			runCount.Add(1)
			return errors.New("simulated")
		},
	}})
	c.Subscribe()
	defer c.Stop()

	// laneIdle reports that the previous recovery has fully drained — its
	// entry in c.active is deleted. Gating each publish on this (not just
	// on runCount) closes a race: the pipeline's Run callback bumps
	// runCount at the start of the stage, but c.active[iid] is only
	// cleared in Run's defer, after the whole pipeline returns. A
	// ConnectionLost published in that window hits the duplicate guard
	// (already_active) and is dropped, so runCount never advances and the
	// lane never reaches the maxAttempts cap.
	laneIdle := func() bool { return !c.InRecoveryFor("HmIP-RF") }

	// One failing attempt — attempts[iid] = 1.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-floor",
		InterfaceID: "HmIP-RF",
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 1 && laneIdle() }, eventWaitTimeout) {
		t.Fatal("first attempt did not run")
	}

	// Heartbeat fires for a non-exhausted lane.
	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "hb-floor",
		InterfaceIDs: []string{"HmIP-RF"},
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 2 && laneIdle() }, eventWaitTimeout) {
		t.Fatal("heartbeat did not run on non-exhausted lane")
	}

	// attempts[iid] is now 2. After two more failing triggers the lane
	// MUST be exhausted (counter at 4, maxAttempts=4). If the heartbeat
	// floor had reset attempts to 3 every time, the cap would never be
	// reached.
	//
	// Publish the two triggers one at a time, waiting for each run before
	// the next: the coordinator drops a ConnectionLost that arrives while a
	// recovery for the same lane is still in-flight (the duplicate guard,
	// see TestSubscribeSkipsDuplicateRecovery), so back-to-back publishes
	// would race and collapse into a single run.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-floor",
		InterfaceID: "HmIP-RF",
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 3 && laneIdle() }, eventWaitTimeout) {
		t.Fatalf("third attempt did not run, got %d", runCount.Load())
	}
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-floor",
		InterfaceID: "HmIP-RF",
	})
	if !waitFor(t, func() bool { return runCount.Load() >= 4 && laneIdle() }, eventWaitTimeout) {
		t.Fatalf("expected 4 runs before exhaustion, got %d", runCount.Load())
	}

	// A vanilla ConnectionLostEvent now must NOT run — lane is exhausted.
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "hb-floor",
		InterfaceID: "HmIP-RF",
	})
	time.Sleep(40 * time.Millisecond)
	if runCount.Load() != 4 {
		t.Fatalf("non-heartbeat trigger must be rejected after exhaustion: runCount=%d, want 4", runCount.Load())
	}
}
