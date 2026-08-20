// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrInvalidStateTransition is returned when
// [ClientStateMachine.TransitionTo] is called with a state that is not
// reachable from the current state and force=false. Prevents silent
// race-condition state corruption by making bad transitions loud.
var ErrInvalidStateTransition = errors.New("client: invalid state transition")

// validTransitions maps each ClientState to the set of states it may
// transition to. Mirrors the _VALID_TRANSITIONS table in the Python
// reference (client/state_machine.py).
//
// Key design decisions:
//   - CREATED → INITIALIZING only: forces the full init path before any
//     connect attempt.
//   - STOPPING is reachable from EVERY non-terminal state: a graceful
//     shutdown must never depend on force, no matter where the client
//     is in its lifecycle (a client stuck in CONNECTING or FAILED is
//     stopped just like a CONNECTED one).
//   - STOPPED is terminal: once stopped the client is finished and cannot
//     be restarted in-place. Callers that need a fresh start must
//     construct a new InterfaceClient.
//   - INITIALIZED → DISCONNECTED: allows the recovery coordinator to
//     reset a client that completed init but never connected.
//   - DISCONNECTED → DISCONNECTED: idempotent deinit calls are safe.
//   - FAILED allows retry via INITIALIZING / CONNECTING / RECONNECTING /
//     DISCONNECTED without re-creating the client.
var validTransitions = map[hmenum.ClientState][]hmenum.ClientState{
	// CREATED → INITIALIZING only (strict start-up gate), or STOPPING.
	hmenum.ClientStateCreated: {hmenum.ClientStateInitializing, hmenum.ClientStateStopping},
	// INITIALIZING → INITIALIZED on success; → FAILED when metadata loading fails.
	hmenum.ClientStateInitializing: {hmenum.ClientStateInitialized, hmenum.ClientStateFailed, hmenum.ClientStateStopping},
	// INITIALIZED → CONNECTING (normal) or DISCONNECTED (recovery reset; allows
	// the recovery coordinator to reset an initialised-but-never-connected client).
	hmenum.ClientStateInitialized: {hmenum.ClientStateConnecting, hmenum.ClientStateDisconnected, hmenum.ClientStateStopping},
	// CONNECTING → CONNECTED on success; → FAILED when the connect attempt fails.
	hmenum.ClientStateConnecting: {hmenum.ClientStateConnected, hmenum.ClientStateFailed, hmenum.ClientStateStopping},
	// CONNECTED → DISCONNECTED (link lost), RECONNECTING (auto-reconnect), or
	// STOPPING (graceful shutdown).
	hmenum.ClientStateConnected: {hmenum.ClientStateDisconnected, hmenum.ClientStateReconnecting, hmenum.ClientStateStopping},
	// DISCONNECTED → RECONNECTING (auto), CONNECTING (manual), STOPPING (shutdown),
	// or DISCONNECTED (idempotent deinit).
	hmenum.ClientStateDisconnected: {hmenum.ClientStateReconnecting, hmenum.ClientStateConnecting, hmenum.ClientStateStopping, hmenum.ClientStateDisconnected},
	// RECONNECTING → CONNECTED (success), DISCONNECTED (gave up), FAILED
	// (permanent failure), or CONNECTING (retry).
	hmenum.ClientStateReconnecting: {hmenum.ClientStateConnecting, hmenum.ClientStateConnected, hmenum.ClientStateDisconnected, hmenum.ClientStateFailed, hmenum.ClientStateStopping},
	// STOPPING → STOPPED (one-way, graceful shutdown).
	hmenum.ClientStateStopping: {hmenum.ClientStateStopped},
	// STOPPED is terminal — no outgoing transitions.
	hmenum.ClientStateStopped: {},
	// FAILED → INITIALIZING (retry init), CONNECTING (retry connect),
	// RECONNECTING (auto-reconnect), DISCONNECTED (graceful deinit shutdown).
	hmenum.ClientStateFailed: {hmenum.ClientStateInitializing, hmenum.ClientStateConnecting, hmenum.ClientStateReconnecting, hmenum.ClientStateDisconnected, hmenum.ClientStateStopping},
}

// StateChangedPublisher is the function signature used to publish a
// ClientStateChangedEvent onto the event bus after each transition.
// wired in by [InterfaceClient] when a bus is available.
type StateChangedPublisher func(from, to hmenum.ClientState, reason string, failureReason hmenum.FailureReason)

// ClientStateMachine manages the lifecycle state of an [InterfaceClient]. It
// validates transitions, records failure metadata, and exposes convenience
// predicates for the most common state checks.
//
// ClientStateMachine is safe for concurrent use.
type ClientStateMachine struct { //nolint:revive // stutter intentional
	mu             sync.RWMutex
	state          hmenum.ClientState
	failureReason  hmenum.FailureReason
	failureMessage string

	// listeners is called after every successful state transition.
	listeners []func(from, to hmenum.ClientState)

	// publisher, when non-nil, is called after every successful
	// state transition to emit a ClientStateChangedEvent onto the
	// event bus. Set via [SetStateChangedPublisher].
	publisher StateChangedPublisher
}

// NewClientStateMachine returns a state machine starting in
// [hmenum.ClientStateCreated].
func NewClientStateMachine() *ClientStateMachine {
	return &ClientStateMachine{
		state:         hmenum.ClientStateCreated,
		failureReason: hmenum.FailureReasonNone,
	}
}

// State returns the current state.
func (sm *ClientStateMachine) State() hmenum.ClientState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state
}

// FailureMessage returns the human-readable failure description set by the
// last transition that moved the machine into the FAILED state, or an empty
// string when no failure has occurred.
func (sm *ClientStateMachine) FailureMessage() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.failureMessage
}

// FailureReason returns the machine-readable failure category set by the last
// transition into the FAILED state. Returns [hmenum.FailureReasonNone] when
// no failure has occurred.
func (sm *ClientStateMachine) FailureReason() hmenum.FailureReason {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.failureReason
}

// IsAvailable reports whether the current state implies the interface can
// process commands (CONNECTED or RECONNECTING).
func (sm *ClientStateMachine) IsAvailable() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s := sm.state
	return s == hmenum.ClientStateConnected || s == hmenum.ClientStateReconnecting
}

// IsConnected reports whether the current state is CONNECTED.
func (sm *ClientStateMachine) IsConnected() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == hmenum.ClientStateConnected
}

// IsFailed reports whether the current state is FAILED.
func (sm *ClientStateMachine) IsFailed() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == hmenum.ClientStateFailed
}

// IsStopped reports whether the current state is STOPPED.
func (sm *ClientStateMachine) IsStopped() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == hmenum.ClientStateStopped
}

// CanReconnect reports whether the current state allows the recovery
// coordinator to initiate a reconnect cycle. Only DISCONNECTED and FAILED
// support reconnect.
func (sm *ClientStateMachine) CanReconnect() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s := sm.state
	return s == hmenum.ClientStateDisconnected || s == hmenum.ClientStateFailed
}

// CanTransitionTo reports whether the machine can move from its current
// state to target. It consults the validated transition table
// ClientStateMachine.can_transition_to (client/state_machine.py:205).
func (sm *ClientStateMachine) CanTransitionTo(target hmenum.ClientState) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.canTransitLocked(sm.state, target)
}

func (sm *ClientStateMachine) canTransitLocked(from, to hmenum.ClientState) bool {
	return slices.Contains(validTransitions[from], to)
}

// TransitionTo moves the machine to target. reason is a human-readable
// description of why the transition was requested (used in logs and returned
// by [FailureMessage] on FAILED transitions). failureReason is the
// machine-readable category (used only on FAILED transitions).
//
// When force is false, TransitionTo validates the transition against the
// table; an invalid transition returns [ErrInvalidStateTransition]. When
// force is true, any transition is accepted — use only in shutdown paths or
// tests.
//
// Transitions to the same state are no-ops (no listeners fired).
func (sm *ClientStateMachine) TransitionTo(
	target hmenum.ClientState,
	reason string,
	force bool,
	failureReason hmenum.FailureReason,
) error {
	return sm.transition(target, reason, force, failureReason, true)
}

// transition is the single-step core of [ClientStateMachine.TransitionTo] and
// [ClientStateMachine.TransitionPath]. publish decides whether the step
// reaches the event bus; registered listeners always see it, because they are
// the client's own internal wiring (waking WaitForState waiters, clearing the
// connectivity streak) and a step hidden from them is a step the client
// itself missed.
func (sm *ClientStateMachine) transition(
	target hmenum.ClientState,
	reason string,
	force bool,
	failureReason hmenum.FailureReason,
	publish bool,
) error {
	sm.mu.Lock()
	from := sm.state
	if from == target {
		sm.mu.Unlock()
		return nil
	}
	if !force && !sm.canTransitLocked(from, target) {
		sm.mu.Unlock()
		return fmt.Errorf("%w: %s → %s", ErrInvalidStateTransition, from, target)
	}
	sm.state = target
	if target == hmenum.ClientStateFailed {
		sm.failureMessage = reason
		if failureReason != hmenum.FailureReasonNone {
			sm.failureReason = failureReason
		} else {
			sm.failureReason = hmenum.FailureReasonUnknown
		}
	}
	listeners := sm.snapshotListenersLocked()
	publisher := sm.publisher
	sm.mu.Unlock()

	for _, cb := range listeners {
		cb(from, target)
	}
	// emit ClientStateChangedEvent on every successful transition.
	if publish && publisher != nil {
		publisher(from, target, reason, failureReason)
	}
	return nil
}

// TransitionPath walks the machine through targets in order and publishes a
// single ClientStateChangedEvent for the whole walk: from the state held
// before the first step to the state held after the last. A step the
// transition table rejects is skipped and reported through onSkip (nil to
// ignore); the walk continues with the next target either way, which is what
// lets a caller hand in a full path and leave it to the table to decide which
// parts of it still apply.
//
// The intermediate steps exist because the table has no direct edge from
// CREATED to CONNECTED, not because the client visited a state a consumer
// could act on. Publishing each one announces an interface that is briefly
// not connected, and every consumer that reads "no interface is connected" as
// an outage then acts on a bring-up step as if it were a failure: the
// central-state evaluation demotes the central to FAILED, the recovery
// coordinator's CentralStateChanged lane triggers a reconnect pipeline
// against a CCU that was never gone, and that reconnect cycles every device's
// availability on every north-bound plane.
//
// Returns the state the machine holds when the walk ends.
func (sm *ClientStateMachine) TransitionPath(
	reason string,
	failureReason hmenum.FailureReason,
	onSkip func(target hmenum.ClientState, err error),
	targets ...hmenum.ClientState,
) hmenum.ClientState {
	sm.mu.Lock()
	start := sm.state
	sm.mu.Unlock()

	for _, target := range targets {
		if err := sm.transition(target, reason, false, failureReason, false); err != nil && onSkip != nil {
			onSkip(target, err)
		}
	}

	sm.mu.Lock()
	end := sm.state
	publisher := sm.publisher
	sm.mu.Unlock()

	if end != start && publisher != nil {
		publisher(start, end, reason, failureReason)
	}
	return end
}

// Reset moves the machine back to [hmenum.ClientStateCreated] and clears
// failure metadata. Equivalent to an unconditional TransitionTo(Created).
func (sm *ClientStateMachine) Reset() {
	sm.mu.Lock()
	from := sm.state
	sm.state = hmenum.ClientStateCreated
	sm.failureReason = hmenum.FailureReasonNone
	sm.failureMessage = ""
	listeners := sm.snapshotListenersLocked()
	publisher := sm.publisher
	sm.mu.Unlock()

	if from != hmenum.ClientStateCreated {
		for _, cb := range listeners {
			cb(from, hmenum.ClientStateCreated)
		}
		// emit state change event on Reset as well.
		if publisher != nil {
			publisher(from, hmenum.ClientStateCreated, "reset", hmenum.FailureReasonNone)
		}
	}
}

// AddOnStateChange appends a listener called after each successful state
// transition. Multiple listeners coexist and are fired in registration
// order. Safe to call concurrently.
func (sm *ClientStateMachine) AddOnStateChange(fn func(from, to hmenum.ClientState)) {
	if fn == nil {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.listeners = append(sm.listeners, fn)
}

// SetStateChangedPublisher installs the bus-publishing hook for
// [hmevent.ClientStateChangedEvent]. Called by [InterfaceClient] after
// construction when an event bus is wired in. Passing nil removes the hook.
func (sm *ClientStateMachine) SetStateChangedPublisher(p StateChangedPublisher) {
	sm.mu.Lock()
	sm.publisher = p
	sm.mu.Unlock()
}

func (sm *ClientStateMachine) snapshotListenersLocked() []func(from, to hmenum.ClientState) {
	if len(sm.listeners) == 0 {
		return nil
	}
	out := make([]func(from, to hmenum.ClientState), len(sm.listeners))
	copy(out, sm.listeners)
	return out
}
