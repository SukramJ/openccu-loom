// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observer

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// HealthRecorder is the subset of [*health.Tracker] the observer
// touches. Defined as an interface so tests can drop in a fake.
type HealthRecorder interface {
	RecordRequest(name string, success bool)
}

// Health is an [interfaces.TransportObserver] that funnels every
// south-bound RPC outcome into [HealthRecorder.RecordRequest], which
// pumps the per-interface [health.ClientHealth] detail metrics
// (LastSuccessful/FailedRequest, ConsecutiveFailures) exposed
// in the operator-facing diagnostics API.
//
// Semantic CCU faults (Unknown Parameter, validation rejections —
// anything [hmerr.XMLRPCFault.IsRetryable] reports false for) are
// treated as successful from the wire's perspective: they describe
// the device, not the transport. Counting them as failures would
// drag a perfectly healthy interface to "unhealthy" the first time
// the daemon polls a write-only data point. Mirrors the circuit
// breaker's semantic-fault filter in `reliability/circuit.go`.
type Health struct {
	tracker      HealthRecorder
	overrideName string
}

// HealthOption mutates a [Health] observer.
type HealthOption func(*Health)

// WithComponentName overrides the per-record component name. Use
// this for transports that do not carry a usable Interface field —
// the JSON-RPC hub session is the canonical case (the hub is one
// resource per central, not per interface). Pass the empty string
// to fall back to the default behaviour (read from
// [interfaces.RequestInfo.Interface]).
func WithComponentName(name string) HealthOption {
	return func(h *Health) { h.overrideName = name }
}

// NewHealth builds an observer bound to tracker. A nil tracker
// disables the recording — useful in tests that wire the observer
// without spinning up a full health tracker.
func NewHealth(tracker HealthRecorder, opts ...HealthOption) *Health {
	h := &Health{tracker: tracker}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// healthSpan is the opaque RequestSpan carried between
// OnRequestStart and OnRequestEnd. We pass the interface ID through
// so OnRequestEnd does not have to repeat the lookup the transport
// already did.
type healthSpan struct {
	iface string
}

// OnRequestStart implements [interfaces.TransportObserver].
func (h *Health) OnRequestStart(_ context.Context, info interfaces.RequestInfo) interfaces.RequestSpan {
	name := info.Interface
	if h.overrideName != "" {
		name = h.overrideName
	}
	return &healthSpan{iface: name}
}

// OnRequestEnd implements [interfaces.TransportObserver].
func (h *Health) OnRequestEnd(span interfaces.RequestSpan, result interfaces.RequestResult) {
	if h.tracker == nil {
		return
	}
	hs, ok := span.(*healthSpan)
	if !ok || hs == nil || hs.iface == "" {
		return
	}
	success := result.Err == nil || isSemanticFault(result.Err)
	h.tracker.RecordRequest(hs.iface, success)
}

// isSemanticFault reports whether err is a permanent CCU-side
// classification (Unknown Parameter, missing entity, validation
// rejection) that should not be counted against the interface's
// failure metric. Mirrors the same predicate in
// `internal/client/reliability/circuit.go`.
func isSemanticFault(err error) bool {
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		return false
	}
	return !fault.IsRetryable()
}

// Compile-time assertion.
var _ interfaces.TransportObserver = (*Health)(nil)
