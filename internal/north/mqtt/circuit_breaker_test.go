// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"
	"time"
)

// TestMqttCircuitBreakerClosedToOpen verifies the CLOSED → OPEN transition
// after reaching the failure threshold.
func TestMqttCircuitBreakerClosedToOpen(t *testing.T) {
	cb := NewMqttCircuitBreaker(3, time.Minute, nil)

	if cb.State() != CircuitClosed {
		t.Fatalf("initial state: want closed, got %s", cb.State())
	}

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Errorf("after 2 failures (threshold=3): want closed, got %s", cb.State())
	}

	cb.RecordFailure() // third failure → open
	if cb.State() != CircuitOpen {
		t.Fatalf("after 3 failures: want open, got %s", cb.State())
	}
	if !cb.IsOpen() {
		t.Error("IsOpen() must return true in open state")
	}
}

// TestMqttCircuitBreakerOpenToHalfOpen verifies the OPEN → HALF_OPEN
// transition after the recovery timeout.
func TestMqttCircuitBreakerOpenToHalfOpen(t *testing.T) {
	// Use a very short recovery timeout so the test does not need to sleep.
	cb := NewMqttCircuitBreaker(1, 1*time.Millisecond, nil)

	cb.RecordFailure() // → open
	if cb.State() != CircuitOpen {
		t.Fatalf("state should be open after 1 failure")
	}

	// Wait for recovery window to elapse.
	time.Sleep(5 * time.Millisecond)

	// IsOpen() triggers the OPEN → HALF_OPEN transition.
	if cb.IsOpen() {
		t.Fatalf("IsOpen() should return false after recovery timeout (half-open probe)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected half_open after recovery timeout, got %s", cb.State())
	}
}

// TestMqttCircuitBreakerHalfOpenToClosedOnSuccess verifies the
// HALF_OPEN → CLOSED transition on RecordSuccess.
func TestMqttCircuitBreakerHalfOpenToClosedOnSuccess(t *testing.T) {
	cb := NewMqttCircuitBreaker(1, 1*time.Millisecond, nil)

	cb.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	cb.IsOpen() // trigger OPEN → HALF_OPEN
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected half_open, got %s", cb.State())
	}

	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Errorf("after RecordSuccess in half_open: want closed, got %s", cb.State())
	}
	if cb.FailureCount() != 0 {
		t.Errorf("failure count should be 0 after success, got %d", cb.FailureCount())
	}
}

// TestMqttCircuitBreakerReset verifies the manual Reset path.
func TestMqttCircuitBreakerReset(t *testing.T) {
	cb := NewMqttCircuitBreaker(2, time.Minute, nil)
	cb.RecordFailure()
	cb.RecordFailure() // → open

	cb.Reset()
	if cb.State() != CircuitClosed {
		t.Errorf("after Reset: want closed, got %s", cb.State())
	}
	if cb.FailureCount() != 0 {
		t.Errorf("failure count after Reset: want 0, got %d", cb.FailureCount())
	}
}
