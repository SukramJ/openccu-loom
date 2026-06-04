// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// newJSONLogger returns a Debug-level JSON slog.Logger writing into buf.
func newJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// parseLines splits buf content into a slice of parsed JSON maps — one per
// log line. Empty trailing lines are skipped.
func parseLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range bytes.SplitSeq(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("failed to parse log line %q: %v", line, err)
		}
		records = append(records, m)
	}
	return records
}

// --------------------------------------------------------------------------
// 1. Start record is Debug-level with msg="op.start" and op attribute
// --------------------------------------------------------------------------

func TestStartOp_StartRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)
	ctx, closer := hmlog.StartOp(context.Background(), "test.op", hmlog.OpOptions{Logger: logger})
	defer closer(nil)

	records := parseLines(t, &buf)
	if len(records) < 1 {
		t.Fatal("expected at least one log record after StartOp")
	}
	start := records[0]
	if got := start["msg"]; got != "op.start" {
		t.Errorf("msg = %q, want %q", got, "op.start")
	}
	if got := start["level"]; got != "DEBUG" {
		t.Errorf("level = %q, want DEBUG", got)
	}
	if got := start["op"]; got != "test.op" {
		t.Errorf("op = %q, want %q", got, "test.op")
	}
	_ = ctx
}

// --------------------------------------------------------------------------
// 2. Child span: returned ctx has a different SpanID; ParentSpanID is set
// --------------------------------------------------------------------------

func TestStartOp_ChildSpan_WithParent(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	parentRC := reqctx.RequestContext{
		TraceID: reqctx.NewTraceID(),
		SpanID:  "aabbccdd11223344",
	}
	parentCtx := reqctx.WithRequestContext(context.Background(), parentRC)

	childCtx, closer := hmlog.StartOp(parentCtx, "child.op", hmlog.OpOptions{Logger: logger})
	defer closer(nil)

	childRC, ok := reqctx.FromContext(childCtx)
	if !ok {
		t.Fatal("returned ctx has no RequestContext")
	}
	if childRC.SpanID == "" {
		t.Error("child SpanID must not be empty")
	}
	if childRC.SpanID == parentRC.SpanID {
		t.Errorf("child SpanID %q must differ from parent SpanID %q", childRC.SpanID, parentRC.SpanID)
	}
	if childRC.ParentSpanID != parentRC.SpanID {
		t.Errorf("ParentSpanID = %q, want %q", childRC.ParentSpanID, parentRC.SpanID)
	}
}

func TestStartOp_ChildSpan_NoParent(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	childCtx, closer := hmlog.StartOp(context.Background(), "root.op", hmlog.OpOptions{Logger: logger})
	defer closer(nil)

	rc, ok := reqctx.FromContext(childCtx)
	if !ok {
		t.Fatal("returned ctx has no RequestContext")
	}
	if rc.SpanID == "" {
		t.Error("SpanID must be assigned even without prior RequestContext")
	}
	// No parent context means ParentSpanID must be empty.
	if rc.ParentSpanID != "" {
		t.Errorf("ParentSpanID = %q, want empty for root span", rc.ParentSpanID)
	}
}

// --------------------------------------------------------------------------
// 3. closer(nil) → Debug end record, outcome="ok", elapsed_ms >= 0
// --------------------------------------------------------------------------

func TestStartOp_Closer_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "ok.op", hmlog.OpOptions{Logger: logger})
	closer(nil)

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records (start+end), got %d", len(records))
	}
	end := records[1]
	if got := end["msg"]; got != "op.end" {
		t.Errorf("msg = %q, want %q", got, "op.end")
	}
	if got := end["level"]; got != "DEBUG" {
		t.Errorf("level = %q, want DEBUG", got)
	}
	if got := end["outcome"]; got != "ok" {
		t.Errorf("outcome = %q, want %q", got, "ok")
	}
	elapsedRaw, exists := end["elapsed_ms"]
	if !exists {
		t.Fatal("elapsed_ms must be present in end record")
	}
	elapsed, ok := elapsedRaw.(float64)
	if !ok {
		t.Fatalf("elapsed_ms is not a number: %T", elapsedRaw)
	}
	if elapsed < 0 {
		t.Errorf("elapsed_ms = %v, must be >= 0", elapsed)
	}
}

// --------------------------------------------------------------------------
// 4. closer(err) → Error level, outcome="error"
// --------------------------------------------------------------------------

func TestStartOp_Closer_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "fail.op", hmlog.OpOptions{Logger: logger})
	closer(errors.New("something went wrong"))

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	end := records[1]
	if got := end["level"]; got != "ERROR" {
		t.Errorf("level = %q, want ERROR", got)
	}
	if got := end["outcome"]; got != "error" {
		t.Errorf("outcome = %q, want %q", got, "error")
	}
}

// --------------------------------------------------------------------------
// 5. SlowThreshold > 0 and elapsed >= threshold → Warn + outcome="slow"
// --------------------------------------------------------------------------

func TestStartOp_SlowThreshold_Exceeded(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "slow.op", hmlog.OpOptions{
		Logger:        logger,
		SlowThreshold: 1 * time.Nanosecond,
	})
	// Busy-wait to ensure at least 1 ns has elapsed.
	deadline := time.Now().Add(10 * time.Millisecond)
	for time.Now().Before(deadline) {
	}
	closer(nil)

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	end := records[1]
	if got := end["level"]; got != "WARN" {
		t.Errorf("level = %q, want WARN", got)
	}
	if got := end["outcome"]; got != "slow" {
		t.Errorf("outcome = %q, want %q", got, "slow")
	}
}

// --------------------------------------------------------------------------
// 6. SlowThreshold < 0 and closer(nil) → no end record (success suppressed)
// --------------------------------------------------------------------------

func TestStartOp_NegativeThreshold_SuccessSuppressed(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "suppress.op", hmlog.OpOptions{
		Logger:        logger,
		SlowThreshold: -1,
	})
	closer(nil)

	records := parseLines(t, &buf)
	for _, r := range records {
		if r["msg"] == "op.end" {
			t.Errorf("op.end must not be emitted when SlowThreshold < 0 and closer(nil); got: %v", r)
		}
	}
}

// --------------------------------------------------------------------------
// 7. SlowThreshold < 0 and closer(err) → end record IS emitted
// --------------------------------------------------------------------------

func TestStartOp_NegativeThreshold_ErrorStillLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "suppress-err.op", hmlog.OpOptions{
		Logger:        logger,
		SlowThreshold: -1,
	})
	closer(errors.New("still an error"))

	records := parseLines(t, &buf)
	var found bool
	for _, r := range records {
		if r["msg"] == "op.end" {
			found = true
			if got := r["level"]; got != "ERROR" {
				t.Errorf("level = %q, want ERROR", got)
			}
		}
	}
	if !found {
		t.Error("op.end must be emitted for error even when SlowThreshold < 0")
	}
}

// --------------------------------------------------------------------------
// 8. Double-close is a no-op: second call emits nothing
// --------------------------------------------------------------------------

func TestStartOp_DoubleClose_NoOp(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "double.op", hmlog.OpOptions{Logger: logger})
	closer(nil)
	buf.Reset() // discard start + first end
	closer(nil) // second call — must not log
	if buf.Len() != 0 {
		t.Errorf("second closer call must not emit any log; got: %s", buf.String())
	}
}

// --------------------------------------------------------------------------
// 9. Nil Logger falls back to slog.Default without panic
// --------------------------------------------------------------------------

func TestStartOp_NilLogger_FallsBackToDefault(t *testing.T) {
	// Replace the default logger with one that discards output; we only
	// verify that no panic occurs when Logger is nil.
	origDefault := slog.Default()
	defer slog.SetDefault(origDefault)

	var buf bytes.Buffer
	slog.SetDefault(newJSONLogger(&buf))

	_, closer := hmlog.StartOp(context.Background(), "nil-logger.op", hmlog.OpOptions{})
	closer(nil) // must not panic
}

// --------------------------------------------------------------------------
// 10. opts.Attrs appear in both start and end records
// --------------------------------------------------------------------------

func TestStartOp_Attrs_InStartAndEnd(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "attrs.op", hmlog.OpOptions{
		Logger: logger,
		Attrs:  []slog.Attr{slog.String("iface", "HmIP-RF"), slog.Int("channel", 4)},
	})
	closer(nil)

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for i, label := range []string{"start", "end"} {
		r := records[i]
		if got := r["iface"]; got != "HmIP-RF" {
			t.Errorf("[%s] iface = %q, want %q", label, got, "HmIP-RF")
		}
		if got, _ := r["channel"].(float64); got != 4 {
			t.Errorf("[%s] channel = %v, want 4", label, r["channel"])
		}
	}
}

// --------------------------------------------------------------------------
// 11. context.Canceled → outcome="cancelled"; DeadlineExceeded → "timeout"
// --------------------------------------------------------------------------

func TestStartOp_Closer_Cancelled(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "cancel.op", hmlog.OpOptions{Logger: logger})
	closer(context.Canceled)

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if got := records[1]["outcome"]; got != "cancelled" {
		t.Errorf("outcome = %q, want %q", got, "cancelled")
	}
}

func TestStartOp_Closer_Timeout(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "timeout.op", hmlog.OpOptions{Logger: logger})
	closer(context.DeadlineExceeded)

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if got := records[1]["outcome"]; got != "timeout" {
		t.Errorf("outcome = %q, want %q", got, "timeout")
	}
}

// Wrapped errors must also be classified via errors.Is.
func TestStartOp_Closer_WrappedCancelled(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	_, closer := hmlog.StartOp(context.Background(), "wrapped-cancel.op", hmlog.OpOptions{Logger: logger})
	closer(errors.Join(errors.New("outer"), context.Canceled))

	records := parseLines(t, &buf)
	if len(records) < 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if got := records[1]["outcome"]; got != "cancelled" {
		t.Errorf("outcome = %q, want %q", got, "cancelled")
	}
}

// --------------------------------------------------------------------------
// 12. Operation name is set in RequestContext.Operation
// --------------------------------------------------------------------------

func TestStartOp_OperationSetInRequestContext(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	childCtx, closer := hmlog.StartOp(context.Background(), "my.operation", hmlog.OpOptions{Logger: logger})
	defer closer(nil)

	rc, ok := reqctx.FromContext(childCtx)
	if !ok {
		t.Fatal("returned ctx has no RequestContext")
	}
	if rc.Operation != "my.operation" {
		t.Errorf("Operation = %q, want %q", rc.Operation, "my.operation")
	}
}

// Operation in pre-existing RequestContext is overwritten.
func TestStartOp_OperationOverwritesPrevious(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	parentRC := reqctx.RequestContext{
		TraceID:   reqctx.NewTraceID(),
		SpanID:    reqctx.NewSpanID(),
		Operation: "old.operation",
	}
	parentCtx := reqctx.WithRequestContext(context.Background(), parentRC)

	childCtx, closer := hmlog.StartOp(parentCtx, "new.operation", hmlog.OpOptions{Logger: logger})
	defer closer(nil)

	rc, ok := reqctx.FromContext(childCtx)
	if !ok {
		t.Fatal("returned ctx has no RequestContext")
	}
	if rc.Operation != "new.operation" {
		t.Errorf("Operation = %q, want %q", rc.Operation, "new.operation")
	}
}
