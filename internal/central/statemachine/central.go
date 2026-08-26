// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package statemachine implements the central and client state
// machines described in SPECIFICATION §11 / §10.
//
// Both machines enforce the same two invariants:
// - Transitions are validated against a fixed graph; invalid
// transitions return [ErrInvalidTransition].
// - Every transition emits a typed event on an optional [events.Bus]
// so the rest of the daemon can react.
package statemachine

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// CentralTransition is a single transition entry kept on the state-machine
// history ring buffer.
type CentralTransition struct {
	From   hmenum.CentralState
	To     hmenum.CentralState
	Reason hmenum.FailureReason
	At     time.Time
	// Forced is true when the transition was forced via [ForceTransitionTo]
	// and skipped the validation table.
	Forced bool
}

// DefaultHistorySize is the upper bound on retained transitions. Older
// entries are discarded when the buffer overflows.
const DefaultHistorySize = 100

// ErrInvalidTransition is returned by [*Central.TransitionTo] and
// [*Client.TransitionTo] when the requested target is not reachable
// from the current state.
var ErrInvalidTransition = errors.New("statemachine: invalid transition")

// centralTransitions enumerates every valid transition of the central
// state machine. Keep in sync with SPECIFICATION §11.6.
var centralTransitions = map[hmenum.CentralState]map[hmenum.CentralState]struct{}{
	hmenum.CentralStateStarting: {
		hmenum.CentralStateInitializing: {},
		hmenum.CentralStateStopped:      {},
	},
	hmenum.CentralStateInitializing: {
		hmenum.CentralStateRunning:  {},
		hmenum.CentralStateDegraded: {},
		hmenum.CentralStateFailed:   {},
		hmenum.CentralStateStopped:  {},
	},
	hmenum.CentralStateRunning: {
		hmenum.CentralStateDegraded: {},
		// A central loses every interface at once — a single-interface CCU
		// going offline, or a CCU reboot dropping all of them between two
		// evaluations. DEGRADED is not reachable then (it requires at least
		// one connected client), so without this edge the computed FAILED is
		// rejected and the central reports RUNNING for as long as the outage
		// lasts: /health stays green and every north-bound consumer is told
		// the central is fine.
		hmenum.CentralStateFailed:     {},
		hmenum.CentralStateRecovering: {},
		hmenum.CentralStateStopped:    {},
	},
	hmenum.CentralStateDegraded: {
		hmenum.CentralStateRunning:    {},
		hmenum.CentralStateRecovering: {},
		hmenum.CentralStateFailed:     {},
		hmenum.CentralStateStopped:    {},
	},
	hmenum.CentralStateRecovering: {
		hmenum.CentralStateRunning:  {},
		hmenum.CentralStateDegraded: {},
		hmenum.CentralStateFailed:   {},
		hmenum.CentralStateStopped:  {},
	},
	hmenum.CentralStateFailed: {
		hmenum.CentralStateRecovering: {},
		// FAILED is recoverable, not terminal (only STOPPED is). When every
		// interface reconnects outside an active recovery pipeline — e.g. the
		// clients' own reconnect path, in_recovery=false — evaluate_central_state
		// computes RUNNING/DEGRADED directly. Without these edges that
		// transition is silently rejected and the central is trapped in FAILED
		// (permanent /health 503, endless futile failed→running heartbeats)
		// even though connectivity is fine. Mirrors the client state machine,
		// where ClientStateFailed transitions back into the connect path.
		hmenum.CentralStateRunning:  {},
		hmenum.CentralStateDegraded: {},
		hmenum.CentralStateStopped:  {},
	},
	hmenum.CentralStateStopped: {}, // terminal
}

// Central is the state machine that tracks the daemon-level central
// lifecycle. Safe for concurrent use.
type Central struct {
	name string
	mu   sync.Mutex
	cur  hmenum.CentralState
	why  hmenum.FailureReason
	bus  *events.Bus

	// degraded tracks which interfaces are currently in a degraded state and the
	// reason for each.
	degraded map[string]hmenum.FailureReason
	// failureInterface is the interface_id that triggered the most
	// recent transition into FAILED / DEGRADED. Mirrors
	// `_failure_interface_id` (state_machine.py:168).
	failureInterface string
	// failureMsg is a human-readable message set alongside failure
	// transitions. Mirrors `_failure_message` (state_machine.py).
	failureMsg string
	// lastChange records the timestamp of the latest transition.
	// Mirrors `_last_state_change` (state_machine.py:170).
	lastChange time.Time
	// history is a bounded ring buffer of past transitions. Mirrors
	// `_state_history` (state_machine.py:172). Newer entries replace
	// the oldest once [HistorySize] is exceeded.
	history     []CentralTransition
	historyCap  int
	historyHead int
	historyLen  int

	now func() time.Time
}

// NewCentral returns a machine pinned to [hmenum.CentralStateStarting].
// bus may be nil; callers that do not care about events pass nil.
func NewCentral(name string, bus *events.Bus) *Central {
	return &Central{
		name:       name,
		cur:        hmenum.CentralStateStarting,
		bus:        bus,
		degraded:   make(map[string]hmenum.FailureReason),
		history:    make([]CentralTransition, DefaultHistorySize),
		historyCap: DefaultHistorySize,
		now:        time.Now,
	}
}

// State returns the current state.
func (c *Central) State() hmenum.CentralState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// FailureReason returns the reason recorded on the last transition into
// a FAILED / DEGRADED state.
func (c *Central) FailureReason() hmenum.FailureReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.why
}

// centralTransitionCfg holds the optional extra fields for [Central.TransitionTo].
type centralTransitionCfg struct {
	failureInterface   string
	degradedInterfaces map[string]hmenum.FailureReason
}

// CentralTransitionOption is a functional option for [Central.TransitionTo].
type CentralTransitionOption func(*centralTransitionCfg)

// WithFailureInterface records the interface_id that triggered a FAILED or
// DEGRADED transition. Stored atomically together with the state change.
func WithFailureInterface(id string) CentralTransitionOption {
	return func(c *centralTransitionCfg) { c.failureInterface = id }
}

// WithDegradedInterfaces replaces the degraded-interface map atomically
// during the transition. Each entry maps interface_id → failure reason.
// Passing a nil map is allowed; it clears the set on DEGRADED transitions
// when no specific interfaces are provided.
func WithDegradedInterfaces(m map[string]hmenum.FailureReason) CentralTransitionOption {
	return func(c *centralTransitionCfg) { c.degradedInterfaces = m }
}

// TransitionTo attempts to move to target. Returns
// [ErrInvalidTransition] when the target is unreachable from the
// current state. Emits a [hmevent.CentralStateChangedEvent] on success
// and appends an audit entry to the history ring buffer.
//
// Optional [CentralTransitionOption] values are applied atomically
// inside the same mutex section as the state change, so callers can set
// failure metadata without a separate [MarkInterfaceDegraded] call.
func (c *Central) TransitionTo(target hmenum.CentralState, reason hmenum.FailureReason, opts ...CentralTransitionOption) error {
	var cfg centralTransitionCfg
	for _, o := range opts {
		o(&cfg)
	}

	c.mu.Lock()
	from := c.cur
	if !c.canTransitionLocked(target) {
		c.mu.Unlock()
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, target)
	}
	c.cur = target
	c.why = reason
	c.lastChange = c.now()
	// Entering Running clears the degraded-interface set: the central is
	// fully recovered and no interfaces are considered degraded any longer.
	if target == hmenum.CentralStateRunning {
		c.degraded = make(map[string]hmenum.FailureReason)
		c.failureInterface = ""
	} else {
		// Apply optional failure metadata atomically with the state change.
		if cfg.failureInterface != "" {
			c.failureInterface = cfg.failureInterface
		}
		if cfg.degradedInterfaces != nil {
			// Replace the entire degraded-interface set so that interfaces
			// that recovered between two DEGRADED transitions are not
			// carried forward. The caller always supplies the complete
			// current set of degraded interfaces, never a diff.
			c.degraded = make(map[string]hmenum.FailureReason, len(cfg.degradedInterfaces))
			maps.Copy(c.degraded, cfg.degradedInterfaces)
		}
	}
	c.history[c.historyHead] = CentralTransition{
		From:   from,
		To:     target,
		Reason: reason,
		At:     c.lastChange,
		Forced: false,
	}
	c.historyHead = (c.historyHead + 1) % c.historyCap
	if c.historyLen < c.historyCap {
		c.historyLen++
	}
	bus := c.bus
	name := c.name
	c.mu.Unlock()

	if bus != nil {
		events.Publish(bus, hmevent.CentralStateChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: name,
			From:        from,
			To:          target,
			Reason:      reason,
		})
	}
	return nil
}

// ForceTransitionTo forces a state transition without validation against the
// allowed transitions in [centralTransitions]. Identical to [TransitionTo]
// but records `Forced: true` in the history entry.
//
// RUNNING → STOPPED without a STOPPING intermediate) is required.
//
// The history entry is marked with [CentralTransition.Forced] = true so that
// the audit log and tests can identify forced transitions.
func (c *Central) ForceTransitionTo(target hmenum.CentralState, reason hmenum.FailureReason) error {
	c.mu.Lock()
	from := c.cur
	c.cur = target
	c.why = reason
	c.lastChange = c.now()
	c.history[c.historyHead] = CentralTransition{
		From:   from,
		To:     target,
		Reason: reason,
		At:     c.lastChange,
		Forced: true,
	}
	c.historyHead = (c.historyHead + 1) % c.historyCap
	if c.historyLen < c.historyCap {
		c.historyLen++
	}
	bus := c.bus
	name := c.name
	c.mu.Unlock()

	if bus != nil {
		events.Publish(bus, hmevent.CentralStateChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: name,
			From:        from,
			To:          target,
			Reason:      reason,
		})
	}
	return nil
}

// LastStateChange returns the timestamp of the most recent transition,
// or the zero value when no transition has happened yet.
func (c *Central) LastStateChange() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastChange
}

// History returns a snapshot of the transition history, oldest first.
// The slice is a copy — callers may mutate freely.
func (c *Central) History() []CentralTransition {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.historyLen == 0 {
		return nil
	}
	out := make([]CentralTransition, c.historyLen)
	start := c.historyHead - c.historyLen
	if start < 0 {
		start += c.historyCap
	}
	for i := range c.historyLen {
		out[i] = c.history[(start+i)%c.historyCap]
	}
	return out
}

// MarkInterfaceDegraded records `interfaceID` as currently degraded with
// `reason`. The state machine itself is not advanced — callers transition
// explicitly.
func (c *Central) MarkInterfaceDegraded(interfaceID string, reason hmenum.FailureReason) {
	c.mu.Lock()
	c.degraded[interfaceID] = reason
	c.failureInterface = interfaceID
	c.mu.Unlock()
}

// ClearInterfaceDegraded removes `interfaceID` from the degraded set.
// Idempotent.
func (c *Central) ClearInterfaceDegraded(interfaceID string) {
	c.mu.Lock()
	delete(c.degraded, interfaceID)
	if c.failureInterface == interfaceID {
		c.failureInterface = ""
	}
	c.mu.Unlock()
}

// DegradedInterfaces returns a snapshot of currently-degraded
// interfaces with their failure reasons.
func (c *Central) DegradedInterfaces() map[string]hmenum.FailureReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.degraded) == 0 {
		return nil
	}
	out := make(map[string]hmenum.FailureReason, len(c.degraded))
	maps.Copy(out, c.degraded)
	return out
}

// FailureInterfaceID returns the interface_id that triggered the most
// recent FAILED / DEGRADED transition, or "" when no failure has been
// recorded yet.
func (c *Central) FailureInterfaceID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failureInterface
}

func (c *Central) canTransitionLocked(target hmenum.CentralState) bool {
	allowed, ok := centralTransitions[c.cur]
	if !ok {
		return false
	}
	_, can := allowed[target]
	return can
}

// CanTransitionTo reports whether the state machine can move to target from
// its current state.
func (c *Central) CanTransitionTo(target hmenum.CentralState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canTransitionLocked(target)
}

// FailureMessage returns the free-form human-readable message recorded
// on the last failure transition, or "" when no failure message was set.
func (c *Central) FailureMessage() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failureMsg
}

// SetFailureMessage stores a human-readable message alongside the
// failure state. Should be called before or during a transition to
// FAILED / DEGRADED.
func (c *Central) SetFailureMessage(msg string) {
	c.mu.Lock()
	c.failureMsg = msg
	c.mu.Unlock()
}

// IsDegraded reports whether the current state is DEGRADED.
func (c *Central) IsDegraded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateDegraded
}

// IsFailed reports whether the current state is FAILED.
func (c *Central) IsFailed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateFailed
}

// IsOperational reports whether the current state is either RUNNING or
// DEGRADED (i.e. the daemon is serving traffic).
func (c *Central) IsOperational() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateRunning || c.cur == hmenum.CentralStateDegraded
}

// IsRecovering reports whether the current state is RECOVERING.
func (c *Central) IsRecovering() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateRecovering
}

// IsRunning reports whether the current state is RUNNING.
func (c *Central) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateRunning
}

// IsStopped reports whether the current state is STOPPED.
func (c *Central) IsStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur == hmenum.CentralStateStopped
}

// SecondsInCurrentState returns the number of seconds elapsed since the last
// state transition. Returns 0 when no transition has occurred.
func (c *Central) SecondsInCurrentState() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastChange.IsZero() {
		return 0
	}
	return c.now().Sub(c.lastChange).Seconds()
}
