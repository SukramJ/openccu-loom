// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync"
)

// modulePathPrefix is the import prefix every daemon package shares. A
// call site outside it belongs to a dependency, which has no subsystem
// path an operator could address.
const modulePathPrefix = "github.com/SukramJ/openccu-loom/"

// subsystemRoot prefixes every derived path, matching the namespace the
// diagnostics endpoint documents (`openccu-loom.client.transport.xmlrpc`).
const subsystemRoot = "openccu-loom"

// levelFilterHandler applies the per-subsystem level to each record.
//
// slog decides verbosity through Enabled, which sees only a level — never
// the record — so a single handler chain cannot gate on a path there. The
// core handler is therefore gated at the registry's *minimum* level, which
// admits everything any path could want, and this handler sits outermost
// and drops what the record's own path does not want. Without it the
// registry resolves overrides correctly and no handler ever consults them:
// the whole per-subsystem feature reports success and changes nothing.
//
// The cost of that split is that while any path is raised to debug, every
// debug call site in the daemon builds its record before being dropped
// here. That is bounded to the debug window an operator opened, which is
// the trade the registry exists to make.
//
// A record's path is the `logger` attribute when a caller named its logger
// (see [Stack.Named]), and otherwise the package the record was emitted
// from. The derived form is what makes the feature cover the daemon: every
// call site logs through the root logger or slog.Default(), so overrides
// limited to named loggers would address almost nothing.
type levelFilterHandler struct {
	inner  slog.Handler
	reg    *LevelRegistry
	path   string
	pinned bool // path came from a `logger` attribute rather than the call site
}

// newLevelFilterHandler wraps inner so that every record is gated by the
// level reg resolves for that record's subsystem.
func newLevelFilterHandler(inner slog.Handler, reg *LevelRegistry) slog.Handler {
	return &levelFilterHandler{inner: inner, reg: reg}
}

// Enabled reports whether a record at level could survive the filter. For
// a named logger that is an exact answer; for an unnamed one the path is
// still unknown here, so the most verbose level any path resolves to is
// the only sound bound — [levelFilterHandler.Handle] makes the precise
// decision once the record exists.
func (h *levelFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.pinned {
		if level < h.reg.Resolve(h.path) {
			return false
		}
	} else if level < h.reg.Min() {
		return false
	}
	return h.inner.Enabled(ctx, level)
}

// Handle drops the record when its subsystem's effective level is above
// it, and otherwise passes it down the chain — stdout, the capture sink
// and the live ring all sit below, so a raised subsystem reaches every one
// of them and a lowered one reaches none.
func (h *levelFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	path := h.path
	if !h.pinned {
		path = subsystemPathForPC(r.PC)
	}
	if r.Level < h.reg.Resolve(path) {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs pins the subsystem path when the caller binds a `logger`
// attribute, which is how [Stack.Named] and [ForSubsystem] name a logger.
func (h *levelFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	child := &levelFilterHandler{
		inner:  h.inner.WithAttrs(attrs),
		reg:    h.reg,
		path:   h.path,
		pinned: h.pinned,
	}
	for _, a := range attrs {
		if a.Key == "logger" && a.Value.Kind() == slog.KindString {
			child.path = normalisePath(a.Value.String())
			child.pinned = true
		}
	}
	return child
}

// WithGroup delegates and keeps the pinned path: a `logger` attribute
// nested inside a group names a field, not the emitting subsystem.
func (h *levelFilterHandler) WithGroup(name string) slog.Handler {
	return &levelFilterHandler{
		inner:  h.inner.WithGroup(name),
		reg:    h.reg,
		path:   h.path,
		pinned: h.pinned,
	}
}

// subsystemPaths caches the mapping from a call-site program counter to
// its subsystem path. Program counters are stable for the process
// lifetime, so each call site resolves its frame exactly once.
//
// sync.Map rather than a typed map because the standard library offers no
// typed variant and the values are written once and read on every record.
var subsystemPaths sync.Map

// subsystemPathForPC maps the program counter slog captured for a record
// to the subsystem path of the package that emitted it. Returns "" when
// the frame is unavailable or outside this module, which resolves to the
// registry default.
func subsystemPathForPC(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	if cached, ok := subsystemPaths.Load(pc); ok {
		path, _ := cached.(string)
		return path
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	path := subsystemPathForFunc(frame.Function)
	subsystemPaths.Store(pc, path)
	return path
}

// subsystemPathForFunc maps a fully qualified function name to its
// subsystem path: the package's position in the repository tree, with the
// `internal/` and `pkg/` layering prefixes dropped because they say
// nothing about which subsystem a record came from.
//
//	internal/client/transport/xmlrpc → openccu-loom.client.transport.xmlrpc
//	pkg/hmlog                        → openccu-loom.hmlog
//	cmd/openccu-loom                 → openccu-loom.cmd.openccu-loom
func subsystemPathForFunc(fn string) string {
	rest, ok := strings.CutPrefix(fn, modulePathPrefix)
	if !ok {
		return ""
	}
	// The package name is the segment up to the first dot after the last
	// slash; everything past that dot is the receiver and function name.
	dir := ""
	if slash := strings.LastIndex(rest, "/"); slash >= 0 {
		dir, rest = rest[:slash], rest[slash+1:]
	}
	if dot := strings.Index(rest, "."); dot >= 0 {
		rest = rest[:dot]
	}
	if rest == "" {
		return ""
	}
	pkgPath := rest
	if dir != "" {
		pkgPath = dir + "/" + rest
	}
	for _, layer := range []string{"internal/", "pkg/"} {
		if trimmed, cut := strings.CutPrefix(pkgPath, layer); cut {
			pkgPath = trimmed
			break
		}
	}
	if pkgPath == "" {
		return subsystemRoot
	}
	return subsystemRoot + "." + strings.ReplaceAll(pkgPath, "/", ".")
}
