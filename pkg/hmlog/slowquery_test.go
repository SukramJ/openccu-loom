// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// --------------------------------------------------------------------------
// 1. Below threshold: no log record emitted
// --------------------------------------------------------------------------

func TestWatchSlow_BelowThreshold_NoRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	hmlog.WatchSlow(context.Background(), logger, "fast", 1*time.Hour)()

	if buf.Len() != 0 {
		t.Errorf("expected no log output below threshold; got: %s", buf.String())
	}
}

// --------------------------------------------------------------------------
// 2. Above threshold: Warn record with expected fields
// --------------------------------------------------------------------------

func TestWatchSlow_AboveThreshold_WarnRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	done := hmlog.WatchSlow(context.Background(), logger, "slow", 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	done()

	records := parseLines(t, &buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	r := records[0]
	if got := r["level"]; got != "WARN" {
		t.Errorf("level = %q, want WARN", got)
	}
	if got := r["msg"]; got != "query.slow" {
		t.Errorf("msg = %q, want %q", got, "query.slow")
	}
	if got := r["op"]; got != "slow" {
		t.Errorf("op = %q, want %q", got, "slow")
	}
	elapsedRaw, ok := r["elapsed_ms"]
	if !ok {
		t.Fatal("elapsed_ms must be present")
	}
	if elapsed, _ := elapsedRaw.(float64); elapsed < 0 {
		t.Errorf("elapsed_ms = %v, must be >= 0", elapsed)
	}
	thresholdRaw, ok := r["threshold_ms"]
	if !ok {
		t.Fatal("threshold_ms must be present")
	}
	if threshold, _ := thresholdRaw.(float64); threshold != 0 {
		t.Errorf("threshold_ms = %v, want 0 (1ns rounds to 0 ms)", threshold)
	}
}

// --------------------------------------------------------------------------
// 3. Nil logger falls back to slog.Default — no panic
// --------------------------------------------------------------------------

func TestWatchSlow_NilLogger_NoPanel(t *testing.T) {
	origDefault := slog.Default()
	defer slog.SetDefault(origDefault)

	var buf bytes.Buffer
	slog.SetDefault(newJSONLogger(&buf))

	// Must not panic regardless of outcome.
	done := hmlog.WatchSlow(context.Background(), nil, "nil-logger", 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	done()
	// We just verify no panic occurred; output lands in the default logger.
}

// --------------------------------------------------------------------------
// 4. Threshold <= 0 → defaults to DefaultSlowQueryThreshold (100 ms)
// --------------------------------------------------------------------------

func TestWatchSlow_ZeroThreshold_UsesDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf)

	// With threshold=0 the default (100 ms) applies, so a fast call emits nothing.
	hmlog.WatchSlow(context.Background(), logger, "zero-fast", 0)()
	if buf.Len() != 0 {
		t.Errorf("expected no record for fast call with zero threshold; got: %s", buf.String())
	}

	// Sleep past the default threshold.
	buf.Reset()
	done := hmlog.WatchSlow(context.Background(), logger, "zero-slow", 0)
	time.Sleep(hmlog.DefaultSlowQueryThreshold + 5*time.Millisecond)
	done()

	records := parseLines(t, &buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 record after exceeding default threshold; got %d", len(records))
	}
	if got := records[0]["op"]; got != "zero-slow" {
		t.Errorf("op = %q, want %q", got, "zero-slow")
	}
}

// --------------------------------------------------------------------------
// 5. TraceID + SpanID propagation via ContextHandler
// --------------------------------------------------------------------------

func TestWatchSlow_TraceIDPropagation(t *testing.T) {
	var buf bytes.Buffer
	// Wire hmreqctx.NewContextHandler so trace fields are injected.
	jsonHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(hmreqctx.NewContextHandler(jsonHandler))

	rc := hmreqctx.RequestContext{
		TraceID: hmreqctx.NewTraceID(),
		SpanID:  hmreqctx.NewSpanID(),
	}
	ctx := hmreqctx.WithRequestContext(context.Background(), rc)

	done := hmlog.WatchSlow(ctx, logger, "traced", 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	done()

	records := parseLines(t, &buf)
	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	r := records[0]
	if got := r["trace_id"]; got != rc.TraceID {
		t.Errorf("trace_id = %q, want %q", got, rc.TraceID)
	}
	if got := r["span_id"]; got != rc.SpanID {
		t.Errorf("span_id = %q, want %q", got, rc.SpanID)
	}
}
