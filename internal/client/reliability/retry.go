// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ErrRetrySuperseded is returned by [Retrier.DoForKey] when a newer retry for
// the same [hmtypes.DataPointKey] cancels an in-flight one.
var ErrRetrySuperseded = errors.New("retry: superseded by newer command for the same data point")

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
	// [hmerr.ErrCircuitBreakerOpen] or after a network error: as soon
	// as the underlying CircuitBreaker transitions back to HALF_OPEN
	// the wait returns and the next attempt fires immediately.
	//
	// which awaits `RecoveryCompletedEvent` from the central's event
	// bus. The Go implementation passes the same primitive through a
	// small interface so the retrier stays decoupled from the bus.
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

	// Clock is the time source for backoff sleeps + deadline
	// computation. Nil falls back to the real wall clock; tests
	// inject [clock.Fake] for deterministic timing assertions.
	Clock clock.Clock
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
	// external recovery signal (circuit-breaker or network).
	RecoveryWaits int64

	// RecoveryWaitTimeouts is the number of recovery waits that expired
	// before the recovery signal arrived (the retrier fell back to the
	// regular sleep schedule).
	RecoveryWaitTimeouts int64

	// CancelledRetries is the number of chains cancelled by supersede
	// or explicit CancelKey/CancelDevice/CancelInterface calls.
	CancelledRetries int64
}

// Retrier repeats an operation with exponential backoff and jitter. Retry
// aborts when ctx is cancelled, when the error matches one of the
// non-retryable sentinels, or when MaxAttempts is reached.
//
// Active-retry tracking: [Retrier.DoForKey] keys an in-flight retry chain by
// [hmtypes.DataPointKey] so a subsequent write to the same data point can
// supersede the earlier chain.
type Retrier struct {
	cfg RetryConfig

	mu      sync.Mutex
	active  map[hmtypes.DataPointKey]chan struct{}
	metrics CommandRetryMetrics // parity metrics

	// enabled is the runtime kill-switch. When false, Do and DoForKey execute
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
	// Default jitter is `_JITTER_FACTOR = 0.2` (±20 %). A zero value
	// triggers the default; pass a negative value to disable jitter
	// explicitly.
	switch {
	case cfg.Jitter < 0:
		cfg.Jitter = 0
	case cfg.Jitter == 0:
		cfg.Jitter = 0.2
	}
	if cfg.Rand == nil {
		seed := time.Now().UnixNano()
		if seed < 0 {
			seed = -seed
		}
		src := rand.NewPCG(uint64(seed), 0xbaadf00d) //nolint:gosec // jitter only, not security-sensitive
		cfg.Rand = rand.New(src)                     //nolint:gosec // jitter only, not security-sensitive
	}
	if cfg.DutyCycleDelay <= 0 {
		cfg.DutyCycleDelay = defaultDutyCycleDelay
	}
	if cfg.TransmissionPendingDelay <= 0 {
		cfg.TransmissionPendingDelay = defaultTransmissionPendingDelay
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.New()
	}
	return &Retrier{
		cfg:     cfg,
		active:  make(map[hmtypes.DataPointKey]chan struct{}),
		enabled: true,
		sink:    cfg.IncidentSink,
	}
}

// NonRetryable lists the error sentinels that short-circuit retry.
// Auth failures and circuit-breaker-open are not worth hammering;
// unsupported and validation errors are caller-input bugs that
// retries cannot fix.
var nonRetryable = []error{
	hmerr.ErrAuthFailure,
	hmerr.ErrCircuitBreakerOpen,
	hmerr.ErrUnsupported,
	hmerr.ErrValidation,
}

// Do runs fn up to MaxAttempts times, returning the last error on
// exhaustion.
//
// When [Retrier.Enabled] is false, fn is called exactly once (fast-
// Path, mirrors.
//
// Special-case delays for CCU XML-RPC faults take precedence over
// the exponential backoff: DUTY_CYCLE waits a fixed
// [RetryConfig.DutyCycleDelay] (default 40 s) so the RF window has
// time to drain, and TRANSMISSION_PENDING waits a fixed
// [RetryConfig.TransmissionPendingDelay] (default 5 s) to give the
// in-flight command room to settle. Both special delays bypass the
// jitter — the CCU itself enforces these windows, so adding random
// Wiggle would just delay recovery further.
// `_DUTY_CYCLE_DELAY` / `_TRANSMISSION_PENDING_DELAY` paths in
// `client/command_retry.py`.
func (r *Retrier) Do(ctx context.Context, fn func(ctx context.Context, attempt int) error) error {
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
		wait := r.specialDelayFor(err)
		if wait <= 0 {
			wait = applyJitter(delay, r.cfg.Jitter, r.cfg.Rand)
		}

		// Recovery-aware sleep: when the failure was a circuit-breaker rejection
		// (or a transient network error), let the configured RecoveryWaiter
		// shortcut the wait the moment the CB transitions back to HALF_OPEN.
		if r.cfg.RecoveryWaiter != nil && shouldWaitForRecovery(err) {
			r.mu.Lock()
			r.metrics.RecoveryWaits++
			r.mu.Unlock()
			deadline := r.cfg.Clock.Now().Add(r.recoveryDeadline(wait))
			r.cfg.RecoveryWaiter.WaitForRecovery(ctx, deadline)
			if ctx.Err() != nil {
				return err
			}
			// Fall through to the next attempt without burning the
			// remaining timer slice — the recovery signal is a
			// stronger condition than the schedule.
		} else {
			timer := r.cfg.Clock.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return err
			case <-timer.C():
			}
		}
		// Advance the exponential schedule only when the regular
		// path was actually used. A DUTY_CYCLE retry keeps the
		// regular delay fresh for the next non-special failure
		// participate in the backoff sequence.
		if r.specialDelayFor(err) <= 0 {
			delay = nextDelay(delay, r.cfg.Multiplier, r.cfg.Max)
		}
	}
	return err
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
	// Transient network failures (DUTY_CYCLE / TRANSMISSION_PENDING
	// have their own fixed delays) — wait for recovery is most
	// useful when the CCU itself was unreachable.
	var fault *hmerr.XMLRPCFault
	// Only XMLRPCFaultUnreach triggers the circuit-recovery wait.
	if errors.As(err, &fault) && fault.FaultCode() == hmerr.XMLRPCFaultUnreach {
		return true
	}
	return false
}

// NewCircuitRecoveryWaiter constructs a [RecoveryWaiter] that short-circuits
// the retrier's backoff sleep as soon as the supplied CircuitBreaker
// transitions out of OPEN. Wires through [CircuitBreaker.OnStateChange] so
// existing observers (incident recorder, event bus) keep firing — the waiter
// only piggy-backs on the same callback.
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
	w.cb.OnStateChange(func(_, to hmenum.CircuitState) {
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
	// retry. Transient codes (UNREACH, TIMEOUT, DUTY_CYCLE,
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

// DoForKey is like [Do] but registers the retry chain under key so a
// subsequent DoForKey on the same key can supersede this one. The
// superseded retry returns [ErrRetrySuperseded] regardless of the
// underlying transient error so the caller can distinguish "the user
// changed their mind" from "the CCU is still struggling".
//
// Multiple concurrent retries for *different* keys do not interact
// each key has its own supersede slot.
//
// When [Retrier.Enabled] is false, fn is called exactly once without
// registering a supersede slot (the retry=False fast-path).
func (r *Retrier) DoForKey(ctx context.Context, key hmtypes.DataPointKey, fn func(ctx context.Context, attempt int) error) error {
	// enabled kill-switch.
	r.mu.Lock()
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled {
		return fn(ctx, 1)
	}

	cancel := make(chan struct{})
	r.mu.Lock()
	if prev, ok := r.active[key]; ok {
		close(prev) // signal the previous chain to bail out.
		r.metrics.CancelledRetries++
	}
	r.active[key] = cancel
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		// Only delete our slot when we are still the registered owner;
		// a newer DoForKey may have replaced us, in which case it owns
		// the cleanup.
		if r.active[key] == cancel {
			delete(r.active, key)
		}
		r.mu.Unlock()
	}()

	var err error
	delay := r.cfg.Initial
	for attempt := 1; attempt <= r.cfg.MaxAttempts; attempt++ {
		select {
		case <-cancel:
			r.mu.Lock()
			r.metrics.CancelledRetries++
			r.mu.Unlock()
			return ErrRetrySuperseded
		default:
		}
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
		wait := r.specialDelayFor(err)
		if wait <= 0 {
			wait = applyJitter(delay, r.cfg.Jitter, r.cfg.Rand)
		}
		if r.cfg.RecoveryWaiter != nil && shouldWaitForRecovery(err) {
			r.mu.Lock()
			r.metrics.RecoveryWaits++
			r.mu.Unlock()
			deadline := r.cfg.Clock.Now().Add(r.recoveryDeadline(wait))
			r.cfg.RecoveryWaiter.WaitForRecovery(ctx, deadline)
			if ctx.Err() != nil {
				return err
			}
			if r.specialDelayFor(err) <= 0 {
				delay = nextDelay(delay, r.cfg.Multiplier, r.cfg.Max)
			}
			continue
		}
		timer := r.cfg.Clock.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-cancel:
			timer.Stop()
			r.mu.Lock()
			r.metrics.CancelledRetries++
			r.mu.Unlock()
			return ErrRetrySuperseded
		case <-timer.C():
		}
		if r.specialDelayFor(err) <= 0 {
			delay = nextDelay(delay, r.cfg.Multiplier, r.cfg.Max)
		}
	}
	return err
}

// DoOnce executes fn exactly once, without registering a retry chain, without
// backoff, and without supersede tracking. The call still goes through the
// ctx-cancellation check but never re-invokes fn on error.
//
// This is the single-shot fast-path for [WriteOptions.SkipRetry]: fire-
// and-forget commands (virtual key presses, one-shot actuation) where a retry
// would cause a duplicate action.
func (r *Retrier) DoOnce(ctx context.Context, fn func(ctx context.Context, attempt int) error) error {
	return fn(ctx, 1)
}

// CancelKey aborts any in-flight [DoForKey] retry chain for key with
// [ErrRetrySuperseded]. Safe to call concurrently and idempotent missing keys
// are a no-op.
func (r *Retrier) CancelKey(key hmtypes.DataPointKey) {
	r.mu.Lock()
	if cancel, ok := r.active[key]; ok {
		close(cancel)
		delete(r.active, key)
		r.metrics.CancelledRetries++
	}
	r.mu.Unlock()
}

// CancelDevice aborts every in-flight retry chain whose key
// [hmtypes.DataPointKey.ChannelAddress] is on the given device. The channel
// address has the form `<device>:<channel-no>`; this method matches both that
// form and a bare device address, so callers can pass either "VCU1234567" or
// "VCU1234567:3" with the same effect on any DP belonging to VCU1234567.
//
// Returns the number of chains canceled. Used by the device-removal pipeline
// to drop pending optimistic retries for a device that the operator has just
// deleted.
func (r *Retrier) CancelDevice(deviceAddress string) int {
	if deviceAddress == "" {
		return 0
	}
	// Normalize to the bare device portion (everything before ':').
	devOnly := deviceAddress
	if i := indexByte(deviceAddress, ':'); i > 0 {
		devOnly = deviceAddress[:i]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	canceled := 0
	for key, cancel := range r.active {
		ch := key.ChannelAddress
		// Match either an exact device prefix ("VCU…" alone) or the
		// "VCU…:N" channel address form.
		if ch == devOnly || (len(ch) > len(devOnly) && ch[:len(devOnly)] == devOnly && ch[len(devOnly)] == ':') {
			close(cancel)
			delete(r.active, key)
			canceled++
		}
	}
	r.metrics.CancelledRetries += int64(canceled)
	return canceled
}

// CancelInterface aborts every in-flight retry chain registered with this
// Retrier. Returns the number of chains canceled. Used by the
// interface-shutdown pipeline (de-init proxy, reconnect, central stop) to
// ensure no stale retry chains keep wire resources occupied after the
// interface is going away.
func (r *Retrier) CancelInterface() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	canceled := 0
	for key, cancel := range r.active {
		close(cancel)
		delete(r.active, key)
		canceled++
	}
	r.metrics.CancelledRetries += int64(canceled)
	return canceled
}

// indexByte is a tiny helper to avoid importing strings just for this
// one call. Returns the first index of c in s, or -1 if absent.
func indexByte(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ActiveRetryCount reports how many DataPointKey-scoped retry chains
// are currently in flight. Useful for metrics and tests.
func (r *Retrier) ActiveRetryCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// Enabled reports whether retry is globally active. When false, [Do] and
// [DoForKey] execute the operation exactly once and return immediately
// without backoff.
func (r *Retrier) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// SetEnabled turns the retry kill-switch on or off at runtime. Disabling
// clears the pending-active map as a safety measure (any in-flight chains
// will drain naturally via ctx cancellation or the next attempt returning
// before the kill-switch check).
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
