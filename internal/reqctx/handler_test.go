// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reqctx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// TestContextHandlerInjectsRequestID verifies that ContextHandler enriches
// log records with request_id, operation, and elapsed_ms from the
// RequestContext stored in the context.
func TestContextHandlerInjectsRequestID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := reqctx.NewContextHandler(inner)
	logger := slog.New(h)

	rc := reqctx.RequestContext{
		RequestID: "req-abc",
		Operation: "put_paramset",
		StartedAt: time.Now().Add(-50 * time.Millisecond),
	}
	ctx := reqctx.WithRequestContext(context.Background(), rc)

	logger.InfoContext(ctx, "test message")

	line := buf.String()
	if line == "" {
		t.Fatal("no log output produced")
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &record); err != nil {
		t.Fatalf("log line is not valid JSON: %v\n%s", err, line)
	}

	if got, ok := record["request_id"]; !ok || got != "req-abc" {
		t.Errorf("request_id: got %v (present=%v), want %q", got, ok, "req-abc")
	}
	if got, ok := record["operation"]; !ok || got != "put_paramset" {
		t.Errorf("operation: got %v (present=%v), want %q", got, ok, "put_paramset")
	}
	if _, ok := record["elapsed_ms"]; !ok {
		t.Error("elapsed_ms attribute not injected")
	}
}

// TestContextHandlerNoContextPassthrough verifies that records without a
// RequestContext are logged without injected fields.
func TestContextHandlerNoContextPassthrough(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := reqctx.NewContextHandler(inner)
	logger := slog.New(h)

	logger.InfoContext(context.Background(), "no context here")

	line := buf.String()
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &record); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if _, ok := record["request_id"]; ok {
		t.Error("request_id should not be present when context has no RequestContext")
	}
}

// TestContextHandlerPanicsOnNilInner verifies the nil-safety guard.
func TestContextHandlerPanicsOnNilInner(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when inner handler is nil")
		}
	}()
	_ = reqctx.NewContextHandler(nil)
}

// TestContextHandlerInjectsTraceFields verifies that trace_id, span_id, and
// parent_span_id are emitted when the RequestContext carries them, and are
// absent when the fields are empty.
func TestContextHandlerInjectsTraceFields(t *testing.T) {
	traceID := reqctx.NewTraceID()
	spanID := reqctx.NewSpanID()
	parentSpanID := reqctx.NewSpanID()

	tests := []struct {
		name       string
		rc         reqctx.RequestContext
		wantTrace  bool
		wantSpan   bool
		wantParent bool
	}{
		{
			name: "all_trace_fields_present",
			rc: reqctx.RequestContext{
				RequestID:    "r1",
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
				StartedAt:    time.Now(),
			},
			wantTrace:  true,
			wantSpan:   true,
			wantParent: true,
		},
		{
			name: "no_trace_fields",
			rc: reqctx.RequestContext{
				RequestID: "r2",
				StartedAt: time.Now(),
			},
			wantTrace:  false,
			wantSpan:   false,
			wantParent: false,
		},
		{
			name: "trace_and_span_no_parent",
			rc: reqctx.RequestContext{
				RequestID: "r3",
				TraceID:   traceID,
				SpanID:    spanID,
				StartedAt: time.Now(),
			},
			wantTrace:  true,
			wantSpan:   true,
			wantParent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			h := reqctx.NewContextHandler(inner)
			logger := slog.New(h)

			ctx := reqctx.WithRequestContext(context.Background(), tc.rc)
			logger.InfoContext(ctx, "trace test")

			var record map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &record); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
			}

			_, hasTrace := record["trace_id"]
			_, hasSpan := record["span_id"]
			_, hasParent := record["parent_span_id"]

			if hasTrace != tc.wantTrace {
				t.Errorf("trace_id present=%v, want %v", hasTrace, tc.wantTrace)
			}
			if hasSpan != tc.wantSpan {
				t.Errorf("span_id present=%v, want %v", hasSpan, tc.wantSpan)
			}
			if hasParent != tc.wantParent {
				t.Errorf("parent_span_id present=%v, want %v", hasParent, tc.wantParent)
			}
		})
	}
}

// TestContextHandlerInjectsScopeFields pins the fields that make parallel
// work legible. CentralName in particular is the multi-CCU log-correlation
// key, but it is filled only where the scope is actually known: a route whose
// path names the central (via middleware.CentralScope, once chi has resolved
// the URL parameter) or a daemon wired to a single central (via
// middleware.ReqContextWithCentral at boot). A request that matches neither is
// logged without it. The enricher once dropped the field even when present, so
// two CCUs' call paths interleaved indistinguishably and no surface reported
// the scope. Each field stays absent when unset so untargeted records keep
// their existing shape.
func TestContextHandlerInjectsScopeFields(t *testing.T) {
	t.Parallel()

	emit := func(t *testing.T, rc reqctx.RequestContext) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		logger := slog.New(reqctx.NewContextHandler(
			slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		))
		logger.InfoContext(reqctx.WithRequestContext(context.Background(), rc), "test message")
		var record map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &record); err != nil {
			t.Fatalf("log line is not valid JSON: %v\n%s", err, buf.String())
		}
		return record
	}

	t.Run("scoped_request_carries_every_field", func(t *testing.T) {
		t.Parallel()
		record := emit(t, reqctx.RequestContext{
			RequestID:     "r1",
			Operation:     "put_paramset",
			CentralName:   "ccu-nord",
			InterfaceID:   "HmIP-RF",
			DeviceAddress: "0001ABCD:4",
			StartedAt:     time.Now(),
		})
		for field, want := range map[string]string{
			"central_name":   "ccu-nord",
			"interface_id":   "HmIP-RF",
			"device_address": "0001ABCD:4",
		} {
			if got, ok := record[field]; !ok || got != want {
				t.Errorf("%s: got %v (present=%v), want %q", field, got, ok, want)
			}
		}
	})

	t.Run("unscoped_request_omits_them", func(t *testing.T) {
		t.Parallel()
		record := emit(t, reqctx.RequestContext{RequestID: "r2", StartedAt: time.Now()})
		for _, field := range []string{"central_name", "interface_id", "device_address"} {
			if _, ok := record[field]; ok {
				t.Errorf("%s present on an unscoped request: %v", field, record[field])
			}
		}
	})
}
