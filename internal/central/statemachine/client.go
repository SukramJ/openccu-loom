// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// clientTransitions enumerates valid client state transitions.
//
// Key parity fixes vs. the original stale table: - FAILED now allows →
// CONNECTING, RECONNECTING, DISCONNECTED - INITIALIZED now allows →
// DISCONNECTED (recovery reset) - DISCONNECTED now allows a self-transition
// (idempotent deinit)
var clientTransitions = map[hmenum.ClientState]map[hmenum.ClientState]struct{}{
	hmenum.ClientStateCreated: {
		hmenum.ClientStateInitializing: {},
		hmenum.ClientStateStopping:     {},
	},
	hmenum.ClientStateInitializing: {
		hmenum.ClientStateInitialized: {},
		hmenum.ClientStateFailed:      {},
		hmenum.ClientStateStopping:    {},
	},
	hmenum.ClientStateInitialized: {
		hmenum.ClientStateConnecting:   {},
		hmenum.ClientStateDisconnected: {}, // recovery reset
		hmenum.ClientStateStopping:     {},
	},
	hmenum.ClientStateConnecting: {
		hmenum.ClientStateConnected: {},
		hmenum.ClientStateFailed:    {},
		hmenum.ClientStateStopping:  {},
	},
	hmenum.ClientStateConnected: {
		hmenum.ClientStateDisconnected: {},
		hmenum.ClientStateReconnecting: {},
		hmenum.ClientStateStopping:     {},
	},
	hmenum.ClientStateDisconnected: {
		hmenum.ClientStateConnecting:   {},
		hmenum.ClientStateDisconnected: {}, // idempotent deinit
		hmenum.ClientStateReconnecting: {},
		hmenum.ClientStateStopping:     {},
	},
	hmenum.ClientStateReconnecting: {
		hmenum.ClientStateConnected:    {},
		hmenum.ClientStateDisconnected: {},
		hmenum.ClientStateConnecting:   {},
		hmenum.ClientStateFailed:       {},
		hmenum.ClientStateStopping:     {},
	},
	hmenum.ClientStateStopping: {
		hmenum.ClientStateStopped: {},
	},
	hmenum.ClientStateStopped: {},
	hmenum.ClientStateFailed: {
		hmenum.ClientStateInitializing: {},
		hmenum.ClientStateConnecting:   {},
		hmenum.ClientStateReconnecting: {},
		hmenum.ClientStateDisconnected: {},
		hmenum.ClientStateStopping:     {},
	},
}

// Client is the per-interface-client state machine.
type Client struct {
	centralName    string
	interfaceID    string
	iface          hmenum.Interface
	mu             sync.Mutex
	cur            hmenum.ClientState
	why            hmenum.FailureReason
	failureMessage string // human-readable failure message (state_machine.py:_failure_message)
	bus            *events.Bus
}

// NewClient returns a machine pinned to [hmenum.ClientStateCreated].
func NewClient(centralName, interfaceID string, iface hmenum.Interface, bus *events.Bus) *Client {
	return &Client{
		centralName: centralName,
		interfaceID: interfaceID,
		iface:       iface,
		cur:         hmenum.ClientStateCreated,
		bus:         bus,
	}
}

// State returns the current state.
func (c *Client) State() hmenum.ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// FailureReason returns the reason recorded on the last transition into
// a FAILED state.
func (c *Client) FailureReason() hmenum.FailureReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.why
}

// FailureMessage returns the human-readable message recorded on the last
// transition into a FAILED state.
func (c *Client) FailureMessage() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failureMessage
}

// TransitionTo attempts to move to target.
//
// msg is a human-readable reason for the transition (logged; stored when
// target is FAILED). force=true bypasses the transition guard — use with
// caution (mirrors state_machine.py transition_to force=True). failureReason
// is only stored when target == ClientStateFailed; on recovery states
// (CONNECTED, INITIALIZED) both failure fields are cleared.
func (c *Client) TransitionTo(target hmenum.ClientState, reason hmenum.FailureReason, opts ...TransitionOption) error {
	var cfg transitionCfg
	for _, o := range opts {
		o(&cfg)
	}

	c.mu.Lock()
	from := c.cur
	if !cfg.force && !c.canTransitionLocked(target) {
		c.mu.Unlock()
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, target)
	}
	c.cur = target
	// Track failure context when entering FAILED; clear on recovery.
	switch target { //nolint:exhaustive // only failure-relevant states need special handling
	case hmenum.ClientStateFailed:
		c.why = reason
		c.failureMessage = cfg.msg
	case hmenum.ClientStateConnected, hmenum.ClientStateInitialized:
		c.why = hmenum.FailureReasonNone
		c.failureMessage = ""
	}
	bus := c.bus
	centralName := c.centralName
	ifaceID := c.interfaceID
	iface := c.iface
	c.mu.Unlock()

	if bus != nil {
		events.Publish(bus, hmevent.ClientStateChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: centralName,
			InterfaceID: ifaceID,
			Interface:   iface,
			From:        from,
			To:          target,
			Reason:      reason,
		})
	}
	return nil
}

// transitionCfg holds optional parameters for [Client.TransitionTo].
type transitionCfg struct {
	msg   string
	force bool
}

// TransitionOption is a functional option for [Client.TransitionTo].
type TransitionOption func(*transitionCfg)

// WithReason attaches a human-readable message to a transition. Stored on the
// Client when target is FAILED. Mirrors state_machine.py's `reason` parameter.
func WithReason(msg string) TransitionOption {
	return func(c *transitionCfg) { c.msg = msg }
}

// WithForce bypasses the transition guard. Mirrors state_machine.py's
// `force=True` parameter — use only in recovery/test scenarios where a
// semantically invalid transition must be forced through.
func WithForce() TransitionOption {
	return func(c *transitionCfg) { c.force = true }
}

func (c *Client) canTransitionLocked(target hmenum.ClientState) bool {
	allowed, ok := clientTransitions[c.cur]
	if !ok {
		return false
	}
	_, can := allowed[target]
	return can
}

// CanTransitionTo reports whether the machine may transition from its current
// state to target.
func (c *Client) CanTransitionTo(target hmenum.ClientState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canTransitionLocked(target)
}

// IsAvailable reports whether the client can serve RPC traffic.
func (c *Client) IsAvailable() bool {
	s := c.State()
	return s == hmenum.ClientStateConnected || s == hmenum.ClientStateReconnecting
}

// IsConnected reports whether the client is in CONNECTED state.
func (c *Client) IsConnected() bool {
	return c.State() == hmenum.ClientStateConnected
}

// IsFailed reports whether the client is in FAILED state.
func (c *Client) IsFailed() bool {
	return c.State() == hmenum.ClientStateFailed
}

// IsStopped reports whether the client is in STOPPED state.
func (c *Client) IsStopped() bool {
	return c.State() == hmenum.ClientStateStopped
}

// CanReconnect reports whether RECONNECTING is a valid transition from the
// current state.
func (c *Client) CanReconnect() bool {
	return c.CanTransitionTo(hmenum.ClientStateReconnecting)
}
