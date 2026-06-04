// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestRetryWithRecoveryWaiterShortcuts verifies that
// [Retrier.Do] returns to the next attempt as soon as the supplied
// CircuitBreaker transitions out of OPEN, even when the configured
// backoff delay would still be running.
func TestRetryWithRecoveryWaiterShortcuts(t *testing.T) {
	cb := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour, // we will trip+reset manually
		HalfOpenSuccess:  1,
	})
	// Trip the breaker so subsequent calls reject immediately.
	cb.Reset()
	for range 3 {
		_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error {
			return errors.New("forced failure")
		})
	}
	if got := cb.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("breaker should be OPEN, got %s", got)
	}

	r := NewRetrier(RetryConfig{
		MaxAttempts:    2,
		Initial:        500 * time.Millisecond,
		Max:            5 * time.Second,
		Multiplier:     2,
		Jitter:         -1, // explicit "no jitter" for deterministic timing
		RecoveryWaiter: NewCircuitRecoveryWaiter(cb),
	})

	// In a second goroutine, flip the breaker back to closed so the
	// waiter wakes up.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cb.Reset()
	}()

	var attempts atomic.Int32
	start := time.Now()
	err := r.Do(context.Background(), func(_ context.Context, attempt int) error {
		attempts.Add(1)
		if attempt == 1 {
			return hmerr.ErrCircuitBreakerOpen
		}
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
	// Without the recovery shortcut the retrier would have slept a
	// full 500 ms between attempts. With the shortcut it should
	// resume well under that — give it generous slack so the test
	// stays stable on busy CI nodes.
	if elapsed > 300*time.Millisecond {
		t.Errorf("recovery shortcut should resume early; elapsed=%v", elapsed)
	}
}
