// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package reqctx provides cross-cutting request context propagation.
//
// A [RequestContext] captures the identity and metadata of a single logical
// request as it flows from north-bound adapters (REST, WS) through the domain
// core to the south-bound CCU transports.
//
// - [RequestContext] dataclass - [NewContext] / [WithRequestContext]
// (request_context CM) - [FromContext] (get_request_context) -
// [RequestIDFromContext] (get_request_id) - [IsInService] (is_in_service)
//
// The Go equivalent does not use Python's ContextVar mechanism; it stores the
// [RequestContext] inside a standard context.Context value instead.
package reqctx

import (
	"context"
	"maps"
	"time"
)

// RequestContext carries the identity and metadata of a single logical
// operation (REST call, WS command, scheduled job).
type RequestContext struct {
	// RequestID is a unique identifier for this request, typically a UUID.
	RequestID string
	// Operation is a short human-readable name for the operation,
	// e.g. "put_paramset" or "ws_session_save".
	Operation string
	// DeviceAddress is optionally set when the request targets a
	// specific device or channel.
	DeviceAddress string
	// InterfaceID is optionally set when the request targets a specific CCU
	// interface (e.g. "HmIP-RF", "CUxD").
	InterfaceID string
	// CentralName is optionally set when the request is scoped to a
	// specific [internal/central.Unit]. Multi-CCU log
	// correlation uses this field to disambiguate which CCU produced
	// or received the work — without it, parallel call paths from two
	// CCUs interleave indistinguishably in slog output.
	CentralName string
	// TraceID is the W3C-format trace identifier (32 lowercase hex
	// characters). It survives across every hop of a single logical
	// operation — REST adapter → coordinator → client → transport →
	// CCU and back. Empty when the request has not been traced yet
	// (e.g. legacy code paths that have not adopted reqctx.NewTraceID).
	TraceID string
	// SpanID is the W3C-format span identifier (16 lowercase hex
	// characters) for the current unit of work. A child span carries a
	// fresh SpanID and copies the parent's SpanID into ParentSpanID;
	// see [StartChildSpan].
	SpanID string
	// ParentSpanID is the SpanID of the enclosing span, empty for the
	// root span of a trace.
	ParentSpanID string
	// Extra holds caller-supplied annotations. Prefer named fields;
	// use Extra only for ad-hoc enrichment in middleware.
	Extra map[string]any
	// StartedAt records when this request entered the system.
	StartedAt time.Time
}

// ElapsedMS returns the number of milliseconds elapsed since [StartedAt].
// Returns 0 when StartedAt is the zero value — without this guard the
// caller would see ~int64-max ms ("9.2e15") for every domain-internal
// call path that constructs a RequestContext without filling
// StartedAt (e.g. event-bus subscribers).
func (r RequestContext) ElapsedMS() float64 {
	if r.StartedAt.IsZero() {
		return 0
	}
	return float64(time.Since(r.StartedAt).Milliseconds())
}

// WithDevice returns a copy of r with DeviceAddress replaced.
func (r RequestContext) WithDevice(address string) RequestContext {
	r.DeviceAddress = address
	return r
}

// WithOperation returns a copy of r with Operation replaced.
func (r RequestContext) WithOperation(op string) RequestContext {
	r.Operation = op
	return r
}

// WithInterfaceID returns a copy of r with InterfaceID replaced.
func (r RequestContext) WithInterfaceID(ifaceID string) RequestContext {
	r.InterfaceID = ifaceID
	return r
}

// WithCentralName returns a copy of r with CentralName replaced.
// REST/WS adapters that resolve the target central from URL routing or
// session state should call this so that downstream slog records carry
// the central scope.
func (r RequestContext) WithCentralName(name string) RequestContext {
	r.CentralName = name
	return r
}

// WithTrace returns a copy of r with the W3C trace fields replaced.
// Use this when an incoming request carries a `traceparent` header that
// openccu-loom should adopt instead of generating a fresh trace.
func (r RequestContext) WithTrace(traceID, spanID, parentSpanID string) RequestContext {
	r.TraceID = traceID
	r.SpanID = spanID
	r.ParentSpanID = parentSpanID
	return r
}

// WithExtra returns a copy of r with extra key-value pairs merged into
// [Extra].
func (r RequestContext) WithExtra(extra map[string]any) RequestContext {
	merged := make(map[string]any, len(r.Extra)+len(extra))
	maps.Copy(merged, r.Extra)
	maps.Copy(merged, extra)
	r.Extra = merged
	return r
}

// --------------------------------------------------------------------------
// context.Context helpers
// --------------------------------------------------------------------------

type ctxKey struct{}

// WithRequestContext attaches rc to ctx and returns the enriched context.
// Downstream callers retrieve it via [FromContext].
func WithRequestContext(ctx context.Context, rc RequestContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, rc)
}

// FromContext retrieves the [RequestContext] stored in ctx. The second
// return value is false when no context is present.
func FromContext(ctx context.Context) (RequestContext, bool) {
	rc, ok := ctx.Value(ctxKey{}).(RequestContext)
	return rc, ok
}

// RequestIDFromContext is a convenience wrapper that returns the
// [RequestContext.RequestID] from ctx, or "" when absent.
func RequestIDFromContext(ctx context.Context) string {
	if rc, ok := FromContext(ctx); ok {
		return rc.RequestID
	}
	return ""
}

// IsInService reports whether ctx carries an active [RequestContext],
// indicating that the call is executing within a tracked service
// boundary.
func IsInService(ctx context.Context) bool {
	_, ok := FromContext(ctx)
	return ok
}

// SetRequestContextForTesting replaces (or adds) a [RequestContext] in
// ctx and returns the modified context. This is a thin alias for
// [WithRequestContext] intended to make test code self-documenting.
func SetRequestContextForTesting(ctx context.Context, rc RequestContext) context.Context {
	return WithRequestContext(ctx, rc)
}

// ResetRequestContextForTesting returns ctx with any stored
// [RequestContext] removed.
func ResetRequestContextForTesting(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, nil)
}
