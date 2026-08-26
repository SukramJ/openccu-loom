// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newOpenCircuitForProbe returns a breaker tripped OPEN that stays open for
// the duration of the test.
func newOpenCircuitForProbe(t *testing.T) *CircuitBreaker {
	t.Helper()
	c := NewCircuit(CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
	c.RecordFailure()
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("state = %v, want OPEN", c.State())
	}
	return c
}

func TestDoWithPriority_CriticalProbesOpenCircuitOnce(t *testing.T) {
	t.Parallel()
	c := newOpenCircuitForProbe(t)

	calls := 0
	err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { calls++; return nil })
	if err != nil {
		t.Fatalf("critical probe error = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("fn invoked %d times, want 1", calls)
	}
	// A successful probe must NOT close the circuit — recovery stays
	// with the connection checker.
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("state after successful probe = %v, want OPEN", got)
	}
}

func TestDoWithPriority_NonCriticalStaysRejectedWhileOpen(t *testing.T) {
	t.Parallel()
	c := newOpenCircuitForProbe(t)

	for _, p := range []hmenum.CommandPriority{hmenum.CommandPriorityHigh, hmenum.CommandPriorityLow} {
		err := c.DoWithPriority(context.Background(), "setValue", p,
			func(context.Context) error { t.Fatal("must not execute"); return nil })
		if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
			t.Fatalf("priority %d: err = %v, want ErrCircuitBreakerOpen", p, err)
		}
	}
}

func TestDoWithPriority_SingleConcurrentCriticalProbe(t *testing.T) {
	t.Parallel()
	c := newOpenCircuitForProbe(t)

	inProbe := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
			func(context.Context) error {
				close(inProbe)
				<-release
				return nil
			})
	}()
	<-inProbe

	// While the first probe is in flight, a second critical caller is
	// rejected — exactly one probe at a time.
	err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { t.Error("second probe must not execute"); return nil })
	if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("concurrent critical err = %v, want ErrCircuitBreakerOpen", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first probe err = %v", err)
	}

	// After the probe settles the slot is free again.
	if err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("follow-up probe err = %v, want nil", err)
	}
}

func TestDoWithPriority_FailedProbeKeepsCircuitOpen(t *testing.T) {
	t.Parallel()
	c := newOpenCircuitForProbe(t)

	boom := errors.New("wire dead")
	err := c.DoWithPriority(context.Background(), "putParamset", hmenum.CommandPriorityCritical,
		func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the probe error", err)
	}
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("state = %v, want OPEN", got)
	}
}
