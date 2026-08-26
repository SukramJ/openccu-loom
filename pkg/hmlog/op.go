// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// SpanCloser finalises a span opened by [StartOp]. Pass nil for
// success or a non-nil error to record the outcome. Calling Close
// twice is harmless — only the first call emits the end log.
type SpanCloser func(err error)

// OpOptions tunes the behaviour of [StartOp].
type OpOptions struct {
	// Logger is the slog.Logger used for the start + end records. If
	// nil the package's slog default is used.
	Logger *slog.Logger
	// SlowThreshold, when positive, escalates the end record to Warn
	// level if the operation took at least that long. A zero value
	// disables the escalation; an explicit < 0 disables even the
	// success log entirely (the end record is suppressed unless the
	// operation errored).
	SlowThreshold time.Duration
	// Attrs are merged into both the start and end records. Use for
	// caller-specific context (interface_id, device_address, etc.)
	// that is not already in the [reqctx.RequestContext].
	Attrs []slog.Attr
}

// opToleranceKey is the context key under which [WithRetriedFailures]
// and [WithExpectedSlowness] record what a caller already knows about
// the operation it is about to make.
type opToleranceKey struct{}

// opTolerance carries what the caller knows that the span layer cannot
// see for itself.
//
// The span layer classifies an operation by its result alone: an error
// is an error, a slow call is slow. That is right for a steady-state
// daemon and wrong during a bring-up, where a caller retries across a
// backoff window and a co-starting CCU answers in seconds rather than
// milliseconds. Both were reported at their nominal severity, so a
// completely healthy boot shipped error and warn lines that resolved
// themselves — the kind of noise that teaches operators to stop reading
// the log.
type opTolerance struct {
	// retriedFailures demotes a failed end record to Warn: the caller
	// retries, so this attempt failing is not yet an outcome.
	retriedFailures bool
	// expectedSlowness demotes a slow end record to Info: the caller
	// knows the peer is under startup load.
	expectedSlowness bool
}

// WithRetriedFailures marks ctx as covering an attempt whose failure the
// caller retries. A failed end record on such an operation is logged at
// Warn with `retried: true` instead of Error.
//
// Set it on every attempt but the last one. The final attempt carries no
// marker, so an interface that never comes up still reports an error.
func WithRetriedFailures(ctx context.Context) context.Context {
	tol := toleranceFrom(ctx)
	tol.retriedFailures = true
	return context.WithValue(ctx, opToleranceKey{}, tol)
}

// WithExpectedSlowness marks ctx as covering an operation against a peer
// the caller knows to be under startup load. A slow end record on such
// an operation is logged at Info with `expected: true` instead of Warn.
//
// It does not touch the failure branch: a call that fails inside a
// tolerant window is still a failure.
func WithExpectedSlowness(ctx context.Context) context.Context {
	tol := toleranceFrom(ctx)
	tol.expectedSlowness = true
	return context.WithValue(ctx, opToleranceKey{}, tol)
}

// toleranceFrom returns the tolerance recorded on ctx, or the zero
// value (tolerate nothing) when the caller set none.
func toleranceFrom(ctx context.Context) opTolerance {
	tol, _ := ctx.Value(opToleranceKey{}).(opTolerance)
	return tol
}

// StartOp opens a tracing span for a named operation and returns:
//   - a derived context whose [reqctx.RequestContext] carries a fresh
//     SpanID (with ParentSpanID copied from the previous SpanID), and
//   - a [SpanCloser] that callers MUST invoke (typically via
//     `defer`) to record the end of the span.
//
// The start record is emitted at Debug level; the end record is
// emitted at Debug for success, Warn when [OpOptions.SlowThreshold]
// is exceeded, and Error when the closer is called with a non-nil
// error. The end record always carries an `elapsed_ms` attribute,
// alongside the W3C trace_id / span_id / parent_span_id pair that
// the [reqctx.ContextHandler] surfaces automatically.
//
// Use this everywhere an operation crosses a meaningful boundary —
// REST handler entry, coordinator method, RPC dispatch, store query —
// so the diagnostics tooling can reconstruct the call tree.
func StartOp(ctx context.Context, op string, opts OpOptions) (context.Context, SpanCloser) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ctx = reqctx.StartChildSpan(ctx)
	// Propagate the op name through the RequestContext so downstream
	// log records — even from helpers that do not pass through StartOp
	// themselves — still carry the operation tag.
	if rc, ok := reqctx.FromContext(ctx); ok {
		rc.Operation = op
		ctx = reqctx.WithRequestContext(ctx, rc)
	}
	startAttrs := append([]slog.Attr{}, opts.Attrs...)
	logger.LogAttrs(ctx, slog.LevelDebug, "op.start", append(startAttrs, slog.String("op", op))...)
	started := time.Now()
	closed := false
	return ctx, func(err error) {
		if closed {
			return
		}
		closed = true
		elapsed := time.Since(started)
		tol := toleranceFrom(ctx)
		level := slog.LevelDebug
		outcome := "ok"
		var demotion slog.Attr
		switch {
		case err != nil:
			level = slog.LevelError
			outcome = classifyError(err)
			if tol.retriedFailures {
				level = slog.LevelWarn
				demotion = slog.Bool("retried", true)
			}
		case opts.SlowThreshold > 0 && elapsed >= opts.SlowThreshold:
			level = slog.LevelWarn
			outcome = "slow"
			if tol.expectedSlowness {
				level = slog.LevelInfo
				demotion = slog.Bool("expected", true)
			}
		case opts.SlowThreshold < 0:
			// Suppress success record entirely.
			return
		}
		endAttrs := append([]slog.Attr{}, opts.Attrs...)
		if demotion.Key != "" {
			endAttrs = append(endAttrs, demotion)
		}
		endAttrs = append(
			endAttrs,
			slog.String("op", op),
			slog.String("outcome", outcome),
			slog.Float64("elapsed_ms", float64(elapsed.Milliseconds())),
		)
		if err != nil {
			endAttrs = append(endAttrs, slog.String("err", err.Error()))
		}
		logger.LogAttrs(ctx, level, "op.end", endAttrs...)
	}
}

// classifyError maps an error to a short outcome tag used in the
// `outcome` slog attribute. Recognised tags allow log post-processing
// to group failures without parsing the err string.
func classifyError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}
