// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// isOperationalGateStates lists every lifecycle state a central can hold,
// together with whether a scheduled job is expected to execute in it. The
// value set is pkg/hmenum/state.go (CentralState); hmenum exports no
// all-values slice, so a new state has to be added here as well — a missing
// row silently narrows this guard's coverage.
var isOperationalGateStates = []struct {
	state    hmenum.CentralState
	jobFires bool
}{
	{hmenum.CentralStateStarting, false},
	{hmenum.CentralStateInitializing, false},
	{hmenum.CentralStateRunning, true},
	{hmenum.CentralStateDegraded, true},
	{hmenum.CentralStateRecovering, false},
	{hmenum.CentralStateFailed, false},
	{hmenum.CentralStateStopped, false},
}

// isOperationalGateJobRun registers a counting standard job through the
// production registration path and returns its scheduled Run func together
// with the counter. Nothing here constructs the gate itself: the wrapper under
// test is the one RegisterStandardJobs installs.
func isOperationalGateJobRun(t *testing.T, name string) (*central.Unit, func(context.Context) error, *atomic.Int32) {
	t.Helper()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	var calls atomic.Int32
	cfg := central.StandardJobs{
		RefreshClientData:         func(context.Context) error { calls.Add(1); return nil },
		RefreshClientDataInterval: 10 * time.Second,
	}
	if _, err := central.RegisterStandardJobs(unit, cfg); err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	for _, j := range unit.Scheduler.Jobs() {
		if j.Name == "central.refresh_client_data" {
			return unit, j.Run, &calls
		}
	}
	t.Fatal("central.refresh_client_data job was not registered")
	return nil, nil, nil
}

// TestIsOperationalJobGateFollowsStateMachinePredicate pins the scheduler's
// job gate to the central state machine's own operational predicate. The gate
// used to re-derive "RUNNING or DEGRADED" from State() with its own literals,
// so the two definitions could drift without any test noticing. Driving the
// real state machine across every lifecycle state and asserting the *effect*
// (did the registered job body run?) makes the state machine the single
// source: widening or narrowing IsOperational must show up here.
func TestIsOperationalJobGateFollowsStateMachinePredicate(t *testing.T) {
	for _, tc := range isOperationalGateStates {
		t.Run(string(tc.state), func(t *testing.T) {
			unit, run, calls := isOperationalGateJobRun(t, "is-operational-gate-"+string(tc.state))
			if err := unit.StateMachine.ForceTransitionTo(tc.state, hmenum.FailureReasonNone); err != nil {
				t.Fatalf("force transition to %s: %v", tc.state, err)
			}
			if err := run(context.Background()); err != nil {
				t.Fatalf("job run in %s: %v", tc.state, err)
			}
			got := calls.Load()
			if tc.jobFires && got != 1 {
				t.Errorf("job did not run in %s: calls=%d, want 1 (the state machine's IsOperational reports this state operational)", tc.state, got)
			}
			if !tc.jobFires && got != 0 {
				t.Errorf("job ran in %s: calls=%d, want 0 (the state machine's IsOperational reports this state non-operational)", tc.state, got)
			}
		})
	}
}

// TestIsOperationalJobGateSurvivesMissingStateMachine pins the nil-safety the
// scheduler's wrapper exists to carry: the state machine's predicate locks its
// own mutex, so a unit without a state machine must be answered by the wrapper
// rather than reaching the method.
func TestIsOperationalJobGateSurvivesMissingStateMachine(t *testing.T) {
	unit, run, calls := isOperationalGateJobRun(t, "is-operational-gate-nil-sm")
	unit.StateMachine = nil
	if err := run(context.Background()); err != nil {
		t.Fatalf("job run without state machine: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("job ran without a state machine: calls=%d, want 0", calls.Load())
	}
}
