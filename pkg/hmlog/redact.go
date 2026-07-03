// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"context"
	"log/slog"
	"strings"
)

// RedactMask is the literal substituted for the value of any field
// whose key matches the sensitive-field allowlist. The fixed token
// makes redacted fields obvious in log output and keeps log size
// constant regardless of the original value length.
const RedactMask = "***REDACTED***"

// defaultSensitiveKeys names the slog-attribute keys whose values are
// always masked before reaching the underlying handler. The list is
// case-insensitive and matches both exact keys ("password") and key
// suffixes / substrings that contain the token (e.g. "db_password",
// "client_secret", "oidc_client_secret", "x-api-key").
//
// Keep this list small and additive. Anything matched here MUST be a
// genuine secret — broadening the list to incidental data hides
// useful debug context.
var defaultSensitiveKeys = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"api-key",
	"apikey",
	"authorization",
	"auth_header",
	"cookie",
	"set_cookie",
	"session_id",
	"sessionid",
	"client_secret",
	"refresh_token",
	"access_token",
	"id_token",
	"bearer",
	"private_key",
}

// RedactingHandler wraps a [slog.Handler] and masks the value of any
// attribute whose key matches one of the configured sensitive
// patterns. Matching is case-insensitive and substring-based so that
// nested keys like `oidc.client_secret` or HTTP headers like
// `X-Api-Key` are caught without per-call configuration.
//
// Redaction is shallow on the attribute graph: top-level attribute
// values are replaced wholesale with [RedactMask]; nested
// [slog.GroupValue] attributes are recursed into so that a record like
//
//	logger.Info("oidc", slog.Group("auth", slog.String("client_secret", "...")))
//
// still masks the inner secret. Map / struct values that reach the
// handler as [slog.AnyValue] are NOT introspected — callers must use
// slog.Group or slog.Attr to expose individual fields.
//
// Hard rule: never log a secret-bearing struct via slog.Any(...). Because
// redaction is key-based and shallow, slog.Any("cfg", secretStruct) reaches
// the underlying handler verbatim and leaks every secret field the struct
// carries. Pass the individual, named fields through slog.Group / slog.Attr
// instead, so each sensitive key is matched and masked. A contract test
// (tests/contract) guards production call sites against this pattern.
type RedactingHandler struct {
	inner    slog.Handler
	patterns []string // lowercase substrings to match against attribute keys
}

// NewRedactingHandler wraps inner with the built-in sensitive-key
// allowlist. Use [NewRedactingHandlerWithKeys] to supply a custom set
// of patterns (or append to the defaults).
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return NewRedactingHandlerWithKeys(inner, defaultSensitiveKeys)
}

// NewRedactingHandlerWithKeys wraps inner with an explicit list of
// case-insensitive substring patterns. Passing nil or an empty slice
// disables redaction — the wrapper then becomes a thin pass-through.
func NewRedactingHandlerWithKeys(inner slog.Handler, patterns []string) *RedactingHandler {
	if inner == nil {
		panic("hmlog: NewRedactingHandlerWithKeys: inner handler must not be nil")
	}
	lc := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		lc = append(lc, strings.ToLower(p))
	}
	return &RedactingHandler{inner: inner, patterns: lc}
}

// DefaultSensitiveKeys returns a copy of the built-in pattern list.
// Callers that want to extend rather than replace the defaults can use
// this to seed a custom list.
//
// loom:reachable:reason="used by config-UI and REST handlers that need to extend the redaction list"
func DefaultSensitiveKeys() []string {
	out := make([]string, len(defaultSensitiveKeys))
	copy(out, defaultSensitiveKeys)
	return out
}

// IsSensitiveKey reports whether key matches one of the built-in
// sensitive-key patterns ([DefaultSensitiveKeys]). Matching is
// case-insensitive and substring-based, identical to the predicate the
// [RedactingHandler] applies. Callers that serialise their own attribute
// graphs outside the slog pipeline (e.g. the OTLP span exporter) use this
// to apply the same redaction rule at their wire boundary.
func IsSensitiveKey(key string) bool {
	if key == "" {
		return false
	}
	lc := strings.ToLower(key)
	for _, p := range defaultSensitiveKeys {
		if strings.Contains(lc, p) {
			return true
		}
	}
	return false
}

// Enabled delegates to the inner handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle walks the record's attributes, masks sensitive values, and
// forwards the rewritten record to the inner handler. The original
// record is left untouched so concurrent handlers (e.g. a capture
// sink) see the same input.
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	if len(h.patterns) == 0 {
		return h.inner.Handle(ctx, r)
	}
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

// WithAttrs applies redaction to the pre-bound attributes too. The
// returned handler reuses the same pattern list, so a child logger
// produced via slog.Logger.With(...) is also protected.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(h.patterns) == 0 {
		return &RedactingHandler{inner: h.inner.WithAttrs(attrs), patterns: h.patterns}
	}
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redactAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(redacted), patterns: h.patterns}
}

// WithGroup delegates to the inner handler. Group names themselves are
// not sensitive; the redaction logic operates on attribute keys.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name), patterns: h.patterns}
}

// IsSensitive reports whether key matches any configured pattern.
// Exported so that callers building their own slog.Attr graphs can
// pre-redact before invoking a sibling sink that bypasses this
// handler.
func (h *RedactingHandler) IsSensitive(key string) bool {
	if key == "" {
		return false
	}
	lc := strings.ToLower(key)
	for _, p := range h.patterns {
		if strings.Contains(lc, p) {
			return true
		}
	}
	return false
}

func (h *RedactingHandler) redactAttr(a slog.Attr) slog.Attr {
	if h.IsSensitive(a.Key) {
		return slog.String(a.Key, RedactMask)
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]any, 0, len(group)*2)
		for _, g := range group {
			redacted := h.redactAttr(g)
			out = append(out, redacted.Key, redacted.Value)
		}
		return slog.Group(a.Key, out...)
	}
	return a
}

// Compile-time assertion.
var _ slog.Handler = (*RedactingHandler)(nil)
