// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CommandHandler implements one RPC-style WebSocket command. The returned
// `data` is JSON-encoded into the response envelope under the "data" field;
// the returned error becomes a `{code, message}` payload in the "error"
// field.
type CommandHandler func(ctx context.Context, args json.RawMessage) (any, error)

// CommandError is the wire shape of a command failure. Handlers may
// return any error; the router wraps non-CommandError errors into a
// generic `internal_error` code so untyped failures still produce a
// well-formed envelope.
type CommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements error.
func (e *CommandError) Error() string { return e.Code + ": " + e.Message }

// NewCommandError constructs a typed command error.
func NewCommandError(code, message string) *CommandError {
	return &CommandError{Code: code, Message: message}
}

// Common error codes a handler may use without inventing its own.
const (
	CommandErrorUnknownCommand = "unknown_command"
	CommandErrorBadRequest     = "bad_request"
	CommandErrorInternal       = "internal_error"
	CommandErrorUnauthorized   = "unauthorized"
	// CommandErrorForbidden is returned when a write is rejected by a
	// policy gate, e.g. the visibility gate for hidden parameters.
	CommandErrorForbidden = "forbidden"
	// CommandErrorNotImplemented is returned by registered stubs that
	// know their domain wiring has not yet landed. Distinct from
	// CommandErrorUnknownCommand (which means the router has no entry
	// for the name) and from CommandErrorInternal (which signals a
	// handler bug).
	CommandErrorNotImplemented = "not_implemented"
)

// Router routes inbound `call` frames to a registered handler by
// command name. Mirrors Home Assistant's `async_register_command`
// Pattern that
// and returns a JSON-serialisable result.
//
// The router applies the same cross-cutting boundary as the REST
// router (audit O13): every Dispatch enriches its context with a
// [reqctx.RequestContext], times the call, and (when a logger is
// wired via [SetBoundary]) emits one `ws.command` slog record per
// outcome with the same `request_id`/`operation`/`elapsed`/
// `central_name` shape as REST. That keeps log aggregation and
// metrics queries unified across the two transports.
//
// Safe for concurrent registration and dispatch.
type Router struct {
	mu       sync.RWMutex
	handlers map[string]CommandHandler

	// boundary fields — set via [SetBoundary]; nil values disable the
	// associated cross-cutting work. Read on the hot path under no
	// lock — the daemon installs them once at boot before any client
	// connects, so no synchronisation is needed.
	logger      *slog.Logger
	centralName string
}

// NewRouter returns an empty router with no boundary wiring (no
// logger, empty central name). Tests typically use this; production
// composes via [SetBoundary] after construction.
func NewRouter() *Router {
	return &Router{handlers: make(map[string]CommandHandler)}
}

// SetBoundary installs the cross-cutting boundary that every future
// [Dispatch] applies. Pass a non-nil logger to receive structured
// `ws.command` records keyed by the same fields the REST Logger
// middleware uses; pass the daemon's central name so log aggregation
// can filter by central. Both arguments are independent — passing
// `(nil, "ccu-01")` skips logging but still tags the request scope.
//
// Calling [SetBoundary] twice replaces the previous wiring. Must be
// called before clients connect; concurrent calls during dispatch
// have undefined ordering relative to in-flight calls.
func (r *Router) SetBoundary(logger *slog.Logger, centralName string) {
	if r == nil {
		return
	}
	r.logger = logger
	r.centralName = centralName
}

// Register binds fn to command. Subsequent registrations under the
// same name replace the previous handler — useful for tests.
func (r *Router) Register(command string, fn CommandHandler) {
	if r == nil || command == "" || fn == nil {
		return
	}
	r.mu.Lock()
	r.handlers[command] = fn
	r.mu.Unlock()
}

// Has reports whether a handler is registered under command.
func (r *Router) Has(command string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[command]
	return ok
}

// Commands returns the list of registered command names. Useful for
// diagnostics and the `system.commands` introspection endpoint.
func (r *Router) Commands() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		out = append(out, name)
	}
	return out
}

// Dispatch invokes the handler bound to command and packages the
// result into a [Result]. Unknown commands surface as a CommandError
// with code [CommandErrorUnknownCommand].
//
// Cross-cutting work (audit O13): the dispatch enriches ctx with a
// [reqctx.RequestContext] tagged `op="ws.command"`, captures the
// elapsed time, and emits one structured slog record per outcome
// when [SetBoundary] has wired a logger. The shape mirrors the REST
// Logger middleware: `command`, `status`, `elapsed`, `request_id`,
// `central_name`, plus `error_code` on failure.
func (r *Router) Dispatch(ctx context.Context, command string, args json.RawMessage) Result {
	start := time.Now()
	ctx = r.enrichContext(ctx, command, start)

	r.mu.RLock()
	fn, ok := r.handlers[command]
	r.mu.RUnlock()
	if !ok {
		res := Result{Error: NewCommandError(CommandErrorUnknownCommand, "no handler for "+command)}
		r.logOutcome(ctx, command, res, time.Since(start))
		return res
	}
	data, err := fn(ctx, args)
	if err != nil {
		if ce, ok := errors.AsType[*CommandError](err); ok {
			res := Result{Error: ce}
			r.logOutcome(ctx, command, res, time.Since(start))
			return res
		}
		// Map domain-level policy rejections to structured error codes so
		// callers can branch without string-matching.
		if errors.Is(err, hmerr.ErrParameterHidden) {
			res := Result{Error: NewCommandError(CommandErrorForbidden, err.Error())}
			r.logOutcome(ctx, command, res, time.Since(start))
			return res
		}
		res := Result{Error: NewCommandError(CommandErrorInternal, err.Error())}
		r.logOutcome(ctx, command, res, time.Since(start))
		return res
	}
	res := Result{Data: data}
	r.logOutcome(ctx, command, res, time.Since(start))
	return res
}

// enrichContext returns ctx with a [reqctx.RequestContext] installed
// under the WS-boundary tag. The request id is preserved from a
// caller-supplied RequestContext (when present, e.g. when the same
// connection was already tagged), otherwise a fresh one is minted.
// Empty boundary fields fall through unchanged.
func (r *Router) enrichContext(ctx context.Context, command string, start time.Time) context.Context {
	rid := reqctx.RequestIDFromContext(ctx)
	rc := reqctx.RequestContext{
		RequestID:   rid,
		Operation:   "ws.command:" + command,
		StartedAt:   start,
		CentralName: r.centralName,
	}
	return reqctx.WithRequestContext(ctx, rc)
}

// logOutcome emits one structured slog record describing the
// dispatch result. Idempotent and cheap when no logger is wired —
// the function is a no-op in tests.
func (r *Router) logOutcome(ctx context.Context, command string, res Result, elapsed time.Duration) {
	if r.logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("command", command),
		slog.Duration("elapsed", elapsed),
	}
	if rid := reqctx.RequestIDFromContext(ctx); rid != "" {
		attrs = append(attrs, slog.String("request_id", rid))
	}
	if r.centralName != "" {
		attrs = append(attrs, slog.String("central_name", r.centralName))
	}
	if res.Error != nil {
		attrs = append(
			attrs,
			slog.String("status", "error"),
			slog.String("error_code", res.Error.Code),
		)
		r.logger.LogAttrs(ctx, slog.LevelWarn, "ws.command", attrs...)
		return
	}
	attrs = append(attrs, slog.String("status", "ok"))
	// A successful command is debug-level noise (one line per WS call);
	// the error path above stays at WARN so failures remain visible.
	r.logger.LogAttrs(ctx, slog.LevelDebug, "ws.command", attrs...)
}

// Result is the in-process representation of a command outcome before
// it is serialised into a WebSocket response frame. Exactly one of
// Data and Error is populated.
type Result struct {
	Data  any
	Error *CommandError
}

// outboundResult is the wire shape sent back for `call` frames.
type outboundResult struct {
	Op    string        `json:"op"`
	ID    string        `json:"id"`
	Data  any           `json:"data,omitempty"`
	Error *CommandError `json:"error,omitempty"`
}
