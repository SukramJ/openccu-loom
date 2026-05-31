// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCircuitDefaultHalfOpenSuccessIsTwo asserts the default
// HalfOpenSuccess matches. Two consecutive successes
// in HALF_OPEN are required to close the breaker — one stray
// success on a flapping backend doesn't yet prove stability.
func TestCircuitDefaultHalfOpenSuccessIsTwo(t *testing.T) {
	tick := time.Unix(0, 0)
	clock := func() time.Time { return tick }
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     1 * time.Second,
		Clock:            clock,
		// HalfOpenSuccess intentionally unset — pick up default.
	})

	ctx := context.Background()
	boom := errors.New("boom")

	// Trip the breaker.
	if err := c.Do(ctx, "setValue", func(_ context.Context) error { return boom }); err == nil {
		t.Fatal("expected boom error")
	}
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state after failure = %s, want OPEN", c.State())
	}

	// Advance clock past ResetTimeout → next Do flips to HALF_OPEN
	// and runs fn.
	tick = tick.Add(2 * time.Second)
	if err := c.Do(ctx, "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Do after reset window: %v", err)
	}
	// First success in HALF_OPEN — must NOT close yet (default = 2).
	if c.State() != hmenum.CircuitStateHalfOpen {
		t.Fatalf("after 1 success state = %s, want HALF_OPEN (default threshold is 2)", c.State())
	}

	// Second success closes.
	if err := c.Do(ctx, "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("second Do: %v", err)
	}
	if c.State() != hmenum.CircuitStateClosed {
		t.Fatalf("after 2 successes state = %s, want CLOSED", c.State())
	}
}
