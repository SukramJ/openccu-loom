// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hmlog provides a contextual logger wrapper that enriches
// [log/slog] output with request-scoped fields.
//
// A [ContextualLogger] decorates a base *[slog.Logger] with a fixed
// set of attributes that are prepended to every log record, so that
// log lines emitted deep in a call-stack automatically carry the
// central name, interface, session ID, or any other call-site tag
// without requiring callers to thread extra arguments.
//
// # Skipped items
//
// (full RequestContext cross-cutting with middleware
// injection and HTTP context propagation) are explicitly skipped
// here. That work requires the REST handler layer to be complete;
// the types below provide the logging primitive that the future
// middleware will wrap. When are implemented they
// should embed or delegate to [ContextualLogger].
package hmlog

import (
	"context"
	"log/slog"
	"maps"
)

// Fields is a free-form set of key-value pairs attached to every log
// record emitted through a [ContextualLogger].
type Fields map[string]any

// ContextualLogger wraps a *[slog.Logger] and prepends a fixed set of
// [Fields] to every record it emits. The wrapper is immutable — use
// [With] to derive a child with additional fields.
type ContextualLogger struct {
	inner *slog.Logger
}

// New wraps base with the supplied fields. Passing a nil base uses
// [slog.Default]. Passing empty fields returns a thin wrapper over the
// base logger with no added attributes.
func New(base *slog.Logger, fields Fields) *ContextualLogger {
	if base == nil {
		base = slog.Default()
	}
	if len(fields) == 0 {
		return &ContextualLogger{inner: base}
	}
	attrs := fieldsToAttrs(fields)
	return &ContextualLogger{inner: base.With(attrs...)}
}

// Get returns a [ContextualLogger] that combines base with the request
// fields stored under [contextKey] in ctx. If ctx carries no fields,
// the result wraps base directly.
func Get(ctx context.Context, base *slog.Logger) *ContextualLogger {
	if base == nil {
		base = slog.Default()
	}
	f, _ := ctx.Value(contextKey{}).(Fields)
	if len(f) == 0 {
		return &ContextualLogger{inner: base}
	}
	return New(base, f)
}

// WithContext returns a new context that carries the given fields.
// Subsequent calls to [Get] with that context produce a logger
// pre-loaded with those fields.
func WithContext(ctx context.Context, fields Fields) context.Context {
	if len(fields) == 0 {
		return ctx
	}
	existing, _ := ctx.Value(contextKey{}).(Fields)
	merged := make(Fields, len(existing)+len(fields))
	maps.Copy(merged, existing)
	maps.Copy(merged, fields)
	return context.WithValue(ctx, contextKey{}, merged)
}

// With returns a derived [ContextualLogger] with additional fields
// merged on top of the existing ones. The receiver is not modified.
func (l *ContextualLogger) With(fields Fields) *ContextualLogger {
	if len(fields) == 0 {
		return l
	}
	attrs := fieldsToAttrs(fields)
	return &ContextualLogger{inner: l.inner.With(attrs...)}
}

// Logger returns the underlying *[slog.Logger]. Use this when an API
// requires a *slog.Logger directly.
func (l *ContextualLogger) Logger() *slog.Logger {
	return l.inner
}

// Debug logs at [slog.LevelDebug].
func (l *ContextualLogger) Debug(msg string, args ...any) {
	l.inner.Debug(msg, args...)
}

// Info logs at [slog.LevelInfo].
func (l *ContextualLogger) Info(msg string, args ...any) {
	l.inner.Info(msg, args...)
}

// Warn logs at [slog.LevelWarn].
func (l *ContextualLogger) Warn(msg string, args ...any) {
	l.inner.Warn(msg, args...)
}

// Error logs at [slog.LevelError].
func (l *ContextualLogger) Error(msg string, args ...any) {
	l.inner.Error(msg, args...)
}

// DebugContext logs at [slog.LevelDebug] with context.
func (l *ContextualLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.inner.DebugContext(ctx, msg, args...)
}

// InfoContext logs at [slog.LevelInfo] with context.
func (l *ContextualLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.inner.InfoContext(ctx, msg, args...)
}

// WarnContext logs at [slog.LevelWarn] with context.
func (l *ContextualLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.inner.WarnContext(ctx, msg, args...)
}

// ErrorContext logs at [slog.LevelError] with context.
func (l *ContextualLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.inner.ErrorContext(ctx, msg, args...)
}

// --------------------------------------------------------------------------
// internal helpers
// --------------------------------------------------------------------------

// contextKey is the unexported key type used for storing [Fields] in a
// context. Using a private type prevents collisions with other packages.
type contextKey struct{}

func fieldsToAttrs(fields Fields) []any {
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	return attrs
}
