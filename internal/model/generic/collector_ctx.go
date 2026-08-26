// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import "context"

// collectorCtxKey is the unexported key used to store a
// [CallParameterCollector] in a context.
type collectorCtxKey struct{}

// ContextWithCollector attaches c to parent. [Channel.Set] and
// [Channel.SetMany] inspect the context via [CollectorFromContext];
// when present they Add to the collector instead of dispatching
// directly. This lets the caller group a logical "set" across several
// parameters (or channels) into one CCU put_paramset round-trip.
//
// Nesting is allowed: wrapping a ctx that already carries a collector
// Replaces the outer one — the deepest-wins rule matches
// bind_collector behaviour.
func ContextWithCollector(parent context.Context, c *CallParameterCollector) context.Context {
	return context.WithValue(parent, collectorCtxKey{}, c)
}

// CollectorFromContext returns the [CallParameterCollector] stored in
// ctx by [ContextWithCollector], or nil when none is present.
func CollectorFromContext(ctx context.Context) *CallParameterCollector {
	v, _ := ctx.Value(collectorCtxKey{}).(*CallParameterCollector)
	return v
}
