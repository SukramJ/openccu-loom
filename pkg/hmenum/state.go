// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// CentralState is the central state machine's lifecycle state.
type CentralState string

// CentralState values.
const (
	CentralStateStarting     CentralState = "starting"
	CentralStateInitializing CentralState = "initializing"
	CentralStateRunning      CentralState = "running"
	CentralStateDegraded     CentralState = "degraded"
	CentralStateRecovering   CentralState = "recovering"
	CentralStateFailed       CentralState = "failed"
	CentralStateStopped      CentralState = "stopped"
)

// String returns the wire representation.
func (s CentralState) String() string { return string(s) }

// ClientState is a client's lifecycle state.
type ClientState string

// ClientState values.
const (
	ClientStateCreated      ClientState = "created"
	ClientStateInitializing ClientState = "initializing"
	ClientStateInitialized  ClientState = "initialized"
	ClientStateConnecting   ClientState = "connecting"
	ClientStateConnected    ClientState = "connected"
	ClientStateDisconnected ClientState = "disconnected"
	ClientStateReconnecting ClientState = "reconnecting"
	ClientStateStopping     ClientState = "stopping"
	ClientStateStopped      ClientState = "stopped"
	ClientStateFailed       ClientState = "failed"
)

// String returns the wire representation.
func (s ClientState) String() string { return string(s) }

// CircuitState is the client-side circuit breaker state.
type CircuitState string

// CircuitState values.
const (
	CircuitStateClosed   CircuitState = "closed"
	CircuitStateOpen     CircuitState = "open"
	CircuitStateHalfOpen CircuitState = "half_open"
)

// String returns the wire representation.
func (s CircuitState) String() string { return string(s) }

// FailureReason is the machine-readable cause when a state machine
// enters a FAILED state.
type FailureReason string

// FailureReason values.
const (
	FailureReasonNone           FailureReason = "none"
	FailureReasonAuth           FailureReason = "auth"
	FailureReasonNetwork        FailureReason = "network"
	FailureReasonInternal       FailureReason = "internal"
	FailureReasonTimeout        FailureReason = "timeout"
	FailureReasonCircuitBreaker FailureReason = "circuit_breaker"
	// FailureReasonExhausted signals that the recovery coordinator
	// hit its per-interface attempt cap. Operator action (or
	// `ResetAttempts`) is required to continue retrying.
	FailureReasonExhausted FailureReason = "exhausted"
	FailureReasonUnknown   FailureReason = "unknown"
)

// String returns the wire representation.
func (r FailureReason) String() string { return string(r) }
