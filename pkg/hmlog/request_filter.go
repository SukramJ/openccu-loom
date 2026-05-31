// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// RequestContextFilter is a [slog.Handler] wrapper that automatically
// enriches every log record with the request_id, operation, and elapsed_ms
// fields stored in the [context.Context] by [reqctx].
//
// When no [reqctx.RequestContext] is present in the context the record is
// forwarded unchanged; the filter never drops records.
//
// Python's filter attaches these fields in the logging framework's filter
// chain; here we implement a [slog.Handler] wrapper that performs the same
// enrichment at the handler level.
//
// Usage:
//
// logger := slog.New(NewRequestContextFilter(slog.Default().Handler()))
type RequestContextFilter struct {
	inner slog.Handler
}

// NewRequestContextFilter wraps inner in a [RequestContextFilter].
// Passing a nil handler panics — the caller is responsible for
// providing a real handler.
//
// loom:reachable:reason="used by BuildFullStack to enrich log records with per-request fields"
func NewRequestContextFilter(inner slog.Handler) *RequestContextFilter {
	if inner == nil {
		panic("hmlog: NewRequestContextFilter: inner handler must not be nil")
	}
	return &RequestContextFilter{inner: inner}
}

// Enabled delegates to the inner handler.
func (f *RequestContextFilter) Enabled(ctx context.Context, level slog.Level) bool {
	return f.inner.Enabled(ctx, level)
}

// Handle enriches the record with request_id, operation, elapsed_ms,
// central_name, interface_id, and the W3C trace_id / span_id /
// parent_span_id from the [reqctx.RequestContext] stored in ctx, then
// forwards it to the inner handler. Empty optional fields (central_name,
// interface_id, device_address, trace_id, span_id, parent_span_id)
// are omitted.
func (f *RequestContextFilter) Handle(ctx context.Context, r slog.Record) error {
	if rc, ok := reqctx.FromContext(ctx); ok {
		r.AddAttrs(
			slog.String("request_id", rc.RequestID),
			slog.String("operation", rc.Operation),
		)
		// Only emit elapsed_ms when StartedAt is populated. Domain-
		// internal call paths that construct a RequestContext on the
		// fly (event-bus subscribers, scheduler jobs) leave it zero;
		// duplicating an elapsed=0 here on top of the StartOp closer's
		// elapsed_ms would yield two same-name JSON keys and confuse
		// log readers.
		if !rc.StartedAt.IsZero() {
			r.AddAttrs(slog.Float64("elapsed_ms", rc.ElapsedMS()))
		}
		if rc.CentralName != "" {
			r.AddAttrs(slog.String("central_name", rc.CentralName))
		}
		if rc.InterfaceID != "" {
			r.AddAttrs(slog.String("interface_id", rc.InterfaceID))
		}
		if rc.DeviceAddress != "" {
			r.AddAttrs(slog.String("device_address", rc.DeviceAddress))
		}
		if rc.TraceID != "" {
			r.AddAttrs(slog.String("trace_id", rc.TraceID))
		}
		if rc.SpanID != "" {
			r.AddAttrs(slog.String("span_id", rc.SpanID))
		}
		if rc.ParentSpanID != "" {
			r.AddAttrs(slog.String("parent_span_id", rc.ParentSpanID))
		}
	}
	return f.inner.Handle(ctx, r)
}

// WithAttrs returns a new handler with additional attributes, delegating
// to the inner handler.
func (f *RequestContextFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RequestContextFilter{inner: f.inner.WithAttrs(attrs)}
}

// WithGroup returns a new handler with a group prefix, delegating to
// the inner handler.
func (f *RequestContextFilter) WithGroup(name string) slog.Handler {
	return &RequestContextFilter{inner: f.inner.WithGroup(name)}
}

// Compile-time assertion: RequestContextFilter implements slog.Handler.
var _ slog.Handler = (*RequestContextFilter)(nil)
