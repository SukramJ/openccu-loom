// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"io"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// Format selects the encoding of the underlying slog handler.
type Format int

const (
	// FormatJSON emits records as one JSON object per line.
	FormatJSON Format = iota
	// FormatText emits records as key=value pairs (slog text handler).
	FormatText
	// FormatTextColor is FormatText with ANSI level + attribute
	// colouring, gated by a TTY detector. When the configured
	// [StackOptions.Writer] is not a terminal (file, pipe,
	// journald), the handler falls back to plain FormatText so
	// captured logs stay grep-friendly.
	FormatTextColor
)

// ParseFormat maps a config string to a [Format]. Unknown values fall
// back to [FormatJSON] — the strict alternative would be an error, but
// the daemon already validates this elsewhere and we prefer not to
// abort startup over a typo in a non-critical field.
func ParseFormat(raw string) Format {
	switch raw {
	case "text":
		return FormatText
	case "text-color", "color", "tint":
		return FormatTextColor
	}
	return FormatJSON
}

// StackOptions configures the handler chain returned by [BuildFullStack]
// and [ForSubsystem].
//
// The default chain, in order from outer to inner, is:
//
//	reqctx.ContextHandler (adds request_id / operation / trace fields)
//	  → RedactingHandler (masks sensitive attribute values)
//	    → core handler (JSON or text, gated by a Leveler)
//
// A nil [Sensitive] slice keeps the default redaction list; passing an
// empty slice disables redaction entirely.
type StackOptions struct {
	Writer    io.Writer
	Format    Format
	Sensitive []string // nil ⇒ defaults; empty slice ⇒ disabled
}

// Stack bundles the components built by [BuildFullStack].
type Stack struct {
	// Logger is the root [slog.Logger]; caller installs it via
	// [slog.SetDefault].
	Logger *slog.Logger
	// Levels is the per-subsystem level registry. The diagnostics
	// REST endpoint operates on this instance.
	Levels *LevelRegistry
	// Tee is the runtime capture switch. Attach a [CaptureSink]
	// before the operator-triggered capture window opens; detach +
	// snapshot at the end to assemble the download archive.
	Tee *TeeHandler
	// Live is the always-on ring buffer that backs the log viewer's
	// tail / backfill / download. It is attached to the root [TeeHandler]
	// at construction and mirrored onto every derived logger.
	Live *LiveLog
}

// BuildFullStack returns the global root logger plus its level
// registry, capture tee, and live ring. The root logger is gated by
// the registry's default level — every subsequent
// [LevelRegistry.SetDefault] call takes effect on the next log record
// without rebuilding the logger.
//
// Callers typically wire [Stack.Logger] into [slog.SetDefault] and
// hold [Stack.Levels] + [Stack.Tee] for the diagnostics endpoints.
func BuildFullStack(opts StackOptions, defaultLevel slog.Level) Stack {
	reg := NewLevelRegistry(defaultLevel)
	logger, tee := loggerForLevelerWithTee(opts, reg.Leveler(""))
	// Attach the live ring to the root tee before any derived logger is
	// spawned via With(...), so every child inherits the same instance.
	live := NewLiveLog(DefaultLiveLogCapacity)
	tee.AttachLive(live)
	return Stack{Logger: logger, Levels: reg, Tee: tee, Live: live}
}

// ForSubsystem returns a logger gated by the level configured (or
// inherited) for path. The handler chain is identical to the one
// installed by [BuildFullStack] so that subsystem records carry the same
// request-context fields and the same redaction guarantees as the
// root logger.
//
// Use a dotted, lowercase path that describes the subsystem
// uniquely, e.g. `openccu-loom.client.transport.xmlrpc` or
// `openccu-loom.north.matter.bridge`. The registry resolves the
// effective level hierarchically — a debug override on
// `openccu-loom.client` covers every descendant.
func ForSubsystem(reg *LevelRegistry, opts StackOptions, path string) *slog.Logger {
	if reg == nil {
		// Caller failed to wire the registry. Falling back to a plain
		// stdout logger keeps the daemon running but loses subsystem
		// gating — better than nil-pointer at log time.
		reg = NewLevelRegistry(slog.LevelInfo)
	}
	return loggerForLeveler(opts, reg.Leveler(path)).With(slog.String("logger", path))
}

func loggerForLeveler(opts StackOptions, leveler slog.Leveler) *slog.Logger {
	logger, _ := loggerForLevelerWithTee(opts, leveler)
	return logger
}

func loggerForLevelerWithTee(opts StackOptions, leveler slog.Leveler) (*slog.Logger, *TeeHandler) {
	hopts := &slog.HandlerOptions{Level: leveler}
	var core slog.Handler
	switch opts.Format {
	case FormatTextColor:
		// Fall back to plain text when the writer is not a
		// terminal — keeps captured logfiles + journald output
		// free of ANSI escape sequences.
		if writerIsTTY(opts.Writer) {
			core = tint.NewHandler(opts.Writer, &tint.Options{Level: leveler})
		} else {
			core = slog.NewTextHandler(opts.Writer, hopts)
		}
	case FormatText:
		core = slog.NewTextHandler(opts.Writer, hopts)
	default:
		core = slog.NewJSONHandler(opts.Writer, hopts)
	}
	// Apply redaction first so that pre-bound attributes added via
	// With(...) on the returned logger are masked too.
	var redacted slog.Handler
	switch {
	case opts.Sensitive == nil:
		redacted = NewRedactingHandler(core)
	case len(opts.Sensitive) == 0:
		redacted = core
	default:
		redacted = NewRedactingHandlerWithKeys(core, opts.Sensitive)
	}
	// TeeHandler sits between reqctx and redact so capture sees the
	// already-redacted payload (capture archives are operator-facing
	// artefacts and must respect the same redaction guarantees).
	tee := NewTeeHandler(redacted)
	// Reqctx filter sits outermost so the injected attributes (which
	// include the W3C trace IDs) are visible to downstream handlers
	// and themselves never trip the redaction patterns.
	enriched := reqctx.NewContextHandler(tee)
	return slog.New(enriched), tee
}

// writerIsTTY reports whether w is a terminal that supports ANSI
// escape sequences. Returns false for any writer the daemon cannot
// inspect (file, pipe, in-memory buffer, custom io.Writer) — the
// caller then falls back to monochrome text rather than polluting
// the stream with escape codes.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}
