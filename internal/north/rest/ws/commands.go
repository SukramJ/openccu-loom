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

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// writeCommandRoles is the single source of truth for the minimum role a
// caller must hold to invoke a state-changing WebSocket command. Any command
// absent from this map is read-only and needs only an authenticated (viewer)
// identity — the same contract the REST router applies, where reads are open
// to any authenticated user and writes are wrapped in `.With(op)` / `.With(admin)`.
//
// Keeping the policy in one table — rather than tagging each of the ~90
// registration sites across the command files — makes the privilege boundary
// auditable and testable in a single place. TestWriteCommandRolesAreRegistered
// guards against typos by pinning every entry to a really-registered command,
// and the viewer-rejection tests assert the gate actually blocks writes.
var writeCommandRoles = map[string]auth.Role{
	// Admin-tier: backup + cache invalidation mirror the REST
	// `/backups` and `/admin/cache/clear` routes (both `.With(admin)`).
	// programs.delete mirrors DELETE /programs/{id} (admin-gated like
	// DELETE /devices — deletion is irreversible).
	"backup.trigger":        auth.RoleAdmin,
	"backups.trigger":       auth.RoleAdmin,
	"ccu.cache_clear":       auth.RoleAdmin,
	"device.replace":        auth.RoleAdmin,
	"device.restore_config": auth.RoleAdmin,
	"programs.delete":       auth.RoleAdmin,

	// Operator-tier: every real device / config / schedule / link mutation.
	"alarm_messages.ack":      auth.RoleOperator,
	"alarm_messages.ack_all":  auth.RoleOperator,
	"alarm_panel.acknowledge": auth.RoleOperator,
	"alarm_panel.arm":         auth.RoleOperator,
	"alarm_panel.disarm":      auth.RoleOperator,
	"alarm_panel.silence":     auth.RoleOperator,
	"alarm_panel.silence_all": auth.RoleOperator,
	// Alarm codes are security material: every code command — including
	// the list — is operator-gated, never viewer-open (§11/§16).
	"alarm_panel.codes_list":              auth.RoleOperator,
	"alarm_panel.codes_create":            auth.RoleOperator,
	"alarm_panel.codes_update":            auth.RoleOperator,
	"alarm_panel.codes_delete":            auth.RoleOperator,
	"ccu.reload_channel_config":           auth.RoleOperator,
	"ccu.reload_device_config":            auth.RoleOperator,
	"cdp.invoke":                          auth.RoleOperator,
	"central.create_links":                auth.RoleOperator,
	"central.reconcile":                   auth.RoleOperator,
	"central.remove_links":                auth.RoleOperator,
	"change_history.clear":                auth.RoleOperator,
	"config.reload_channel_config":        auth.RoleOperator,
	"config.reload_device_config":         auth.RoleOperator,
	"config.session.discard":              auth.RoleOperator,
	"config.session.open":                 auth.RoleOperator,
	"config.session.redo":                 auth.RoleOperator,
	"config.session.save":                 auth.RoleOperator,
	"config.session.set":                  auth.RoleOperator,
	"config.session.undo":                 auth.RoleOperator,
	"device.install_mode":                 auth.RoleOperator,
	"device.rename":                       auth.RoleOperator,
	"device.test":                         auth.RoleOperator,
	"device.rename_channel":               auth.RoleOperator,
	"device.set_channel_functions":        auth.RoleOperator,
	"device.set_channel_rooms":            auth.RoleOperator,
	"firmware.refresh":                    auth.RoleOperator,
	"firmware.update":                     auth.RoleOperator,
	"inbox.accept":                        auth.RoleOperator,
	"incidents.clear":                     auth.RoleOperator,
	"install_mode.disable":                auth.RoleOperator,
	"install_mode.enable":                 auth.RoleOperator,
	"install_mode.search":                 auth.RoleOperator,
	"links.add":                           auth.RoleOperator,
	"links.put_paramset":                  auth.RoleOperator,
	"links.remove":                        auth.RoleOperator,
	"links.set_info":                      auth.RoleOperator,
	"master_profiles.apply":               auth.RoleOperator,
	"paramset.copy":                       auth.RoleOperator,
	"paramset.put":                        auth.RoleOperator,
	"programs.execute":                    auth.RoleOperator,
	"recording.start":                     auth.RoleOperator,
	"recording.stop":                      auth.RoleOperator,
	"schedules.active_profile.set":        auth.RoleOperator,
	"schedules.climate.copy_profile":      auth.RoleOperator,
	"schedules.climate.set":               auth.RoleOperator,
	"schedules.copy":                      auth.RoleOperator,
	"schedules.device.active_profile.set": auth.RoleOperator,
	"schedules.device.set":                auth.RoleOperator,
	"schedules.set_enabled":               auth.RoleOperator,
	"service_messages.ack":                auth.RoleOperator,
	"service_messages.ack_all":            auth.RoleOperator,
	"service_messages.disable":            auth.RoleOperator,
	"service_messages.unsuppress":         auth.RoleOperator,
	"sysvars.set":                         auth.RoleOperator,
}

// readOnlyCommands is the explicit complement of writeCommandRoles: every
// registered command that intentionally requires no more than an
// authenticated (viewer) identity. A command absent from *both* this set
// and writeCommandRoles is unclassified policy —
// TestEveryRegisteredWSCommandIsClassified
// (tests/contract/ws_command_classification_test.go) fails the build in
// that case instead of letting the command default to viewer-accessible.
//
// This pair of maps is the reverse guard against the failure mode
// writeCommandRoles alone cannot catch: a new mutating command registered
// without a writeCommandRoles entry silently falls through Dispatch's role
// gate and becomes callable by any viewer. Every new
// `router.Register(...)` call site must add its command name to exactly
// one of these two sets.
//
// Not read by Dispatch itself — writeCommandRoles alone still drives the
// runtime gate, so an entry missing here has no production effect. This
// set exists purely as the second half of the classification the contract
// test above cross-checks against the registered-command set extracted
// from commands_default.go / commands_extended.go / commands_missing.go /
// custom_data_points.go via go/ast.
//
//nolint:unused // consumed by tests/contract via go/ast source parsing, not by Go code in this package — see the comment above.
var readOnlyCommands = map[string]struct{}{
	"alarm_messages.list":         {},
	"alarm_panel.journal":         {},
	"alarm_panel.readiness":       {},
	"alarm_panel.state":           {},
	"alarm_panel.walktest_status": {},
	"alarm_panel.panels":          {},
	"backup.status":               {},
	"calc_dp.get":                 {},
	"calc_dp.list":                {},
	"ccu.device_statistics":       {},
	"ccu.get_hub_data":            {},
	"ccu.get_rssi_info":           {},
	"ccu.get_signal_quality":      {},
	"ccu.throttle_stats":          {},
	"cdp.get":                     {},
	"cdp.list":                    {},
	"central.connectivity":        {},
	"central.info":                {},
	"central.links_status":        {},
	"central.system_health":       {},
	"change_history.list":         {},
	"config.session.changes":      {},
	"device.replace_candidates":   {},
	"devices.export_definition":   {},
	"devices.get":                 {},
	"devices.list":                {},
	"firmware.info":               {},
	"groups.list":                 {},
	"inbox.list":                  {},
	"incidents.get":               {},
	"incidents.list":              {},
	"install_mode.status":         {},
	"links.get_form_schema":       {},
	"links.get_paramset":          {},
	"links.get_profiles":          {},
	"links.linkable_channels":     {},
	"links.list":                  {},
	"links.test_profile":          {},
	"master_profiles.get":         {},
	"master_profiles.list":        {},
	"master_profiles.match":       {},
	"paramset.description":        {},
	"paramset.determine":          {},
	"paramset.form_schema":        {},
	"paramset.get":                {},
	"programs.list":               {},
	"recording.status":            {},
	"schedules.climate.get":       {},
	"schedules.device.get":        {},
	"schedules.list_devices":      {},
	"service_messages.list":       {},
	"service_messages.suppressed": {},
	"system.commands":             {},
	"system.health":               {},
	"system.user_permissions":     {},
	"sysvars.fetch":               {},
	"sysvars.list":                {},
}

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
	// CommandErrorRateLimited is returned when a connection exceeds the
	// per-identity command rate. Clients should back off and retry.
	CommandErrorRateLimited = "rate_limited"
	// CommandErrorLocked mirrors REST's 423 Locked: a MASTER/LINK
	// paramset write was rejected because the caller does not hold the
	// per-resource edit lock. The client must open an edit session and
	// pass its token as `edit_token` before retrying.
	CommandErrorLocked = "locked"
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

	// limiter throttles the command channel per auth identity. Set once
	// by NewRouter; the same lock-free hot-path read rule as the boundary
	// fields applies (the limiter has its own internal lock).
	limiter *commandRateLimiter
}

// NewRouter returns an empty router with no boundary wiring (no
// logger, empty central name). Tests typically use this; production
// composes via [SetBoundary] after construction.
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]CommandHandler),
		limiter:  newCommandRateLimiter(commandRatePerSec, commandRateBurst),
	}
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

	if r.limiter != nil && !r.limiter.allow(commandRateKey(ctx)) {
		res := Result{Error: NewCommandError(CommandErrorRateLimited, "command rate limit exceeded")}
		r.logOutcome(ctx, command, res, time.Since(start))
		return res
	}

	r.mu.RLock()
	fn, ok := r.handlers[command]
	r.mu.RUnlock()
	if !ok {
		res := Result{Error: NewCommandError(CommandErrorUnknownCommand, "no handler for "+command)}
		r.logOutcome(ctx, command, res, time.Since(start))
		return res
	}
	// Per-command role gate: a state-changing command listed in
	// writeCommandRoles requires the caller's identity to hold at least the
	// mapped role. This is the WebSocket counterpart to the REST router's
	// `.With(op)` / `.With(admin)` wrappers; without it a read-only viewer
	// could invoke operator/admin writes over the socket.
	if minRole, gated := writeCommandRoles[command]; gated {
		id, authed := auth.IdentityFrom(ctx)
		switch {
		case !authed || id.Subject == "":
			res := Result{Error: NewCommandError(CommandErrorUnauthorized, "authentication required for "+command)}
			r.logOutcome(ctx, command, res, time.Since(start))
			return res
		case !id.HasRole(minRole):
			res := Result{Error: NewCommandError(CommandErrorForbidden, command+" requires "+string(minRole)+" role")}
			r.logOutcome(ctx, command, res, time.Since(start))
			return res
		}
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
