// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// --------------------------------------------------------------------------
// RequestContextFilter (A7v4-02)
// --------------------------------------------------------------------------

func TestRequestContextFilter_EnrichesLogRecord(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	filter := NewRequestContextFilter(base)
	logger := slog.New(filter)

	rc := reqctx.RequestContext{
		RequestID: "req-abc",
		Operation: "test_op",
		StartedAt: time.Now().Add(-50 * time.Millisecond),
	}
	ctx := reqctx.WithRequestContext(context.Background(), rc)
	logger.InfoContext(ctx, "hello from filter")

	out := buf.String()
	if !strings.Contains(out, "request_id=req-abc") {
		t.Errorf("expected request_id=req-abc in log output; got: %s", out)
	}
	if !strings.Contains(out, "operation=test_op") {
		t.Errorf("expected operation=test_op in log output; got: %s", out)
	}
	if !strings.Contains(out, "elapsed_ms=") {
		t.Errorf("expected elapsed_ms= in log output; got: %s", out)
	}
	if !strings.Contains(out, "hello from filter") {
		t.Errorf("expected message in log output; got: %s", out)
	}
}

func TestRequestContextFilter_NoEnrichmentWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	filter := NewRequestContextFilter(base)
	logger := slog.New(filter)

	// No RequestContext in context — record should pass through unchanged.
	logger.InfoContext(context.Background(), "no context here")

	out := buf.String()
	if strings.Contains(out, "request_id=") {
		t.Errorf("unexpected request_id= in output without context; got: %s", out)
	}
	if !strings.Contains(out, "no context here") {
		t.Errorf("expected message in log output; got: %s", out)
	}
}

func TestRequestContextFilter_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	filter := NewRequestContextFilter(base)
	derived := filter.WithAttrs([]slog.Attr{slog.String("component", "test")})
	logger := slog.New(derived)

	rc := reqctx.RequestContext{RequestID: "attrs-test", StartedAt: time.Now()}
	ctx := reqctx.WithRequestContext(context.Background(), rc)
	logger.InfoContext(ctx, "attr-msg")

	out := buf.String()
	if !strings.Contains(out, "component=test") {
		t.Errorf("WithAttrs should propagate; got: %s", out)
	}
	if !strings.Contains(out, "request_id=attrs-test") {
		t.Errorf("enrichment should still work after WithAttrs; got: %s", out)
	}
}

func TestRequestContextFilter_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	filter := NewRequestContextFilter(base)
	derived := filter.WithGroup("grp")
	if derived == nil {
		t.Fatal("WithGroup returned nil")
	}
}

func TestRequestContextFilter_PanicOnNilInner(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRequestContextFilter(nil) should panic")
		}
	}()
	NewRequestContextFilter(nil)
}

func TestRequestContextFilter_ImplementsHandler(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, nil)
	var _ slog.Handler = NewRequestContextFilter(base)
}

// TestRequestContextFilter_TraceFields verifies that trace_id, span_id, and
// parent_span_id are emitted when the RequestContext carries them, and are
// absent when those fields are empty.
func TestRequestContextFilter_TraceFields(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID := "00f067aa0ba902b7"
	parentSpanID := "abcdef1234567890"

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
			base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			filter := NewRequestContextFilter(base)
			logger := slog.New(filter)

			ctx := reqctx.WithRequestContext(context.Background(), tc.rc)
			logger.InfoContext(ctx, "trace filter test")

			out := buf.String()
			hasTrace := strings.Contains(out, "trace_id=")
			hasSpan := strings.Contains(out, "span_id=")
			hasParent := strings.Contains(out, "parent_span_id=")

			if hasTrace != tc.wantTrace {
				t.Errorf("trace_id present=%v, want %v; output: %s", hasTrace, tc.wantTrace, out)
			}
			if hasSpan != tc.wantSpan {
				t.Errorf("span_id present=%v, want %v; output: %s", hasSpan, tc.wantSpan, out)
			}
			if hasParent != tc.wantParent {
				t.Errorf("parent_span_id present=%v, want %v; output: %s", hasParent, tc.wantParent, out)
			}

			if tc.wantTrace && !strings.Contains(out, traceID) {
				t.Errorf("expected trace_id value %q in output; got: %s", traceID, out)
			}
		})
	}
}
