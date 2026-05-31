// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reqctx

import (
	"context"
	"log/slog"
)

// ContextHandler wraps a [slog.Handler] and injects [RequestContext] fields
// into every log record.
//
// When a [RequestContext] is present in the record's context the following
// attributes are appended: - "request_id" (string) - "operation"  (string) -
// "elapsed_ms" (float64)
type ContextHandler struct {
	inner slog.Handler
}

// NewContextHandler wraps inner with [RequestContext]-aware enrichment.
// inner must not be nil.
func NewContextHandler(inner slog.Handler) *ContextHandler {
	if inner == nil {
		panic("reqctx.NewContextHandler: inner handler must not be nil")
	}
	return &ContextHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects RequestContext fields into record, then delegates to
// the inner handler. Fields are only appended when a RequestContext is
// stored in ctx. Trace fields (trace_id, span_id, parent_span_id) are
// emitted when populated so that legacy callers without trace
// instrumentation continue to produce the same slog shape as before.
func (h *ContextHandler) Handle(ctx context.Context, record slog.Record) error {
	if rc, ok := FromContext(ctx); ok {
		record.AddAttrs(
			slog.String("request_id", rc.RequestID),
			slog.String("operation", rc.Operation),
		)
		// Only emit elapsed_ms when StartedAt is populated. Internal
		// call paths that construct a RequestContext on the fly leave
		// it zero; suppressing a 0-valued elapsed here avoids a
		// duplicate same-name key alongside the StartOp closer's
		// elapsed_ms.
		if !rc.StartedAt.IsZero() {
			record.AddAttrs(slog.Float64("elapsed_ms", rc.ElapsedMS()))
		}
		if rc.TraceID != "" {
			record.AddAttrs(slog.String("trace_id", rc.TraceID))
		}
		if rc.SpanID != "" {
			record.AddAttrs(slog.String("span_id", rc.SpanID))
		}
		if rc.ParentSpanID != "" {
			record.AddAttrs(slog.String("parent_span_id", rc.ParentSpanID))
		}
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs delegates to the inner handler and wraps the result.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup delegates to the inner handler and wraps the result.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{inner: h.inner.WithGroup(name)}
}
