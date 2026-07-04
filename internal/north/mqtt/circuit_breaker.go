// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// CircuitState represents one of the three states of [CircuitBreaker].
type CircuitState uint8

const (
	// CircuitClosed is normal operation — calls pass through.
	CircuitClosed CircuitState = iota
	// CircuitOpen blocks calls due to accumulated failures.
	CircuitOpen
	// CircuitHalfOpen is a recovery probe: one call is allowed through
	// to test whether the service has recovered.
	CircuitHalfOpen
)

// String returns a human-readable state label.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// CircuitBreaker guards MQTT operations against cascading failures. After
// [FailureThreshold] consecutive failures the circuit opens and subsequent
// [IsOpen] calls return true until the recovery window ([RecoveryTimeout])
// elapses, at which point the circuit transitions to half-open and admits one
// probe call.
type CircuitBreaker struct {
	FailureThreshold int
	RecoveryTimeout  time.Duration
	logger           *slog.Logger
	collector        *metrics.MqttCollector // may be nil
	clk              clock.Clock            // wall-clock seam; real unless overridden in tests

	mu              sync.Mutex
	state           CircuitState
	failureCount    int
	lastFailureTime time.Time
}

// NewMqttCircuitBreaker constructs a [CircuitBreaker] with the
// supplied thresholds. Default values are used when zero is passed:
// 5 failures and a 30-second recovery window.
//
// loom:reachable:reason="currently unwired; retained until breaker semantics move into the shared go-mqtt module, which will replace this type"
func NewMqttCircuitBreaker(failureThreshold int, recoveryTimeout time.Duration, logger *slog.Logger) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if recoveryTimeout <= 0 {
		recoveryTimeout = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CircuitBreaker{
		FailureThreshold: failureThreshold,
		RecoveryTimeout:  recoveryTimeout,
		logger:           logger,
		clk:              clock.New(),
	}
}

// WithCollector attaches the metrics collector so the circuit breaker can
// increment the CircuitBreakerOpened counter on every CLOSED→OPEN transition.
// Returns the receiver for call-site chaining.
func (cb *CircuitBreaker) WithCollector(col *metrics.MqttCollector) *CircuitBreaker {
	cb.collector = col
	return cb
}

// WithClock overrides the wall-clock source. Tests inject a [clock.Fake]
// so the OPEN → HALF_OPEN recovery transition is driven by an explicit
// Advance rather than real time — a 1 ms timeout plus a sleep is racy on
// coarse-grained clocks (notably Windows CI). Returns the receiver for
// call-site chaining.
func (cb *CircuitBreaker) WithClock(clk clock.Clock) *CircuitBreaker {
	if clk != nil {
		cb.clk = clk
	}
	return cb
}

// State returns the current [CircuitState]. Transitions OPEN → HALF_OPEN
// when the recovery timeout has elapsed (same behaviour as [IsOpen]).
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maybeTransitionToHalfOpenLocked()
	return cb.state
}

// IsOpen returns true when the circuit is open and calls should be
// blocked. Automatically transitions OPEN → HALF_OPEN after the
// recovery timeout so the next probe call is admitted.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maybeTransitionToHalfOpenLocked()
	return cb.state == CircuitOpen
}

// RecordSuccess records a successful MQTT operation. If the circuit was
// HALF_OPEN (recovery probe), it is fully closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	wasHalfOpen := cb.state == CircuitHalfOpen
	cb.failureCount = 0
	cb.state = CircuitClosed
	cb.lastFailureTime = time.Time{}
	cb.mu.Unlock()
	if wasHalfOpen {
		cb.logger.Info("mqtt.circuit_breaker: recovered, circuit closed")
	}
}

// RecordFailure records a failed MQTT operation, incrementing the
// failure counter. Once [FailureThreshold] is reached the circuit opens.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	cb.failureCount++
	cb.lastFailureTime = cb.clk.Now()
	if cb.failureCount >= cb.FailureThreshold && cb.state != CircuitOpen {
		cb.state = CircuitOpen
		count := cb.failureCount
		collector := cb.collector
		cb.mu.Unlock()
		cb.logger.Warn(
			"mqtt.circuit_breaker: opened",
			slog.Int("failures", count),
			slog.Int("threshold", cb.FailureThreshold),
		)
		if collector != nil {
			collector.CircuitBreakerOpened.Inc()
		}
		return
	}
	count := cb.failureCount
	threshold := cb.FailureThreshold
	cb.mu.Unlock()
	cb.logger.Debug(
		"mqtt.circuit_breaker: failure recorded",
		slog.Int("failures", count),
		slog.Int("threshold", threshold),
	)
}

// Reset manually resets the circuit to CLOSED state, clearing all
// counters.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	cb.failureCount = 0
	cb.state = CircuitClosed
	cb.lastFailureTime = time.Time{}
	cb.mu.Unlock()
	cb.logger.Info("mqtt.circuit_breaker: manually reset")
}

// FailureCount returns the current failure count.
func (cb *CircuitBreaker) FailureCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failureCount
}

// maybeTransitionToHalfOpenLocked checks whether the recovery window
// has elapsed and transitions OPEN → HALF_OPEN if so. Must be called
// with cb.mu held.
func (cb *CircuitBreaker) maybeTransitionToHalfOpenLocked() {
	if cb.state != CircuitOpen {
		return
	}
	if cb.lastFailureTime.IsZero() {
		return
	}
	if cb.clk.Now().Sub(cb.lastFailureTime) >= cb.RecoveryTimeout {
		cb.state = CircuitHalfOpen
		cb.logger.Info("mqtt.circuit_breaker: recovery window elapsed, half-open")
	}
}
