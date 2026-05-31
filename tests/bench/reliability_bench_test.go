// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build bench

package bench

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
)

// BenchmarkCircuitBreakerClosed runs a no-op call through the circuit while
// the breaker is closed.
func BenchmarkCircuitBreakerClosed(b *testing.B) {
	cb := reliability.NewCircuit(reliability.CircuitConfig{})
	ctx := context.Background()
	noop := func(context.Context) error { return nil }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.Do(ctx, "bench", noop)
	}
}

// BenchmarkCoalescerSingleKey measures the cost of a hot-path
// coalesce call when the dedup channel is empty — the common case
// for unique-key calls.
func BenchmarkCoalescerSingleKey(b *testing.B) {
	c := reliability.NewCoalescer()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Do(ctx, "k", func(context.Context) (any, error) { return nil, nil })
	}
}
