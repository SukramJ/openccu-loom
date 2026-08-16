// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// recordedOutcome is one hook invocation.
type recordedOutcome struct {
	method   string
	outcome  RPCOutcome
	duration time.Duration
}

type outcomeCollector struct {
	mu   sync.Mutex
	seen []recordedOutcome
}

func (c *outcomeCollector) hook(method string, duration time.Duration, outcome RPCOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, recordedOutcome{method: method, outcome: outcome, duration: duration})
}

func (c *outcomeCollector) snapshot() []recordedOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedOutcome(nil), c.seen...)
}

// TestCallReportsEveryRPCOutcome pins that each of the three ways a call
// can end is reported once, and told apart.
//
// A rejection and a failure are different facts: a failure is evidence
// about the CCU, a rejection is only this daemon refusing to try. Rolling
// them together makes an open breaker look like a broken link, which is
// what the RPC metrics section exists to distinguish.
func TestCallReportsEveryRPCOutcome(t *testing.T) {
	t.Parallel()

	wireErr := errors.New("ccu unreachable")
	cases := []struct {
		name    string
		callErr error
		open    bool
		want    RPCOutcome
	}{
		{name: "success", want: RPCOutcomeSuccess},
		{name: "failure", callErr: wireErr, want: RPCOutcomeFailed},
		{name: "rejected by an open breaker", open: true, want: RPCOutcomeRejected},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			col := &outcomeCollector{}
			breaker := reliability.NewCircuit(reliability.CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
			c, err := New(Config{
				CentralName: "ccu1",
				Interface:   hmenum.InterfaceHmIPRF,
				Enabled:     true,
				Circuit:     breaker,
				// A single fast attempt: this test is about the reported
				// outcome, not about the retry ladder above it.
				Retrier: reliability.NewRetrier(reliability.RetryConfig{MaxAttempts: 1}),
				Caller: CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
					return "ok", tc.callErr
				}),
				RPCOutcomeHook: col.hook,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if tc.open {
				// Trip the breaker so the next call is shed before the wire.
				breaker.RecordFailure()
			}

			_, callErr := c.Call(t.Context(), "getValue", nil, hmenum.CommandPriorityLow, "")

			seen := col.snapshot()
			if len(seen) != 1 {
				t.Fatalf("expected exactly one reported outcome, got %d (%+v)", len(seen), seen)
			}
			if seen[0].method != "getValue" {
				t.Errorf("method = %q, want %q", seen[0].method, "getValue")
			}
			if seen[0].outcome != tc.want {
				t.Errorf("outcome = %v, want %v (call returned %v)", seen[0].outcome, tc.want, callErr)
			}
			if tc.open && !errors.Is(callErr, hmerr.ErrCircuitBreakerOpen) {
				t.Fatalf("setup broke: expected an open-breaker rejection, got %v", callErr)
			}
		})
	}
}

// A client without the hook must behave exactly as before — the metrics
// wiring is optional, and every test and tool that builds a client
// without it goes through this path.
func TestCallWithoutOutcomeHookStillWorks(t *testing.T) {
	t.Parallel()

	c, err := New(Config{
		CentralName: "ccu1",
		Interface:   hmenum.InterfaceHmIPRF,
		Enabled:     true,
		Caller: CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return "ok", nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Call(t.Context(), "getValue", nil, hmenum.CommandPriorityLow, ""); err != nil {
		t.Fatalf("Call: %v", err)
	}
}
