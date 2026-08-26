// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package observer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time interface assertions.
var (
	_ interfaces.TransportObserver = (*observer.Logging)(nil)
	_ interfaces.TransportObserver = (*observer.Multi)(nil)
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

// defaultInfo returns a fully-populated RequestInfo for use in tests.
func defaultInfo() interfaces.RequestInfo {
	return interfaces.RequestInfo{
		Protocol:  "xml-rpc",
		Method:    "getValue",
		Host:      "ccu:2010",
		Interface: "HmIP-RF",
	}
}

// =============================================================================
// Logging observer
// =============================================================================

// TestLogging_Lifecycle verifies OnRequestStart returns a non-nil span and
// that OnRequestEnd consumes it without panicking.
func TestLogging_Lifecycle(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(observer.WithLogger(newJSONLogger(&buf)))

	span := obs.OnRequestStart(context.Background(), defaultInfo())
	if span == nil {
		t.Fatal("OnRequestStart must return a non-nil span")
	}
	// Must not panic.
	obs.OnRequestEnd(span, interfaces.RequestResult{})
}

// TestLogging_StartRecord checks that the start record is Debug-level with
// msg="op.start" and the expected attributes.
func TestLogging_StartRecord(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(observer.WithLogger(newJSONLogger(&buf)))

	span := obs.OnRequestStart(context.Background(), defaultInfo())
	defer obs.OnRequestEnd(span, interfaces.RequestResult{})

	records := parseLines(t, &buf)
	if len(records) < 1 {
		t.Fatal("expected at least one log record after OnRequestStart")
	}
	r := records[0]

	if got := r["msg"]; got != "op.start" {
		t.Errorf("msg = %q, want %q", got, "op.start")
	}
	if got := r["level"]; got != "DEBUG" {
		t.Errorf("level = %q, want DEBUG", got)
	}
	// op attribute must be "<protocol>.<method>"
	if got := r["op"]; got != "xml-rpc.getValue" {
		t.Errorf("op = %q, want %q", got, "xml-rpc.getValue")
	}
	if got := r["protocol"]; got != "xml-rpc" {
		t.Errorf("protocol = %q, want %q", got, "xml-rpc")
	}
	if got := r["rpc_method"]; got != "getValue" {
		t.Errorf("rpc_method = %q, want %q", got, "getValue")
	}
	if got := r["interface_id"]; got != "HmIP-RF" {
		t.Errorf("interface_id = %q, want %q", got, "HmIP-RF")
	}
	if got := r["host"]; got != "ccu:2010" {
		t.Errorf("host = %q, want %q", got, "ccu:2010")
	}
}

// TestLogging_EndRecord_Success checks the end record for a successful call:
// Debug level, outcome="ok", elapsed_ms >= 0.
func TestLogging_EndRecord_Success(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(observer.WithLogger(newJSONLogger(&buf)))

	span := obs.OnRequestStart(context.Background(), defaultInfo())
	obs.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

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

// TestLogging_EndRecord_Error checks the end record for a failed call:
// Error level, outcome="error", err attribute present.
func TestLogging_EndRecord_Error(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(observer.WithLogger(newJSONLogger(&buf)))

	span := obs.OnRequestStart(context.Background(), defaultInfo())
	obs.OnRequestEnd(span, interfaces.RequestResult{Err: errors.New("connection refused")})

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
	errVal, exists := end["err"]
	if !exists {
		t.Fatal("err attribute must be present on error end record")
	}
	if errVal == "" || errVal == nil {
		t.Errorf("err attribute must be non-empty; got %v", errVal)
	}
}

// TestLogging_SlowThreshold checks that a call exceeding the threshold
// produces a Warn-level end record with outcome="slow".
func TestLogging_SlowThreshold(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(
		observer.WithLogger(newJSONLogger(&buf)),
		observer.WithSlowThreshold(1*time.Nanosecond),
	)

	span := obs.OnRequestStart(context.Background(), defaultInfo())
	time.Sleep(1 * time.Millisecond)
	obs.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

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

// TestLogging_WithLogger verifies that a custom logger is used and its output
// lands in the correct buffer.
func TestLogging_WithLogger(t *testing.T) {
	var bufA, bufB bytes.Buffer
	loggerA := newJSONLogger(&bufA)
	// loggerB is not used — it serves as a control.
	_ = newJSONLogger(&bufB)

	obs := observer.NewLogging(observer.WithLogger(loggerA))
	span := obs.OnRequestStart(context.Background(), defaultInfo())
	obs.OnRequestEnd(span, interfaces.RequestResult{})

	if bufA.Len() == 0 {
		t.Error("expected output in bufA (the configured logger buffer)")
	}
	if bufB.Len() != 0 {
		t.Error("expected bufB to be empty (different logger not used)")
	}
}

// TestLogging_NilSpanInOnRequestEnd checks that passing nil to OnRequestEnd
// does not panic.
func TestLogging_NilSpanInOnRequestEnd(t *testing.T) {
	obs := observer.NewLogging()
	// Must not panic.
	obs.OnRequestEnd(nil, interfaces.RequestResult{})
}

// TestLogging_EmptyInterfaceAndHost checks that interface_id and host
// attributes are omitted when the corresponding RequestInfo fields are empty.
func TestLogging_EmptyInterfaceAndHost(t *testing.T) {
	var buf bytes.Buffer
	obs := observer.NewLogging(observer.WithLogger(newJSONLogger(&buf)))

	info := interfaces.RequestInfo{
		Protocol:  "bin-rpc",
		Method:    "listDevices",
		Host:      "",
		Interface: "",
	}
	span := obs.OnRequestStart(context.Background(), info)
	defer obs.OnRequestEnd(span, interfaces.RequestResult{})

	records := parseLines(t, &buf)
	if len(records) < 1 {
		t.Fatal("expected at least one log record")
	}
	r := records[0]

	if _, ok := r["interface_id"]; ok {
		t.Error("interface_id must be absent when Interface is empty")
	}
	if _, ok := r["host"]; ok {
		t.Error("host must be absent when Host is empty")
	}
	// protocol and rpc_method are always present.
	if got := r["protocol"]; got != "bin-rpc" {
		t.Errorf("protocol = %q, want %q", got, "bin-rpc")
	}
	if got := r["rpc_method"]; got != "listDevices" {
		t.Errorf("rpc_method = %q, want %q", got, "listDevices")
	}
}

// =============================================================================
// Multi observer
// =============================================================================

// TestMulti_FanOut verifies that OnRequestStart on a Multi with two observers
// delivers a start record to both.
func TestMulti_FanOut(t *testing.T) {
	var bufA, bufB bytes.Buffer
	obsA := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufA)))
	obsB := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufB)))

	m := observer.NewMulti(obsA, obsB)
	span := m.OnRequestStart(context.Background(), defaultInfo())
	defer m.OnRequestEnd(span, interfaces.RequestResult{})

	if bufA.Len() == 0 {
		t.Error("obsA must have received a start record")
	}
	if bufB.Len() == 0 {
		t.Error("obsB must have received a start record")
	}

	recsA := parseLines(t, &bufA)
	if len(recsA) < 1 || recsA[0]["msg"] != "op.start" {
		t.Errorf("obsA start record missing or wrong msg; records: %v", recsA)
	}
	recsB := parseLines(t, &bufB)
	if len(recsB) < 1 || recsB[0]["msg"] != "op.start" {
		t.Errorf("obsB start record missing or wrong msg; records: %v", recsB)
	}
}

// TestMulti_EndDistribution verifies that after OnRequestStart, calling
// OnRequestEnd distributes the result to both observers.
func TestMulti_EndDistribution(t *testing.T) {
	var bufA, bufB bytes.Buffer
	obsA := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufA)))
	obsB := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufB)))

	m := observer.NewMulti(obsA, obsB)
	span := m.OnRequestStart(context.Background(), defaultInfo())
	m.OnRequestEnd(span, interfaces.RequestResult{Err: nil})

	recsA := parseLines(t, &bufA)
	if len(recsA) < 2 {
		t.Fatalf("obsA: expected 2 records, got %d", len(recsA))
	}
	if got := recsA[1]["msg"]; got != "op.end" {
		t.Errorf("obsA end record msg = %q, want op.end", got)
	}
	if got := recsA[1]["outcome"]; got != "ok" {
		t.Errorf("obsA end record outcome = %q, want ok", got)
	}

	recsB := parseLines(t, &bufB)
	if len(recsB) < 2 {
		t.Fatalf("obsB: expected 2 records, got %d", len(recsB))
	}
	if got := recsB[1]["msg"]; got != "op.end" {
		t.Errorf("obsB end record msg = %q, want op.end", got)
	}
	if got := recsB[1]["outcome"]; got != "ok" {
		t.Errorf("obsB end record outcome = %q, want ok", got)
	}
}

// TestMulti_NilEntriesSkipped verifies that nil entries passed to NewMulti
// are silently dropped and do not cause a panic in OnRequestStart.
func TestMulti_NilEntriesSkipped(t *testing.T) {
	var bufA, bufB bytes.Buffer
	obsA := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufA)))
	obsB := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufB)))

	m := observer.NewMulti(obsA, nil, obsB)
	// Must not panic.
	span := m.OnRequestStart(context.Background(), defaultInfo())
	m.OnRequestEnd(span, interfaces.RequestResult{})

	// Both real observers must have received records.
	if bufA.Len() == 0 {
		t.Error("obsA must have received records despite nil entry")
	}
	if bufB.Len() == 0 {
		t.Error("obsB must have received records despite nil entry")
	}
}

// TestMulti_EmptyList verifies that NewMulti() with no observers returns nil
// from OnRequestStart and that OnRequestEnd is a no-op.
func TestMulti_EmptyList(t *testing.T) {
	m := observer.NewMulti()

	span := m.OnRequestStart(context.Background(), defaultInfo())
	if span != nil {
		t.Errorf("OnRequestStart on empty Multi must return nil, got %v", span)
	}
	// Must not panic.
	m.OnRequestEnd(span, interfaces.RequestResult{})
}

// TestMulti_EndDistribution_TypeMismatchedSpan verifies that passing a
// type-mismatched (nil) span to OnRequestEnd calls each observer with nil
// and does not panic.
func TestMulti_EndDistribution_TypeMismatchedSpan(t *testing.T) {
	var bufA, bufB bytes.Buffer
	obsA := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufA)))
	obsB := observer.NewLogging(observer.WithLogger(newJSONLogger(&bufB)))

	m := observer.NewMulti(obsA, obsB)
	// Skip OnRequestStart — pass nil directly to simulate a type-mismatched span.
	// Must not panic; each observer receives nil and should be a no-op.
	m.OnRequestEnd(nil, interfaces.RequestResult{Err: errors.New("transport error")})
}
