// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// connection_recovery_backoff_statemachine_test.go — backoff, pipeline stage
// transitions, max-retries exhaustion, CircuitBreaker subscription wiring,
// and StateMachine transition coverage for ConnectionRecoveryCoordinator.
//
// Covers: backoff table (initial, multiplier, max), multi-interface reconnect
// order, stage pipeline transitions, heartbeat timer handling, max-retries
// exhaustion, CircuitBreaker state-change subscription wiring, ConnectionLost
// subscription, Stop-after-Subscribe, and StateMachine.TransitionTo calls.
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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// stubSM records every TransitionTo call for assertion in tests.
type stubSM struct {
	mu          sync.Mutex
	transitions []hmenum.CentralState
}

func (s *stubSM) TransitionTo(state hmenum.CentralState) error {
	s.mu.Lock()
	s.transitions = append(s.transitions, state)
	s.mu.Unlock()
	return nil
}

func (s *stubSM) all() []hmenum.CentralState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hmenum.CentralState, len(s.transitions))
	copy(out, s.transitions)
	return out
}

// stubCBResetter records ResetForInterface calls.
type stubCBResetter struct {
	mu  sync.Mutex
	ids []string
}

func (r *stubCBResetter) ResetForInterface(id string) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}

func (r *stubCBResetter) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.ids))
	copy(out, r.ids)
	return out
}

// newParityCoord returns an unlimited coordinator (no attempt cap).
func newParityCoord(t *testing.T) (*ConnectionRecoveryCoordinator, *events.Bus) {
	t.Helper()
	bus := events.NewBus()
	return NewConnectionRecoveryCoordinatorWithLimit("parity", bus, 0), bus
}

// simpleFailPipeline returns a single-stage pipeline whose step always fails.
func simpleFailPipeline(stage hmenum.RecoveryStage) []Pipeline {
	return []Pipeline{{
		Stage: stage,
		Run:   func(_ context.Context) error { return errors.New("forced failure") },
	}}
}

// simpleSuccessPipeline returns a single-stage pipeline whose step always succeeds.
func simpleSuccessPipeline(stage hmenum.RecoveryStage) []Pipeline {
	return []Pipeline{{
		Stage: stage,
		Run:   func(_ context.Context) error { return nil },
	}}
}

// ── 1. Backoff: initial delay equals base ─────────────────────────────────────

// TestParityBackoffInitialDelayEqualsBase verifies that NextRetryDelay after
// the first failure equals the configured base delay.
func TestParityBackoffInitialDelayEqualsBase(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	c.SetBackoff(5*time.Second, 60*time.Second)

	c.Run(context.Background(), "base-iface", simpleFailPipeline(hmenum.RecoveryStageDetecting))

	got := c.NextRetryDelay("base-iface")
	if got != 5*time.Second {
		t.Fatalf("NextRetryDelay after 1 failure = %v, want 5s (base)", got)
	}
}

// ── 2. Backoff: multiplier doubles on each failure ────────────────────────────

// TestParityBackoffDoublesOnConsecutiveFailures verifies that the delay
// doubles for each additional consecutive failure.
func TestParityBackoffDoublesOnConsecutiveFailures(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	base := 100 * time.Millisecond
	c.SetBackoff(base, 10*time.Second)

	failing := simpleFailPipeline(hmenum.RecoveryStageDetecting)

	c.Run(context.Background(), "dbl-iface", failing) // consecutive=1 → base
	d1 := c.NextRetryDelay("dbl-iface")

	c.Run(context.Background(), "dbl-iface", failing) // consecutive=2 → base*2
	d2 := c.NextRetryDelay("dbl-iface")

	c.Run(context.Background(), "dbl-iface", failing) // consecutive=3 → base*4
	d3 := c.NextRetryDelay("dbl-iface")

	if d1 != base {
		t.Fatalf("after 1 failure: delay=%v want %v", d1, base)
	}
	if d2 != base*2 {
		t.Fatalf("after 2 failures: delay=%v want %v", d2, base*2)
	}
	if d3 != base*4 {
		t.Fatalf("after 3 failures: delay=%v want %v", d3, base*4)
	}
}

// ── 3. Backoff: saturates at max ─────────────────────────────────────────────

// TestParityBackoffSaturatesAtMax verifies that the delay caps at the configured
// maximum and does not grow beyond it.
func TestParityBackoffSaturatesAtMax(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	c.SetBackoff(100*time.Millisecond, 400*time.Millisecond)

	failing := simpleFailPipeline(hmenum.RecoveryStageDetecting)
	for range 20 {
		c.Run(context.Background(), "sat-iface", failing)
	}
	got := c.NextRetryDelay("sat-iface")
	if got != 400*time.Millisecond {
		t.Fatalf("delay after 20 failures = %v, want 400ms (cap)", got)
	}
}

// ── 4. Stage pipeline transitions ─────────────────────────────────────────────

// TestParityDefaultPipelineStageOrder verifies that DefaultRecoveryPipeline
// walks stages in the expected canonical order and that each stage appears
// exactly once in the emitted RecoveryStageChangedEvent stream.
func TestParityDefaultPipelineStageOrder(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-stages", bus, 0)

	wantOrder := []hmenum.RecoveryStage{
		hmenum.RecoveryStageCooldown,
		hmenum.RecoveryStageTCPChecking,
		hmenum.RecoveryStageRPCChecking,
		hmenum.RecoveryStageWarmingUp,
		hmenum.RecoveryStageStabilityCheck,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageDataLoading,
		hmenum.RecoveryStageRecovered,
	}

	var mu sync.Mutex
	var gotStages []hmenum.RecoveryStage
	unsub := events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
		if e.CentralName != "parity-stages" {
			return
		}
		mu.Lock()
		gotStages = append(gotStages, e.To)
		mu.Unlock()
	})
	defer unsub()

	pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{}) // all no-ops
	result := c.Run(context.Background(), "stage-iface", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run() = %v, want success", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotStages) != len(wantOrder) {
		t.Fatalf("stage count: got %d, want %d; stages=%v", len(gotStages), len(wantOrder), gotStages)
	}
	for i, want := range wantOrder {
		if gotStages[i] != want {
			t.Errorf("stage[%d]: got %v, want %v", i, gotStages[i], want)
		}
	}
}

// ── 5. Max retries reached ────────────────────────────────────────────────────

// TestParityMaxRetriesExhaustsCoordinator verifies that once the per-interface
// attempt cap is reached the coordinator refuses further runs with
// FailureReasonExhausted and the step function is NOT called.
func TestParityMaxRetriesExhaustsCoordinator(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-exhaust", bus, 3)

	failing := simpleFailPipeline(hmenum.RecoveryStageDetecting)
	c.Run(context.Background(), "ex-iface", failing)
	c.Run(context.Background(), "ex-iface", failing)
	c.Run(context.Background(), "ex-iface", failing)

	// 4th call must not invoke the step.
	called := false
	result := c.Run(context.Background(), "ex-iface", []Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run: func(_ context.Context) error {
			called = true
			return nil
		},
	}})

	if called {
		t.Fatal("step must not run after cap exhaustion")
	}
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("Run() after exhaustion = %v, want RecoveryResultFailed", result)
	}
	hist := c.History("ex-iface")
	last := hist[len(hist)-1]
	if last.Reason != hmenum.FailureReasonExhausted {
		t.Fatalf("history last Reason = %v, want FailureReasonExhausted", last.Reason)
	}
}

// ── 6. CircuitBreaker state-change subscription wiring ───────────────────────

// TestParityCBStateChangedHalfOpenDoesNotTrigger verifies that a CB transition
// to HalfOpen is silently ignored (only Open triggers recovery).
func TestParityCBStateChangedHalfOpenDoesNotTrigger(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-cb", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { count.Add(1); return nil },
	}})
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "parity-cb",
		InterfaceID: "HmIP-RF",
		From:        hmenum.CircuitStateClosed,
		To:          hmenum.CircuitStateHalfOpen,
	})
	time.Sleep(30 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("recovery triggered by CB→HalfOpen (count=%d), want 0", count.Load())
	}
}

// TestParityCBStateChangedOpenTriggers verifies that a CB transition to Open
// triggers recovery (the critical wiring path).
func TestParityCBStateChangedOpenTriggers(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-cb2", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { count.Add(1); return nil },
	}})
	armInterfaces(c, "HmIP-RF")
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "parity-cb2",
		InterfaceID: "HmIP-RF",
		From:        hmenum.CircuitStateClosed,
		To:          hmenum.CircuitStateOpen,
	})
	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery not triggered by CB→Open (count=%d)", count.Load())
	}
}

// ── 7. ConnectionLost event subscription ─────────────────────────────────────

// TestParityConnectionLostSubscriptionWiring verifies that Subscribe hooks the
// ConnectionLostEvent handler and that Stop unregisters it.
func TestParityConnectionLostSubscriptionWiring(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-lost", bus, 0)

	var count atomic.Int32
	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { count.Add(1); return nil },
	}})
	armInterfaces(c, "BidCos-RF")
	c.Subscribe()

	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "parity-lost",
		InterfaceID: "BidCos-RF",
	})
	if !waitFor(t, func() bool { return count.Load() >= 1 }, eventWaitTimeout) {
		t.Fatalf("recovery not triggered after ConnectionLost (count=%d)", count.Load())
	}

	// Stop — further events must be ignored.
	c.Stop()
	before := count.Load()
	events.Publish(bus, hmevent.ConnectionLostEvent{
		Base:        hmevent.NewBase(),
		CentralName: "parity-lost",
		InterfaceID: "BidCos-RF",
	})
	time.Sleep(40 * time.Millisecond)
	if count.Load() != before {
		t.Fatalf("recovery triggered after Stop (count went from %d to %d)", before, count.Load())
	}
}

// ── 8. HeartbeatTimerFired per-interface ─────────────────────────────────────

// TestParityHeartbeatTimerFiresPerInterfaceRecovery verifies that
// HeartbeatTimerFiredEvent with N interface IDs triggers up to N independent
// recovery runs.
func TestParityHeartbeatTimerFiresPerInterfaceRecovery(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("parity-hb", bus, 0)

	var mu sync.Mutex
	started := map[string]int{}

	// Collect RecoveryStartedEvent to know which interfaces began recovery.
	events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) {
		if e.CentralName != "parity-hb" {
			return
		}
		mu.Lock()
		started[e.InterfaceID]++
		mu.Unlock()
	})

	c.WithDefaultPipeline([]Pipeline{{
		Stage: hmenum.RecoveryStageDetecting,
		Run:   func(_ context.Context) error { return nil },
	}})
	armInterfaces(c, "HmIP-RF", "BidCos-RF", "BidCos-Wired")
	c.Subscribe()
	defer c.Stop()

	events.Publish(bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  "parity-hb",
		InterfaceIDs: []string{"HmIP-RF", "BidCos-RF", "BidCos-Wired"},
	})

	if !waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(started) >= 3
	}, eventWaitTimeout) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("not all 3 interfaces recovered; got %v", started)
	}
}

// ── 9. StateMachine transitions on Recovering/Running/Failed ──────────────────

// TestParityStateMachineTransitionsOnSuccess verifies that a successful run
// drives the state machine through Recovering → Running.
func TestParityStateMachineTransitionsOnSuccess(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	sm := &stubSM{}
	c.WithStateMachine(sm)

	result := c.Run(context.Background(), "sm-iface", simpleSuccessPipeline(hmenum.RecoveryStageDetecting))
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run() = %v, want success", result)
	}

	transitions := sm.all()
	if len(transitions) < 2 {
		t.Fatalf("transitions=%v, want at least [Recovering, Running]", transitions)
	}
	if transitions[0] != hmenum.CentralStateRecovering {
		t.Errorf("transitions[0]=%v, want CentralStateRecovering", transitions[0])
	}
	last := transitions[len(transitions)-1]
	if last != hmenum.CentralStateRunning {
		t.Errorf("last transition=%v, want CentralStateRunning", last)
	}
}

// TestParityStateMachineTransitionsOnFailure verifies that a failed run ends
// with a CentralStateFailed transition.
func TestParityStateMachineTransitionsOnFailure(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	sm := &stubSM{}
	c.WithStateMachine(sm)

	result := c.Run(context.Background(), "smf-iface", simpleFailPipeline(hmenum.RecoveryStageDetecting))
	if result != hmenum.RecoveryResultFailed {
		t.Fatalf("Run() = %v, want failed", result)
	}

	transitions := sm.all()
	last := transitions[len(transitions)-1]
	if last != hmenum.CentralStateFailed {
		t.Errorf("last transition=%v, want CentralStateFailed", last)
	}
}

// ── 10. CircuitBreaker resetter called on success ─────────────────────────────

// TestParityCBResetterCalledOnSuccess verifies that the optional
// CircuitBreakerResetter is invoked after a successful run and NOT invoked after
// a failed run.
func TestParityCBResetterCalledOnSuccess(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)
	cbr := &stubCBResetter{}
	c.WithCircuitBreakerResetter(cbr)

	// Successful run → resetter must fire.
	c.Run(context.Background(), "cb-iface", simpleSuccessPipeline(hmenum.RecoveryStageDetecting))
	if ids := cbr.called(); len(ids) != 1 || ids[0] != "cb-iface" {
		t.Fatalf("resetter called with %v, want [cb-iface]", ids)
	}

	// Failing run → resetter must NOT fire a second time.
	before := len(cbr.called())
	c.Run(context.Background(), "cb-iface", simpleFailPipeline(hmenum.RecoveryStageDetecting))
	after := len(cbr.called())
	if after != before {
		t.Fatalf("resetter called on failure (before=%d after=%d)", before, after)
	}
}

// ── 11. Multi-interface concurrent recovery ───────────────────────────────────

// TestParityMultiInterfaceRecoveryRun verifies that two different interfaces can
// each undergo independent recovery runs concurrently without interfering with
// each other's state counters.
func TestParityMultiInterfaceRecoveryRun(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)

	var wg sync.WaitGroup
	for _, id := range []string{"iface-A", "iface-B"} {
		wg.Go(func() {
			c.Run(context.Background(), id, simpleFailPipeline(hmenum.RecoveryStageRPCChecking))
		})
	}
	wg.Wait()

	for _, id := range []string{"iface-A", "iface-B"} {
		s := c.State(id)
		if s.ConsecutiveFailures != 1 {
			t.Errorf("%s: ConsecutiveFailures=%d, want 1", id, s.ConsecutiveFailures)
		}
	}
}

// ── 12. InRecovery flag tracks active run ────────────────────────────────────

// TestParityInRecoveryFlagDuringRun verifies that InRecovery returns true while
// a run is in progress and false after it completes.
func TestParityInRecoveryFlagDuringRun(t *testing.T) {
	t.Parallel()
	c, _ := newParityCoord(t)

	enter := make(chan struct{})
	release := make(chan struct{})
	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			close(enter)
			<-release
			return nil
		},
	}}

	done := make(chan struct{})
	go func() {
		c.Run(context.Background(), "inflight", pipeline)
		close(done)
	}()

	<-enter
	if !c.InRecoveryFor("inflight") {
		t.Error("InRecovery() = false while run is in progress, want true")
	}
	close(release)
	<-done
	if c.InRecoveryFor("inflight") {
		t.Error("InRecovery() = true after run completed, want false")
	}
}
