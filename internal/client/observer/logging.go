// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package observer collects concrete [interfaces.TransportObserver]
// implementations and a small fan-out helper for composing several
// observers into one.
package observer

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Logging is a [interfaces.TransportObserver] that emits a structured
// span for every south-bound RPC. The span carries the W3C trace IDs
// and surfaces method / interface / host attributes so that a single
// log line is enough to identify a misbehaving call.
//
// Logging operates in tandem with [hmlog.StartOp]: a child span is
// opened in OnRequestStart and closed in OnRequestEnd, so the full
// trace tree shows the RPC as a leaf of whichever request triggered
// it (REST handler, scheduler job, etc.).
type Logging struct {
	logger        *slog.Logger
	slowThreshold time.Duration
}

// LoggingOption mutates a [Logging] observer.
type LoggingOption func(*Logging)

// WithLogger overrides the slog.Logger used for the span. Default is
// [slog.Default].
func WithLogger(logger *slog.Logger) LoggingOption {
	return func(o *Logging) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithSlowThreshold escalates the closing record to Warn when a call
// runs at least this long. Zero disables the escalation (default).
func WithSlowThreshold(d time.Duration) LoggingOption {
	return func(o *Logging) { o.slowThreshold = d }
}

// NewLogging builds a Logging observer with the supplied options
// applied in order.
func NewLogging(opts ...LoggingOption) *Logging {
	obs := &Logging{logger: slog.Default()}
	for _, opt := range opts {
		opt(obs)
	}
	return obs
}

// loggingSpan is the opaque RequestSpan carried between
// OnRequestStart and OnRequestEnd.
type loggingSpan struct {
	closer hmlog.SpanCloser
	method string
	iface  string
}

// OnRequestStart implements [interfaces.TransportObserver]. The
// returned [interfaces.RequestSpan] holds the [hmlog.SpanCloser] that
// OnRequestEnd uses to record the outcome.
func (o *Logging) OnRequestStart(ctx context.Context, info interfaces.RequestInfo) interfaces.RequestSpan {
	op := info.Protocol + "." + info.Method
	attrs := []slog.Attr{
		slog.String("protocol", info.Protocol),
		slog.String("rpc_method", info.Method),
	}
	if info.Interface != "" {
		attrs = append(attrs, slog.String("interface_id", info.Interface))
	}
	if info.Host != "" {
		attrs = append(attrs, slog.String("host", info.Host))
	}
	_, closer := hmlog.StartOp(ctx, op, hmlog.OpOptions{
		Logger:        o.logger,
		SlowThreshold: o.slowThreshold,
		Attrs:         attrs,
	})
	return &loggingSpan{closer: closer, method: info.Method, iface: info.Interface}
}

// OnRequestEnd implements [interfaces.TransportObserver]. A non-span
// argument (e.g. nil from a buggy caller) is a no-op so the transport
// can never deadlock on a stuck closer.
//
// Semantic CCU faults (non-retryable XMLRPC, e.g. `Unknown Parameter`
// when polling a write-only data point) are treated as successful
// from the wire's perspective — they describe the device, not the
// transport. Counting them as errors would flood the log with
// Error-level records the operator cannot act on. Mirrors the same
// predicate the circuit breaker and the Health observer use.
func (o *Logging) OnRequestEnd(span interfaces.RequestSpan, result interfaces.RequestResult) {
	ls, ok := span.(*loggingSpan)
	if !ok || ls == nil {
		return
	}
	if isSemanticFault(result.Err) {
		ls.closer(nil)
		return
	}
	ls.closer(result.Err)
}

// Compile-time assertion.
var _ interfaces.TransportObserver = (*Logging)(nil)
