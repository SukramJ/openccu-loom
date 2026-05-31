// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// CircuitConfig configures a [CircuitBreaker].
type CircuitConfig struct {
	// FailureThreshold is the consecutive-failure count that trips the
	// breaker OPEN.
	FailureThreshold int

	// ResetTimeout is how long to stay OPEN before switching to
	// HALF_OPEN and letting a single probe through.
	ResetTimeout time.Duration

	// HalfOpenSuccess is how many consecutive successes in HALF_OPEN
	// are required to close the breaker.
	HalfOpenSuccess int

	// Clock overrides the wall clock for tests.
	Clock func() time.Time
}

// defaultCircuitConfig fills in a reasonable baseline. Values come
// from [hmreliability] so the snapshot test there locks them down
// Against silent drift versus.
func defaultCircuitConfig() CircuitConfig {
	return CircuitConfig{
		FailureThreshold: hmreliability.CircuitFailureThreshold,
		ResetTimeout:     hmreliability.CircuitResetTimeout,
		HalfOpenSuccess:  hmreliability.CircuitHalfOpenSuccess,
		Clock:            time.Now,
	}
}

// bypassOps lists the XML-RPC / JSON-RPC operation names that must be
// executed regardless of the circuit breaker state. "init" and "ping" ensure
// the callback channel stays live; getVersion / system.* allow diagnostics
// while the circuit is tripped. Session.login / Session.logout /
// Session.renew must bypass so the JSON-RPC client can (re-)establish
// its session even when the breaker is open.
var bypassOps = map[string]struct{}{
	"init":               {},
	"ping":               {},
	"getVersion":         {},
	"system.listMethods": {},
	"system.methodHelp":  {},
	"Session.login":      {},
	"Session.logout":     {},
	"Session.renew":      {},
}

// isBypassOp reports whether operationID is in the bypass list.
func isBypassOp(operationID string) bool {
	_, ok := bypassOps[operationID]
	return ok
}

// CircuitBreaker gates calls based on recent failure rate. Safe for
// concurrent use.
type CircuitBreaker struct {
	cfg CircuitConfig

	mu               sync.Mutex
	state            hmenum.CircuitState
	consecutiveErr   int
	halfOpenOK       int
	openedAt         time.Time
	totalRequests    int64 // counts non-bypass calls (blocked + executed)
	halfOpenInFlight int32 // atomic: number of probes currently executing in HALF_OPEN

	// listeners is the slice of state-change callbacks. Setters are
	// additive (siehe AddOnStateChange). [OnStateChange] retains its
	// historic single-listener semantics and replaces the *primary*
	// listener at index 0; AddOnStateChange appends.
	listeners []func(from, to hmenum.CircuitState)
}

// NewCircuit returns a breaker in CLOSED state. cfg fields that are
// zero are replaced with sensible defaults.
func NewCircuit(cfg CircuitConfig) *CircuitBreaker {
	base := defaultCircuitConfig()
	if cfg.FailureThreshold > 0 {
		base.FailureThreshold = cfg.FailureThreshold
	}
	if cfg.ResetTimeout > 0 {
		base.ResetTimeout = cfg.ResetTimeout
	}
	if cfg.HalfOpenSuccess > 0 {
		base.HalfOpenSuccess = cfg.HalfOpenSuccess
	}
	if cfg.Clock != nil {
		base.Clock = cfg.Clock
	}
	return &CircuitBreaker{
		cfg:   base,
		state: hmenum.CircuitStateClosed,
	}
}

// OnStateChange registers (or replaces) the *primary* state-change
// callback. Passing nil removes the primary callback while leaving
// any listeners installed via [AddOnStateChange] in place.
//
// Backwards-compatible with the original single-listener contract:
// the primary callback is always at index 0 of the listener slice.
func (c *CircuitBreaker) OnStateChange(fn func(from, to hmenum.CircuitState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		if len(c.listeners) > 0 {
			c.listeners[0] = nil
		}
		return
	}
	if len(c.listeners) == 0 {
		c.listeners = append(c.listeners, fn)
		return
	}
	c.listeners[0] = fn
}

// AddOnStateChange appends a listener. Multiple subscribers can
// coexist without replacing each other — used by the incident
// recorder, the retry-recovery hook and the event-bus publisher.
func (c *CircuitBreaker) AddOnStateChange(fn func(from, to hmenum.CircuitState)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.listeners) == 0 {
		c.listeners = append(c.listeners, nil)
	}
	c.listeners = append(c.listeners, fn)
}

// snapshotListenersLocked returns a copy of the current listener slice
// (skipping the nil-able primary slot). Caller must hold c.mu.
func (c *CircuitBreaker) snapshotListenersLocked() []func(from, to hmenum.CircuitState) {
	if len(c.listeners) == 0 {
		return nil
	}
	out := make([]func(from, to hmenum.CircuitState), 0, len(c.listeners))
	for _, l := range c.listeners {
		if l != nil {
			out = append(out, l)
		}
	}
	return out
}

// State returns the current state.
func (c *CircuitBreaker) State() hmenum.CircuitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshLocked()
}

// TotalRequests returns the cumulative count of non-bypass calls that
// have been submitted to this breaker (both executed and rejected).
// Bypass operations (init, ping, …) are excluded.
func (c *CircuitBreaker) TotalRequests() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalRequests
}

// Do wraps fn with the breaker. operationID is the RPC method name;
// bypass operations (init, ping, getVersion, system.*) are always
// executed regardless of circuit state and do not count toward
// [TotalRequests] or affect the state machine.
// Non-bypass calls return [hmerr.ErrCircuitBreakerOpen] when the
// circuit is OPEN or when a probe is already in flight in HALF_OPEN
// (only one probe is allowed at a time — mirrors
// single-probe HALF_OPEN contract where `is_available` returns True
// once and subsequent concurrent callers get blocked until the probe
// settles).
func (c *CircuitBreaker) Do(ctx context.Context, operationID string, fn func(ctx context.Context) error) error {
	if isBypassOp(operationID) {
		// Bypass path: execute without touching state or counters.
		return fn(ctx)
	}

	c.mu.Lock()
	state := c.refreshLocked()
	if state == hmenum.CircuitStateOpen {
		c.totalRequests++
		c.mu.Unlock()
		return hmerr.ErrCircuitBreakerOpen
	}
	// HALF_OPEN: allow exactly one concurrent probe. Additional callers
	// see ErrCircuitBreakerOpen until the probe settles.
	if state == hmenum.CircuitStateHalfOpen {
		if !atomic.CompareAndSwapInt32(&c.halfOpenInFlight, 0, 1) {
			c.totalRequests++
			c.mu.Unlock()
			return hmerr.ErrCircuitBreakerOpen
		}
		// We are the probe. Decrement the in-flight counter after fn returns.
		defer atomic.StoreInt32(&c.halfOpenInFlight, 0)
	}
	c.totalRequests++
	c.mu.Unlock()

	err := fn(ctx)
	c.record(err)
	return err
}

// record updates breaker state based on the outcome of one call.
//
// Only transport-level failures (timeouts, unreach, transient CCU
// faults) advance the failure counter. Semantic XML-RPC faults
// (`Unknown Parameter`, missing entity, validation rejections) are
// permanent device-side outcomes that say nothing about the wire
// channel — counting them as breaker failures would trip OPEN on a
// healthy CCU that happens to be polled for a write-only parameter.
// The retrier already classifies these as non-retryable.
func (c *CircuitBreaker) record(err error) {
	c.mu.Lock()
	from := c.state
	if err == nil {
		c.consecutiveErr = 0
		if c.state == hmenum.CircuitStateHalfOpen {
			c.halfOpenOK++
			if c.halfOpenOK >= c.cfg.HalfOpenSuccess {
				c.state = hmenum.CircuitStateClosed
				c.halfOpenOK = 0
			}
		}
	} else if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) && !isSemanticFault(err) {
		c.halfOpenOK = 0
		c.consecutiveErr++
		if c.state == hmenum.CircuitStateHalfOpen {
			c.state = hmenum.CircuitStateOpen
			c.openedAt = c.cfg.Clock()
		} else if c.state == hmenum.CircuitStateClosed && c.consecutiveErr >= c.cfg.FailureThreshold {
			c.state = hmenum.CircuitStateOpen
			c.openedAt = c.cfg.Clock()
		}
	}
	to := c.state
	listeners := c.snapshotListenersLocked()
	c.mu.Unlock()

	if from != to {
		for _, cb := range listeners {
			cb(from, to)
		}
	}
}

// RecordFailure records an external failure event and advances the breaker
// state machine exactly as if the failure had come from a [Do] call. Useful
// for callers that drive the breaker from outside the Do/record hot-path
// (e.g. connection-health aggregator, unit tests).
func (c *CircuitBreaker) RecordFailure() {
	c.record(errors.New("recorded failure"))
}

// isSemanticFault reports whether err is a permanent CCU-side
// classification (Unknown Parameter, missing entity, validation
// rejection) that should not advance the breaker's failure counter.
// Retryable transport faults (UNREACH, TIMEOUT, DUTYCYCLE,
// DEVICE_OUT_OF_RANGE, TRANSMISSION_PENDING) keep their failure-
// counting semantics — those signal a wire-side condition the breaker
// is supposed to react to.
func isSemanticFault(err error) bool {
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		return false
	}
	return !fault.IsRetryable()
}

// RecordRejection increments the rejection counter for telemetry. Does not
// affect the breaker state. The rejection counter is embedded in
// [TotalRequests] — every blocked call already counts there; this method
// exists for callers that want to signal a rejection from outside the Do
// path.
func (c *CircuitBreaker) RecordRejection() {
	c.mu.Lock()
	c.totalRequests++
	c.mu.Unlock()
}

// RecordSuccess records an external success event and advances the breaker
// state machine as if it had come from a [Do] call.
func (c *CircuitBreaker) RecordSuccess() {
	c.record(nil)
}

// LastFailureTime returns the wall-clock time of the most recent failure that
// tripped or kept the breaker OPEN, or the zero value when no failure has
// been recorded yet.
func (c *CircuitBreaker) LastFailureTime() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openedAt
}

// Reset forces the breaker back to CLOSED, clearing the consecutive failure
// counter, the half-open success counter and the timestamp of the last open.
// Used by administrators after a manual recovery to short-circuit the lazy
// OPEN→HALF_OPEN→CLOSED cycle.
//
// Fires the OnStateChange callback when the state actually changes.
func (c *CircuitBreaker) Reset() {
	c.mu.Lock()
	from := c.state
	c.state = hmenum.CircuitStateClosed
	c.consecutiveErr = 0
	c.halfOpenOK = 0
	c.openedAt = time.Time{}
	listeners := c.snapshotListenersLocked()
	c.mu.Unlock()
	atomic.StoreInt32(&c.halfOpenInFlight, 0)

	if from != hmenum.CircuitStateClosed {
		for _, cb := range listeners {
			cb(from, hmenum.CircuitStateClosed)
		}
	}
}

// refreshLocked moves OPEN → HALF_OPEN when ResetTimeout has elapsed.
// Must be called with c.mu held; returns the possibly-updated state.
func (c *CircuitBreaker) refreshLocked() hmenum.CircuitState {
	if c.state != hmenum.CircuitStateOpen {
		return c.state
	}
	if c.cfg.Clock().Sub(c.openedAt) >= c.cfg.ResetTimeout {
		from := c.state
		c.state = hmenum.CircuitStateHalfOpen
		// Snapshot listeners under lock and fire async to avoid
		// running user code while holding c.mu.
		listeners := c.snapshotListenersLocked()
		for _, cb := range listeners {
			go cb(from, c.state)
		}
	}
	return c.state
}
