// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// stateNotice is one pending state-change notification: the transition
// plus the listeners registered when it happened. It is built while the
// breaker lock is held and published once the caller has released it, so
// listener code never runs under c.mu.
type stateNotice struct {
	from, to  hmenum.CircuitState
	listeners []func(from, to hmenum.CircuitState)
}

// publish delivers the transition to every listener inline, in
// registration order.
//
// Every transition is published this way — the lazy OPEN → HALF_OPEN one
// included. Dispatching that single transition on a goroutine instead let
// it arrive after the HALF_OPEN → OPEN transition a probe that failed
// immediately had already published, so the incident log and the
// diagnostics stream ended on "half-open" while the breaker was OPEN.
func (n stateNotice) publish() {
	for _, cb := range n.listeners {
		safeCall(cb, n.from, n.to)
	}
}

// safeCall invokes cb and recovers from a panicking listener: a
// misbehaving subscriber must not unwind the call that produced the
// transition, nor stop the remaining subscribers from being notified.
func safeCall(cb func(from, to hmenum.CircuitState), from, to hmenum.CircuitState) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("circuit.state_listener_panic",
				slog.String("from", from.String()),
				slog.String("to", to.String()),
				slog.String("panic", fmt.Sprintf("%v", r)))
		}
	}()
	cb(from, to)
}

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
	totalRequests    int64        // counts non-bypass calls (blocked + executed)
	halfOpenInFlight atomic.Int32 // atomic: number of probes currently executing in HALF_OPEN

	// listeners is the slice of state-change callbacks. Setters are
	// additive (see AddOnStateChange). [OnStateChange] retains its
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
	state, notice := c.refreshLocked()
	c.mu.Unlock()
	notice.publish()
	return state
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
	return c.DoWithPriority(ctx, operationID, hmenum.CommandPriorityLow, fn)
}

// DoWithPriority behaves like [Do], with one deliberate exception:
// a [hmenum.CommandPriorityCritical] call may attempt a single probe
// through an OPEN circuit. Stop/silence commands of the alarm engine
// must be attempted even when the interface breaker is open
// (notes/concepts/alarm-concept.md §2, S5) — a siren stop that is rejected
// unsent is worse than one wasted probe on a dead wire. The probe
// shares the single-concurrent-probe slot with HALF_OPEN (concurrent
// critical callers get [hmerr.ErrCircuitBreakerOpen]), its outcome is
// recorded, and a success does NOT close the circuit — recovery stays
// driven by the connection checker.
func (c *CircuitBreaker) DoWithPriority(ctx context.Context, operationID string, priority hmenum.CommandPriority, fn func(ctx context.Context) error) error {
	if isBypassOp(operationID) {
		// Bypass path: execute without touching state or counters.
		return fn(ctx)
	}

	c.mu.Lock()
	// The lazy OPEN → HALF_OPEN flip is published before fn runs, so a
	// probe that fails immediately cannot publish its HALF_OPEN → OPEN
	// transition first.
	state, notice := c.refreshLocked()
	if state == hmenum.CircuitStateOpen {
		if priority == hmenum.CommandPriorityCritical {
			if !c.halfOpenInFlight.CompareAndSwap(0, 1) {
				c.totalRequests++
				c.mu.Unlock()
				notice.publish()
				return hmerr.ErrCircuitBreakerOpen
			}
			defer c.halfOpenInFlight.Store(0)
			c.totalRequests++
			c.mu.Unlock()
			notice.publish()
			err := fn(ctx)
			c.record(ctx, err)
			return err
		}
		c.totalRequests++
		c.mu.Unlock()
		notice.publish()
		return hmerr.ErrCircuitBreakerOpen
	}
	// HALF_OPEN: allow exactly one concurrent probe. Additional callers
	// see ErrCircuitBreakerOpen until the probe settles.
	if state == hmenum.CircuitStateHalfOpen {
		if !c.halfOpenInFlight.CompareAndSwap(0, 1) {
			c.totalRequests++
			c.mu.Unlock()
			notice.publish()
			return hmerr.ErrCircuitBreakerOpen
		}
		// We are the probe. Decrement the in-flight counter after fn returns.
		defer c.halfOpenInFlight.Store(0)
	}
	c.totalRequests++
	c.mu.Unlock()
	notice.publish()

	err := fn(ctx)
	c.record(ctx, err)
	return err
}

// record updates breaker state based on the outcome of one call. ctx is
// the caller's context, which is what tells a failure the CCU produced
// apart from one the caller produced — see [IsWireFailure].
func (c *CircuitBreaker) record(ctx context.Context, err error) {
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
	} else if IsWireFailure(ctx, err) {
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
		stateNotice{from: from, to: to, listeners: listeners}.publish()
	}
}

// RecordFailure records an external failure event and advances the breaker
// state machine exactly as if the failure had come from a [Do] call. Useful
// for callers that drive the breaker from outside the Do/record hot-path
// (e.g. connection-health aggregator, unit tests).
func (c *CircuitBreaker) RecordFailure() {
	c.record(context.Background(), errors.New("recorded failure"))
}

// IsWireFailure reports whether err is evidence about the channel to the
// CCU, which is the only thing the breaker is allowed to react to. ctx is
// the context the caller handed to [CircuitBreaker.Do].
//
// Four classes of error reach this point without having learned anything
// about the wire:
//
//   - The breaker's own rejection, which would otherwise feed itself.
//   - Semantic XML-RPC faults (`Unknown Parameter`, missing entity,
//     validation rejections): permanent device-side outcomes, so a healthy
//     CCU polled for a write-only parameter would trip the breaker. The
//     retrier already classifies these as non-retryable.
//   - The caller going away. The retrier returns immediately once the
//     caller's context is done and the throttle hands back ctx.Err() from
//     its queue, so one abandoned request — a browser tab closing on a view
//     that reads several paramsets through a pool with one command in
//     flight — unwinds every parked call at once and reaches the failure
//     threshold without a single wire attempt.
//   - The daemon shedding its own load: a full throttle queue, a throttle
//     closed at shutdown, a waiter purged in favour of a newer command.
//
// A deadline the transport sets on its own derived context is *not* in
// that set: the caller's context is still live, so a CCU that stops
// answering keeps tripping the breaker as it must.
func IsWireFailure(ctx context.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, hmerr.ErrCircuitBreakerOpen):
		return false
	case isSemanticFault(err):
		return false
	case isCallerGone(ctx, err):
		return false
	case errors.Is(err, ErrThrottleQueueFull),
		errors.Is(err, ErrThrottleClosed),
		errors.Is(err, ErrSuperseded):
		return false
	}
	return true
}

// isCallerGone reports whether err is this call unwinding because the
// context its caller supplied is done. Both halves are required: a bare
// [context.DeadlineExceeded] with a live caller context comes from a
// per-attempt deadline further down the stack and is a real timeout.
func isCallerGone(ctx context.Context, err error) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// isSemanticFault reports whether err is a permanent CCU-side
// classification (Unknown Device, Unknown Parameter, missing entity,
// validation rejection) that should not advance the breaker's failure
// counter. The retryable faults (GENERAL, DUTYCYCLE,
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
	c.record(context.Background(), nil)
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
	c.halfOpenInFlight.Store(0)

	if from != hmenum.CircuitStateClosed {
		stateNotice{from: from, to: hmenum.CircuitStateClosed, listeners: listeners}.publish()
	}
}

// refreshLocked moves OPEN → HALF_OPEN when ResetTimeout has elapsed.
// Must be called with c.mu held; returns the possibly-updated state and
// the notification the caller must publish after releasing c.mu (the
// zero value when nothing changed).
func (c *CircuitBreaker) refreshLocked() (hmenum.CircuitState, stateNotice) {
	if c.state != hmenum.CircuitStateOpen {
		return c.state, stateNotice{}
	}
	if c.cfg.Clock().Sub(c.openedAt) >= c.cfg.ResetTimeout {
		from := c.state
		c.state = hmenum.CircuitStateHalfOpen
		return c.state, stateNotice{from: from, to: c.state, listeners: c.snapshotListenersLocked()}
	}
	return c.state, stateNotice{}
}
