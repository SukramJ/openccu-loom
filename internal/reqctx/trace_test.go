// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reqctx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// --------------------------------------------------------------------------
// NewTraceID
// --------------------------------------------------------------------------

func TestNewTraceID_Length(t *testing.T) {
	id := reqctx.NewTraceID()
	if len(id) != 32 {
		t.Errorf("len = %d, want 32", len(id))
	}
}

func TestNewTraceID_LowercaseHex(t *testing.T) {
	id := reqctx.NewTraceID()
	for i, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("char[%d] = %q is not lowercase hex", i, c)
		}
	}
}

func TestNewTraceID_NotAllZero(t *testing.T) {
	for range 10 {
		if id := reqctx.NewTraceID(); id == "00000000000000000000000000000000" {
			t.Fatal("NewTraceID returned all-zero value")
		}
	}
}

func TestNewTraceID_UniquePerCall(t *testing.T) {
	a, b := reqctx.NewTraceID(), reqctx.NewTraceID()
	if a == b {
		t.Errorf("two consecutive calls returned the same ID: %q", a)
	}
}

// --------------------------------------------------------------------------
// NewSpanID
// --------------------------------------------------------------------------

func TestNewSpanID_Length(t *testing.T) {
	id := reqctx.NewSpanID()
	if len(id) != 16 {
		t.Errorf("len = %d, want 16", len(id))
	}
}

func TestNewSpanID_LowercaseHex(t *testing.T) {
	id := reqctx.NewSpanID()
	for i, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("char[%d] = %q is not lowercase hex", i, c)
		}
	}
}

func TestNewSpanID_NotAllZero(t *testing.T) {
	for range 10 {
		if id := reqctx.NewSpanID(); id == "0000000000000000" {
			t.Fatal("NewSpanID returned all-zero value")
		}
	}
}

func TestNewSpanID_UniquePerCall(t *testing.T) {
	a, b := reqctx.NewSpanID(), reqctx.NewSpanID()
	if a == b {
		t.Errorf("two consecutive calls returned the same ID: %q", a)
	}
}

// --------------------------------------------------------------------------
// ParseTraceparent
// --------------------------------------------------------------------------

func TestParseTraceparent_Valid(t *testing.T) {
	traceID, spanID, flags, ok := reqctx.ParseTraceparent(
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("traceID = %q", traceID)
	}
	if spanID != "00f067aa0ba902b7" {
		t.Errorf("spanID = %q", spanID)
	}
	if flags != "01" {
		t.Errorf("flags = %q", flags)
	}
}

func TestParseTraceparent_MixedCase_OutputLowercase(t *testing.T) {
	_, spanID, _, ok := reqctx.ParseTraceparent(
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01",
	)
	if !ok {
		t.Fatal("ok = false for mixed-case input, want true")
	}
	if spanID != strings.ToLower(spanID) {
		t.Errorf("spanID output is not lowercase: %q", spanID)
	}
}

var parseTraceparentInvalidCases = []struct {
	name   string
	header string
}{
	{"empty", ""},
	{"too_few_parts", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"},
	{"too_many_parts", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra"},
	{"trace_id_too_short", "00-4bf92f3577b34da6a3ce929d0e0e473-00f067aa0ba902b7-01"},
	{"trace_id_too_long", "00-4bf92f3577b34da6a3ce929d0e0e47360-00f067aa0ba902b7-01"},
	{"span_id_too_short", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b-01"},
	{"span_id_too_long", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b700-01"},
	{"non_hex_trace_id", "00-4bf92f3577b34da6a3ce929d0e0e473z-00f067aa0ba902b7-01"},
	{"non_hex_span_id", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902bz-01"},
	{"all_zero_trace_id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
	{"all_zero_span_id", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
	{"dot_separator", "00.4bf92f3577b34da6a3ce929d0e0e4736.00f067aa0ba902b7.01"},
	{"space_inside_parts", "00-4bf92f3577b34da6a3 ce929d0e0e4736-00f067aa0ba902b7-01"},
}

func TestParseTraceparent_Invalid(t *testing.T) {
	for _, tc := range parseTraceparentInvalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, ok := reqctx.ParseTraceparent(tc.header)
			if ok {
				t.Errorf("ParseTraceparent(%q) = ok, want false", tc.header)
			}
		})
	}
}

func TestParseTraceparent_WhitespaceTrimmed(t *testing.T) {
	_, _, _, ok := reqctx.ParseTraceparent(
		"  00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01  ",
	)
	if !ok {
		t.Error("ParseTraceparent should accept leading/trailing whitespace")
	}
}

// --------------------------------------------------------------------------
// FormatTraceparent
// --------------------------------------------------------------------------

func TestFormatTraceparent_Sampled(t *testing.T) {
	out := reqctx.FormatTraceparent(
		"4bf92f3577b34da6a3ce929d0e0e4736",
		"00f067aa0ba902b7",
		true,
	)
	if !strings.HasSuffix(out, "-01") {
		t.Errorf("sampled=true output should end with -01; got %q", out)
	}
}

func TestFormatTraceparent_NotSampled(t *testing.T) {
	out := reqctx.FormatTraceparent(
		"4bf92f3577b34da6a3ce929d0e0e4736",
		"00f067aa0ba902b7",
		false,
	)
	if !strings.HasSuffix(out, "-00") {
		t.Errorf("sampled=false output should end with -00; got %q", out)
	}
}

func TestFormatTraceparent_Roundtrip(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID := "00f067aa0ba902b7"
	header := reqctx.FormatTraceparent(traceID, spanID, true)

	gotTrace, gotSpan, _, ok := reqctx.ParseTraceparent(header)
	if !ok {
		t.Fatalf("ParseTraceparent on formatted header returned ok=false: %q", header)
	}
	if gotTrace != traceID {
		t.Errorf("traceID after roundtrip = %q, want %q", gotTrace, traceID)
	}
	if gotSpan != spanID {
		t.Errorf("spanID after roundtrip = %q, want %q", gotSpan, spanID)
	}
}

// --------------------------------------------------------------------------
// StartChildSpan
// --------------------------------------------------------------------------

func TestStartChildSpan_EmptyCtx_CreatesFreshTrace(t *testing.T) {
	ctx := reqctx.StartChildSpan(context.Background())
	rc, ok := reqctx.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned false after StartChildSpan on empty ctx")
	}
	if rc.TraceID == "" {
		t.Error("TraceID should be set for fresh context")
	}
	if rc.SpanID == "" {
		t.Error("SpanID should be set for fresh context")
	}
	if rc.ParentSpanID != "" {
		t.Errorf("ParentSpanID should be empty for root span; got %q", rc.ParentSpanID)
	}
}

func TestStartChildSpan_WithExistingTrace_PreservesTraceID(t *testing.T) {
	parentTraceID := reqctx.NewTraceID()
	parentSpanID := reqctx.NewSpanID()

	rc0 := reqctx.RequestContext{
		RequestID: "r1",
		TraceID:   parentTraceID,
		SpanID:    parentSpanID,
	}
	ctx0 := reqctx.WithRequestContext(context.Background(), rc0)
	ctx1 := reqctx.StartChildSpan(ctx0)

	rc1, ok := reqctx.FromContext(ctx1)
	if !ok {
		t.Fatal("FromContext returned false after StartChildSpan")
	}
	if rc1.TraceID != parentTraceID {
		t.Errorf("TraceID = %q, want %q (must be preserved)", rc1.TraceID, parentTraceID)
	}
	if rc1.ParentSpanID != parentSpanID {
		t.Errorf("ParentSpanID = %q, want %q", rc1.ParentSpanID, parentSpanID)
	}
	if rc1.SpanID == parentSpanID {
		t.Errorf("SpanID should differ from parent; got %q", rc1.SpanID)
	}
	if rc1.SpanID == "" {
		t.Error("SpanID must not be empty after StartChildSpan")
	}
}

func TestStartChildSpan_EmptyTraceID_GetsNewTraceID(t *testing.T) {
	rc0 := reqctx.RequestContext{
		RequestID: "r2",
		TraceID:   "",
		SpanID:    "",
	}
	ctx0 := reqctx.WithRequestContext(context.Background(), rc0)
	ctx1 := reqctx.StartChildSpan(ctx0)

	rc1, ok := reqctx.FromContext(ctx1)
	if !ok {
		t.Fatal("FromContext returned false")
	}
	if rc1.TraceID == "" {
		t.Error("TraceID should be generated when it was empty before StartChildSpan")
	}
}

// --------------------------------------------------------------------------
// TraceparentFromContext
// --------------------------------------------------------------------------

func TestTraceparentFromContext_EmptyCtx(t *testing.T) {
	if got := reqctx.TraceparentFromContext(context.Background()); got != "" {
		t.Errorf("TraceparentFromContext on empty ctx = %q, want empty", got)
	}
}

func TestTraceparentFromContext_WithTrace(t *testing.T) {
	traceID := reqctx.NewTraceID()
	spanID := reqctx.NewSpanID()
	rc := reqctx.RequestContext{TraceID: traceID, SpanID: spanID}
	ctx := reqctx.WithRequestContext(context.Background(), rc)

	got := reqctx.TraceparentFromContext(ctx)
	if got == "" {
		t.Fatal("TraceparentFromContext returned empty string for context with trace")
	}
	want := "00-" + traceID + "-" + spanID + "-01"
	if got != want {
		t.Errorf("TraceparentFromContext = %q, want %q", got, want)
	}
}

func TestTraceparentFromContext_EmptySpanID(t *testing.T) {
	rc := reqctx.RequestContext{TraceID: reqctx.NewTraceID(), SpanID: ""}
	ctx := reqctx.WithRequestContext(context.Background(), rc)

	if got := reqctx.TraceparentFromContext(ctx); got != "" {
		t.Errorf("TraceparentFromContext with empty SpanID = %q, want empty", got)
	}
}

func TestTraceparentFromContext_EmptyTraceID(t *testing.T) {
	rc := reqctx.RequestContext{TraceID: "", SpanID: reqctx.NewSpanID()}
	ctx := reqctx.WithRequestContext(context.Background(), rc)

	if got := reqctx.TraceparentFromContext(ctx); got != "" {
		t.Errorf("TraceparentFromContext with empty TraceID = %q, want empty", got)
	}
}
