// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Pin the reliability invariants flagged as load-bearing by the
// algorithmic-pitfalls review. Each test states one behaviour; the
// underlying constant is verified indirectly so the internal tuning
// fields stay private.

func TestCircuitBreakerNeedsTwoSuccessesToClose(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	cb := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Millisecond,
		Clock:            clock,
	})

	// One failure trips the breaker into OPEN.
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("boom") })
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("breaker did not open: %s", cb.State())
	}

	// After the reset timeout we transition to HALF_OPEN on the next
	// call. One success there must NOT yet close the breaker.
	now = now.Add(2 * time.Millisecond)
	if err := cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("first half-open success returned err=%v", err)
	}
	if got := cb.State(); got == hmenum.CircuitStateClosed {
		t.Fatalf("breaker closed after only ONE success, want HALF_OPEN; got %s", got)
	}

	// The second consecutive success closes it.
	if err := cb.Do(context.Background(), "setValue", func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("second success returned err=%v", err)
	}
	if got := cb.State(); got != hmenum.CircuitStateClosed {
		t.Fatalf("breaker did not close after two successes: %s", got)
	}
}

// TestXMLRPCFaultCodeValues pins the wire-level CCU fault codes the
// retry layer treats as retryable. Drift here breaks duty-cycle and
// transmission-pending handling silently.
func TestXMLRPCFaultCodeValues(t *testing.T) {
	t.Parallel()
	cases := map[hmerr.XMLRPCFaultCode]int{
		hmerr.XMLRPCFaultUnreach:             -1,
		hmerr.XMLRPCFaultTimeout:             -2,
		hmerr.XMLRPCFaultDutyCycle:           -8,
		hmerr.XMLRPCFaultDeviceOutOfRange:    -9,
		hmerr.XMLRPCFaultTransmissionPending: -10,
	}
	for code, want := range cases {
		if int(code) != want {
			t.Errorf("%v = %d, want %d", code, int(code), want)
		}
	}
}

// TestXMLRPCFaultCodeRetryability ensures the retry classifier marks
// the four operational codes (-1, -8, -9, -10) as retryable. Auth /
// permanent errors must NOT be in the retry set.
func TestXMLRPCFaultCodeRetryability(t *testing.T) {
	t.Parallel()
	retryable := []hmerr.XMLRPCFaultCode{
		hmerr.XMLRPCFaultUnreach,
		hmerr.XMLRPCFaultDutyCycle,
		hmerr.XMLRPCFaultDeviceOutOfRange,
		hmerr.XMLRPCFaultTransmissionPending,
	}
	for _, c := range retryable {
		if !c.IsRetryable() {
			t.Errorf("%v must be retryable", c)
		}
	}
}

func TestCircuitBreakerHalfOpenFailReopens(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	cb := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Millisecond,
		Clock:            clock,
	})
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("boom") })
	now = now.Add(2 * time.Millisecond)
	// HALF_OPEN reached on the next call; a failure there must
	// re-open the breaker, not close it.
	_ = cb.Do(context.Background(), "setValue", func(_ context.Context) error { return errors.New("still down") })
	if got := cb.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("breaker must re-open after HALF_OPEN failure: %s", got)
	}
}
