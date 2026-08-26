// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestClassifyRPCOutcomeAgreesWithCircuitBreaker pins that classifyRPCOutcome
// never books an error as a failure that the circuit breaker itself does not
// treat as evidence about the CCU link. A metric named after the breaker
// (circuit.failure.<interfaceID>) must not disagree with the breaker's own
// classification, or an operator watching that counter draws conclusions the
// breaker state contradicts.
func TestClassifyRPCOutcomeAgreesWithCircuitBreaker(t *testing.T) {
	t.Parallel()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	fault := &hmerr.XMLRPCFault{Code: int(hmerr.XMLRPCFaultInvalidParameter), Message: "Unknown Parameter"}

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want RPCOutcome
	}{
		{"nil error is success", context.Background(), nil, RPCOutcomeSuccess},
		{"open breaker is rejected", context.Background(), hmerr.ErrCircuitBreakerOpen, RPCOutcomeRejected},
		{"generic wire error is failed", context.Background(), errors.New("connection reset"), RPCOutcomeFailed},
		{"caller cancellation is ignored, not failed", cancelledCtx, context.Canceled, RPCOutcomeIgnored},
		{"permanent semantic fault is ignored, not failed", context.Background(), fault, RPCOutcomeIgnored},
		{"throttle queue full is ignored, not failed", context.Background(), reliability.ErrThrottleQueueFull, RPCOutcomeIgnored},
		{"throttle closed is ignored, not failed", context.Background(), reliability.ErrThrottleClosed, RPCOutcomeIgnored},
		{"superseded command is ignored, not failed", context.Background(), reliability.ErrSuperseded, RPCOutcomeIgnored},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyRPCOutcome(tc.ctx, tc.err)
			if got != tc.want {
				t.Errorf("classifyRPCOutcome(%v) = %v, want %v", tc.err, got, tc.want)
			}
			// Cross-check against the breaker's own classifier directly:
			// classifyRPCOutcome must return Failed if and only if
			// reliability.IsWireFailure says the error is wire evidence
			// (open-breaker rejection aside, which the breaker excludes for
			// a different reason — it is the breaker's own state, not an
			// unclassified error).
			if tc.err != nil && !errors.Is(tc.err, hmerr.ErrCircuitBreakerOpen) {
				wire := reliability.IsWireFailure(tc.ctx, tc.err)
				isFailed := got == RPCOutcomeFailed
				if wire != isFailed {
					t.Errorf("IsWireFailure=%v but classifyRPCOutcome=%v (mismatch) for %v", wire, got, tc.err)
				}
			}
		})
	}
}
