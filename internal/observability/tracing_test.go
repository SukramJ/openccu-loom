// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/observability"
)

// isLowerHex reports whether s consists solely of lowercase hex digits.
func isLowerHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// TestStartSpan_IDsMatchW3CSchema guards against the 32-bit collision
// risk of a truncated UUID span id: root spans must mint TraceID/SpanID
// using the same W3C Trace Context shape (128-bit / 64-bit lowercase
// hex, no dashes) [hmreqctx.NewTraceID] / [hmreqctx.NewSpanID] produce, so
// the whole daemon shares one collision-resistant ID scheme.
func TestStartSpan_IDsMatchW3CSchema(t *testing.T) {
	sp, _ := observability.StartSpan(context.Background(), "test_op", nil)
	if len(sp.TraceID) != 32 || !isLowerHex(sp.TraceID) {
		t.Errorf("TraceID = %q, want 32 lowercase hex chars", sp.TraceID)
	}
	if len(sp.SpanID) != 16 || !isLowerHex(sp.SpanID) {
		t.Errorf("SpanID = %q, want 16 lowercase hex chars", sp.SpanID)
	}
}

func TestStartSpan_RootSpan(t *testing.T) {
	sp, ctx := observability.StartSpan(context.Background(), "test_op", nil)
	if sp == nil {
		t.Fatal("StartSpan returned nil span")
	}
	if sp.Name != "test_op" {
		t.Errorf("Name = %q, want test_op", sp.Name)
	}
	if sp.TraceID == "" {
		t.Error("TraceID should not be empty for root span")
	}
	if sp.ParentSpanID != "" {
		t.Errorf("ParentSpanID = %q, want empty for root span", sp.ParentSpanID)
	}
	if !sp.IsRoot() {
		t.Error("IsRoot() should be true for root span")
	}
	// Context should store the span.
	retrieved, ok := observability.GetCurrentSpan(ctx)
	if !ok || retrieved != sp {
		t.Error("GetCurrentSpan should return the just-created span")
	}
}

func TestStartSpan_ChildInheritesTraceID(t *testing.T) {
	parent, parentCtx := observability.StartSpan(context.Background(), "parent", nil)
	child, _ := observability.StartSpan(parentCtx, "child", nil)

	if child.TraceID != parent.TraceID {
		t.Errorf("child TraceID %q != parent TraceID %q", child.TraceID, parent.TraceID)
	}
	if child.ParentSpanID != parent.SpanID {
		t.Errorf("child ParentSpanID %q != parent SpanID %q", child.ParentSpanID, parent.SpanID)
	}
	if child.IsRoot() {
		t.Error("child span should not be root")
	}
}

func TestSpan_End_SetsDuration(t *testing.T) {
	sp, _ := observability.StartSpan(context.Background(), "timed", nil)
	if sp.DurationMS() >= 0 {
		t.Error("DurationMS should be -1 before End")
	}
	time.Sleep(2 * time.Millisecond)
	sp.End()
	if sp.DurationMS() < 0 {
		t.Error("DurationMS should be ≥ 0 after End")
	}
}

func TestSpan_SetAttribute(t *testing.T) {
	sp, _ := observability.StartSpan(context.Background(), "attrs", nil)
	sp.SetAttribute("key", "value")
	attrs := sp.Attributes()
	if attrs["key"] != "value" {
		t.Errorf("Attribute key = %v, want value", attrs["key"])
	}
}

func TestSpan_AddEvent(t *testing.T) {
	// AddEvent must not panic.
	sp, _ := observability.StartSpan(context.Background(), "events", nil)
	sp.AddEvent("checkpoint", map[string]any{"step": 1})
}

func TestGetCurrentSpan_AbsentReturnsFalse(t *testing.T) {
	_, ok := observability.GetCurrentSpan(context.Background())
	if ok {
		t.Error("GetCurrentSpan on empty context should return false")
	}
}

func TestGetCurrentTraceID(t *testing.T) {
	sp, ctx := observability.StartSpan(context.Background(), "trace_id_test", nil)
	got := observability.GetCurrentTraceID(ctx)
	if got != sp.TraceID {
		t.Errorf("GetCurrentTraceID = %q, want %q", got, sp.TraceID)
	}
}

func TestGetCurrentTraceID_EmptyWhenAbsent(t *testing.T) {
	if got := observability.GetCurrentTraceID(context.Background()); got != "" {
		t.Errorf("GetCurrentTraceID on empty ctx = %q, want empty", got)
	}
}

func TestSetCurrentSpan_AndReset(t *testing.T) {
	sp, _ := observability.StartSpan(context.Background(), "manual", nil)
	ctx := observability.SetCurrentSpan(context.Background(), sp)
	retrieved, ok := observability.GetCurrentSpan(ctx)
	if !ok || retrieved != sp {
		t.Error("SetCurrentSpan did not store span in context")
	}
	ctx = observability.ResetCurrentSpan(ctx)
	_, ok = observability.GetCurrentSpan(ctx)
	if ok {
		t.Error("After ResetCurrentSpan, GetCurrentSpan should return false")
	}
}

func TestStartSpan_WithAttributes(t *testing.T) {
	attrs := map[string]any{"count": 5}
	sp, _ := observability.StartSpan(context.Background(), "with_attrs", attrs)
	got := sp.Attributes()
	if got["count"] != 5 {
		t.Errorf("initial attribute count = %v, want 5", got["count"])
	}
}
