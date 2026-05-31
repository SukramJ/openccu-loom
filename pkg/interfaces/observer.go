// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package interfaces

import (
	"context"
	"time"
)

// TransportObserver receives per-request lifecycle callbacks from a
// southbound transport (XML-RPC, BIN-RPC, JSON-RPC). Implementations
// fan out to the circuit breaker, Prometheus metrics, and the optional
// session recorder.
//
// Every method receives the same ctx the transport used; implementations
// must honour cancellation. Methods run on the calling goroutine and
// must not block on I/O — state updates should be O(1) or deferred to
// a worker the observer owns.
type TransportObserver interface {
	// OnRequestStart is called after validation but before the wire call.
	// The returned [RequestSpan] is completed by the transport via
	// OnRequestEnd on the same span.
	OnRequestStart(ctx context.Context, info RequestInfo) RequestSpan

	// OnRequestEnd is called exactly once per OnRequestStart span.
	// err is nil on success; otherwise it carries the same error the
	// transport returns to its caller, already wrapped with hmerr.Context.
	OnRequestEnd(span RequestSpan, result RequestResult)
}

// RequestInfo describes a southbound request before it is sent.
type RequestInfo struct {
	Protocol  string // "xml-rpc", "bin-rpc", "json-rpc"
	Method    string // remote method name
	Host      string // e.g. "ccu-host:2010"
	Interface string // optional; "HmIP-RF", "CUxD", ...
}

// RequestResult carries the outcome of a completed request.
type RequestResult struct {
	Duration time.Duration
	Err      error
}

// RequestSpan is an observer-private handle returned by OnRequestStart
// and passed back to OnRequestEnd. The transport treats it as opaque.
type RequestSpan any

// NoopObserver is a zero-overhead TransportObserver useful as a default.
type NoopObserver struct{}

// OnRequestStart implements TransportObserver.
func (NoopObserver) OnRequestStart(_ context.Context, _ RequestInfo) RequestSpan { return nil }

// OnRequestEnd implements TransportObserver.
func (NoopObserver) OnRequestEnd(_ RequestSpan, _ RequestResult) {}
