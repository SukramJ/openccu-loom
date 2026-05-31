// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmerr

import (
	"context"
	"errors"
	"log/slog"
)

// redactedKeys is the set of context-map keys whose values are masked
// with "***" before logging to prevent credential leakage.
var redactedKeys = map[string]struct{}{
	"password":      {},
	"passwd":        {},
	"pwd":           {},
	"token":         {},
	"authorization": {},
	"auth":          {},
}

// safeLogContext returns a copy of context with sensitive keys redacted.
// If context is nil, a non-nil empty map is returned.
func safeLogContext(logCtx map[string]any) map[string]any {
	out := make(map[string]any, len(logCtx))
	for k, v := range logCtx {
		if _, redact := redactedKeys[k]; redact {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}

// BoundaryLevel is the log level override for [LogBoundaryError].
// When zero the level is chosen automatically: [slog.LevelWarn] for
// domain errors (those that wrap a known sentinel), [slog.LevelError]
// for all others.
type BoundaryLevel = slog.Level

// BoundaryLevelAuto is the zero-value sentinel that causes
// [LogBoundaryError] to pick the level automatically.
const BoundaryLevelAuto BoundaryLevel = slog.LevelDebug - 1 // sentinel below Debug

// LogBoundaryError logs a structured error at the appropriate level.
// It differentiates between expected/recoverable domain errors (those
// that unwrap to a known sentinel in this package) and unexpected failures.
//
// loom:reachable:reason="called by transport and coordinator error-handling paths"
//
// Parameters:
// - logger: destination logger; uses slog.Default() when nil
// - boundary: machine label for the service boundary (e.g. "rpc.init")
// - action: human label for the operation (e.g. "setDeviceValue")
// - err: the error to log
// - level: override level; pass [BoundaryLevelAuto] for automatic selection
// - ctx: optional key-value annotations; sensitive keys are redacted
// - message: optional free-form annotation appended after the bracket
func LogBoundaryError(
	logger *slog.Logger,
	boundary, action string,
	err error,
	level BoundaryLevel,
	ctx map[string]any,
	message string,
) {
	if err == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Choose level.
	chosenLevel := level
	if chosenLevel == BoundaryLevelAuto {
		if isDomainError(err) {
			chosenLevel = slog.LevelWarn
		} else {
			chosenLevel = slog.LevelError
		}
	}

	// Build base attrs.
	attrs := []any{
		slog.String("boundary", boundary),
		slog.String("action", action),
		slog.String("err_type", errorTypeName(err)),
		slog.String("err", err.Error()),
	}
	if message != "" {
		attrs = append(attrs, slog.String("message", message))
	}
	if len(ctx) > 0 {
		safe := safeLogContext(ctx)
		for k, v := range safe {
			attrs = append(attrs, slog.Any(k, v))
		}
	}

	// boundary logger must not be cancelled by the request context;
	// context.TODO() carries no cancellation but satisfies SA1012.
	logger.Log(context.TODO(), chosenLevel, "error_boundary", attrs...) //nolint:sloglint // intentional decoupling from request ctx
}

// isDomainError reports whether err wraps one of the domain sentinels
// defined in this package (recoverable, expected errors).
func isDomainError(err error) bool {
	return errors.Is(err, ErrAuthFailure) ||
		errors.Is(err, ErrNoConnection) ||
		errors.Is(err, ErrCircuitBreakerOpen) ||
		errors.Is(err, ErrClientException) ||
		errors.Is(err, ErrInternalBackendException) ||
		errors.Is(err, ErrUnsupported) ||
		errors.Is(err, ErrDescriptionNotFound) ||
		errors.Is(err, ErrParameterHidden)
}

// errorTypeName returns the last segment of err's type name, matching
// Python's err.__class__.__name__ pattern used in log_boundary_error.
func errorTypeName(err error) string {
	if err == nil {
		return ""
	}
	// Use the error message itself as the type label for sentinel errors,
	// since Go sentinel errors have no dedicated type name.
	return err.Error()
}
