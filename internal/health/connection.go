// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import (
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// EventStalenessThreshold is the cut-off after which a connection is
// considered "no longer receiving events".
const EventStalenessThreshold = 60 * time.Second

// Connection captures per-interface health metrics — events, request
// outcomes, reconnect attempts, circuit-breaker state.
//
// Connection is the typed companion to the generic [Tracker] — the tracker
// treats every component the same, the Connection knows about CCU semantics
// (events vs. requests, recovery flag, circuit state).
//
// Methods are safe for concurrent use.
type Connection struct {
	mu  sync.Mutex
	clk clock.Clock

	// Identifying fields. Set once at construction.
	interfaceID string
	iface       hmenum.Interface

	// State drivers — outside callers update via Record* methods so
	// internal invariants stay consistent.
	clientState         hmenum.ClientState
	xmlRPCCircuit       hmenum.CircuitState
	jsonRPCCircuit      hmenum.CircuitState
	jsonRPCCircuitKnown bool

	lastSuccess       time.Time
	lastFailure       time.Time
	lastEvent         time.Time
	lastReconnect     time.Time
	consecutiveFails  int
	reconnectAttempts int
	inRecovery        bool
}

// ConnectionSnapshot is the immutable read-side view. Callers own the
// returned value and may keep it indefinitely.
type ConnectionSnapshot struct {
	InterfaceID         string
	Interface           hmenum.Interface
	ClientState         hmenum.ClientState
	XMLRPCCircuit       hmenum.CircuitState
	JSONRPCCircuit      hmenum.CircuitState
	JSONRPCCircuitKnown bool
	LastSuccess         time.Time
	LastFailure         time.Time
	LastEvent           time.Time
	LastReconnect       time.Time
	ConsecutiveFailures int
	ReconnectAttempts   int
	InRecovery          bool
}

// NewConnection constructs a Connection in the CREATED state.
func NewConnection(interfaceID string, iface hmenum.Interface, opts ...ConnectionOption) *Connection {
	c := &Connection{
		interfaceID:   interfaceID,
		iface:         iface,
		clk:           clock.New(),
		clientState:   hmenum.ClientStateCreated,
		xmlRPCCircuit: hmenum.CircuitStateClosed,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// ConnectionOption configures a Connection at construction time.
type ConnectionOption func(*Connection)

// WithConnectionClock injects a clock — primarily for tests.
func WithConnectionClock(clk clock.Clock) ConnectionOption {
	return func(c *Connection) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// SetClientState updates the client-state-machine view.
func (c *Connection) SetClientState(s hmenum.ClientState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientState = s
}

// SetXMLRPCCircuit updates the XML-RPC circuit-breaker view.
func (c *Connection) SetXMLRPCCircuit(s hmenum.CircuitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xmlRPCCircuit = s
}

// SetJSONRPCCircuit updates the JSON-RPC circuit-breaker view. Pass a
// non-nil value to mark this connection as "knows about JSON-RPC";
// non-CCU connections leave it unset and queries return false.
func (c *Connection) SetJSONRPCCircuit(s hmenum.CircuitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jsonRPCCircuit = s
	c.jsonRPCCircuitKnown = true
}

// SetInRecovery toggles the recovery flag.
func (c *Connection) SetInRecovery(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inRecovery = v
}

// RecordSuccessfulRequest stamps the success timestamps and clears
// the consecutive-failure counter.
func (c *Connection) RecordSuccessfulRequest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSuccess = c.clk.Now()
	c.consecutiveFails = 0
}

// RecordFailedRequest stamps the failure timestamps and bumps the
// consecutive-failure counter.
func (c *Connection) RecordFailedRequest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastFailure = c.clk.Now()
	c.consecutiveFails++
}

// RecordEventReceived stamps the last-event timestamp. Used by the
// CanReceiveEvents check.
func (c *Connection) RecordEventReceived() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastEvent = c.clk.Now()
}

// RecordReconnectAttempt bumps the reconnect counter and stamps the
// timestamp. Counter is cleared by [ResetReconnectCounter] (typically
// after a successful recovery).
func (c *Connection) RecordReconnectAttempt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnectAttempts++
	c.lastReconnect = c.clk.Now()
}

// ResetReconnectCounter clears the reconnect-attempt counter.
func (c *Connection) ResetReconnectCounter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnectAttempts = 0
}

// Snapshot returns a coherent read of the connection.
func (c *Connection) Snapshot() ConnectionSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConnectionSnapshot{
		InterfaceID:         c.interfaceID,
		Interface:           c.iface,
		ClientState:         c.clientState,
		XMLRPCCircuit:       c.xmlRPCCircuit,
		JSONRPCCircuit:      c.jsonRPCCircuit,
		JSONRPCCircuitKnown: c.jsonRPCCircuitKnown,
		LastSuccess:         c.lastSuccess,
		LastFailure:         c.lastFailure,
		LastEvent:           c.lastEvent,
		LastReconnect:       c.lastReconnect,
		ConsecutiveFailures: c.consecutiveFails,
		ReconnectAttempts:   c.reconnectAttempts,
		InRecovery:          c.inRecovery,
	}
}

// IsConnected mirrors: client state
// machine reports CONNECTED.
func (c *Connection) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clientState == hmenum.ClientStateConnected
}

// IsDegraded mirrors: connected but the
// XML-RPC circuit is half-open OR a recovery is in flight.
func (c *Connection) IsDegraded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientState != hmenum.ClientStateConnected {
		return false
	}
	if c.xmlRPCCircuit == hmenum.CircuitStateHalfOpen {
		return true
	}
	if c.jsonRPCCircuitKnown && c.jsonRPCCircuit == hmenum.CircuitStateHalfOpen {
		return true
	}
	if c.inRecovery {
		return true
	}
	return false
}

// IsFailed mirrors: not connected OR an
// XML-RPC circuit is open. Connections in recovery are NOT failed.
func (c *Connection) IsFailed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientState != hmenum.ClientStateConnected {
		return true
	}
	if c.xmlRPCCircuit == hmenum.CircuitStateOpen {
		return true
	}
	if c.jsonRPCCircuitKnown && c.jsonRPCCircuit == hmenum.CircuitStateOpen {
		return true
	}
	return false
}

// CanReceiveEvents reports whether the connection has received an event
// within [EventStalenessThreshold].
func (c *Connection) CanReceiveEvents() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientState != hmenum.ClientStateConnected {
		return false
	}
	if c.lastEvent.IsZero() {
		return false
	}
	return c.clk.Now().Sub(c.lastEvent) < EventStalenessThreshold
}

// IsAvailable reports whether the connection is not in a failed state.
func (c *Connection) IsAvailable() bool {
	return !c.IsFailed()
}

// HealthScore returns a 0..1 numeric health score weighted 40 % state machine
// + 30 % circuit + 30 % activity.
func (c *Connection) HealthScore() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	score := 0.0
	// State machine (40 %)
	if c.clientState == hmenum.ClientStateConnected {
		score += 0.4
	}

	// Circuit breaker (30 %)
	circuitWeight := 0.3
	if c.jsonRPCCircuitKnown {
		// Average XML-RPC + JSON-RPC contribution.
		score += circuitScore(c.xmlRPCCircuit, circuitWeight/2)
		score += circuitScore(c.jsonRPCCircuit, circuitWeight/2)
	} else {
		score += circuitScore(c.xmlRPCCircuit, circuitWeight)
	}

	// Activity (30 %): last_successful_request inside staleness window.
	if !c.lastSuccess.IsZero() && c.clk.Now().Sub(c.lastSuccess) < EventStalenessThreshold {
		score += 0.3
	}
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func circuitScore(state hmenum.CircuitState, weight float64) float64 {
	switch state {
	case hmenum.CircuitStateClosed:
		return weight
	case hmenum.CircuitStateHalfOpen:
		return weight * 0.5
	default:
		return 0
	}
}

// ConnectionRegistry tracks per-interface [Connection] entries with
// thread-safe Get/All semantics.
type ConnectionRegistry struct {
	mu          sync.RWMutex
	connections map[string]*Connection
}

// NewConnectionRegistry returns an empty registry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{connections: make(map[string]*Connection)}
}

// Register adds c under c.interfaceID. Idempotent — re-registering
// with the same id replaces the entry.
func (r *ConnectionRegistry) Register(c *Connection) {
	if c == nil {
		return
	}
	r.mu.Lock()
	r.connections[c.interfaceID] = c
	r.mu.Unlock()
}

// Get returns the registered Connection by interface id, or (nil, false).
func (r *ConnectionRegistry) Get(interfaceID string) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connections[interfaceID]
	return c, ok
}

// Remove drops the entry by interface id. Returns whether anything was removed.
func (r *ConnectionRegistry) Remove(interfaceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.connections[interfaceID]; !ok {
		return false
	}
	delete(r.connections, interfaceID)
	return true
}

// All returns every registered connection, sorted by interface id.
func (r *ConnectionRegistry) All() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Connection, 0, len(r.connections))
	for _, c := range r.connections {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].interfaceID < out[j].interfaceID })
	return out
}

// AllHealthy reports whether every registered connection passes
// IsConnected + !IsDegraded. An empty registry returns false ("nothing
// is healthy yet").
func (r *ConnectionRegistry) AllHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.connections) == 0 {
		return false
	}
	for _, c := range r.connections {
		if !c.IsConnected() || c.IsDegraded() {
			return false
		}
	}
	return true
}

// AnyHealthy reports whether at least one registered connection is
// connected + not degraded.
func (r *ConnectionRegistry) AnyHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.connections {
		if c.IsConnected() && !c.IsDegraded() {
			return true
		}
	}
	return false
}

// OverallHealthScore averages [Connection.HealthScore] over every
// registered connection. Empty registry returns 0.
func (r *ConnectionRegistry) OverallHealthScore() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.connections) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range r.connections {
		total += c.HealthScore()
	}
	return total / float64(len(r.connections))
}

// UpdateFromClient refreshes the Connection's client-state snapshot from the
// given client state. The actual authoritative source remains the client
// state machine; this is a point-in-time pull to keep the health view
// consistent after a batch update.
func (c *Connection) UpdateFromClient(clientState hmenum.ClientState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientState = clientState
}

// UpdateAllFromClients refreshes every registered [Connection] by calling
// [Connection.UpdateFromClient] for each entry whose interfaceID is found in
// the states map. Extra interface IDs in the map are silently ignored.
func (r *ConnectionRegistry) UpdateAllFromClients(states map[string]hmenum.ClientState) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, state := range states {
		if c, ok := r.connections[id]; ok {
			c.UpdateFromClient(state)
		}
	}
}

// UpdateClientHealth applies a single client state change to the registered
// Connection for interfaceID. When the new state is RECONNECTING (and the old
// was not), it records a reconnect attempt. When the new state is CONNECTED
// it resets the reconnect counter.
func (r *ConnectionRegistry) UpdateClientHealth(interfaceID string, oldState, newState hmenum.ClientState) {
	r.mu.RLock()
	c, ok := r.connections[interfaceID]
	r.mu.RUnlock()
	if !ok || c == nil {
		return
	}
	c.UpdateFromClient(newState)
	switch {
	case newState == hmenum.ClientStateReconnecting && oldState != hmenum.ClientStateReconnecting:
		c.RecordReconnectAttempt()
	case newState == hmenum.ClientStateConnected:
		c.ResetReconnectCounter()
	}
}

// ShouldBeDegraded reports whether the central should transition to DEGRADED
// state: at least one connection is healthy but not all.
func (r *ConnectionRegistry) ShouldBeDegraded() bool {
	return r.AnyHealthy() && !r.AllHealthy()
}

// ShouldBeRunning reports whether the central should be in RUNNING state:
// every registered connection is healthy (all available).
func (r *ConnectionRegistry) ShouldBeRunning() bool {
	return r.AllHealthy()
}

// PrimaryClientHealthy reports whether the primary client (the HmIP-RF
// interface if present, otherwise the first registered) is healthy.
func (r *ConnectionRegistry) PrimaryClientHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.connections) == 0 {
		return false
	}
	// Prefer HmIP-RF as the primary interface.
	for _, c := range r.connections {
		if c.iface == hmenum.InterfaceHmIPRF {
			return c.IsConnected() && !c.IsDegraded()
		}
	}
	// Fallback: first registered connection.
	for _, c := range r.connections {
		return c.IsConnected() && !c.IsDegraded()
	}
	return false
}
