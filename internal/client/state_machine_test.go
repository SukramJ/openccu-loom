// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for ClientStateMachine: validated transition table,
// ErrInvalidStateTransition, force-bypass, CanTransitionTo,
// FailureMessage/FailureReason, Reset, state-changed publisher,
// idempotent same-state transitions.

package client

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// validated transition table + ErrInvalidStateTransition
// ---------------------------------------------------------------------------

func TestClientStateMachineValidTransitions(t *testing.T) {
	t.Parallel()
	// Representative happy-path transitions drawn from validTransitions.
	cases := []struct {
		from hmenum.ClientState
		to   hmenum.ClientState
	}{
		{hmenum.ClientStateCreated, hmenum.ClientStateInitializing},
		{hmenum.ClientStateInitializing, hmenum.ClientStateInitialized},
		{hmenum.ClientStateInitialized, hmenum.ClientStateConnecting},
		{hmenum.ClientStateConnecting, hmenum.ClientStateConnected},
		{hmenum.ClientStateConnected, hmenum.ClientStateDisconnected},
		{hmenum.ClientStateDisconnected, hmenum.ClientStateReconnecting},
		{hmenum.ClientStateReconnecting, hmenum.ClientStateConnecting},
		{hmenum.ClientStateStopping, hmenum.ClientStateStopped},
		{hmenum.ClientStateFailed, hmenum.ClientStateInitializing},
	}
	for _, tc := range cases {
		sm := NewClientStateMachine()
		sm.mu.Lock()
		sm.state = tc.from
		sm.mu.Unlock()
		if err := sm.TransitionTo(tc.to, "", false, hmenum.FailureReasonNone); err != nil {
			t.Errorf("TransitionTo(%s→%s) unexpected error: %v", tc.from, tc.to, err)
		}
	}
}

func TestClientStateMachineInvalidTransitionReturnsError(t *testing.T) {
	t.Parallel()
	// CREATED cannot jump directly to CONNECTED.
	sm := NewClientStateMachine()
	err := sm.TransitionTo(hmenum.ClientStateConnected, "skip", false, hmenum.FailureReasonNone)
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Errorf("invalid transition returned %v; want ErrInvalidStateTransition", err)
	}
}

func TestClientStateMachineForceBypassesValidation(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	// Normally CREATED → CONNECTED is invalid; force=true should allow it.
	if err := sm.TransitionTo(hmenum.ClientStateConnected, "force", true, hmenum.FailureReasonNone); err != nil {
		t.Errorf("forced invalid transition returned error: %v", err)
	}
	if got := sm.State(); got != hmenum.ClientStateConnected {
		t.Errorf("State() = %s after forced transition; want CONNECTED", got)
	}
}

// ---------------------------------------------------------------------------
// CanTransitionTo
// ---------------------------------------------------------------------------

func TestCanTransitionToValid(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	if !sm.CanTransitionTo(hmenum.ClientStateInitializing) {
		t.Error("CanTransitionTo(INITIALIZING) from CREATED = false; want true")
	}
}

func TestCanTransitionToInvalid(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	if sm.CanTransitionTo(hmenum.ClientStateConnected) {
		t.Error("CanTransitionTo(CONNECTED) from CREATED = true; want false")
	}
}

// ---------------------------------------------------------------------------
// FailureMessage / FailureReason on FAILED transitions
// ---------------------------------------------------------------------------

func TestFailureMetadataSetOnFailedTransition(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	// Transition from INITIALIZING → FAILED (valid table entry).
	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone)
	wantMsg := "auth failed"
	wantReason := hmenum.FailureReasonAuth
	if err := sm.TransitionTo(hmenum.ClientStateFailed, wantMsg, false, wantReason); err != nil {
		t.Fatalf("TransitionTo(FAILED) unexpected error: %v", err)
	}
	if got := sm.FailureMessage(); got != wantMsg {
		t.Errorf("FailureMessage() = %q; want %q", got, wantMsg)
	}
	if got := sm.FailureReason(); got != wantReason {
		t.Errorf("FailureReason() = %s; want %s", got, wantReason)
	}
}

func TestFailureMetadataClearedByReset(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	// Transition through a valid path to FAILED before testing Reset.
	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone)
	_ = sm.TransitionTo(hmenum.ClientStateFailed, "gone", false, hmenum.FailureReasonNetwork)
	sm.Reset()

	if got := sm.FailureMessage(); got != "" {
		t.Errorf("FailureMessage() after Reset = %q; want empty", got)
	}
	if got := sm.FailureReason(); got != hmenum.FailureReasonNone {
		t.Errorf("FailureReason() after Reset = %s; want None", got)
	}
	if got := sm.State(); got != hmenum.ClientStateCreated {
		t.Errorf("State() after Reset = %s; want CREATED", got)
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestResetMovesToCreated(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone)
	_ = sm.TransitionTo(hmenum.ClientStateInitialized, "", false, hmenum.FailureReasonNone)
	sm.Reset()
	if got := sm.State(); got != hmenum.ClientStateCreated {
		t.Errorf("State() after Reset = %s; want CREATED", got)
	}
}

// ---------------------------------------------------------------------------
// Idempotent same-state transition (no-op)
// ---------------------------------------------------------------------------

func TestSameStateTansitionIsNoop(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	var listenerFired bool
	sm.AddOnStateChange(func(_, _ hmenum.ClientState) { listenerFired = true })
	// Transitioning to the current state should be a no-op.
	if err := sm.TransitionTo(hmenum.ClientStateCreated, "", false, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("same-state transition returned error: %v", err)
	}
	if listenerFired {
		t.Error("listener fired for a same-state no-op transition; expected no callback")
	}
}

// ---------------------------------------------------------------------------
// — INITIALIZED → DISCONNECTED
// ---------------------------------------------------------------------------

func TestStateMachineInitializedToDisconnected(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	// Advance to INITIALIZED.
	if err := sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("→INITIALIZING: %v", err)
	}
	if err := sm.TransitionTo(hmenum.ClientStateInitialized, "", false, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("→INITIALIZED: %v", err)
	}
	// INITIALIZED → DISCONNECTED must be valid.
	if err := sm.TransitionTo(hmenum.ClientStateDisconnected, "recovery reset", false, hmenum.FailureReasonNone); err != nil {
		t.Errorf("INITIALIZED→DISCONNECTED unexpected error: %v", err)
	}
	if got := sm.State(); got != hmenum.ClientStateDisconnected {
		t.Errorf("State() = %s; want DISCONNECTED", got)
	}
}

// ---------------------------------------------------------------------------
// — DISCONNECTED → CONNECTING
// ---------------------------------------------------------------------------

func TestStateMachineDisconnectedToConnecting(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	sm.mu.Lock()
	sm.state = hmenum.ClientStateDisconnected
	sm.mu.Unlock()
	// DISCONNECTED → CONNECTING must be valid.
	if err := sm.TransitionTo(hmenum.ClientStateConnecting, "manual reconnect", false, hmenum.FailureReasonNone); err != nil {
		t.Errorf("DISCONNECTED→CONNECTING unexpected error: %v", err)
	}
	if got := sm.State(); got != hmenum.ClientStateConnecting {
		t.Errorf("State() = %s; want CONNECTING", got)
	}
}

// ---------------------------------------------------------------------------
// — SetStateChangedPublisher fires on each transition
// ---------------------------------------------------------------------------

func TestStateMachinePublisherFiredOnTransition(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()

	type record struct {
		from, to      hmenum.ClientState
		reason        string
		failureReason hmenum.FailureReason
	}
	var events []record
	sm.SetStateChangedPublisher(func(from, to hmenum.ClientState, reason string, fr hmenum.FailureReason) {
		events = append(events, record{from, to, reason, fr})
	})

	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "init", false, hmenum.FailureReasonNone)
	_ = sm.TransitionTo(hmenum.ClientStateInitialized, "done", false, hmenum.FailureReasonNone)

	if len(events) != 2 {
		t.Fatalf("expected 2 publisher calls, got %d", len(events))
	}
	if events[0].from != hmenum.ClientStateCreated || events[0].to != hmenum.ClientStateInitializing {
		t.Errorf("event[0]: got %+v", events[0])
	}
	if events[1].from != hmenum.ClientStateInitializing || events[1].to != hmenum.ClientStateInitialized {
		t.Errorf("event[1]: got %+v", events[1])
	}
}

func TestStateMachinePublisherNotFiredForSameState(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	fired := false
	sm.SetStateChangedPublisher(func(_, _ hmenum.ClientState, _ string, _ hmenum.FailureReason) {
		fired = true
	})
	// Same-state transition is a no-op — publisher must not fire.
	_ = sm.TransitionTo(hmenum.ClientStateCreated, "", false, hmenum.FailureReasonNone)
	if fired {
		t.Error("publisher fired on same-state no-op transition")
	}
}

func TestStateMachinePublisherFiredOnReset(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone)

	var resets []hmenum.ClientState
	sm.SetStateChangedPublisher(func(_, to hmenum.ClientState, _ string, _ hmenum.FailureReason) {
		resets = append(resets, to)
	})
	sm.Reset()
	if len(resets) != 1 || resets[0] != hmenum.ClientStateCreated {
		t.Errorf("Reset: publisher events = %v; want [CREATED]", resets)
	}
}

func TestStateMachineSetPublisherNilRemovesHook(t *testing.T) {
	t.Parallel()
	sm := NewClientStateMachine()
	fired := false
	sm.SetStateChangedPublisher(func(_, _ hmenum.ClientState, _ string, _ hmenum.FailureReason) {
		fired = true
	})
	sm.SetStateChangedPublisher(nil) // remove hook
	_ = sm.TransitionTo(hmenum.ClientStateInitializing, "", false, hmenum.FailureReasonNone)
	if fired {
		t.Error("publisher fired after being removed with nil")
	}
}

// TestClientStateMachineStoppingReachableFromEveryLiveState locks the
// shutdown contract: a graceful stop must be a VALID transition from
// every non-terminal state, so Close/teardown paths never depend on
// force. STOPPED stays terminal.
func TestClientStateMachineStoppingReachableFromEveryLiveState(t *testing.T) {
	t.Parallel()
	live := []hmenum.ClientState{
		hmenum.ClientStateCreated,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateDisconnected,
		hmenum.ClientStateReconnecting,
		hmenum.ClientStateFailed,
	}
	for _, from := range live {
		sm := NewClientStateMachine()
		// Force into the source state, then require a NON-forced stop.
		if err := sm.TransitionTo(from, "arrange", true, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("arrange %s: %v", from, err)
		}
		if !sm.CanTransitionTo(hmenum.ClientStateStopping) {
			t.Errorf("STOPPING not reachable from %s", from)
		}
	}
	sm := NewClientStateMachine()
	if err := sm.TransitionTo(hmenum.ClientStateStopped, "arrange", true, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("arrange stopped: %v", err)
	}
	if sm.CanTransitionTo(hmenum.ClientStateStopping) {
		t.Error("STOPPED must stay terminal")
	}
}

// ---------------------------------------------------------------------------
// TransitionPath: one bus event per bring-up walk
// ---------------------------------------------------------------------------

// pathObserver records what a TransitionPath walk told the bus and the
// client's own listeners.
type pathObserver struct {
	published [][2]hmenum.ClientState
	seen      [][2]hmenum.ClientState
}

func (o *pathObserver) attach(sm *ClientStateMachine) {
	sm.AddOnStateChange(func(from, to hmenum.ClientState) {
		o.seen = append(o.seen, [2]hmenum.ClientState{from, to})
	})
	sm.SetStateChangedPublisher(func(from, to hmenum.ClientState, _ string, _ hmenum.FailureReason) {
		o.published = append(o.published, [2]hmenum.ClientState{from, to})
	})
}

// TestTransitionPathPublishesOneEventForTheWholeWalk is the regression guard
// for the boot-time false outage. The bring-up drives the client from CREATED
// to CONNECTED, and the table has no direct edge, so it walks three
// intermediate states. Publishing each one announces an interface that is not
// connected, and the central-state evaluation reads "no interface connected"
// as an outage: the central was demoted to FAILED mid-bring-up, the recovery
// coordinator's CentralStateChanged lane started a reconnect pipeline against
// a CCU that was never gone, and the reconnect flipped every bridged device's
// availability twice on every north-bound plane.
func TestTransitionPathPublishesOneEventForTheWholeWalk(t *testing.T) {
	t.Parallel()

	sm := NewClientStateMachine()
	var obs pathObserver
	obs.attach(sm)

	end := sm.TransitionPath(
		"bring-up", hmenum.FailureReasonNone, nil,
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	)

	if end != hmenum.ClientStateConnected {
		t.Errorf("end state = %s, want %s", end, hmenum.ClientStateConnected)
	}
	if len(obs.published) != 1 {
		t.Fatalf("published %d bus events, want 1: %v", len(obs.published), obs.published)
	}
	if got := obs.published[0]; got[0] != hmenum.ClientStateCreated || got[1] != hmenum.ClientStateConnected {
		t.Errorf("published %s → %s, want %s → %s",
			got[0], got[1], hmenum.ClientStateCreated, hmenum.ClientStateConnected)
	}
	// The client's own listeners are a different audience: they wake
	// WaitForState waiters and clear the connectivity streak, so a step
	// hidden from them is a step the client itself missed.
	if len(obs.seen) != 4 {
		t.Errorf("listeners saw %d steps, want 4: %v", len(obs.seen), obs.seen)
	}
}

// TestTransitionPathReportsSkippedStepsAndKeepsWalking pins that a target the
// table rejects neither aborts the walk nor passes silently: the caller is
// told, and the remaining targets still apply. That is what lets the bring-up
// hand in a full path and leave it to the table to decide which parts of it
// are still reachable from the client's current state.
func TestTransitionPathReportsSkippedStepsAndKeepsWalking(t *testing.T) {
	t.Parallel()

	sm := NewClientStateMachine()
	var obs pathObserver
	obs.attach(sm)

	var skipped []hmenum.ClientState
	end := sm.TransitionPath(
		"bring-up", hmenum.FailureReasonNone,
		func(target hmenum.ClientState, err error) {
			if err == nil {
				t.Errorf("onSkip called with a nil error for %s", target)
			}
			skipped = append(skipped, target)
		},
		hmenum.ClientStateConnected, // illegal from CREATED — skipped
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
	)

	if len(skipped) != 1 || skipped[0] != hmenum.ClientStateConnected {
		t.Errorf("skipped = %v, want [%s]", skipped, hmenum.ClientStateConnected)
	}
	if end != hmenum.ClientStateInitialized {
		t.Errorf("end state = %s, want %s", end, hmenum.ClientStateInitialized)
	}
	if len(obs.published) != 1 {
		t.Fatalf("published %d bus events, want 1: %v", len(obs.published), obs.published)
	}
}

// TestTransitionPathPublishesNothingWhenTheWalkMovesNowhere pins the empty
// case: a path whose every step the table rejects leaves the machine where it
// was, and a bus event announcing a state change that did not happen would be
// a lie to every north-bound consumer.
func TestTransitionPathPublishesNothingWhenTheWalkMovesNowhere(t *testing.T) {
	t.Parallel()

	sm := NewClientStateMachine()
	var obs pathObserver
	obs.attach(sm)

	end := sm.TransitionPath("bring-up", hmenum.FailureReasonNone, nil, hmenum.ClientStateConnected)

	if end != hmenum.ClientStateCreated {
		t.Errorf("end state = %s, want %s", end, hmenum.ClientStateCreated)
	}
	if len(obs.published) != 0 {
		t.Errorf("published %d bus events, want 0: %v", len(obs.published), obs.published)
	}
}
