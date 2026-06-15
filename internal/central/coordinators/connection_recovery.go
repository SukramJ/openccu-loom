// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/syncx"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// RecoveryStep is the unit of work a stage handler performs. It
// receives ctx and must honour cancellation; returning a non-nil error
// transitions the recovery pipeline to FAILED.
type RecoveryStep func(ctx context.Context) error

// Pipeline describes the sequence of stages a recovery attempt
// traverses. Stages run in slice order.
type Pipeline struct {
	Stage hmenum.RecoveryStage
	Run   RecoveryStep
	// Classify is consulted when Run returns an error. It maps the
	// underlying cause to a richer [hmenum.FailureReason] than the
	// default `Internal`. Optional — leaving it nil keeps the prior
	// behaviour.
	Classify FailureReasonStep
}

// DefaultMaxRecoveryAttempts caps the number of consecutive recovery attempts
// per (central, interface) pair before the coordinator gives up and emits
// [hmenum.FailureReasonExhausted]. Zero disables the cap.
//
// Matches MAX_RECOVERY_ATTEMPTS = 8 from the reference implementation
// (connection_recovery.py:102). The cap balances persistence (giving a
// recovering CCU enough chances to come back) against CCU-hammering risk —
// every attempt costs an init() round-trip, and a CCU stuck in a bad state
// can be made worse by relentless probes.
const DefaultMaxRecoveryAttempts = 8

// DefaultHeartbeatInterval is the period between heartbeat ticks that scan
// for exhausted interfaces and re-open one recovery attempt per tick.
// Matches HEARTBEAT_RETRY_INTERVAL = 60.0 in the reference implementation
// (connection_recovery.py:105).
const DefaultHeartbeatInterval = 60 * time.Second

// Backoff bounds for [ConnectionRecoveryCoordinator.NextRetryDelay].
// The exponential curve is base * 2^(attempt-1), capped at max.
//   - 2s initial — a freshly rebooted CCU often answers on the second
//     attempt, so we retry quickly while the wire is still warm.
//   - 120s cap — a CCU that has been hard-down for longer than two
//     minutes typically needs a full ReGa startup; we do not gain
//     anything by retrying sooner.
//
// Callers that need a custom curve can call [SetBackoff] to replace
// these defaults; the exported constants are here for documentation.
const (
	defaultBaseRetryDelay = 2 * time.Second
	defaultMaxRetryDelay  = 120 * time.Second
)

// historySize caps the per-interface ring buffer of recent recovery
// Outcomes. Equal to
// diagnostics endpoint without unbounded memory growth.
const historySize = 20

// FailureReasonStep is an optional contract a [RecoveryStep] may
// satisfy via [Pipeline.Classify] to surface a richer
// [hmenum.FailureReason] than the default `Internal`. Returning a
// non-nil pointer overrides the default; returning nil means "keep
// the default FailureReasonInternal". Returning an error from the
// step still drives the recovery to FAILED; the classifier just
// labels which reason fires the [hmevent.RecoveryFailedEvent].
//
// The closure is invoked **only** when [RecoveryStep] returns a
// non-nil error.
type FailureReasonStep func(err error) *hmenum.FailureReason

// HistoryEntry is one entry in a [ConnectionRecoveryCoordinator]'s
// per-interface history ring. The slice is newest-last so callers
// rendering a timeline can iterate forward without reversing.
type HistoryEntry struct {
	StartedAt time.Time
	Duration  time.Duration
	Result    hmenum.RecoveryResult
	// Reason is set only when Result is RecoveryResultFailed.
	Reason hmenum.FailureReason
}

// InterfaceRecoveryState is the diagnostics snapshot of one interface's
// Recovery counters. It mirrors
// `InterfaceRecoveryState`. Snapshots are immutable copies — callers
// own the result and may keep it indefinitely.
type InterfaceRecoveryState struct {
	InterfaceID         string
	Attempts            int
	ConsecutiveFailures int
	LastAttempt         time.Time
	LastSuccess         time.Time
	NextRetryAfter      time.Time
	// CurrentStage is the recovery stage the interface is currently in. Idle
	// when no recovery is active.
	CurrentStage hmenum.RecoveryStage
}

// StateTransitioner is an optional dependency that lets the
// ConnectionRecoveryCoordinator drive CentralStateMachine transitions. Wire
// it via [ConnectionRecoveryCoordinator.WithStateMachine]. A nil value is
// valid — all transition calls become no-ops so tests and legacy-wiring need
// no state machine.
type StateTransitioner interface {
	TransitionTo(state hmenum.CentralState) error
}

// CircuitBreakerResetter resets one or more circuit breakers on successful
// reconnect. Wire it via
// [ConnectionRecoveryCoordinator.WithCircuitBreakerResetter].
type CircuitBreakerResetter interface {
	// ResetForInterface resets all circuit breakers associated with the
	// given interfaceID. A nil implementation is valid — all calls
	// become no-ops.
	ResetForInterface(interfaceID string)
}

// ConnectionRecoveryCoordinator orchestrates one recovery attempt per
// (central, interface) pair. Concurrent Run invocations for the same
// interface are serialised.
//
// Per-interface global attempt counters cap how often a single
// interface may fail recovery in a row before the coordinator gives
// up. The counter resets on a successful Run, so flapping interfaces
// don't accumulate against a healthy steady state.
type ConnectionRecoveryCoordinator struct {
	centralName       string
	bus               *events.Bus
	maxAttempts       int
	recorder          observability.Recorder
	baseDelay         time.Duration
	maxDelay          time.Duration
	heartbeatInterval time.Duration

	mu       syncx.Mutex
	active   map[string]chan struct{}
	attempts map[string]int
	state    map[string]*InterfaceRecoveryState
	history  map[string][]HistoryEntry

	// --- optional wiring (all nil-safe) ---------------------------------
	// defaultPipeline is used by Subscribe-triggered recoveries when no
	// per-interface pipeline has been registered. Acts as the catch-all
	// fallback for interfaces that are not covered by interfacePipelines.
	defaultPipeline []Pipeline
	// interfacePipelines maps an interface_id to its dedicated recovery
	// pipeline. Each entry typically closes over the matching
	// InterfaceClient + backend so the reconnect step targets the right
	// wire. A nil/empty slice for a key falls through to defaultPipeline.
	// Guarded by mu (alongside defaultPipeline). Wired via
	// WithPipelineFor; nil-safe — multi-interface centrals require this
	// to be populated, single-interface setups may rely on
	// defaultPipeline alone.
	interfacePipelines map[string][]Pipeline
	// sm drives CentralStateMachine transitions on Recovering/Running/Failed.
	sm StateTransitioner
	// cbResetter resets circuit breakers after a successful reconnect.
	cbResetter CircuitBreakerResetter

	// subMu guards unsubscribers and stopped.
	subMu         syncx.Mutex
	unsubscribers []func()
	stopped       bool
	// stopCh is closed by Stop to immediately unblock heartbeatLoop,
	// rather than letting it linger until the next ticker tick. Guarded
	// by subMu alongside stopped.
	stopCh chan struct{}

	// incidentLog is the per-coordinator incident ring buffer.
	// Guarded by mu.
	incidentLog []IncidentEntry

	// hubRefresher is the optional HubCoordinator reference used by
	// RefreshHubDataAfterRecovery. Guarded by mu.
	hubRefresher HubRefresher

	// jsonRPCSessionClearer is the optional hook that drops a stale
	// JSON-RPC session before the first recovery stage. Guarded by mu.
	jsonRPCSessionClearer JSONRPCSessionClearer

	// logger emits diagnostic log lines for the recovery pipeline:
	// which event triggered a recovery, when stages start / fail /
	// complete, and whether a fresh init() actually went out to the
	// CCU. Nil-safe — if no logger is wired the pipeline runs silently.
	logger *slog.Logger
}

// SetLogger wires a structured logger so Subscribe-triggered recoveries
// and pipeline transitions show up in the daemon log. Without one the
// pipeline runs silently — only the event bus carries
// `RecoveryStarted/Completed/Failed`, which is fine for tests but blind
// in production when one wants to know whether a CCU reboot triggered a
// fresh init() round-trip. Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) SetLogger(l *slog.Logger) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.logger = l
	c.mu.Unlock()
	return c
}

// log is a nil-safe slog facade used by the recovery handlers.
func (c *ConnectionRecoveryCoordinator) log() *slog.Logger {
	c.mu.Lock()
	l := c.logger
	c.mu.Unlock()
	if l == nil {
		return slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return l
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// SetRecorder rewires the observability recorder. Returns the receiver
// for chaining.
func (c *ConnectionRecoveryCoordinator) SetRecorder(rec observability.Recorder) *ConnectionRecoveryCoordinator {
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	c.mu.Lock()
	c.recorder = rec
	c.mu.Unlock()
	return c
}

// NewConnectionRecoveryCoordinator wires the coordinator with the
// default per-interface attempt cap.
func NewConnectionRecoveryCoordinator(centralName string, bus *events.Bus) *ConnectionRecoveryCoordinator {
	return NewConnectionRecoveryCoordinatorWithLimit(centralName, bus, DefaultMaxRecoveryAttempts)
}

// NewConnectionRecoveryCoordinatorWithLimit wires the coordinator
// with a custom per-interface attempt cap. Pass 0 to disable the
// cap (matches the historical behaviour).
//
// loom:reachable:reason="called by NewConnectionRecoveryCoordinator (the standard production entry point); also used directly in integration tests that need a bounded attempt count"
func NewConnectionRecoveryCoordinatorWithLimit(centralName string, bus *events.Bus, maxAttempts int) *ConnectionRecoveryCoordinator {
	return &ConnectionRecoveryCoordinator{
		centralName:       centralName,
		bus:               bus,
		maxAttempts:       maxAttempts,
		recorder:          observability.NoopRecorder{},
		baseDelay:         defaultBaseRetryDelay,
		maxDelay:          defaultMaxRetryDelay,
		heartbeatInterval: DefaultHeartbeatInterval,
		active:            make(map[string]chan struct{}),
		attempts:          make(map[string]int),
		state:             make(map[string]*InterfaceRecoveryState),
		history:           make(map[string][]HistoryEntry),
		stopCh:            make(chan struct{}),
	}
}

// WithHeartbeatInterval overrides the default 60-second interval between
// heartbeat ticks that scan for exhausted interfaces and re-open one
// recovery attempt per tick. Values ≤ 0 are ignored.
// Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) WithHeartbeatInterval(d time.Duration) *ConnectionRecoveryCoordinator {
	if d > 0 {
		c.mu.Lock()
		c.heartbeatInterval = d
		c.mu.Unlock()
	}
	return c
}

// SetBackoff replaces the exponential-backoff bounds used by
// [NextRetryDelay]. Both values must be positive; either zero leaves
// the matching default in place.
func (c *ConnectionRecoveryCoordinator) SetBackoff(base, maximum time.Duration) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	if base > 0 {
		c.baseDelay = base
	}
	if maximum > 0 {
		c.maxDelay = maximum
	}
	c.mu.Unlock()
	return c
}

// WithStateMachine injects an optional [StateTransitioner] that the
// coordinator calls at the start (Recovering), on success (Running), and on
// exhaustion (Failed) of every Run. Passing nil is valid and disables
// transitions. Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) WithStateMachine(sm StateTransitioner) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.sm = sm
	c.mu.Unlock()
	return c
}

// WithCircuitBreakerResetter injects an optional [CircuitBreakerResetter]
// called on successful recovery. Nil disables CB resets. Returns the receiver.
func (c *ConnectionRecoveryCoordinator) WithCircuitBreakerResetter(r CircuitBreakerResetter) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.cbResetter = r
	c.mu.Unlock()
	return c
}

// WithDefaultPipeline sets the catch-all pipeline used by event-driven
// recoveries when no per-interface pipeline has been registered via
// [WithPipelineFor]. Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) WithDefaultPipeline(p []Pipeline) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.defaultPipeline = p
	c.mu.Unlock()
	return c
}

// WithPipelineFor registers a per-interface recovery pipeline. The
// pipeline runs when a recovery is triggered for interfaceID. Pass an
// empty slice to clear a previously registered pipeline (the trigger
// then falls through to the defaultPipeline). Returns the receiver for
// chaining.
//
// Multi-interface centrals must call this once per interface so each
// reconnect step closes over the right InterfaceClient / backend pair.
// Without it, repeated WithDefaultPipeline calls during wiring would
// leave only the last registration usable.
func (c *ConnectionRecoveryCoordinator) WithPipelineFor(interfaceID string, p []Pipeline) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	if c.interfacePipelines == nil {
		c.interfacePipelines = make(map[string][]Pipeline)
	}
	if len(p) == 0 {
		delete(c.interfacePipelines, interfaceID)
	} else {
		c.interfacePipelines[interfaceID] = p
	}
	c.mu.Unlock()
	return c
}

// InRecovery reports whether the given interface is currently undergoing
// active recovery.
// interfaceIDs returns the union of all interface IDs that are either
// registered in interfacePipelines or currently active. Used to fan
// out a central-level state transition across all known interfaces.
func (c *ConnectionRecoveryCoordinator) interfaceIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{})
	for id := range c.interfacePipelines {
		seen[id] = struct{}{}
	}
	for id := range c.active {
		seen[id] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// InRecoveryFor reports whether a recovery is currently active for the
// given interface. Use [InRecovery] to check whether any recovery is active.
func (c *ConnectionRecoveryCoordinator) InRecoveryFor(interfaceID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.active[interfaceID]
	return ok
}

// InRecovery returns true when at least one interface is currently in an
// active recovery pipeline. Equivalent to Python's global `in_recovery`
// property which checks `bool(self._active_recoveries)`.
func (c *ConnectionRecoveryCoordinator) InRecovery() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active) > 0
}

// triggerRecovery fires an async recovery for interfaceID using the
// coordinator's defaultPipeline. It is a no-op if:
// - the coordinator is stopped
// - no defaultPipeline has been configured
// - another recovery for the same interfaceID is already active
func (c *ConnectionRecoveryCoordinator) triggerRecovery(interfaceID string) {
	c.subMu.Lock()
	stopped := c.stopped
	c.subMu.Unlock()
	if stopped {
		c.log().Debug("recovery.skip",
			slog.String("central", c.centralName),
			slog.String("interface", interfaceID),
			slog.String("reason", "coordinator_stopped"))
		return
	}

	c.mu.Lock()
	pipeline := c.interfacePipelines[interfaceID]
	if len(pipeline) == 0 {
		pipeline = c.defaultPipeline
	}
	_, alreadyActive := c.active[interfaceID]
	c.mu.Unlock()

	if len(pipeline) == 0 {
		c.log().Debug("recovery.skip",
			slog.String("central", c.centralName),
			slog.String("interface", interfaceID),
			slog.String("reason", "no_pipeline_registered"))
		return
	}
	if alreadyActive {
		c.log().Debug("recovery.skip",
			slog.String("central", c.centralName),
			slog.String("interface", interfaceID),
			slog.String("reason", "already_active"))
		return
	}

	c.log().Info("recovery.trigger",
		slog.String("central", c.centralName),
		slog.String("interface", interfaceID))
	go c.Run(context.Background(), interfaceID, pipeline)
}

// Subscribe registers the coordinator as a listener for connection-loss and
// circuit-breaker events that should trigger automatic recovery. It
// subscribes to:
// - [hmevent.ConnectionLostEvent]
// - [hmevent.CircuitBreakerTrippedEvent]
// - [hmevent.CircuitBreakerStateChangedEvent] (only To==Open transitions)
// - [hmevent.HeartbeatTimerFiredEvent]
//
// Only events whose CentralName matches the coordinator's own name are acted
// upon. Call [Stop] to release the subscriptions. Calling Subscribe twice
// registers duplicate handlers — call it once per coordinator lifetime.
func (c *ConnectionRecoveryCoordinator) Subscribe() {
	unsub1 := events.Subscribe(c.bus, func(e hmevent.ConnectionLostEvent) {
		if e.CentralName != c.centralName {
			return
		}
		c.triggerRecovery(e.InterfaceID)
	})

	unsub2 := events.Subscribe(c.bus, func(e hmevent.CircuitBreakerTrippedEvent) {
		if e.CentralName != c.centralName {
			return
		}
		c.triggerRecovery(e.InterfaceID)
	})

	unsub3 := events.Subscribe(c.bus, func(e hmevent.CircuitBreakerStateChangedEvent) {
		if e.CentralName != c.centralName {
			return
		}
		switch e.To {
		case hmenum.CircuitStateOpen:
			c.triggerRecovery(e.InterfaceID)
		case hmenum.CircuitStateClosed:
			// Half-open → Closed transition signals the breaker
			// reset itself after a successful probe call (without
			// the recovery coordinator's involvement). The CCU
			// forgets the XML-RPC callback registration during the
			// outage; without a fresh init() the bridge never
			// receives state-echo events again, the optimistic-
			// update tracker times out after 30 s, and live-Subscribe
			// consumers (notably the Matter bridge) report the wrong
			// switch state. Re-run the recovery pipeline once even
			// though the breaker itself is already closed so the
			// default-pipeline's reconnect step re-issues init().
			if e.From == hmenum.CircuitStateHalfOpen {
				c.triggerRecovery(e.InterfaceID)
			}
		case hmenum.CircuitStateHalfOpen:
			// The transition into half_open is purely a probe-state
			// signal; the breaker is exploring whether the backend
			// is reachable again. No recovery action — the matching
			// Open→Closed (after probe success) or Closed→Open
			// (after probe failure) follow-up event drives the
			// pipeline.
		}
	})

	unsub4 := events.Subscribe(c.bus, func(e hmevent.HeartbeatTimerFiredEvent) {
		if e.CentralName != c.centralName {
			return
		}
		for _, id := range e.InterfaceIDs {
			// Heartbeat is the comeback lane out of FAILED. Without
			// allowing one extra attempt, an interface that exhausted
			// maxAttempts stays in FAILED forever — every heartbeat
			// fires triggerRecovery, the exhausted-guard rejects, and
			// no progress is ever made. Mirrors the reference
			// `attempt_count = MAX-1` line, which grants exactly one
			// attempt per heartbeat tick: the upcoming triggerRecovery
			// bumps to MAX, and if it fails again we go back to the
			// exhausted branch on the next call.
			c.allowOneRecoveryAttempt(id)
			c.triggerRecovery(id)
		}
	})

	// A CentralStateChangedEvent fired externally (e.g. via REST, health
	// tracker, or a manual operator override) transitions the central into
	// FAILED. Without this subscriber, external state transitions would
	// never trigger the recovery pipeline.
	unsub5 := events.Subscribe(c.bus, func(e hmevent.CentralStateChangedEvent) {
		if e.CentralName != c.centralName {
			return
		}
		if e.To == hmenum.CentralStateFailed {
			for _, id := range c.interfaceIDs() {
				c.triggerRecovery(id)
			}
		}
	})

	c.subMu.Lock()
	c.unsubscribers = append(c.unsubscribers, unsub1, unsub2, unsub3, unsub4, unsub5)
	c.subMu.Unlock()

	go c.heartbeatLoop()
}

// Stop releases all subscriptions registered by [Subscribe]. After Stop
// returns, no further event-driven recoveries will be triggered. Idempotent.
func (c *ConnectionRecoveryCoordinator) Stop() {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	if c.stopCh != nil {
		close(c.stopCh)
	}
	for _, unsub := range c.unsubscribers {
		unsub()
	}
	c.unsubscribers = nil
}

// heartbeatLoop runs in the background after Subscribe and periodically
// scans for interfaces whose attempt counter has hit the cap (exhausted).
// For each such interface it emits a HeartbeatTimerFiredEvent so the
// existing HeartbeatTimerFiredEvent subscriber in Subscribe can grant one
// fresh attempt and re-enter the recovery pipeline.
//
// The loop exits when the coordinator is stopped via Stop.
func (c *ConnectionRecoveryCoordinator) heartbeatLoop() {
	c.mu.Lock()
	interval := c.heartbeatInterval
	c.mu.Unlock()

	c.subMu.Lock()
	stopCh := c.stopCh
	c.subMu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.fireHeartbeatIfExhausted()
		}
	}
}

// fireHeartbeatIfExhausted collects all interfaces that have exhausted
// their retry budget (attempts >= maxAttempts) and emits a single
// HeartbeatTimerFiredEvent for them. A no-op when maxAttempts is 0
// (cap disabled) or when no interface is exhausted.
func (c *ConnectionRecoveryCoordinator) fireHeartbeatIfExhausted() {
	if c.maxAttempts == 0 {
		return
	}

	c.mu.Lock()
	var exhausted []string
	for id := range c.state {
		if c.attempts[id] >= c.maxAttempts {
			exhausted = append(exhausted, id)
		}
	}
	c.mu.Unlock()

	if len(exhausted) == 0 {
		return
	}

	events.Publish(c.bus, hmevent.HeartbeatTimerFiredEvent{
		Base:         hmevent.NewBase(),
		CentralName:  c.centralName,
		InterfaceIDs: exhausted,
	})
}

// transitionTo calls the optional state machine with the given state.
// Errors from the transition (e.g. "already in that state") are silently
// ignored — the recovery pipeline continues regardless.
func (c *ConnectionRecoveryCoordinator) transitionTo(state hmenum.CentralState) {
	c.mu.Lock()
	sm := c.sm
	c.mu.Unlock()
	if sm == nil {
		return
	}
	_ = sm.TransitionTo(state)
}

// resetCircuitBreakers calls the optional CB resetter for interfaceID.
func (c *ConnectionRecoveryCoordinator) resetCircuitBreakers(interfaceID string) {
	c.mu.Lock()
	r := c.cbResetter
	c.mu.Unlock()
	if r == nil {
		return
	}
	r.ResetForInterface(interfaceID)
}

// State returns a snapshot of the recovery counters for interfaceID.
// Returns the zero value when the interface has no recorded history.
func (c *ConnectionRecoveryCoordinator) State(interfaceID string) InterfaceRecoveryState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.state[interfaceID]; ok {
		snapshot := *s
		snapshot.NextRetryAfter = c.nextRetryAfterLocked(interfaceID)
		return snapshot
	}
	return InterfaceRecoveryState{InterfaceID: interfaceID}
}

// History returns the per-interface ring of recent recovery outcomes,
// newest-last. Empty when the interface has no recorded runs.
func (c *ConnectionRecoveryCoordinator) History(interfaceID string) []HistoryEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.history[interfaceID]
	out := make([]HistoryEntry, len(src))
	copy(out, src)
	return out
}

// NextRetryDelay reports the exponential-backoff delay before the
// caller should retry recovery for interfaceID. Equals baseDelay for
// the first retry and saturates at maxDelay.
func (c *ConnectionRecoveryCoordinator) NextRetryDelay(interfaceID string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextRetryDelayLocked(interfaceID)
}

func (c *ConnectionRecoveryCoordinator) nextRetryDelayLocked(interfaceID string) time.Duration {
	s := c.state[interfaceID]
	if s == nil || s.ConsecutiveFailures == 0 {
		return c.baseDelay
	}
	delay := c.baseDelay
	for i := 1; i < s.ConsecutiveFailures; i++ {
		delay *= 2
		if delay >= c.maxDelay {
			return c.maxDelay
		}
	}
	if delay > c.maxDelay {
		return c.maxDelay
	}
	return delay
}

func (c *ConnectionRecoveryCoordinator) nextRetryAfterLocked(interfaceID string) time.Time {
	s := c.state[interfaceID]
	if s == nil || s.LastAttempt.IsZero() {
		return time.Time{}
	}
	return s.LastAttempt.Add(c.nextRetryDelayLocked(interfaceID))
}

func (c *ConnectionRecoveryCoordinator) ensureStateLocked(interfaceID string) *InterfaceRecoveryState {
	s, ok := c.state[interfaceID]
	if !ok {
		s = &InterfaceRecoveryState{InterfaceID: interfaceID}
		c.state[interfaceID] = s
	}
	return s
}

func (c *ConnectionRecoveryCoordinator) appendHistoryLocked(interfaceID string, entry HistoryEntry) {
	hist := c.history[interfaceID]
	hist = append(hist, entry)
	if len(hist) > historySize {
		hist = hist[len(hist)-historySize:]
	}
	c.history[interfaceID] = hist
}

// AttemptCount reports the number of consecutive failed recovery
// attempts for interfaceID. Useful for diagnostics endpoints.
func (c *ConnectionRecoveryCoordinator) AttemptCount(interfaceID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts[interfaceID]
}

// --- Metrics provider surface (mirrors
// `RecoveryProviderForMetricsProtocol`) ------------------------------

// MetricsInRecovery reports whether at least one interface is
// currently undergoing recovery. Multi-CCU safe — the coordinator
// only reflects its own interfaces, scoped by the owning
// [Unit.Name].
func (c *ConnectionRecoveryCoordinator) MetricsInRecovery() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active) > 0
}

// RecoveryStateView is an immutable per-interface view exposed via
// [MetricsRecoveryStates]. The struct's accessor methods satisfy the
// metrics RecoveryStateMetrics interface; we keep them as struct
// methods (instead of a provider-level interface) so the snapshot
// stays decoupled from the live coordinator state.
type RecoveryStateView struct {
	attempts            int
	consecutiveFailures int
	canRetry            bool
}

// AttemptCount returns the cumulative attempt count for this interface.
func (s RecoveryStateView) AttemptCount() int { return s.attempts }

// ConsecutiveFailures returns the current consecutive-failure streak.
func (s RecoveryStateView) ConsecutiveFailures() int { return s.consecutiveFailures }

// CanRetry reports whether further retries are permitted.
func (s RecoveryStateView) CanRetry() bool { return s.canRetry }

// SetActiveForTest injects an interface ID into the active recovery set.
// This method exists for unit tests that need to simulate an in-progress
// recovery without running a real recovery pipeline. It must not be called
// from production code.
func (c *ConnectionRecoveryCoordinator) SetActiveForTest(interfaceID string) {
	done := make(chan struct{})
	c.mu.Lock()
	c.active[interfaceID] = done
	c.mu.Unlock()
}

// MetricsRecoveryStates returns a per-interface snapshot of the recovery
// counters keyed by interfaceID.
func (c *ConnectionRecoveryCoordinator) MetricsRecoveryStates() map[string]RecoveryStateView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]RecoveryStateView, len(c.state))
	for id, s := range c.state {
		canRetry := true
		if c.maxAttempts > 0 && c.attempts[id] >= c.maxAttempts {
			canRetry = false
		}
		out[id] = RecoveryStateView{
			attempts:            s.Attempts,
			consecutiveFailures: s.ConsecutiveFailures,
			canRetry:            canRetry,
		}
	}
	return out
}

// Run walks the pipeline for interfaceID. Emits RecoveryStartedEvent,
// RecoveryStageChangedEvent per stage, and RecoveryCompletedEvent or
// RecoveryFailedEvent at the end. Returns the final result.
//
// Instrumented via observability.Recorder: every Run records its wall-
// clock latency tagged with interfaceID; success / failure / exhausted
// outcomes increment the matching counter so dashboards can spot a
// flapping interface without subscribing to the event bus.
func (c *ConnectionRecoveryCoordinator) Run(ctx context.Context, interfaceID string, pipeline []Pipeline) hmenum.RecoveryResult {
	c.mu.Lock()
	rec := c.recorder
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	c.mu.Unlock()
	t0 := time.Now()
	result := c.runInternal(ctx, interfaceID, pipeline)
	rec.ObserveLatency("connection_recovery.run", observability.ScopeCoordinator, time.Since(t0), nil)
	switch result { //nolint:exhaustive // Partial and MaxRetries are not separately counted; they fall through to the caller
	case hmenum.RecoveryResultSuccess:
		rec.IncCounter("connection_recovery.run.success", observability.ScopeCoordinator, 1)
	case hmenum.RecoveryResultFailed:
		rec.IncCounter("connection_recovery.run.failed", observability.ScopeCoordinator, 1)
	case hmenum.RecoveryResultCancelled:
		rec.IncCounter("connection_recovery.run.cancelled", observability.ScopeCoordinator, 1)
	}
	return result
}

func (c *ConnectionRecoveryCoordinator) runInternal(ctx context.Context, interfaceID string, pipeline []Pipeline) hmenum.RecoveryResult { //nolint:funlen // single-purpose connection recovery state machine with many pipeline branches
	c.mu.Lock()
	// Serialise per interface.
	if existing, ok := c.active[interfaceID]; ok {
		c.mu.Unlock()
		<-existing
		c.mu.Lock()
	}
	done := make(chan struct{})
	c.active[interfaceID] = done
	c.mu.Unlock()

	// closeOnce guards the done-channel close so a future contributor
	// cannot accidentally introduce a close-of-closed panic by adding
	// an extra cleanup path. Today, the defer is the only closer; the
	// sync.Once makes that invariant defensive — see audit R2.
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	defer func() {
		c.mu.Lock()
		delete(c.active, interfaceID)
		c.mu.Unlock()
		closeDone()
	}()

	// Global cap: once attempts hit maxAttempts, refuse the run with
	// FailureReasonExhausted instead of hammering the CCU. The
	// counter is reset by a successful Run; explicit
	// [ResetAttempts] also clears it (operator escape hatch).
	if c.maxAttempts > 0 {
		c.mu.Lock()
		exhausted := c.attempts[interfaceID] >= c.maxAttempts
		c.mu.Unlock()
		if exhausted {
			c.recordOutcome(interfaceID, time.Now(), 0, hmenum.RecoveryResultFailed, hmenum.FailureReasonExhausted)
			c.transitionTo(hmenum.CentralStateFailed)
			events.Publish(c.bus, hmevent.RecoveryFailedEvent{
				Base:                       hmevent.NewBase(),
				CentralName:                c.centralName,
				InterfaceID:                interfaceID,
				Reason:                     hmenum.FailureReasonExhausted,
				LastStageReached:           hmenum.RecoveryStageFailed,
				RequiresManualIntervention: true,
			})
			return hmenum.RecoveryResultFailed
		}
	}

	c.transitionTo(hmenum.CentralStateRecovering)

	// Snapshot attempt number and cap before the pipeline runs so the
	// RecoveryAttemptedEvent carries consistent values regardless of
	// whether the run succeeds or fails.
	c.mu.Lock()
	attemptBefore := c.attempts[interfaceID] + 1
	maxAttempts := c.maxAttempts
	c.mu.Unlock()

	events.Publish(c.bus, hmevent.RecoveryStartedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.centralName,
		InterfaceID: interfaceID,
	})
	c.log().Info("recovery.started",
		slog.String("central", c.centralName),
		slog.String("interface", interfaceID),
		slog.Int("stages", len(pipeline)))

	start := time.Now()
	stageEnteredAt := start
	from := hmenum.RecoveryStageIdle
	for _, step := range pipeline {
		durationMs := time.Since(stageEnteredAt).Milliseconds()
		c.setCurrentStageLocked(interfaceID, step.Stage)
		events.Publish(c.bus, hmevent.RecoveryStageChangedEvent{
			Base:                 hmevent.NewBase(),
			CentralName:          c.centralName,
			InterfaceID:          interfaceID,
			From:                 from,
			To:                   step.Stage,
			DurationInOldStageMs: durationMs,
		})
		from = step.Stage
		stageEnteredAt = time.Now()
		if err := step.Run(ctx); err != nil {
			reason := hmenum.FailureReasonInternal
			if step.Classify != nil {
				if r := step.Classify(err); r != nil {
					reason = *r
				}
			}
			c.setCurrentStageLocked(interfaceID, hmenum.RecoveryStageFailed)
			c.bumpAttempt(interfaceID)
			c.recordOutcome(interfaceID, start, time.Since(start), hmenum.RecoveryResultFailed, reason)
			c.transitionTo(hmenum.CentralStateFailed)
			events.Publish(c.bus, hmevent.RecoveryAttemptedEvent{
				Base:          hmevent.NewBase(),
				CentralName:   c.centralName,
				InterfaceID:   interfaceID,
				AttemptNumber: attemptBefore,
				MaxAttempts:   maxAttempts,
				StageReached:  step.Stage,
				Success:       false,
				ErrorMessage:  err.Error(),
			})
			events.Publish(c.bus, hmevent.RecoveryFailedEvent{
				Base:                       hmevent.NewBase(),
				CentralName:                c.centralName,
				InterfaceID:                interfaceID,
				Reason:                     reason,
				TotalDurationMs:            time.Since(start).Milliseconds(),
				LastStageReached:           step.Stage,
				RequiresManualIntervention: reason == hmenum.FailureReasonExhausted,
			})
			c.log().Warn("recovery.failed",
				slog.String("central", c.centralName),
				slog.String("interface", interfaceID),
				slog.String("stage", string(step.Stage)),
				slog.String("reason", string(reason)),
				slog.String("err", err.Error()),
				slog.Int64("total_ms", time.Since(start).Milliseconds()))
			return hmenum.RecoveryResultFailed
		}
		if ctx.Err() != nil {
			c.bumpAttempt(interfaceID)
			c.recordOutcome(interfaceID, start, time.Since(start), hmenum.RecoveryResultCancelled, hmenum.FailureReasonTimeout)
			events.Publish(c.bus, hmevent.RecoveryFailedEvent{
				Base:             hmevent.NewBase(),
				CentralName:      c.centralName,
				InterfaceID:      interfaceID,
				Reason:           hmenum.FailureReasonTimeout,
				TotalDurationMs:  time.Since(start).Milliseconds(),
				LastStageReached: step.Stage,
			})
			return hmenum.RecoveryResultCancelled
		}
	}

	c.setCurrentStageLocked(interfaceID, hmenum.RecoveryStageIdle)
	c.resetCircuitBreakers(interfaceID)
	c.recordSuccess(interfaceID, start, time.Since(start))
	c.transitionTo(hmenum.CentralStateRunning)
	events.Publish(c.bus, hmevent.RecoveryAttemptedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   c.centralName,
		InterfaceID:   interfaceID,
		AttemptNumber: attemptBefore,
		MaxAttempts:   maxAttempts,
		StageReached:  hmenum.RecoveryStageRecovered,
		Success:       true,
	})
	events.Publish(c.bus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.centralName,
		InterfaceID: interfaceID,
		Result:      hmenum.RecoveryResultSuccess,
		Duration:    int(time.Since(start) / time.Millisecond),
	})
	c.log().Info("recovery.completed",
		slog.String("central", c.centralName),
		slog.String("interface", interfaceID),
		slog.Int64("total_ms", time.Since(start).Milliseconds()))
	return hmenum.RecoveryResultSuccess
}

// recordOutcome captures a failed / cancelled / exhausted run. Updates
// the per-interface state (attempt counters, timestamps) and appends
// to the history ring.
func (c *ConnectionRecoveryCoordinator) recordOutcome(
	interfaceID string,
	startedAt time.Time,
	dur time.Duration,
	result hmenum.RecoveryResult,
	reason hmenum.FailureReason,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.ensureStateLocked(interfaceID)
	s.Attempts++
	s.ConsecutiveFailures++
	s.LastAttempt = time.Now()
	c.appendHistoryLocked(interfaceID, HistoryEntry{
		StartedAt: startedAt,
		Duration:  dur,
		Result:    result,
		Reason:    reason,
	})
}

// recordSuccess captures a successful run. Resets the attempt counter
// and the consecutive-failure counter; the success timestamp drives
// dashboards and SLO calculations.
func (c *ConnectionRecoveryCoordinator) recordSuccess(interfaceID string, startedAt time.Time, dur time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.attempts, interfaceID)
	s := c.ensureStateLocked(interfaceID)
	s.Attempts++
	s.ConsecutiveFailures = 0
	s.LastAttempt = time.Now()
	s.LastSuccess = s.LastAttempt
	c.appendHistoryLocked(interfaceID, HistoryEntry{
		StartedAt: startedAt,
		Duration:  dur,
		Result:    hmenum.RecoveryResultSuccess,
	})
}

// ResetAttempts clears the consecutive-failure counter for
// interfaceID. Operators trigger this from the diagnostics endpoint
// when they want to allow recovery to retry after manual
// intervention.
func (c *ConnectionRecoveryCoordinator) ResetAttempts(interfaceID string) {
	c.resetAttempts(interfaceID)
	c.mu.Lock()
	if s, ok := c.state[interfaceID]; ok {
		s.ConsecutiveFailures = 0
	}
	c.mu.Unlock()
}

func (c *ConnectionRecoveryCoordinator) bumpAttempt(interfaceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts[interfaceID]++
}

func (c *ConnectionRecoveryCoordinator) resetAttempts(interfaceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.attempts, interfaceID)
}

// allowOneRecoveryAttempt floors the attempt counter at maxAttempts-1 so the
// next triggerRecovery is guaranteed one run before the exhausted-guard
// fires. If the counter is already below the cap (recovery is mid-progress),
// it stays untouched — heartbeat does not erase progress, only revives a
// fully-exhausted lane.
//
// Mirrors `recovery_state.attempt_count = MAX_RECOVERY_ATTEMPTS - 1` in the
// reference _heartbeat_loop. A full resetAttempts would be too generous
// (it grants maxAttempts fresh tries per heartbeat tick, which hammers the
// CCU); the cap-minus-one floor grants exactly one.
func (c *ConnectionRecoveryCoordinator) allowOneRecoveryAttempt(interfaceID string) {
	if c.maxAttempts == 0 {
		return // cap disabled — nothing to revive.
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempts[interfaceID] > c.maxAttempts-1 {
		c.attempts[interfaceID] = c.maxAttempts - 1
	}
}

// setCurrentStageLocked updates the live CurrentStage for interfaceID and
// mirrors it into the InterfaceRecoveryState snapshot. Must be called
// with mu NOT held (acquires mu internally).
func (c *ConnectionRecoveryCoordinator) setCurrentStageLocked(interfaceID string, stage hmenum.RecoveryStage) {
	c.mu.Lock()
	s := c.ensureStateLocked(interfaceID)
	s.CurrentStage = stage
	c.mu.Unlock()
}

// CurrentStage returns the recovery stage the given interface is currently
// in. Returns [hmenum.RecoveryStageIdle] when no recovery is active.
func (c *ConnectionRecoveryCoordinator) CurrentStage(interfaceID string) hmenum.RecoveryStage {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.state[interfaceID]
	if !ok {
		return hmenum.RecoveryStageIdle
	}
	return s.CurrentStage
}

// --- IncidentLog ring buffer -------------------------------------

// incidentRingSize caps the per-coordinator incident ring buffer.
const incidentRingSize = 100

// IncidentEntry is one entry in the coordinator's incident log. REST
// diagnostics endpoints expose the log via [History] [AllIncidents].
type IncidentEntry struct {
	Timestamp   time.Time
	InterfaceID string
	Stage       hmenum.RecoveryStage
	Reason      hmenum.FailureReason
	Message     string
}

// AppendIncident appends one entry to the coordinator's incident ring
// (maximum [incidentRingSize] entries). Older entries are evicted FIFO.
func (c *ConnectionRecoveryCoordinator) AppendIncident(entry IncidentEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.incidentLog = append(c.incidentLog, entry)
	if len(c.incidentLog) > incidentRingSize {
		c.incidentLog = c.incidentLog[len(c.incidentLog)-incidentRingSize:]
	}
}

// AllIncidents returns a copy of the incident ring, oldest-first.
// Mirrors the REST diagnostics endpoint contract.
func (c *ConnectionRecoveryCoordinator) AllIncidents() []IncidentEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]IncidentEntry, len(c.incidentLog))
	copy(out, c.incidentLog)
	return out
}

// --- RefreshHubDataAfterRecovery --------------------------------

// HubRefresher is the optional dependency used by
// [RefreshHubDataAfterRecovery] to reload hub data after a successful
// reconnect. Wire via [ConnectionRecoveryCoordinator.SetHubRefresher].
type HubRefresher interface {
	RefreshSystemUpdate(ctx context.Context) error
	RefreshSysvars(ctx context.Context) error
	RefreshPrograms(ctx context.Context) error
}

// SetHubRefresher injects an optional [HubRefresher] called by
// [RefreshHubDataAfterRecovery]. Nil disables the post-recovery hub
// refresh. Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) SetHubRefresher(r HubRefresher) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.hubRefresher = r
	c.mu.Unlock()
	return c
}

// JSONRPCSessionClearer is the contract for invalidating a cached JSON-RPC
// session before starting a recovery pipeline. Wired via
// [ConnectionRecoveryCoordinator.WithJSONRPCSessionClearer].
type JSONRPCSessionClearer interface {
	// ClearJSONRPCSession drops the cached session ID so the next
	// JSON-RPC request re-authenticates against the CCU hub. This must
	// be called before the first reconnect stage so a stale session does
	// not cause an immediate 401 in the init() call.
	ClearJSONRPCSession()
}

// WithJSONRPCSessionClearer registers an optional [JSONRPCSessionClearer]
// used by [ClearJSONRPCSessionBeforeRecovery]. Nil is valid — the step
// becomes a no-op. Returns the receiver for chaining.
func (c *ConnectionRecoveryCoordinator) WithJSONRPCSessionClearer(cl JSONRPCSessionClearer) *ConnectionRecoveryCoordinator {
	c.mu.Lock()
	c.jsonRPCSessionClearer = cl
	c.mu.Unlock()
	return c
}

// ClearJSONRPCSessionBeforeRecovery returns a [RecoveryStep] that
// invalidates the cached JSON-RPC session before the reconnect stage.
// Drop a stale session early so the first init() round-trip does not
// fail with a 401 when the CCU restarted and issued a new session ID.
//
// Usage:
//
//	Pipeline{Stage: hmenum.RecoveryStageDetecting, Run: coord.ClearJSONRPCSessionBeforeRecovery()}
//
// The step is a no-op when no [JSONRPCSessionClearer] has been wired.
func (c *ConnectionRecoveryCoordinator) ClearJSONRPCSessionBeforeRecovery() RecoveryStep {
	return func(_ context.Context) error {
		c.mu.Lock()
		cl := c.jsonRPCSessionClearer
		c.mu.Unlock()
		if cl != nil {
			cl.ClearJSONRPCSession()
		}
		return nil
	}
}

// RefreshHubDataAfterRecovery is an optional recovery pipeline step that
// reloads hub data after a successful reconnect. Add it as the last stage
// of the recovery [Pipeline] to keep the hub model consistent after a
// connection loss.
//
// The refresh order follows the reference implementation: system update
// first (so the firmware state is up-to-date before user-visible data
// is reloaded), then sysvars, then programs.
//
// Returns a [RecoveryStep] ready for inclusion in a [Pipeline]:
//
//	Pipeline{Stage: hmenum.RecoveryStageSyncHubData, Run:
//	coord.RefreshHubDataAfterRecovery()}
//
// The step is a no-op when no [HubRefresher] has been wired.
func (c *ConnectionRecoveryCoordinator) RefreshHubDataAfterRecovery() RecoveryStep {
	return func(ctx context.Context) error {
		c.mu.Lock()
		r := c.hubRefresher
		c.mu.Unlock()
		if r == nil {
			return nil
		}
		// Hub metadata (firmware/system-update state, sysvars, programs)
		// travels over ReGa — the CCU's JSON-RPC web port — which can be slow
		// or intermittently dropped (an overloaded CCU, or a firewall/IPS
		// eating bursty HTTP). The device data itself already arrived over
		// XML-RPC during the earlier reconnect stage. So this reload is
		// best-effort: a transient ReGa failure is logged and skipped rather
		// than failing the whole recovery — otherwise the interface never
		// reaches the ready state and its already-enumerated devices stay
		// hidden until a manual restart. Each refresh is reattempted by the
		// scheduler's periodic hub jobs, so a miss is self-healing. Mirrors
		// the boot-time best-effort load (runInitialSystemUpdateLoad).
		// SystemUpdate runs first so the firmware state is current before
		// sysvars/programs are reloaded.
		c.bestEffortHubRefresh(ctx, "system_update", r.RefreshSystemUpdate)
		c.bestEffortHubRefresh(ctx, "sysvars", r.RefreshSysvars)
		c.bestEffortHubRefresh(ctx, "programs", r.RefreshPrograms)
		return nil
	}
}

// bestEffortHubRefresh runs one post-recovery hub refresh and downgrades a
// failure to a WARN instead of failing the recovery stage. See
// [ConnectionRecoveryCoordinator.RefreshHubDataAfterRecovery] for why
// post-recovery hub metadata reloads are non-blocking.
func (c *ConnectionRecoveryCoordinator) bestEffortHubRefresh(ctx context.Context, op string, fn func(context.Context) error) {
	if err := fn(ctx); err != nil {
		c.log().Warn("recovery.hub_refresh.best_effort_failed",
			slog.String("central", c.centralName),
			slog.String("op", op),
			slog.String("err", err.Error()))
	}
}
