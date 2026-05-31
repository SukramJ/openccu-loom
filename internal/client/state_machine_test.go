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
