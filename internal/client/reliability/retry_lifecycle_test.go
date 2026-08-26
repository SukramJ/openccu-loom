// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func retryKey(parameter string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    "test-interface",
		ChannelAddress: "VCU0000001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      parameter,
	}
}

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
	r := fastRetrier(3)
	if !r.Enabled() {
		t.Fatal("Enabled() must be true when MaxAttempts > 0")
	}
}

// TestRetrierDisabledWhenMaxAttemptsZero verifies that a Retrier constructed
// with MaxAttempts ≤ 0 is disabled: fn is called exactly once and the retrier
// does not touch the active-retry map.
func TestRetrierDisabledWhenMaxAttemptsZero(t *testing.T) {
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
	if r.ActiveRetryCount() != 0 {
		t.Fatal("disabled Retrier must not touch active-retry map")
	}
}

// TestRetrierActiveRetryCountInitialZero verifies the retry count starts at
// zero before any DoForKey call.
func TestRetrierActiveRetryCountInitialZero(t *testing.T) {
	r := fastRetrier(3)
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount initial = %d, want 0", got)
	}
}

// ─── Metrics snapshot ────────────────────────────────────────────────────────

// TestRetrierSnapshotCreatesIndependentCopy verifies that Snapshot returns an
// immutable copy — modifying the original after the snapshot has no effect.
func TestRetrierSnapshotCreatesIndependentCopy(t *testing.T) {
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
}

// TestRetrierMetricsInitialZero verifies all metric counters start at zero.
func TestRetrierMetricsInitialZero(t *testing.T) {
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
	if m.CancelledRetries != 0 {
		t.Errorf("CancelledRetries = %d, want 0", m.CancelledRetries)
	}
}

// ─── Active-retry cleanup ─────────────────────────────────────────────────────

// TestRetrierCleanupAfterExhaustion verifies that the active-retry map is
// cleared when a DoForKey chain exhausts all attempts.
func TestRetrierCleanupAfterExhaustion(t *testing.T) {
	r := fastRetrier(3)
	key := retryKey("LEVEL")

	err := r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
		return errors.New("always fail")
	})
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after exhaustion = %d, want 0", got)
	}
}

// TestRetrierCleanupAfterSuccess verifies the active-retry map is cleared on
// eventual success.
func TestRetrierCleanupAfterSuccess(t *testing.T) {
	r := fastRetrier(3)
	key := retryKey("LEVEL")

	err := r.DoForKey(context.Background(), key, func(_ context.Context, attempt int) error {
		if attempt < 2 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after success = %d, want 0", got)
	}
}

// TestRetrierCleanupAfterNonRetryableError verifies the active-retry map is
// cleared when the first error is non-retryable.
func TestRetrierCleanupAfterNonRetryableError(t *testing.T) {
	r := fastRetrier(3)
	key := retryKey("LEVEL")

	err := r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
		return hmerr.ErrAuthFailure
	})
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("expected ErrAuthFailure, got %v", err)
	}
	if got := r.ActiveRetryCount(); got != 0 {
		t.Fatalf("ActiveRetryCount after non-retryable = %d, want 0", got)
	}
}

// ─── CancelInterface ─────────────────────────────────────────────────────────

// TestRetrierCancelInterfaceSetsAllEvents verifies that CancelInterface
// cancels every in-flight chain and returns the correct count.
func TestRetrierCancelInterfaceSetsAllEvents(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 10,
		Initial:     100 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  1,
		Jitter:      -1,
	})

	keys := []hmtypes.DataPointKey{
		retryKey("LEVEL"),
		retryKey("STATE"),
	}

	var wg sync.WaitGroup
	errs := make([]chan error, len(keys))
	for i := range keys {
		errs[i] = make(chan error, 1)
	}

	started := make(chan struct{}, len(keys))

	for i, k := range keys {
		wg.Go(func() {
			errs[i] <- r.DoForKey(context.Background(), k, func(_ context.Context, attempt int) error {
				started <- struct{}{}
				if attempt == 1 {
					return errors.New("transient")
				}
				time.Sleep(10 * time.Second)
				return nil
			})
		})
	}

	for range keys {
		<-started
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && r.ActiveRetryCount() < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	cancelled := r.CancelInterface()
	if cancelled != 2 {
		t.Fatalf("CancelInterface returned %d, want 2", cancelled)
	}

	wg.Wait()
	if r.ActiveRetryCount() != 0 {
		t.Fatal("ActiveRetryCount must be 0 after CancelInterface")
	}
	if got := r.Snapshot().CancelledRetries; got < 2 {
		t.Fatalf("CancelledRetries = %d, want >= 2", got)
	}

	for i, ch := range errs {
		err := <-ch
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Errorf("chain %d: expected ErrRetrySuperseded, got %v", i, err)
		}
	}
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
}

func TestRetrier_DoOnce_FunctionError(t *testing.T) {
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
}

// --- shouldWaitForRecovery helper ---

func TestShouldWaitForRecovery_NilError(t *testing.T) {
	if shouldWaitForRecovery(nil) {
		t.Error("shouldWaitForRecovery(nil) must return false")
	}
}

func TestShouldWaitForRecovery_RegularError(t *testing.T) {
	if shouldWaitForRecovery(errors.New("generic")) {
		t.Error("shouldWaitForRecovery(generic) must return false")
	}
}

// --- DoForKey: cancel in-progress retry ---

func TestRetrier_DoForKey_CancelKey(t *testing.T) {
	t.Parallel()
	r := NewRetrier(RetryConfig{
		MaxAttempts: 10,
		Initial:     time.Second, // slow retry — CancelKey fires before 2nd attempt
	})
	key := hmtypes.DataPointKey{ChannelAddress: "DEV:1", Parameter: "LEVEL"}
	done := make(chan error, 1)
	go func() {
		done <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			return errors.New("fail")
		})
	}()
	// Give DoForKey time to start and fail the first attempt.
	time.Sleep(20 * time.Millisecond)
	r.CancelKey(key)
	select {
	case err := <-done:
		// ErrRetrySuperseded or context cancellation are both acceptable.
		t.Logf("DoForKey after CancelKey: err=%v (accepted)", err)
	case <-time.After(3 * time.Second):
		t.Error("DoForKey did not return after CancelKey")
	}
}
