// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

// connection_recovery_state_machine_test.go — C-RECOV-5:
// StateTransitioner integration: Run drives CentralStateMachine transitions.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// spyTransitioner is a StateTransitioner spy that records every TransitionTo call.
type spyTransitioner struct {
	mu          sync.Mutex
	transitions []hmenum.CentralState
	// rejectState, when non-empty, causes TransitionTo to return an error
	// for that specific target state (simulates invalid transitions).
	rejectState hmenum.CentralState
}

func (s *spyTransitioner) TransitionTo(state hmenum.CentralState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectState != "" && s.rejectState == state {
		return errors.New("invalid transition")
	}
	s.transitions = append(s.transitions, state)
	return nil
}

func (s *spyTransitioner) recorded() []hmenum.CentralState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hmenum.CentralState, len(s.transitions))
	copy(out, s.transitions)
	return out
}

func (s *spyTransitioner) contains(state hmenum.CentralState) bool {
	return slices.Contains(s.recorded(), state)
}

// newSmCoord builds a coordinator with the spy state machine wired in.
func newSmCoord(t *testing.T, sm StateTransitioner) *ConnectionRecoveryCoordinator {
	t.Helper()
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 2)
	c.WithStateMachine(sm)
	return c
}

// TestRunTransitionsToRecoveringOnStart verifies that Run calls
// TransitionTo(CentralStateRecovering) before executing the pipeline.
func TestRunTransitionsToRecoveringOnStart(t *testing.T) {
	t.Parallel()

	spy := &spyTransitioner{}
	c := newSmCoord(t, spy)

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}}
	result := c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %v, want success", result)
	}

	if !spy.contains(hmenum.CentralStateRecovering) {
		t.Errorf("TransitionTo(Recovering) was not called; got %v", spy.recorded())
	}
}

// TestRunTransitionsToRunningOnSuccess verifies that a successful Run calls
// TransitionTo(CentralStateRunning) after all stages complete.
func TestRunTransitionsToRunningOnSuccess(t *testing.T) {
	t.Parallel()

	spy := &spyTransitioner{}
	c := newSmCoord(t, spy)

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}}
	_ = c.Run(context.Background(), "HmIP-RF", pipeline)

	if !spy.contains(hmenum.CentralStateRunning) {
		t.Errorf("TransitionTo(Running) was not called; got %v", spy.recorded())
	}
}

// TestRunTransitionsToFailedOnMaxRetries verifies that once the attempt cap
// is exhausted, Run calls TransitionTo(CentralStateFailed).
func TestRunTransitionsToFailedOnMaxRetries(t *testing.T) {
	t.Parallel()

	spy := &spyTransitioner{}
	bus := events.NewBus()
	// cap = 1 so the first run exhausts the budget
	c := NewConnectionRecoveryCoordinatorWithLimit("c1", bus, 1)
	c.WithStateMachine(spy)

	failing := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return errors.New("down") },
	}}

	// First run: bumps the attempt counter to 1 (the cap).
	_ = c.Run(context.Background(), "HmIP-RF", failing)
	spy.mu.Lock()
	spy.transitions = nil // clear to isolate the max-retries path
	spy.mu.Unlock()

	// Second run: cap already reached → exhaustion path → Failed.
	_ = c.Run(context.Background(), "HmIP-RF", failing)

	if !spy.contains(hmenum.CentralStateFailed) {
		t.Errorf("TransitionTo(Failed) was not called; got %v", spy.recorded())
	}
}

// TestRunWithNilStateMachineDoesNotPanic verifies that Run works
// correctly when no StateTransitioner is injected (nil safety).
func TestRunWithNilStateMachineDoesNotPanic(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("c1", bus)
	// no WithStateMachine call — sm is nil

	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run:   func(_ context.Context) error { return nil },
	}}
	result := c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %v, want success", result)
	}
}

// TestRunRejectedTransitionDoesNotAbortPipeline verifies that a rejected
// state transition (e.g. Recovering already set) does not abort the
// pipeline — the coordinator logs and continues.
func TestRunRejectedTransitionDoesNotAbortPipeline(t *testing.T) {
	t.Parallel()

	spy := &spyTransitioner{rejectState: hmenum.CentralStateRecovering}
	c := newSmCoord(t, spy)

	var ran bool
	pipeline := []Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(_ context.Context) error {
			ran = true
			return nil
		},
	}}
	result := c.Run(context.Background(), "HmIP-RF", pipeline)
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %v, want success", result)
	}
	if !ran {
		t.Fatal("pipeline stage did not run despite rejected Recovering transition")
	}
}
