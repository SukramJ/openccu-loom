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

// This file pins the siren-safety invariants of docs/alarm-concept.md
// §2 at the contract level. It grows with the output-driver layer;
// every S-invariant behaviour lands here with the code that carries it.

// TestAlarmS5CriticalCommandProbesOpenCircuit pins the S5 exception in
// the reliability layer: a CommandPriorityCritical call (the alarm
// engine's stop/silence path) is attempted as a single probe even
// while the interface circuit breaker is OPEN, while non-critical
// traffic keeps being shed. If this carve-out disappears, a siren
// stop issued during a wire outage is rejected unsent — the exact
// failure S5 exists to prevent.
func TestAlarmS5CriticalCommandProbesOpenCircuit(t *testing.T) {
	t.Parallel()

	c := reliability.NewCircuit(reliability.CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
	c.RecordFailure()
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state = %v, want OPEN", c.State())
	}

	attempted := 0
	if err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { attempted++; return nil }); err != nil {
		t.Fatalf("critical stop while OPEN: err = %v, want attempted probe", err)
	}
	if attempted != 1 {
		t.Fatalf("critical stop attempted %d times, want exactly 1", attempted)
	}

	if err := c.DoWithPriority(context.Background(), "setValue", hmenum.CommandPriorityHigh,
		func(context.Context) error { t.Fatal("non-critical must be shed"); return nil }); !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("non-critical while OPEN: err = %v, want ErrCircuitBreakerOpen", err)
	}
}
