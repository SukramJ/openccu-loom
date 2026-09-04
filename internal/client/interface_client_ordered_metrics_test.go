// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// interface_client_ordered_metrics_test.go — pins the meaning of the
// ExecutedRequests counter on the CallOrdered path: one increment per
// logical call, not one per wire attempt, so the executed/total ratio the
// metrics aggregator publishes stays a coalescing ratio.

package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCallOrderedCountsOneExecutionPerLogicalCall drives one CallOrdered
// through a transport that fails twice before succeeding, with a retrier
// permitting three attempts. Three wire attempts happen; ExecutedRequests
// must still read 1.
func TestCallOrderedCountsOneExecutionPerLogicalCall(t *testing.T) {
	t.Parallel()

	attempts := 0
	oc := client.OrderedCallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("transport: temporary")
		}
		return "ok", nil
	})
	nop := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, nil
	})

	ic, err := client.New(client.Config{
		CentralName:   "test",
		Interface:     hmenum.InterfaceHmIPRF,
		Caller:        nop,
		OrderedCaller: oc,
		Retrier: reliability.NewRetrier(reliability.RetryConfig{
			MaxAttempts: 3,
			Initial:     time.Millisecond,
			Max:         time.Millisecond,
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer ic.Close()

	if _, err := ic.CallOrdered(
		context.Background(), "listDevices", nil, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("CallOrdered: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("transport attempts = %d, want 3 (the retrier must actually retry)", attempts)
	}

	if got := ic.MetricsExecutedRequests(); got != 1 {
		t.Errorf("MetricsExecutedRequests() = %d after one CallOrdered with 3 wire attempts, want 1", got)
	}
	if executed, total := ic.MetricsExecutedRequests(), ic.MetricsTotalRequests(); executed > total {
		t.Errorf("executed=%d exceeds total=%d; executed must never outrun the logical call count", executed, total)
	}
}
