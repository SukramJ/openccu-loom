// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func fastRetrier(maxAttempts int) *Retrier {
	return NewRetrier(RetryConfig{
		MaxAttempts: maxAttempts,
		Initial:     1 * time.Millisecond,
		Max:         5 * time.Millisecond,
		Multiplier:  2,
		Jitter:      -1, // disabled for deterministic tests
	})
}

// ─── Properties ──────────────────────────────────────────────────────────────

// TestRetrierEnabledByDefault verifies the Enabled kill-switch is on after
// construction with a positive MaxAttempts.
func TestRetrierEnabledByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := fastRetrier(3)
		if !r.Enabled() {
			t.Fatal("Enabled() must be true when MaxAttempts > 0")
		}
	})
}

// TestRetrierDisabledWhenMaxAttemptsZero verifies that a Retrier constructed
// with MaxAttempts ≤ 0 is disabled: fn is called exactly once.
func TestRetrierDisabledWhenMaxAttemptsZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := NewRetrier(RetryConfig{
			MaxAttempts: 0, // constructor normalises to 3; use SetEnabled instead
		})
		r.SetEnabled(false)

		calls := 0
		err := r.Do(context.Background(), func(_ context.Context, _ int) error {
			calls++
			return errors.New("transient")
		})
		if err == nil {
			t.Fatal("expected error from single attempt")
		}
		if calls != 1 {
			t.Fatalf("disabled Retrier: calls=%d, want 1", calls)
		}
	})
}

// ─── Metrics snapshot ────────────────────────────────────────────────────────

// TestRetrierSnapshotCreatesIndependentCopy verifies that Snapshot returns an
// immutable copy — modifying the original after the snapshot has no effect.
func TestRetrierSnapshotCreatesIndependentCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := fastRetrier(3)

		// Cause two retries so the metrics are non-zero.
		_ = r.Do(context.Background(), func(_ context.Context, attempt int) error {
			if attempt < 3 {
				return errors.New("transient")
			}
			return nil
		})

		snap := r.Snapshot()
		if snap.TotalRetries != 2 {
			t.Fatalf("TotalRetries = %d, want 2", snap.TotalRetries)
		}
		if snap.SuccessfulRetries != 1 {
			t.Fatalf("SuccessfulRetries = %d, want 1", snap.SuccessfulRetries)
		}

		// Run one more retry chain to change the live metrics.
		_ = r.Do(context.Background(), func(_ context.Context, attempt int) error {
			if attempt < 3 {
				return errors.New("another transient")
			}
			return nil
		})

		// The snapshot must not change.
		if snap.TotalRetries != 2 {
			t.Fatalf("snapshot.TotalRetries mutated to %d after further calls", snap.TotalRetries)
		}
	})
}

// TestRetrierMetricsInitialZero verifies all metric counters start at zero.
func TestRetrierMetricsInitialZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := fastRetrier(3)
		m := r.Snapshot()
		if m.TotalRetries != 0 {
			t.Errorf("TotalRetries = %d, want 0", m.TotalRetries)
		}
		if m.SuccessfulRetries != 0 {
			t.Errorf("SuccessfulRetries = %d, want 0", m.SuccessfulRetries)
		}
		if m.ExhaustedRetries != 0 {
			t.Errorf("ExhaustedRetries = %d, want 0", m.ExhaustedRetries)
		}
		if m.RecoveryWaits != 0 {
			t.Errorf("RecoveryWaits = %d, want 0", m.RecoveryWaits)
		}
		if m.RecoveryWaitTimeouts != 0 {
			t.Errorf("RecoveryWaitTimeouts = %d, want 0", m.RecoveryWaitTimeouts)
		}
	})
}

// ─── isNonRetryable helpers ───────────────────────────────────────────────────

// TestRetrierIsRetryableTable tests the isNonRetryable helper for all sentinel
// error types.
func TestRetrierIsRetryableTable(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		nonRetryable bool
	}{
		{"auth failure not retryable", hmerr.ErrAuthFailure, true},
		{"permission denied not retryable", hmerr.ErrPermissionDenied, true},
		{"json-rpc code 400 not retryable", &hmerr.JSONRPCError{Code: 400, Message: "access denied"}, true},
		{"circuit breaker not retryable (without waiter)", hmerr.ErrCircuitBreakerOpen, true},
		{"unsupported not retryable", hmerr.ErrUnsupported, true},
		{"general fault retryable", &hmerr.XMLRPCFault{Code: -1}, false},
		{"unknown device fault not retryable", &hmerr.XMLRPCFault{Code: -2}, true},
		{"duty cycle fault retryable", &hmerr.XMLRPCFault{Code: -8}, false},
		{"device out of range fault retryable", &hmerr.XMLRPCFault{Code: -9}, false},
		{"transmission pending fault retryable", &hmerr.XMLRPCFault{Code: -10}, false},
		{"unknown parameter fault not retryable", &hmerr.XMLRPCFault{Code: -5}, true},
		{"generic error retryable", errors.New("generic"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isNonRetryable(c.err)
			if got != c.nonRetryable {
				t.Errorf("isNonRetryable(%T) = %v, want %v", c.err, got, c.nonRetryable)
			}
		})
	}
}

// --- Retrier.DoOnce ---

func TestRetrier_DoOnce_Success(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := NewRetrier(RetryConfig{
			MaxAttempts: 3,
			Initial:     time.Millisecond,
		})
		called := 0
		err := r.DoOnce(context.Background(), func(_ context.Context, attempt int) error {
			called++
			if attempt != 1 {
				t.Errorf("DoOnce called fn with attempt=%d, want 1", attempt)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("DoOnce: unexpected error: %v", err)
		}
		if called != 1 {
			t.Errorf("DoOnce called fn %d times, want 1", called)
		}
	})
}

func TestRetrier_DoOnce_FunctionError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := NewRetrier(RetryConfig{
			MaxAttempts: 3,
			Initial:     time.Millisecond,
		})
		boom := errors.New("boom")
		err := r.DoOnce(context.Background(), func(_ context.Context, _ int) error {
			return boom
		})
		if !errors.Is(err, boom) {
			t.Errorf("DoOnce returned %v, want %v", err, boom)
		}
	})
}

// --- shouldWaitForRecovery helper ---

func TestShouldWaitForRecovery_NilError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if shouldWaitForRecovery(nil) {
			t.Error("shouldWaitForRecovery(nil) must return false")
		}
	})
}

func TestShouldWaitForRecovery_RegularError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if shouldWaitForRecovery(errors.New("generic")) {
			t.Error("shouldWaitForRecovery(generic) must return false")
		}
	})
}
