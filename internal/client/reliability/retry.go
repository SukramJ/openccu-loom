// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// RetryConfig configures a [Retrier]. Zero values pick sensible defaults.
type RetryConfig struct {
	// MaxAttempts is the number of tries including the initial attempt.
	MaxAttempts int

	// Initial is the first backoff delay.
	Initial time.Duration

	// Max caps the per-attempt delay.
	Max time.Duration

	// Multiplier is the exponential growth factor (default 2).
	Multiplier float64

	// Jitter adds random wiggle up to this fraction of the delay.
	Jitter float64

	// Rand is the random source used for jitter. Defaults to a new
	// math/rand/v2 PCG seeded from the wall clock at construction.
	Rand *rand.Rand

	// DutyCycleDelay overrides the fixed delay applied when the CCU returns
	// DUTY_CYCLE (-8). Zero falls back to the default.
	DutyCycleDelay time.Duration

	// TransmissionPendingDelay overrides the fixed delay applied when the CCU
	// returns TRANSMISSION_PENDING (-10). Zero falls back to the default.
	TransmissionPendingDelay time.Duration

	// RecoveryWaiter, when non-nil, lets the retrier wait for an
	// external recovery signal instead of sleeping the full backoff
	// interval. The retrier calls `WaitForRecovery(ctx, deadline)`
	// when the most recent attempt failed with
	// [hmerr.ErrCircuitBreakerOpen] or with a general XML-RPC fault (see
	// shouldWaitForRecovery): as soon as the underlying CircuitBreaker
	// transitions back to HALF_OPEN the wait returns and the next attempt
	// fires immediately. [NewCircuitRecoveryWaiter] builds the standard
	// implementation; the interface keeps the retrier decoupled from the
	// breaker. The daemon's composition root does not currently set this
	// field, so the shortcut is opt-in.
	RecoveryWaiter RecoveryWaiter

	// RecoveryWait caps how long the retrier blocks waiting for the
	// recovery signal from [RecoveryWaiter]. Default 120 s — recovery
	// is most effective when the retrier is allowed to wait far longer
	// than the exponential backoff schedule alone permits. Zero falls
	// back to [hmreliability.RetryRecoveryWait].
	RecoveryWait time.Duration

	// IncidentSink, when non-nil, receives an [IncidentRecord] for every
	// retry chain that exhausts all attempts without success. Build one
	// with [WireRetryIncidents] to route failures into the persistent
	// incident store. Nil disables incident recording (default).
	IncidentSink IncidentSink
}

// RecoveryWaiter is the optional hook a [Retrier] uses to short-circuit
// its backoff sleep when an external recovery signal arrives. Concrete
// implementations bridge to the central's event bus (e.g. listening
// for `CircuitBreakerStateChangedEvent` with `to == HALF_OPEN`). When
// no signal arrives before `deadline`, implementations must return
// without an error so the retrier falls through to the regular sleep
// completion.
//
// Implementations must be safe to call concurrently — multiple
// retriers may share one waiter.
type RecoveryWaiter interface {
	WaitForRecovery(ctx context.Context, deadline time.Time)
}

// RecoveryWaiterFunc adapts an ordinary function value into a
// [RecoveryWaiter].
type RecoveryWaiterFunc func(ctx context.Context, deadline time.Time)

// WaitForRecovery implements [RecoveryWaiter].
func (f RecoveryWaiterFunc) WaitForRecovery(ctx context.Context, deadline time.Time) {
	f(ctx, deadline)
}

// Default fixed delays for CCU faults that demand a specific
// recovery window rather than exponential backoff. Sourced from
// [hmreliability] so the snapshot test there locks them down against
// silent drift.
var (
	defaultDutyCycleDelay           = hmreliability.RetryDutyCycleDelay
	defaultTransmissionPendingDelay = hmreliability.RetryTransmissionPendingDelay
)

// CommandRetryMetrics holds cumulative counters for retry activity. All
// fields are additive since construction; [Retrier.Snapshot] returns an
// immutable copy.
type CommandRetryMetrics struct {
	// TotalRetries is the total number of retry attempts (not counting
	// the initial attempt of each call).
	TotalRetries int64

	// SuccessfulRetries is the number of retry chains that eventually
	// succeeded after one or more transient failures.
	SuccessfulRetries int64

	// ExhaustedRetries is the number of chains that hit MaxAttempts
	// without a successful outcome.
	ExhaustedRetries int64

	// RecoveryWaits is the number of times the retrier waited for an
	// external recovery signal (circuit-breaker open or general fault).
	RecoveryWaits int64

	// RecoveryWaitTimeouts is reserved for recovery waits that expired
	// before the recovery signal arrived. The retrier does not increment
	// it today, so it always reads zero.
	RecoveryWaitTimeouts int64
}

// Retrier repeats an operation with exponential backoff and jitter. Retry
// aborts when ctx is cancelled, when the error matches one of the
// non-retryable sentinels, or when MaxAttempts is reached.
type Retrier struct {
	cfg RetryConfig

	mu      sync.Mutex
	metrics CommandRetryMetrics // parity metrics

	// jitterMu guards cfg.Rand: a *rand.Rand is not safe for concurrent use,
	// and one Retrier serves every concurrent call of its interface, so two
	// goroutines can compute jitter at the same time. A dedicated mutex keeps
	// this off the r.mu hot path and only wraps the pure jitter computation.
	jitterMu sync.Mutex

	// enabled is the runtime kill-switch. When false, Do and DoMethod execute
	// the operation once and return without retrying regardless of the error.
	// Default is true.
	enabled bool

	// sink receives a notification for every exhausted retry chain. Sourced
	// from cfg.IncidentSink at construction; nil disables incident recording.
	sink IncidentSink
}

// NewRetrier returns a retrier.
func NewRetrier(cfg RetryConfig) *Retrier {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Initial <= 0 {
		cfg.Initial = hmreliability.RetryInitialBackoff
	}
	if cfg.Max <= 0 {
		cfg.Max = hmreliability.RetryMaxBackoff
	}
	if cfg.Multiplier <= 1 {
		cfg.Multiplier = 2
	}
	// A zero value triggers the shared default; pass a negative value to
	// disable jitter explicitly.
	switch {
	case cfg.Jitter < 0:
		cfg.Jitter = 0
	case cfg.Jitter == 0:
		cfg.Jitter = hmreliability.RetryJitterFraction
	}
	if cfg.Rand == nil {
		seed := time.Now().UnixNano()
		if seed < 0 {
			seed = -seed
		}
		src := rand.NewPCG(uint64(seed), 0xbaadf00d) //nolint:gosec // jitter only, not security-sensitive; see #20
		cfg.Rand = rand.New(src)                     //nolint:gosec // jitter only, not security-sensitive; see #20
	}
	if cfg.DutyCycleDelay <= 0 {
		cfg.DutyCycleDelay = defaultDutyCycleDelay
	}
	if cfg.TransmissionPendingDelay <= 0 {
		cfg.TransmissionPendingDelay = defaultTransmissionPendingDelay
	}
	return &Retrier{
		cfg:     cfg,
		enabled: true,
		sink:    cfg.IncidentSink,
	}
}

// NonRetryable lists the error sentinels that short-circuit retry.
// Auth failures and circuit-breaker-open are not worth hammering;
// unsupported and validation errors are caller-input bugs that
// retries cannot fix. A permission denial (CCU code 400) is a
// deterministic privilege-level mismatch — retrying never lifts it.
var nonRetryable = []error{
	hmerr.ErrAuthFailure,
	hmerr.ErrPermissionDenied,
	hmerr.ErrCircuitBreakerOpen,
	hmerr.ErrUnsupported,
	hmerr.ErrValidation,
}

// Do runs fn up to MaxAttempts times, returning the last error on
// exhaustion.
//
// When [Retrier.Enabled] is false, fn is called exactly once — the
// kill-switch fast path, equivalent to a caller passing retry=false.
//
// Special-case delays for CCU XML-RPC faults take precedence over
// the exponential backoff: DUTY_CYCLE (-8) waits a fixed
// [RetryConfig.DutyCycleDelay] (default 40 s) and
// TRANSMISSION_PENDING (-10) a fixed
// [RetryConfig.TransmissionPendingDelay] (default 5 s). Both bypass
// the jitter, which is a de-synchronisation choice only: neither
// window is enforced by the CCU. -8 is raised solely inside
// RFDevice::UpdateFirmware (OpenCCU-Base src/rfd/RFDevice.cpp:1492)
// behind an instantaneous airtime-budget test, and -10 is HmIP-only
// and gated on the target device's reachability, not on elapsed time.
// The durations are witness values carried over from the Python
// reference implementation; how long either condition really takes to
// clear is unverified — see [hmreliability.RetryDutyCycleDelay] and
// [hmreliability.RetryTransmissionPendingDelay] for what each fault
// actually means on the wire. The shape of the two special paths
// follows the Python reference implementation's
// `client/command_retry.py`.
//
// Do does not know which call fn makes, so it repeats a lost reply
// ([hmerr.ErrReplyLost]) like any other transient failure. A caller that
// sends a named RPC method uses [Retrier.DoMethod] instead.
func (r *Retrier) Do(ctx context.Context, fn func(ctx context.Context, attempt int) error) error {
	return r.do(ctx, nil, fn)
}

// DoMethod is [Retrier.Do] for one call of the RPC method method. It
// repeats a lost reply only when [RepeatableAfterLostReply] allows it for
// method: the request was sent and the CCU may already have executed it,
// so a state-changing call is answered with the lost-reply error instead
// of being sent again. Every other failure keeps the policy of Do — a dial
// that never reached the CCU and a fault the CCU answered with
// (DUTY_CYCLE, TRANSMISSION_PENDING) are retried for writes too, because
// the CCU did not act on them.
func (r *Retrier) DoMethod(ctx context.Context, method string, fn func(ctx context.Context, attempt int) error) error {
	if RepeatableAfterLostReply(method) {
		return r.do(ctx, nil, fn)
	}
	return r.do(ctx, isLostReply, fn)
}

// isLostReply is the extra stop condition [Retrier.DoMethod] applies to a
// method that is not safe to repeat.
func isLostReply(err error) bool { return errors.Is(err, hmerr.ErrReplyLost) }

// do is the retry loop behind [Retrier.Do] and [Retrier.DoMethod]. stop,
// when non-nil, ends the chain on an error the configured policy would
// otherwise retry.
func (r *Retrier) do(ctx context.Context, stop func(error) bool, fn func(ctx context.Context, attempt int) error) error {
	// enabled kill-switch — single attempt when disabled.
	// retry=false fast-path (same semantic).
	r.mu.Lock()
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled {
		return fn(ctx, 1)
	}

	var err error
	delay := r.cfg.Initial
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		if err = fn(ctx, attempt); err == nil {
			if attempt > 1 {
				r.mu.Lock()
				r.metrics.SuccessfulRetries++
				r.mu.Unlock()
			}
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if r.isNonRetryableUnderConfig(err) {
			return err
		}
		if stop != nil && stop(err) {
			return err
		}
		if attempt == r.cfg.MaxAttempts {
			r.mu.Lock()
			r.metrics.ExhaustedRetries++
			r.mu.Unlock()
			if r.sink != nil {
				r.sink.ReportRetryExhausted(err)
			}
			return fmt.Errorf("retry: exhausted after %d attempts: %w", attempt, err)
		}
		r.mu.Lock()
		r.metrics.TotalRetries++
		r.mu.Unlock()
		wait := r.waitAfter(err, delay)

		// Recovery-aware sleep: when the failure was a circuit-breaker rejection
		// (or a transient network error), let the configured RecoveryWaiter
		// shortcut the wait the moment the CB transitions back to HALF_OPEN.
		if r.cfg.RecoveryWaiter != nil && shouldWaitForRecovery(err) {
			r.mu.Lock()
			r.metrics.RecoveryWaits++
			r.mu.Unlock()
			deadline := time.Now().Add(r.recoveryDeadline(wait))
			r.cfg.RecoveryWaiter.WaitForRecovery(ctx, deadline)
			if ctx.Err() != nil {
				return err
			}
			// Fall through to the next attempt without burning the
			// remaining timer slice — the recovery signal is a
			// stronger condition than the schedule.
		} else {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return err
			case <-timer.C:
			}
		}
		delay = r.advanceSchedule(err, delay)
	}
	return err
}

// waitAfter returns how long to wait before the attempt that follows a
// failure with err, given the current position `delay` in the exponential
// schedule. A CCU fault with a fixed recovery window (DUTY_CYCLE,
// TRANSMISSION_PENDING) takes precedence and bypasses the jitter; everything
// else waits the jittered schedule delay.
func (r *Retrier) waitAfter(err error, delay time.Duration) time.Duration {
	if wait := r.specialDelayFor(err); wait > 0 {
		return wait
	}
	return r.jitter(delay)
}

// advanceSchedule returns the next position in the exponential schedule after
// a failure with err. The schedule advances only when the regular path was
// actually used: a fault served by a fixed special delay keeps the regular
// delay fresh for the next non-special failure instead of consuming a step of
// the backoff sequence.
//
// Like [Retrier.waitAfter] this is deliberately the single copy of the rule —
// it used to be written out three times across the two loops.
func (r *Retrier) advanceSchedule(err error, delay time.Duration) time.Duration {
	if r.specialDelayFor(err) > 0 {
		return delay
	}
	return nextDelay(delay, r.cfg.Multiplier, r.cfg.Max)
}

// shouldWaitForRecovery reports whether err is a class of failure
// that benefits from the recovery shortcut. Permanent / non-retryable
// errors stay out so we don't keep listening for a state-change that
// will never improve the situation.
func shouldWaitForRecovery(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		return true
	}
	// DUTY_CYCLE / TRANSMISSION_PENDING have their own fixed delays and
	// do not wait for recovery.
	var fault *hmerr.XMLRPCFault
	// Only the general fault triggers the circuit-recovery wait.
	if errors.As(err, &fault) && fault.FaultCode() == hmerr.XMLRPCFaultGeneral {
		return true
	}
	return false
}

// NewCircuitRecoveryWaiter constructs a [RecoveryWaiter] that short-circuits
// the retrier's backoff sleep as soon as the supplied CircuitBreaker
// transitions out of OPEN. Wires through [CircuitBreaker.AddOnStateChange] so
// existing observers (incident recorder, event bus) keep firing, and multiple
// waiters on one breaker each keep their own hook — the waiter only
// piggy-backs, it never replaces another listener.
//
// Pass the same CircuitBreaker the retrier is wrapped under (typically inside
// [Client.Call]) so the waiter actually reacts to the right circuit.
func NewCircuitRecoveryWaiter(cb *CircuitBreaker) RecoveryWaiter {
	if cb == nil {
		return nil
	}
	return &circuitRecoveryWaiter{cb: cb}
}

type circuitRecoveryWaiter struct {
	cb *CircuitBreaker

	mu     sync.Mutex
	wakers []chan struct{}
	wired  bool
}

func (w *circuitRecoveryWaiter) WaitForRecovery(ctx context.Context, deadline time.Time) {
	w.ensureHook()

	// Fast path: the breaker may already be back in HALF_OPEN.
	if state := w.cb.State(); state != hmenum.CircuitStateOpen {
		return
	}

	ch := make(chan struct{})
	w.mu.Lock()
	w.wakers = append(w.wakers, ch)
	w.mu.Unlock()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (w *circuitRecoveryWaiter) ensureHook() {
	w.mu.Lock()
	already := w.wired
	if !already {
		w.wired = true
	}
	w.mu.Unlock()
	if already {
		return
	}
	w.cb.AddOnStateChange(func(_, to hmenum.CircuitState) {
		if to == hmenum.CircuitStateOpen {
			return
		}
		w.mu.Lock()
		wakers := w.wakers
		w.wakers = nil
		w.mu.Unlock()
		for _, ch := range wakers {
			close(ch)
		}
	})
}

// recoveryDeadline returns the maximum wait the retrier blocks the
// RecoveryWaiter for. It is the larger of `wait` (the regular backoff
// for the current attempt — so a recovery never *shortens* the
// schedule) and `cfg.RecoveryWait` (default 120 s — lets the retrier
// listen far longer than the exponential backoff alone permits).
func (r *Retrier) recoveryDeadline(wait time.Duration) time.Duration {
	recovery := r.cfg.RecoveryWait
	if recovery <= 0 {
		recovery = hmreliability.RetryRecoveryWait
	}
	if wait > recovery {
		return wait
	}
	return recovery
}

// specialDelayFor returns the fixed wait time mandated by err's
// XML-RPC fault code, or zero when no special handling applies.
func (r *Retrier) specialDelayFor(err error) time.Duration {
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		return 0
	}
	switch fault.FaultCode() { //nolint:exhaustive // only DutyCycle and TransmissionPending have special delays; all other fault codes return 0
	case hmerr.XMLRPCFaultDutyCycle:
		return r.cfg.DutyCycleDelay
	case hmerr.XMLRPCFaultTransmissionPending:
		return r.cfg.TransmissionPendingDelay
	}
	return 0
}

func isNonRetryable(err error) bool {
	for _, s := range nonRetryable {
		if errors.Is(err, s) {
			return true
		}
	}
	// CCU XML-RPC fault classification: unknown codes (and any code
	// not in the retryable set defined in pkg/hmerr) short-circuit
	// retry. The transient codes (GENERAL, DUTY_CYCLE,
	// DEVICE_OUT_OF_RANGE, TRANSMISSION_PENDING) flow through to
	// the regular backoff or to the special-delay path.
	var fault *hmerr.XMLRPCFault
	if errors.As(err, &fault) && !fault.IsRetryable() {
		return true
	}
	return false
}

// isNonRetryableUnderConfig is the policy-aware variant of [isNonRetryable].
// When the retrier has a [RecoveryWaiter] configured,
// [hmerr.ErrCircuitBreakerOpen] is treated as transient: the retrier blocks
// on the waiter and re-issues the call once the breaker opens the gate.
func (r *Retrier) isNonRetryableUnderConfig(err error) bool {
	if r.cfg.RecoveryWaiter != nil && errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		return false
	}
	return isNonRetryable(err)
}

// DoOnce executes fn exactly once, without a retry chain and without
// backoff. The call still goes through the
// ctx-cancellation check but never re-invokes fn on error.
//
// This is the single-shot fast-path for [WriteOptions.SkipRetry]: fire-
// and-forget commands (virtual key presses, one-shot actuation) where a retry
// would cause a duplicate action.
func (r *Retrier) DoOnce(ctx context.Context, fn func(ctx context.Context, attempt int) error) error {
	return fn(ctx, 1)
}

// Enabled reports whether retry is globally active. When false, [Do] and
// [DoMethod] execute the operation exactly once and return immediately
// without backoff.
func (r *Retrier) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// SetEnabled turns the retry kill-switch on or off at runtime. It only
// flips the flag: chains already in flight are not cancelled and drain on
// their own (ctx cancellation or their next attempt).
func (r *Retrier) SetEnabled(on bool) {
	r.mu.Lock()
	r.enabled = on
	r.mu.Unlock()
}

// Snapshot returns an immutable copy of the current retry metrics. Safe to
// call concurrently.
func (r *Retrier) Snapshot() CommandRetryMetrics {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metrics
}

func nextDelay(cur time.Duration, mult float64, limit time.Duration) time.Duration {
	next := time.Duration(float64(cur) * mult)
	if next > limit {
		return limit
	}
	return next
}

// jitter applies configured jitter to d, serialising access to the shared
// (non-concurrency-safe) cfg.Rand.
func (r *Retrier) jitter(d time.Duration) time.Duration {
	r.jitterMu.Lock()
	defer r.jitterMu.Unlock()
	return applyJitter(d, r.cfg.Jitter, r.cfg.Rand)
}

func applyJitter(d time.Duration, frac float64, rng *rand.Rand) time.Duration {
	if frac <= 0 {
		return d
	}
	delta := float64(d) * frac
	offset := (rng.Float64()*2 - 1) * delta
	out := time.Duration(float64(d) + offset)
	if out < 0 {
		return d
	}
	return out
}
