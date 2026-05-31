// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package client orchestrates one reliable southbound channel per
// (central, interface) pair: it composes the transport-level clients
// from internal/client/transport with the reliability primitives from
// internal/client/reliability.
//
// The orchestrator is deliberately small — it does not know about
// CCU method semantics. Backend-specific method wrappers live in
// internal/client/backends (post-0.1.0 scaffolding) and the
// coordinators.
package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Caller is the minimal contract we need from a concrete transport
// client to drive it through the reliability stack. Both xmlrpc.Client
// and binrpc.Client satisfy this shape via the single-method wrapper
// [CallerFunc].
type Caller interface {
	Call(ctx context.Context, method string, params []any) (any, error)
}

// CallerFunc adapts a plain function to the [Caller] interface.
type CallerFunc func(ctx context.Context, method string, params []any) (any, error)

// Call implements Caller.
func (f CallerFunc) Call(ctx context.Context, method string, params []any) (any, error) {
	return f(ctx, method, params)
}

// Config configures an [InterfaceClient].
type Config struct {
	// CentralName identifies the owning central. Appears in every
	// log and error.
	CentralName string

	// Interface is the logical interface (HmIP-RF, CUxD, …).
	Interface hmenum.Interface

	// Enabled, when set to false explicitly, marks the interface as
	// administratively disabled. The client refuses to forward calls and
	// [InterfaceClient.Enabled] returns false. The zero value (false) would
	// disable all clients, so callers must set this to true or use
	// [Config.WithEnabled] to construct a live config. Internally [New] treats
	// an unset field as enabled to stay backwards-compatible — see the enabled
	// field on [InterfaceClient].
	Enabled bool

	// Caller is the underlying transport.
	Caller Caller

	// Circuit, Retry, Throttle, Coalescer, PingPong — constructor
	// defaults apply when left nil.
	Circuit *reliability.CircuitBreaker
	Retrier *reliability.Retrier

	// Throttle is the legacy single-pool throttle. When set, it is
	// used as a *fallback* for any RPC class whose dedicated pool
	// (ReadThrottle / WriteThrottle / ControlThrottle) is left nil.
	// In greenfield wiring callers should fill the per-class pools
	// instead so reads do not get blocked behind writes.
	Throttle *reliability.CommandThrottle

	// ReadThrottle paces RPCs classified as [reliability.RPCClassRead]
	// (getValue, getParamset, listDevices, …). Nil falls back to
	// [Throttle] or, if that is also nil, a fresh single-pool throttle.
	ReadThrottle *reliability.CommandThrottle

	// WriteThrottle paces RPCs classified as [reliability.RPCClassWrite]
	// (setValue, putParamset, addLink, …) and unknown methods (treated
	// as writes for safety). Nil falls back to [Throttle] or a fresh
	// single-pool throttle.
	WriteThrottle *reliability.CommandThrottle

	// ControlThrottle paces RPCs classified as
	// [reliability.RPCClassControl] (init, ping, session.*). Nil falls
	// back to [Throttle] or a fresh single-pool throttle. In typical
	// deployments callers wire a near-unbounded throttle here so a
	// reconnect storm does not stall device traffic.
	ControlThrottle *reliability.CommandThrottle

	Coalescer    *reliability.Coalescer
	PingPong     *reliability.PingPongTracker
	Capabilities backends.Capabilities
	Logger       *slog.Logger

	// Version is the software version string returned by the backend during
	// detection (e.g. from the XML-RPC getVersion call). The coordinator
	// populates this after [backends.DetectBackend] completes. Empty string
	// means "not yet determined".
	Version string

	// BackendKind is the detected backend flavour. Coordinators set this
	// after [backends.DetectBackend] completes. Defaults to
	// [backends.KindCCU] when left zero. Used by [InterfaceClient.Model].
	// Mirrors the implicit backend.model delegation.
	// InterfaceClient.model property (client/interface_client.py:194).
	BackendKind backends.Kind

	// RegaRunner, when non-nil, enables [InterfaceClient.AcknowledgeMessage].
	// Only meaningful on CCU interfaces that expose a JSON-RPC / ReGa
	// endpoint (HmIP-RF, BidCos-RF, …); leave nil for CUxD / wired
	// interfaces that don't have one.
	RegaRunner *rega.Runner

	// SessionRecorderHook, when non-nil, is called after every successful
	// SetValue or PutParamset so the CacheCoordinator's session recorder
	// can capture the CCU-communication trace. The hook is intentionally
	// typed as a plain function (not a full interface) to keep the client
	// package free of a dependency on the coordinators or store packages.
	//
	// rpcType is "xml-rpc" or "json-rpc" (matching session.RPCTypeXML
	// session.RPCTypeJSON constants). method is the RPC method name
	// ("setValue" or "putParamset"). params and response are the wire
	// arguments / result values.
	//
	// Nil = no recording (default; no overhead). The daemon wires this
	// from the CacheCoordinator.RecordSession passthrough. Closes
	// the Item 2 gap in (RecordSession-Wiring).
	SessionRecorderHook func(rpcType, method string, params, response any)
}

// InterfaceClient wraps a transport caller with the reliability stack.
// One instance per (central, interface) pair.
type InterfaceClient struct {
	cfg Config

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	mu          sync.Mutex
	state       hmenum.ClientState
	closed      bool
	enabled     bool            // mirrors Config.Enabled; runtime-mutable via Disable()
	stateWakers []chan struct{} // released on every state transition

	// sm is the validated state machine. It is created in [New] and kept in sync
	// with the legacy inline state via [SetState]. Callers that need the full
	// state-machine API (TransitionTo, Reset, FailureMessage, FailureReason, …)
	// access it via [StateMachine].
	sm *ClientStateMachine

	// callbackMu protects the last-callback timestamp the central's
	// callback handler stamps on every inbound event. The freshness
	// of this timestamp drives [IsCallbackAlive].
	callbackMu     sync.Mutex
	lastCallbackAt time.Time

	// JSON-RPC session integration: the central wires
	// `clear_json_rpc_session` to a hub-coordinator hook that
	// re-issues Login on the next call. Stored as a function so the
	// wiring stays decoupled from the actual transport.
	clearJSONSessionFn func()

	// commandTracker is the last_value_send_tracker: tracks recently sent
	// command values for optimistic-update feedback. Constructed lazily on first
	// access via [CommandTracker()].
	commandTracker *reliability.CommandTracker

	// Atomic request counters mirroring
	// counters surfaced on the metrics aggregator. They are
	// incremented around every [Call] and read lock-free by the
	// metrics provider in interface_client_metrics.go.
	totalRequests    atomic.Int64
	executedRequests atomic.Int64
	pendingRequests  atomic.Int64

	// failureMu guards lastFailureAt — written from the call hot
	// path on every error and read by the metrics provider.
	failureMu     sync.Mutex
	lastFailureAt time.Time

	// modifiedAt tracks the last time a DataPoint value was received for this
	// interface. Protected by mu.
	modifiedAt time.Time

	// forcedAvailability stores the current forced-availability mode
	// requested by MarkAllDevicesForced. Coordinators query this to decide
	// what availability state to push to the device registry. Protected by mu.
	forcedAvailability ForcedAvailability

	// pingSeq is a monotonically-increasing counter used to generate unique
	// per-ping tokens. Incremented atomically on every outbound ping so the
	// CCU's echoed caller_id can be matched back to the correct pending entry.
	pingSeq atomic.Uint64

	// reconnectAttempts is incremented by the caller each time a reconnect
	// attempt fails and reset to zero on success. exposed via
	// ReconnectAttempts() / SetReconnectAttempts().
	reconnectAttempts int
}

// New constructs a client, filling in defaults for any nil reliability
// primitive.
func New(cfg Config) (*InterfaceClient, error) {
	if cfg.CentralName == "" {
		return nil, errors.New("client: Config.CentralName is required")
	}
	if cfg.Interface == "" {
		return nil, errors.New("client: Config.Interface is required")
	}
	if cfg.Caller == nil {
		return nil, errors.New("client: Config.Caller is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Circuit == nil {
		cfg.Circuit = reliability.NewCircuit(reliability.CircuitConfig{})
		// Default incident listener — log the state change on trip / recovery so
		// that an audit consumer (e.g. internal/audit) can observe events without
		// explicit wiring. In the hexagonal architecture openccu-loom has no direct
		// adapter; this default listener provides the equivalent without additional
		// wiring overhead.
		ifaceID := string(cfg.Interface)
		central := cfg.CentralName
		logger := cfg.Logger
		cfg.Circuit.AddOnStateChange(func(from, to hmenum.CircuitState) {
			logger.Info(
				"circuit-breaker state changed",
				slog.String("central", central),
				slog.String("interface_id", ifaceID),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		})
	}
	if cfg.Retrier == nil {
		cfg.Retrier = reliability.NewRetrier(reliability.RetryConfig{})
	}
	if cfg.Throttle == nil {
		cfg.Throttle = reliability.NewThrottle(reliability.ThrottleConfig{})
	}
	// Per-class throttles fall back to the single-pool [Config.Throttle]
	// to keep older callers working unchanged. When operators want
	// separate read/write pacing they fill ReadThrottle / WriteThrottle
	// explicitly — the fallback below ensures nil means "share the
	// legacy pool" rather than "pass everything through unthrottled".
	if cfg.ReadThrottle == nil {
		cfg.ReadThrottle = cfg.Throttle
	}
	if cfg.WriteThrottle == nil {
		cfg.WriteThrottle = cfg.Throttle
	}
	if cfg.ControlThrottle == nil {
		cfg.ControlThrottle = cfg.Throttle
	}
	if cfg.Coalescer == nil {
		cfg.Coalescer = reliability.NewCoalescer()
	}
	if cfg.PingPong == nil {
		cfg.PingPong = reliability.NewPingPongTracker(reliability.PingPongConfig{})
	}
	if (cfg.Capabilities == backends.Capabilities{}) {
		cfg.Capabilities = backends.CapabilityFor(backends.KindFor(cfg.Interface))
	}
	// New always starts the client enabled. Callers that want a disabled
	// client call Disable() after construction. The Config.Enabled field
	// documents intent but the runtime kill-switch is the enabled field
	// on InterfaceClient.
	return &InterfaceClient{
		cfg:     cfg,
		state:   hmenum.ClientStateCreated,
		enabled: true,
		sm:      NewClientStateMachine(),
	}, nil
}

// Capabilities returns the capability profile this client exposes.
func (c *InterfaceClient) Capabilities() backends.Capabilities {
	return c.cfg.Capabilities
}

// Call executes method/params through the reliability stack.
// Priority governs throttle ordering. coalesceKey, when non-empty,
// deduplicates concurrent in-flight calls against the same key.
//
// Every Call increments [TotalRequests] and [PendingRequests] (the
// latter is decremented when the inner stack returns). [ExecutedRequests]
// is bumped only when the call actually reaches the transport — i.e.
// the leader of a coalesce group, never the followers. Errors stamp
// [LastFailureTime] so the metrics aggregator can surface the most
// recent failure across a fleet of clients.
func (c *InterfaceClient) Call(
	ctx context.Context,
	method string,
	params []any,
	priority hmenum.CommandPriority,
	coalesceKey string,
) (any, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("client: closed")
	}
	if !c.enabled {
		c.mu.Unlock()
		return nil, errors.New("client: disabled")
	}
	c.mu.Unlock()

	c.totalRequests.Add(1)
	c.pendingRequests.Add(1)
	defer c.pendingRequests.Add(-1)

	throttle := c.throttleForMethod(method)

	execute := func(ctx context.Context) (any, error) {
		c.executedRequests.Add(1)
		if err := throttle.Acquire(ctx, priority); err != nil {
			return nil, err
		}
		defer throttle.Release()

		var result any
		err := c.cfg.Circuit.Do(ctx, method, func(ctx context.Context) error {
			return c.cfg.Retrier.Do(ctx, func(ctx context.Context, _ int) error {
				v, err := c.cfg.Caller.Call(ctx, method, params)
				if err != nil {
					return err
				}
				result = v
				return nil
			})
		})
		return result, err
	}

	var (
		out any
		err error
	)
	if coalesceKey != "" {
		out, err = c.cfg.Coalescer.Do(ctx, coalesceKey, execute)
	} else {
		out, err = execute(ctx)
	}
	if err != nil {
		c.failureMu.Lock()
		c.lastFailureAt = time.Now()
		c.failureMu.Unlock()
	}
	return out, err
}

// RecordPing notes the outbound ping identifier.
func (c *InterfaceClient) RecordPing(id string) { c.cfg.PingPong.RecordPing(id) }

// RecordPong returns whether id matched an outstanding ping and the
// round-trip time for matched pairs (zero for unmatched).
func (c *InterfaceClient) RecordPong(id string) (matched bool, rtt time.Duration) {
	return c.cfg.PingPong.RecordPong(id)
}

// SweepPingPong drains expired pending / unknown entries.
func (c *InterfaceClient) SweepPingPong() []reliability.Mismatch {
	return c.cfg.PingPong.Sweep()
}

// PingPong exposes the underlying tracker so callers can install
// hooks (e.g. [reliability.PingPongTracker.SetPublishHook]) after
// construction. The returned pointer is stable for the lifetime of
// the client.
func (c *InterfaceClient) PingPong() *reliability.PingPongTracker {
	return c.cfg.PingPong
}

// SetPublishHook installs the threshold-crossing callback on the
// underlying PingPongTracker. Mirrors
// [reliability.PingPongTracker.SetPublishHook] — delegates to keep
// callers from importing the reliability package just for wiring.
func (c *InterfaceClient) SetPublishHook(fn func(kind hmenum.PingPongMismatchType, count int)) {
	c.cfg.PingPong.SetPublishHook(fn)
}

// SetConnectionIssueGate installs the connection-issue predicate on
// the underlying PingPongTracker. Mirrors
// [reliability.PingPongTracker.SetConnectionIssueGate].
func (c *InterfaceClient) SetConnectionIssueGate(fn func() bool) {
	c.cfg.PingPong.SetConnectionIssueGate(fn)
}

// Close marks the client permanently stopped, cancels all in-flight
// retry chains, and closes its throttles. Subsequent Call invocations
// return an error.
func (c *InterfaceClient) Close() {
	c.mu.Lock()
	c.closed = true
	prev := c.state
	c.state = hmenum.ClientStateStopped
	wakers := c.stateWakers
	c.stateWakers = nil
	c.mu.Unlock()
	if prev != hmenum.ClientStateStopped {
		for _, w := range wakers {
			close(w)
		}
	}
	// Cancel any stale retry chains so they do not keep wire resources occupied
	// after the interface shuts down.
	if c.cfg.Retrier != nil {
		c.cfg.Retrier.CancelInterface()
	}
	c.closeThrottles()
}

// throttleForMethod returns the per-class throttle for method, falling
// back to [Config.Throttle] for methods the classifier does not
// recognise (treated as writes — same conservative default as
// [reliability.RPCClassUnknown]).
func (c *InterfaceClient) throttleForMethod(method string) *reliability.CommandThrottle {
	switch reliability.ClassifyMethod(method) {
	case reliability.RPCClassRead:
		return c.cfg.ReadThrottle
	case reliability.RPCClassControl:
		return c.cfg.ControlThrottle
	case reliability.RPCClassWrite, reliability.RPCClassUnknown:
		return c.cfg.WriteThrottle
	default:
		return c.cfg.WriteThrottle
	}
}

// closeThrottles closes every distinct throttle attached to the
// client. Identity-deduplicated so the legacy single-pool layout (one
// throttle aliased to all three slots) does not call Close three times
// — CommandThrottle.Close is idempotent but unnecessary work.
func (c *InterfaceClient) closeThrottles() {
	seen := make(map[*reliability.CommandThrottle]struct{}, 4)
	for _, t := range []*reliability.CommandThrottle{
		c.cfg.Throttle, c.cfg.ReadThrottle, c.cfg.WriteThrottle, c.cfg.ControlThrottle,
	} {
		if t == nil {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		t.Close()
	}
}

// ClientState returns the current client state.
func (c *InterfaceClient) ClientState() hmenum.ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// SetState updates the client state and unblocks every goroutine
// currently waiting in [WaitForState] for the new value.
func (c *InterfaceClient) SetState(s hmenum.ClientState) {
	c.mu.Lock()
	if c.state == s {
		c.mu.Unlock()
		return
	}
	c.state = s
	wakers := c.stateWakers
	c.stateWakers = nil
	c.mu.Unlock()
	for _, w := range wakers {
		close(w)
	}
}

// WaitForState blocks until the client transitions to `target` or the context
// is cancelled. Returns nil on transition, ctx.Err() on timeout/cancel.
//
// Returns immediately when the state already matches.
func (c *InterfaceClient) WaitForState(ctx context.Context, target hmenum.ClientState) error {
	for {
		c.mu.Lock()
		if c.state == target {
			c.mu.Unlock()
			return nil
		}
		w := make(chan struct{})
		c.stateWakers = append(c.stateWakers, w)
		c.mu.Unlock()
		select {
		case <-w:
			// loop back; state may have changed to target or to a
			// different non-target value (in which case we re-arm).
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Interface returns the wrapped interface tag.
func (c *InterfaceClient) Interface() hmenum.Interface { return c.cfg.Interface }

// CentralName returns the owning central's name.
func (c *InterfaceClient) CentralName() string { return c.cfg.CentralName }

// callbackFreshness is the window during which a recent inbound callback
// counts as "alive". 15s scheduler tick × 12 = 180s of headroom before a
// silent interface is treated as broken.
const callbackFreshness = 180 * time.Second

// NotifyCallback stamps the last-callback timestamp. Called from the
// central's callback handler on every inbound event so
// [IsCallbackAlive] can report freshness without polling.
func (c *InterfaceClient) NotifyCallback() {
	c.callbackMu.Lock()
	c.lastCallbackAt = time.Now()
	c.callbackMu.Unlock()
}

// LastCallbackAt returns the timestamp of the most recent inbound
// event, or the zero time when no callback has been observed yet.
func (c *InterfaceClient) LastCallbackAt() time.Time {
	c.callbackMu.Lock()
	defer c.callbackMu.Unlock()
	return c.lastCallbackAt
}

// IsCallbackAlive reports whether the central has received an inbound event
// from this interface within [callbackFreshness].
//
// Order of checks (gating before the freshness window matters — without
// these guards a freshly initialised client or a reconnect in progress
// would otherwise produce false negatives):
//   - Backend without ping/pong (e.g. CUxD via BIN-RPC): always true.
//   - Client state FAILED or RECONNECTING: false (no reliable signal).
//   - No callback observed yet (zero timestamp): true (the silence window
//     starts only after the first event has been seen, otherwise every
//     scheduler tick during init would trip ConnectionLost).
//   - Otherwise: last callback within [callbackFreshness].
func (c *InterfaceClient) IsCallbackAlive() bool {
	if !c.cfg.Capabilities.PingPong {
		return true
	}
	switch c.ClientState() {
	case hmenum.ClientStateFailed, hmenum.ClientStateReconnecting:
		return false
	default:
		// All other states: proceed to check the callback timestamp below.
	}
	c.callbackMu.Lock()
	last := c.lastCallbackAt
	c.callbackMu.Unlock()
	if last.IsZero() {
		return true
	}
	return time.Since(last) < callbackFreshness
}

// SetClearJSONRPCSessionHook wires the function the central calls when
// the JSON-RPC session must be invalidated (e.g. after a 401 from the
// hub coordinator).
// Pass nil to remove the hook.
func (c *InterfaceClient) SetClearJSONRPCSessionHook(fn func()) {
	c.mu.Lock()
	c.clearJSONSessionFn = fn
	c.mu.Unlock()
}

// ClearJSONRPCSession invokes the registered hook (if any) so the
// hub coordinator can drop the cached session ID and re-login on the
// next request.
func (c *InterfaceClient) ClearJSONRPCSession() {
	c.mu.Lock()
	fn := c.clearJSONSessionFn
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// AllCircuitBreakersClosed reports whether every circuit breaker attached to
// this client is currently CLOSED (i.e. the circuit is healthy). In the Go
// implementation each InterfaceClient has exactly one CircuitBreaker, so this
// is equivalent to cb.State() == CLOSED.
func (c *InterfaceClient) AllCircuitBreakersClosed() bool {
	return c.cfg.Circuit.State() == hmenum.CircuitStateClosed
}

// IsInitialized reports whether the client has completed its initialization
// sequence (state ≥ INITIALIZED).
//
// A client is considered initialized once it has reached
// ClientStateInitialized or beyond (Connected, Reconnecting, …). CREATED and
// INITIALIZING states return false.
func (c *InterfaceClient) IsInitialized() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	switch s {
	case hmenum.ClientStateCreated, hmenum.ClientStateInitializing:
		return false
	default:
		return true
	}
}

// IsAvailable reports whether the current client state implies the interface
// can accept commands (CONNECTED or RECONNECTING).
func (c *InterfaceClient) IsAvailable() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s == hmenum.ClientStateConnected || s == hmenum.ClientStateReconnecting
}

// IsConnected reports whether the current client state is CONNECTED.
func (c *InterfaceClient) IsConnected() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s == hmenum.ClientStateConnected
}

// IsFailed reports whether the current client state is FAILED.
func (c *InterfaceClient) IsFailed() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s == hmenum.ClientStateFailed
}

// IsStopped reports whether the current client state is STOPPED.
func (c *InterfaceClient) IsStopped() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s == hmenum.ClientStateStopped
}

// CanReconnect reports whether the current state allows the recovery
// coordinator to initiate a reconnect cycle. Only DISCONNECTED and FAILED
// support reconnect.
func (c *InterfaceClient) CanReconnect() bool {
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	return s == hmenum.ClientStateDisconnected || s == hmenum.ClientStateFailed
}

// ResetCircuitBreakers forces the circuit breaker(s) attached to this client
// back to CLOSED state. Used by admin operators after a manual CCU recovery
// so the client can resume without waiting for the lazy OPEN→HALF_OPEN→CLOSED
// cycle.
func (c *InterfaceClient) ResetCircuitBreakers() {
	if c.cfg.Circuit != nil {
		c.cfg.Circuit.Reset()
	}
}

// ReinitProxy is a convenience shortcut that deinits and then re-inits the
// backend callback registration in one call. The caller provides the same
// interfaceID and callbackURL it would pass to a manual deinit + init
// sequence.
//
// ReinitProxy is intentionally thin — it does not change the client state
// machine; the caller (usually the connection coordinator) drives the state
// transitions around it.
func (c *InterfaceClient) ReinitProxy(ctx context.Context, b backends.Operations, interfaceID, callbackURL string) error {
	if b == nil {
		return errors.New("client.ReinitProxy: backend is nil")
	}
	if err := b.Deinit(ctx, interfaceID); err != nil {
		// Log but do not abort — a deinit failure should not prevent
		// the subsequent init from running (the CCU may already have
		// timed out the old registration).
		c.cfg.Logger.Warn(
			"ReinitProxy: deinit failed",
			slog.String("central", c.cfg.CentralName),
			slog.String("interface", string(c.cfg.Interface)),
			slog.Any("err", err),
		)
	}
	if err := b.Init(ctx, interfaceID, callbackURL); err != nil {
		return err
	}
	// Re-probe capabilities after reconnect. The CCU firmware version may have
	// changed while the connection was down, so the cached capability profile
	// can be stale. Failures are soft: the backend retains its previous probed
	// values (or the conservative static defaults if it was never probed).
	if initErr := backends.MaybeInitialize(ctx, b); initErr != nil {
		c.cfg.Logger.Warn(
			"ReinitProxy: capability re-probe failed; previous capabilities retained",
			slog.String("central", c.cfg.CentralName),
			slog.String("interface", interfaceID),
			slog.Any("err", initErr),
		)
	}
	c.cfg.Logger.Info(
		"wire.reinit.ok",
		slog.String("central", c.cfg.CentralName),
		slog.String("interface", interfaceID),
		slog.String("callback", callbackURL),
	)
	return nil
}

// CheckConnectionAvailability performs a lightweight availability probe by
// calling Ping on the backend when ping_pong capability is present, then
// optionally recording the outbound ping token in the
// [reliability.PingPongTracker]. Returns true when the backend answers.
//
// When handlePingPong is true and the backend declares ping_pong capability,
// a unique token is generated and embedded in the caller_id sent to the CCU
// as "<interfaceID>#<token>". The token is recorded in the tracker BEFORE the
// RPC call — the CCU may echo the PONG back before Call() returns, so the
// tracker must have the entry in place first. The CCU echoes the full
// caller_id string in the PONG event; the event coordinator extracts the
// token suffix and calls RecordPong to close the round-trip.
//
// The method is intentionally thin — the full is_connected orchestration
// (tracking connection-error counts, forcing device availability) lives in
// the coordinator layer.
func (c *InterfaceClient) CheckConnectionAvailability(ctx context.Context, handlePingPong bool) bool {
	if c.cfg.Circuit == nil {
		return false
	}
	// Build the caller_id. When ping_pong correlation is requested and the
	// backend supports it, embed a unique token so the CCU's PONG echo can
	// be matched back to this probe.
	callerID := string(c.cfg.Interface)
	if handlePingPong && c.cfg.Capabilities.PingPong {
		seq := c.pingSeq.Add(1)
		token := fmt.Sprintf("%d", seq)
		callerID = string(c.cfg.Interface) + "#" + token
		// Record the pending ping before sending — the CCU may deliver the
		// PONG event before the RPC call returns on fast transports.
		c.RecordPing(token)
	}
	// Use the circuit breaker's PROBE path — operation name
	// "check_connection" is deliberately NOT on the bypass list so
	// the call drives the state machine: while the breaker is OPEN
	// the call is rejected with ErrCircuitBreakerOpen (CCU unreachable
	// → we report false); once ResetTimeout has elapsed Do() flips
	// the breaker to HALF_OPEN and lets the ping through, and the
	// recorded success advances HALF_OPEN → CLOSED on the second
	// successful probe. "ping" itself is still on the bypass list so
	// other call sites (detection, init) keep their unconditional
	// path.
	var callErr error
	err := c.cfg.Circuit.Do(ctx, "check_connection", func(ctx context.Context) error {
		_, callErr = c.cfg.Caller.Call(ctx, "ping", []any{callerID})
		return callErr
	})
	return err == nil && callErr == nil
}

// VirtualRemote reports the address of the CCU's virtual-remote device for
// the wrapped interface, if any.
//
// The second return reports whether the wrapped interface has a
// virtual-remote concept at all. CUxD / VirtualDevices / wired interfaces
// have none → returns ("", false).
func (c *InterfaceClient) VirtualRemote() (string, bool) {
	switch c.cfg.Interface { //nolint:exhaustive // Wired / CUxD / VirtualDevices have no virtual-remote concept; all non-RF interfaces return ("", false)
	case hmenum.InterfaceHmIPRF:
		return "HmIP-RF", true
	case hmenum.InterfaceBidCosRF:
		return "BidCoS-RF", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// StateMachine — expose the validated state machine
// ---------------------------------------------------------------------------

// StateMachine returns the [ClientStateMachine] attached to this client. The
// state machine validates every transition against the canonical table and
// records failure metadata. North-bound adapters and the connection
// coordinator should drive state via [ClientStateMachine.TransitionTo]
// instead of the lower-level [SetState].
func (c *InterfaceClient) StateMachine() *ClientStateMachine {
	return c.sm
}

// ---------------------------------------------------------------------------
// FailureMessage / FailureReason delegates
// ---------------------------------------------------------------------------

// FailureMessage returns the human-readable failure description set by the
// last transition into the FAILED state, delegating to the embedded
// [ClientStateMachine]. Returns an empty string when no failure has occurred.
func (c *InterfaceClient) FailureMessage() string {
	return c.sm.FailureMessage()
}

// FailureReason returns the machine-readable failure category set by the last
// transition into the FAILED state, delegating to the embedded
// [ClientStateMachine]. Returns [hmenum.FailureReasonNone] when no failure
// has occurred.
func (c *InterfaceClient) FailureReason() hmenum.FailureReason {
	return c.sm.FailureReason()
}

// ---------------------------------------------------------------------------
// Enabled / Disable
// ---------------------------------------------------------------------------

// Enabled reports whether this client is administratively active. When false,
// [Call] returns an error immediately without reaching the transport. Default
// is true for all clients constructed via [New].
func (c *InterfaceClient) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// Disable marks the client as administratively disabled at runtime.
// Subsequent [Call] invocations return an error immediately. The client can
// be re-enabled by calling [Enable].
func (c *InterfaceClient) Disable() {
	c.mu.Lock()
	c.enabled = false
	c.mu.Unlock()
}

// Enable marks the client as administratively active again after a
// previous [Disable]. Safe to call concurrently. This is a openccu-loom
// addition.
func (c *InterfaceClient) Enable() {
	c.mu.Lock()
	c.enabled = true
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// RPCServerTypeFor — computed RpcServerType from interface
// ---------------------------------------------------------------------------

// RPCServerType returns the callback server type required for this client's
// interface. CUxD uses BIN-RPC; all other CCU interfaces use XML-RPC.
// Interfaces without a callback server return [hmenum.RPCServerTypeNone].
func (c *InterfaceClient) RPCServerType() hmenum.RPCServerType {
	return RPCServerTypeForInterface(c.cfg.Interface)
}

// RPCServerTypeForInterface maps an interface tag to its callback server
// type. Extracted as a standalone function so coordinators and backend
// detection code can call it without constructing a full client.
func RPCServerTypeForInterface(iface hmenum.Interface) hmenum.RPCServerType {
	switch iface {
	case hmenum.InterfaceCUxD:
		// CUxD speaks BIN-RPC natively — openccu-loom runs its own
		// BIN-RPC callback server. This is a deliberate divergence.
		// See SPECIFICATION §8.5 and ADR 0002.
		return hmenum.RPCServerTypeBINRPC
	case hmenum.InterfaceHmIPRF,
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices:
		return hmenum.RPCServerTypeXMLRPC
	default:
		return hmenum.RPCServerTypeNone
	}
}

// ---------------------------------------------------------------------------
// TransitionTo / CanTransitionTo delegates on InterfaceClient
// ---------------------------------------------------------------------------

// TransitionTo delegates to the embedded [ClientStateMachine.TransitionTo].
// Keeps both the inline state field and the state machine in sync so callers
// that use [State] / [WaitForState] see the updated value. Prefer this over
// [SetState] for coordinator-level transitions — it validates the transition
// table and records failure metadata.
func (c *InterfaceClient) TransitionTo(
	target hmenum.ClientState,
	reason string,
	force bool,
	failureReason hmenum.FailureReason,
) error {
	if err := c.sm.TransitionTo(target, reason, force, failureReason); err != nil {
		return err
	}
	// Propagate to the inline state so WaitForState / State still work.
	c.SetState(target)
	return nil
}

// CanTransitionTo reports whether the state machine allows moving from its
// current state to target. Delegates to [ClientStateMachine.CanTransitionTo].
func (c *InterfaceClient) CanTransitionTo(target hmenum.ClientState) bool {
	return c.sm.CanTransitionTo(target)
}

// ---------------------------------------------------------------------------
// Version / Model properties on InterfaceClient
// ---------------------------------------------------------------------------

// GetVersion returns the software version string associated with this client.
// For XML-RPC interfaces this is the value returned by the getVersion call
// during backend detection. For JSON-RPC-only interfaces (CCU-Jack) it is
// "0". Empty string means the version has not been determined yet.
func (c *InterfaceClient) GetVersion() string {
	return c.cfg.Version
}

// SetVersion updates the software version string. Coordinators call this
// after backend detection completes so that central-level aggregations
// (e.g. CentralUnit collecting firmware versions across all clients) see
// the correct value.
//
// Mirrors the assignment to InterfaceClient._version that happens in
// Py:113-125).
func (c *InterfaceClient) SetVersion(v string) {
	c.mu.Lock()
	c.cfg.Version = v
	c.mu.Unlock()
}

// Model returns the backend model string for this client. This mirrors
// Model delegated property which reads
// backend.model (client/interface_client.py:194).
//
// The returned string matches the backend Kind string:
// - KindCCU → "ccu"
// - KindHomegear → "homegear"
// - KindCUxD → "cuxd"
func (c *InterfaceClient) Model() string {
	return c.cfg.BackendKind.String()
}

// ---------------------------------------------------------------------------
// PingPong cache clear on SystemStatus connection-restored
// ---------------------------------------------------------------------------

// OnSystemStatusRestored is called by the coordinator (or the central's
// system-status handler) when the CCU connection for this interface has been
// restored. It clears the PingPong cache so stale mismatch counts do not
// pollute the newly established session.
func (c *InterfaceClient) OnSystemStatusRestored() {
	c.cfg.PingPong.Clear()
	c.cfg.Logger.Debug(
		"ping-pong cache cleared on connection restored",
		slog.String("central", c.cfg.CentralName),
		slog.String("interface", string(c.cfg.Interface)),
	)
}

// ---------------------------------------------------------------------------
// SetStateChangedBus — wire event-bus publishing for state changes
// ---------------------------------------------------------------------------

// SetStateChangedBus installs an event bus so that every state-machine
// transition emits a [hmevent.ClientStateChangedEvent]. Pass nil to remove
// the hook.
func (c *InterfaceClient) SetStateChangedBus(bus *events.Bus) {
	if bus == nil {
		c.sm.SetStateChangedPublisher(nil)
		return
	}
	centralName := c.cfg.CentralName
	iface := c.cfg.Interface
	interfaceID := string(iface)
	c.sm.SetStateChangedPublisher(func(from, to hmenum.ClientState, reason string, failureReason hmenum.FailureReason) {
		events.Publish(bus, hmevent.ClientStateChangedEvent{
			Base:        hmevent.Base{},
			CentralName: centralName,
			InterfaceID: interfaceID,
			Interface:   iface,
			From:        from,
			To:          to,
			Reason:      failureReason,
		})
		// when transitioning to CONNECTED, also stamp the callback timestamp so
		// IsCallbackAlive() reflects the reconnect.
		if to == hmenum.ClientStateConnected {
			c.NotifyCallback()
		}
	})
}

// ---------------------------------------------------------------------------
// ReconnectAttempts — counter owned by InterfaceClient
// ---------------------------------------------------------------------------

// ReconnectAttempts returns the number of failed reconnect attempts since
// the last successful reconnect (or since construction).
func (c *InterfaceClient) ReconnectAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconnectAttempts
}

// SetReconnectAttempts updates the reconnect-attempt counter. The
// coordinator increments this on each failed attempt and resets it to 0
// on success.
func (c *InterfaceClient) SetReconnectAttempts(n int) {
	c.mu.Lock()
	c.reconnectAttempts = n
	c.mu.Unlock()
}

// IncrementReconnectAttempts atomically increments the reconnect-attempt
// counter and returns the new value. Convenience wrapper for the common
// error path in the reconnect loop.
func (c *InterfaceClient) IncrementReconnectAttempts() int {
	c.mu.Lock()
	c.reconnectAttempts++
	n := c.reconnectAttempts
	c.mu.Unlock()
	return n
}
