// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

type captureRecorder struct {
	latencies []latencyEntry
	counters  []counterEntry
}

type latencyEntry struct {
	Name  string
	Scope Scope
	Dur   time.Duration
	Err   error
}

type counterEntry struct {
	Name  string
	Scope Scope
	Delta uint64
}

func (r *captureRecorder) ObserveLatency(name string, scope Scope, d time.Duration, err error) {
	r.latencies = append(r.latencies, latencyEntry{name, scope, d, err})
}

func (r *captureRecorder) IncCounter(name string, scope Scope, delta uint64) {
	r.counters = append(r.counters, counterEntry{name, scope, delta})
}

func TestInstrumentSuccess(t *testing.T) {
	rec := &captureRecorder{}
	err := Instrument(context.Background(), rec, "set_value", ScopeService, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(rec.latencies) != 1 {
		t.Fatalf("latency entries=%d", len(rec.latencies))
	}
	if rec.latencies[0].Err != nil || rec.latencies[0].Name != "set_value" || rec.latencies[0].Scope != ScopeService {
		t.Fatalf("entry=%+v", rec.latencies[0])
	}
	if len(rec.counters) != 1 || rec.counters[0].Name != "set_value.ok" {
		t.Fatalf("counters=%+v", rec.counters)
	}
}

func TestInstrumentFailure(t *testing.T) {
	rec := &captureRecorder{}
	wantErr := errors.New("boom")
	err := Instrument(context.Background(), rec, "put_paramset", ScopeService, func(_ context.Context) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if len(rec.counters) != 1 || rec.counters[0].Name != "put_paramset.error" {
		t.Fatalf("counters=%+v", rec.counters)
	}
}

func TestInstrumentPanicPropagatesAndRecords(t *testing.T) {
	rec := &captureRecorder{}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("panic did not propagate")
		}
		if len(rec.counters) == 0 || rec.counters[0].Name != "list_devices.panic" {
			t.Fatalf("counters=%+v", rec.counters)
		}
		if len(rec.latencies) == 0 || rec.latencies[0].Err == nil {
			t.Fatalf("latencies=%+v", rec.latencies)
		}
	}()
	_ = Instrument(context.Background(), rec, "list_devices", ScopeBackend, func(_ context.Context) error {
		panic("nope")
	})
}

func TestInstrumentNilRecorderUsesNoop(t *testing.T) {
	if err := Instrument(context.Background(), nil, "x", ScopeUnknown, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestInstrumentValueReturnsResult(t *testing.T) {
	rec := &captureRecorder{}
	got, err := InstrumentValue(context.Background(), rec, "get_value", ScopeBackend, func(_ context.Context) (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != 42 {
		t.Fatalf("got=%d", got)
	}
}

func TestInstrumentValuePropagatesError(t *testing.T) {
	rec := &captureRecorder{}
	wantErr := errors.New("rpc")
	got, err := InstrumentValue(context.Background(), rec, "get_value", ScopeBackend, func(_ context.Context) (string, error) {
		return "", wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestNoopRecorderImplementsRecorder(t *testing.T) {
	var _ Recorder = NoopRecorder{}
	NoopRecorder{}.ObserveLatency("x", ScopeUnknown, time.Second, nil)
	NoopRecorder{}.IncCounter("x", ScopeUnknown, 1)
}

func TestLogRecorderNilLoggerSafe(t *testing.T) {
	var r LogRecorder
	r.ObserveLatency("x", ScopeUnknown, time.Second, nil)
	r.IncCounter("x", ScopeUnknown, 1)
}
