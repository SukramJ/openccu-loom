// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package observability bundles cross-cutting wrappers that mirror the
// Effect of
// `@measure_execution_time`, `@service_call`). The public API is stable
// so that follow-up patches only need to add individual call sites.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Scope classifies an operation broadly — analogous
// `ServiceScope`. Reflected in logs and metric labels.
type Scope string

// Scope values. Pure categories — new scopes can be added additively
// without breaking existing call sites.
const (
	ScopeUnknown     Scope = ""
	ScopeService     Scope = "service" // high-level service call (set_value, put_paramset, …)
	ScopeBackend     Scope = "backend" // RPC call against a backend
	ScopeCoordinator Scope = "coordinator"
	ScopeStore       Scope = "store"
	ScopeNorthbound  Scope = "northbound" // REST/WS/MQTT entry point
)

// Recorder is the observability sink. Production wires this to the
// metrics registry; tests can pass a no-op or a recording fake.
type Recorder interface {
	// ObserveLatency records the duration a call took.
	ObserveLatency(name string, scope Scope, d time.Duration, err error)
	// IncCounter increments an event counter (success/failure/retries, etc.
	// — naming convention is up to the call site).
	IncCounter(name string, scope Scope, delta uint64)
}

// NoopRecorder discards all observations. Default for tests where the
// caller expects no telemetry.
type NoopRecorder struct{}

// ObserveLatency implements [Recorder].
func (NoopRecorder) ObserveLatency(string, Scope, time.Duration, error) {}

// IncCounter implements [Recorder].
func (NoopRecorder) IncCounter(string, Scope, uint64) {}

// LogRecorder writes observations to a [slog.Logger]. Useful as a bridge
// before the metrics registry is wired.
type LogRecorder struct{ Logger *slog.Logger }

// ObserveLatency implements [Recorder].
func (r LogRecorder) ObserveLatency(name string, scope Scope, d time.Duration, err error) {
	if r.Logger == nil {
		return
	}
	attrs := []any{
		slog.String("op", name),
		slog.String("scope", string(scope)),
		slog.Duration("dur", d),
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
		r.Logger.Warn("op", attrs...)
		return
	}
	r.Logger.Debug("op", attrs...)
}

// IncCounter implements [Recorder].
func (r LogRecorder) IncCounter(name string, scope Scope, delta uint64) {
	if r.Logger == nil {
		return
	}
	r.Logger.Debug(
		"counter",
		slog.String("name", name),
		slog.String("scope", string(scope)),
		slog.Uint64("delta", delta),
	)
}

// Instrument wraps fn with latency + outcome tracking. Errors are
// returned unchanged so callers retain control over wrapping. The
// recorded duration is wall time of fn including any retries inside it.
//
// Panics propagate after recording the latency tagged as failure.
//
// Call sites are responsible for choosing a meaningful name and scope so that
// the emitted metrics are grouped correctly in dashboards. Convention:
// name follows "subsystem.operation" (e.g. "client.set_value"); scope
// selects the observability bucket (ScopeBackend, ScopeService, etc.).
func Instrument(ctx context.Context, rec Recorder, name string, scope Scope, fn func(ctx context.Context) error) (err error) {
	if rec == nil {
		rec = NoopRecorder{}
	}
	start := now()
	defer func() {
		if r := recover(); r != nil {
			d := now().Sub(start)
			perr := errors.New("instrumented panic")
			rec.ObserveLatency(name, scope, d, perr)
			rec.IncCounter(name+".panic", scope, 1)
			panic(r)
		}
		d := now().Sub(start)
		rec.ObserveLatency(name, scope, d, err)
		if err != nil {
			rec.IncCounter(name+".error", scope, 1)
		} else {
			rec.IncCounter(name+".ok", scope, 1)
		}
	}()
	return fn(ctx)
}

// InstrumentValue is the result-carrying variant of [Instrument], for a
// wrapped function that returns a value alongside its error. It records
// the same latency, error and panic observations.
func InstrumentValue[T any](ctx context.Context, rec Recorder, name string, scope Scope, fn func(ctx context.Context) (T, error)) (T, error) {
	var out T
	err := Instrument(ctx, rec, name, scope, func(ctx context.Context) error {
		v, e := fn(ctx)
		out = v
		return e
	})
	return out, err
}
