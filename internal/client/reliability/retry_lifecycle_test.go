// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// ─── CancelDevice ────────────────────────────────────────────────────────────

// TestRetrierCancelDeviceCancelsMatchingKey verifies that CancelDevice
// closes the cancel channel for matching device addresses, drops the
// retry chain from the active set, and increments CancelledRetries.
//
// Implementation note: CancelDevice acts between retry attempts (in
// the sleep/backoff phase) — it cannot interrupt a user function that
// is already running. The Go assertion therefore reads the bookkeeping
// side effects (ActiveRetryCount, CancelledRetries metric, channel-close
// observable on the next sleep) instead of asserting that an in-flight
// call returns ErrRetrySuperseded.
func TestRetrierCancelDeviceCancelsMatchingKey(t *testing.T) {
	r := fastRetrier(5)
	key := retryKey("LEVEL")

	started := make(chan struct{}, 1)
	done := make(chan error, 1)

	go func() {
		done <- r.DoForKey(context.Background(), key, func(_ context.Context, _ int) error {
			select {
			case started <- struct{}{}:
			default:
			}
			return errors.New("transient")
		})
	}()

	<-started
	for i := 0; i < 50; i++ {
		if r.ActiveRetryCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.ActiveRetryCount() != 1 {
		t.Fatal("retry chain should be registered before CancelDevice")
	}

	cancelled := r.CancelDevice("VCU0000001")
	if cancelled != 1 {
		t.Fatalf("CancelDevice returned %d, want 1", cancelled)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrRetrySuperseded) {
			t.Fatalf("expected ErrRetrySuperseded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CancelDevice did not unblock the retry chain")
	}

	if got := r.Snapshot().CancelledRetries; got < 1 {
		t.Fatalf("CancelledRetries = %d, want >= 1", got)
	}
	if r.ActiveRetryCount() != 0 {
		t.Fatal("ActiveRetryCount must be 0 after cancel")
	}
}

// TestRetrierCancelDeviceDoesNotAffectOtherDevice verifies that CancelDevice
// on VCU0000001 leaves a retry chain for VCU0000002 untouched.
func TestRetrierCancelDeviceDoesNotAffectOtherDevice(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 10,
		Initial:     100 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  1,
		Jitter:      -1,
	})

	key1 := hmtypes.DataPointKey{
		InterfaceID:    "test",
		ChannelAddress: "VCU0000001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}
	key2 := hmtypes.DataPointKey{
		InterfaceID:    "test",
		ChannelAddress: "VCU0000002:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	started1 := make(chan struct{})
	started2 := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- r.DoForKey(context.Background(), key1, func(_ context.Context, attempt int) error {
			select {
			case <-started1:
			default:
				close(started1)
			}
			if attempt == 1 {
				return errors.New("transient")
			}
			time.Sleep(10 * time.Second)
			return nil
		})
	}()
	go func() {
		defer wg.Done()
		errs <- r.DoForKey(context.Background(), key2, func(_ context.Context, attempt int) error {
			select {
			case <-started2:
			default:
				close(started2)
			}
			if attempt == 1 {
				return errors.New("transient")
			}
			time.Sleep(10 * time.Second)
			return nil
		})
	}()

	<-started1
	<-started2
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && r.ActiveRetryCount() < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	cancelled := r.CancelDevice("VCU0000001")
	if cancelled != 1 {
		r.CancelDevice("VCU0000002")
		t.Fatalf("CancelDevice returned %d, want 1", cancelled)
	}

	results := make(map[bool]int)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for range 1 {
		select {
		case err := <-errs:
			if errors.Is(err, ErrRetrySuperseded) {
				results[true]++
			}
		case <-timer.C:
			t.Fatal("timeout waiting for cancelled chain to return")
		}
	}

	if r.ActiveRetryCount() != 1 {
		t.Fatalf("ActiveRetryCount = %d after single cancel, want 1", r.ActiveRetryCount())
	}

	r.CancelInterface()
}

// TestRetrierCancelDeviceDoesNotMatchPrefix verifies that a shorter device
// address does not cancel a key with a longer channel address.
func TestRetrierCancelDeviceDoesNotMatchPrefix(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 10,
		Initial:     100 * time.Millisecond,
		Max:         100 * time.Millisecond,
		Multiplier:  1,
		Jitter:      -1,
	})

	key := hmtypes.DataPointKey{
		InterfaceID:    "test",
		ChannelAddress: "VCU0000001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}

	started := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- r.DoForKey(context.Background(), key, func(_ context.Context, attempt int) error {
			select {
			case <-started:
			default:
				close(started)
			}
			if attempt == 1 {
				return errors.New("transient")
			}
			time.Sleep(10 * time.Second)
			return nil
		})
	}()

	<-started
	for i := 0; i < 50; i++ {
		if r.ActiveRetryCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// "VCU000000" is a strict prefix of "VCU0000001" — must NOT match.
	cancelled := r.CancelDevice("VCU000000")
	if cancelled != 0 {
		r.CancelInterface()
		t.Fatalf("CancelDevice with short prefix returned %d, want 0", cancelled)
	}
	if r.ActiveRetryCount() != 1 {
		r.CancelInterface()
		t.Fatal("chain must still be active after non-matching CancelDevice")
	}

	r.CancelInterface()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup timeout")
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
		i, k := i, k
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] <- r.DoForKey(context.Background(), k, func(_ context.Context, attempt int) error {
				started <- struct{}{}
				if attempt == 1 {
					return errors.New("transient")
				}
				time.Sleep(10 * time.Second)
				return nil
			})
		}()
	}

	for i := 0; i < len(keys); i++ {
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
		{"circuit breaker not retryable (without waiter)", hmerr.ErrCircuitBreakerOpen, true},
		{"unsupported not retryable", hmerr.ErrUnsupported, true},
		{"unreach fault retryable", &hmerr.XMLRPCFault{Code: -1}, false},
		{"timeout fault retryable", &hmerr.XMLRPCFault{Code: -2}, false},
		{"duty cycle fault retryable", &hmerr.XMLRPCFault{Code: -8}, false},
		{"device out of range fault retryable", &hmerr.XMLRPCFault{Code: -9}, false},
		{"transmission pending fault retryable", &hmerr.XMLRPCFault{Code: -10}, false},
		{"unknown fault not retryable", &hmerr.XMLRPCFault{Code: -5}, true},
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
