// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// These tests must NOT run in parallel: SetSpanExporter installs a
// process-global exporter and Span.End routes finished spans through it,
// so parallel tests would cross-contaminate each other's collectors.

// TestExportSpan_NonBlockingDropsOnFullBuffer verifies that ExportSpan never
// blocks and that spans are dropped (not queued forever) when the internal
// buffer is full.
func TestExportSpan_NonBlockingDropsOnFullBuffer(t *testing.T) {
	// Use a tiny buffer and block the background goroutine so it can never
	// drain. The collector endpoint hangs indefinitely.
	blocked := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked // never unblocks during this test
	}))
	defer ts.Close()
	defer close(blocked)

	const bufSize = 4
	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BufferSize:    bufSize,
		FlushInterval: time.Hour, // prevent timer-driven flushes
		BatchSize:     bufSize,
		Client:        ts.Client(),
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = exp.Shutdown(ctx)
	}()

	sp, _ := observability.StartSpan(context.Background(), "fill", nil)
	sp.End()

	// Fill the buffer beyond capacity; every call must return immediately.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range bufSize * 3 {
			exp.ExportSpan(sp)
		}
	}()

	select {
	case <-done:
		// good — all calls returned without blocking
	case <-time.After(2 * time.Second):
		t.Fatal("ExportSpan blocked — it must be non-blocking")
	}

	if exp.Dropped() == 0 {
		t.Error("expected at least one dropped span when buffer is full")
	}
}

// TestOTLPJSON_WireShape creates a real span via StartSpan+End with the
// exporter installed, then captures the POST body from an httptest.Server
// and asserts the mandatory OTLP/JSON invariants.
func TestOTLPJSON_WireShape(t *testing.T) {
	var received atomic.Pointer[[]byte]
	gate := make(chan struct{})
	var once sync.Once // guard against a double-close if more than one POST arrives

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		snapshot := append([]byte(nil), body...)
		once.Do(func() {
			received.Store(&snapshot)
			close(gate)
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BatchSize:     1,         // flush immediately after 1 span
		FlushInterval: time.Hour, // timer won't fire
		Client:        ts.Client(),
	})

	// Install process-wide so End() routes through it.
	prev := observability.SetSpanExporter(exp)
	defer observability.SetSpanExporter(prev)

	sp, _ := observability.StartSpan(context.Background(), "wire_test", nil)
	sp.SetAttribute("env", "test")
	sp.AddEvent("checkpoint", map[string]any{"step": 1})
	sp.End()

	// Wait for the collector to receive the POST.
	select {
	case <-gate:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not receive a POST within 5 s")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	body := *received.Load()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v\nbody: %s", err, body)
	}

	// Navigate: resourceSpans[0].scopeSpans[0].spans[0]
	rs := mustSlice(t, doc, "resourceSpans")
	rs0 := mustMap(t, rs[0])
	ss := mustSlice(t, rs0, "scopeSpans")
	ss0 := mustMap(t, ss[0])
	spans := mustSlice(t, ss0, "spans")
	if len(spans) == 0 {
		t.Fatal("no spans in OTLP payload")
	}
	span0 := mustMap(t, spans[0])

	// traceId must be 32 lowercase hex chars.
	traceID := mustStr(t, span0, "traceId")
	if len(traceID) != 32 {
		t.Errorf("traceId len=%d, want 32: %q", len(traceID), traceID)
	}

	// spanId must be 16 lowercase hex chars.
	spanID := mustStr(t, span0, "spanId")
	if len(spanID) != 16 {
		t.Errorf("spanId len=%d, want 16: %q", len(spanID), spanID)
	}

	// parentSpanId must be absent for a root span.
	if _, ok := span0["parentSpanId"]; ok {
		t.Errorf("root span must NOT have parentSpanId field, got %q", span0["parentSpanId"])
	}

	// startTimeUnixNano and endTimeUnixNano must be decimal strings.
	start := mustStr(t, span0, "startTimeUnixNano")
	if start == "" {
		t.Error("startTimeUnixNano must not be empty")
	}
	end := mustStr(t, span0, "endTimeUnixNano")
	if end == "" {
		t.Error("endTimeUnixNano must not be empty")
	}

	// Attribute "env"="test" must round-trip.
	attrs := mustSlice(t, span0, "attributes")
	found := false
	for _, a := range attrs {
		am := mustMap(t, a)
		if mustStr(t, am, "key") == "env" {
			val := mustMap(t, am["value"])
			if val["stringValue"] == "test" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("attribute env=test not found in OTLP payload; attrs=%v", attrs)
	}

	// At least one event.
	events := mustSlice(t, span0, "events")
	if len(events) == 0 {
		t.Error("expected at least one event in OTLP span")
	}
}

// TestOTLPJSON_RedactsSensitiveAttributes verifies that span and event
// attributes keyed like a secret are exported with their value masked, so
// tracing cannot become a side channel that bypasses the log redactor.
func TestOTLPJSON_RedactsSensitiveAttributes(t *testing.T) {
	var received atomic.Pointer[[]byte]
	gate := make(chan struct{})
	var once sync.Once

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		snapshot := append([]byte(nil), body...)
		once.Do(func() {
			received.Store(&snapshot)
			close(gate)
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BatchSize:     1,
		FlushInterval: time.Hour,
		Client:        ts.Client(),
	})
	prev := observability.SetSpanExporter(exp)
	defer observability.SetSpanExporter(prev)

	const secret = "s3cr3t-value"
	sp, _ := observability.StartSpan(context.Background(), "redact_test", nil)
	sp.SetAttribute("password", secret)                          // sensitive key on the span
	sp.SetAttribute("host", "ccu.local")                         // benign key must survive
	sp.AddEvent("auth", map[string]any{"client_secret": secret}) // sensitive event attr
	sp.End()

	select {
	case <-gate:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not receive a POST within 5 s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	body := *received.Load()
	if bytes.Contains(body, []byte(secret)) {
		t.Fatalf("OTLP payload leaked the raw secret value: %s", body)
	}
	if !bytes.Contains(body, []byte(hmlog.RedactMask)) {
		t.Errorf("expected redaction mask %q in payload: %s", hmlog.RedactMask, body)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	rs := mustSlice(t, doc, "resourceSpans")
	ss := mustSlice(t, mustMap(t, rs[0]), "scopeSpans")
	spans := mustSlice(t, mustMap(t, ss[0]), "spans")
	span0 := mustMap(t, spans[0])

	var sawHost, sawMaskedPassword bool
	for _, a := range mustSlice(t, span0, "attributes") {
		am := mustMap(t, a)
		switch mustStr(t, am, "key") {
		case "host":
			if mustMap(t, am["value"])["stringValue"] == "ccu.local" {
				sawHost = true
			}
		case "password":
			if mustMap(t, am["value"])["stringValue"] == hmlog.RedactMask {
				sawMaskedPassword = true
			}
		}
	}
	if !sawHost {
		t.Error("non-sensitive attribute host must round-trip unmasked")
	}
	if !sawMaskedPassword {
		t.Error("sensitive attribute password must be masked in span attributes")
	}
}

// TestOTLPJSON_ChildSpanParentID verifies that a child span includes a
// 16-char parentSpanId in the OTLP payload.
func TestOTLPJSON_ChildSpanParentID(t *testing.T) {
	var received atomic.Pointer[[]byte]
	gate := make(chan struct{})
	var once sync.Once // capture only the first POST, race-free

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		once.Do(func() {
			snapshot := append([]byte(nil), body...)
			received.Store(&snapshot)
			close(gate)
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BatchSize:     2,
		FlushInterval: time.Hour,
		Client:        ts.Client(),
	})

	prev := observability.SetSpanExporter(exp)
	defer observability.SetSpanExporter(prev)

	parent, pCtx := observability.StartSpan(context.Background(), "parent", nil)
	child, _ := observability.StartSpan(pCtx, "child", nil)
	child.End()
	parent.End()

	select {
	case <-gate:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not receive POST")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	body := *received.Load()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	rs := mustSlice(t, doc, "resourceSpans")
	ss := mustSlice(t, mustMap(t, rs[0]), "scopeSpans")
	spans := mustSlice(t, mustMap(t, ss[0]), "spans")

	for _, raw := range spans {
		s := mustMap(t, raw)
		if mustStr(t, s, "name") == "child" {
			pid, ok := s["parentSpanId"]
			if !ok {
				t.Fatal("child span missing parentSpanId")
			}
			pidStr, ok := pid.(string)
			if !ok || len(pidStr) != 16 {
				t.Errorf("child parentSpanId=%q, want 16 hex chars", pidStr)
			}
			return
		}
	}
	t.Error("child span not found in OTLP payload")
}

// TestShutdown_FlushesAndIsIdempotent verifies Shutdown drains a pending
// batch and that a second call returns nil without panic.
func TestShutdown_FlushesAndIsIdempotent(t *testing.T) {
	var count atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BatchSize:     100, // large batch — won't auto-flush
		FlushInterval: time.Hour,
		Client:        ts.Client(),
	})

	sp, _ := observability.StartSpan(context.Background(), "flush_test", nil)
	sp.End()
	exp.ExportSpan(sp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}

	// The batch must have been flushed.
	if count.Load() == 0 {
		t.Error("expected at least one POST after Shutdown flush")
	}

	// Second call must return nil, not panic.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := exp.Shutdown(ctx2); err != nil {
		t.Errorf("second Shutdown (idempotent) returned error: %v", err)
	}
}

// TestEnd_WithNoExporter_DoesNotPanic verifies that Span.End is safe
// when no exporter is registered (the default state).
func TestEnd_WithNoExporter_DoesNotPanic(t *testing.T) {
	// Ensure no exporter is installed.
	prev := observability.SetSpanExporter(nil)
	defer observability.SetSpanExporter(prev)

	sp, _ := observability.StartSpan(context.Background(), "noop_end", nil)
	// Must not panic.
	sp.End()
}

// TestSetSpanExporter_NilDisables verifies that installing nil removes the
// exporter so subsequent End() calls don't route spans anywhere.
func TestSetSpanExporter_NilDisables(t *testing.T) {
	posts := make(chan struct{}, 10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp := observability.NewOTLPHTTPExporter(observability.OTLPHTTPConfig{
		Endpoint:      ts.URL,
		BatchSize:     1,
		FlushInterval: time.Hour,
		Client:        ts.Client(),
	})

	// Install, then immediately remove.
	observability.SetSpanExporter(exp)
	observability.SetSpanExporter(nil)

	sp, _ := observability.StartSpan(context.Background(), "after_nil", nil)
	sp.End()

	// No POST should arrive because the exporter was removed before End.
	select {
	case <-posts:
		t.Error("received a POST after exporter was set to nil — export should be disabled")
	case <-time.After(200 * time.Millisecond):
		// correct: nothing was sent
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustSlice(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in map %v", key, m)
	}
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("key %q: want []any, got %T: %v", key, v, v)
	}
	return s
}

func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T: %v", v, v)
	}
	return m
}

func mustStr(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in %v", key, m)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q: want string, got %T: %v", key, v, v)
	}
	return s
}
