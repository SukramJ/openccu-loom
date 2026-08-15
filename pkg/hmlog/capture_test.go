// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// --------------------------------------------------------------------------
// CaptureSink — construction
// --------------------------------------------------------------------------

func TestNewCaptureSink_DefaultsWhenMaxBytesZero(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	if s == nil {
		t.Fatal("expected non-nil sink")
	}
	// Append a single small line; if the budget were wrong the line
	// would be evicted immediately.
	s.Append([]byte(`{"msg":"hello"}`))
	if s.Events() != 1 {
		t.Errorf("events = %d, want 1", s.Events())
	}
}

func TestNewCaptureSink_DefaultsWhenMaxBytesNegative(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(-99, false)
	if s == nil {
		t.Fatal("expected non-nil sink")
	}
	s.Append([]byte(`{"msg":"hi"}`))
	if s.Events() != 1 {
		t.Errorf("events = %d, want 1", s.Events())
	}
}

// --------------------------------------------------------------------------
// CaptureSink — Append basics
// --------------------------------------------------------------------------

func TestCaptureSink_Append_NDJson(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	s.Append([]byte(`{"msg":"one"}`))
	snap := s.Snapshot()
	// Must end with exactly one newline.
	if !bytes.HasSuffix(snap, []byte("\n")) {
		t.Errorf("snapshot does not end with newline: %q", snap)
	}
	lines := bytes.Split(bytes.TrimRight(snap, "\n"), []byte("\n"))
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
}

func TestCaptureSink_Append_EmptyLineSkipped(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	s.Append([]byte(""))
	s.Append([]byte("\n"))
	s.Append([]byte("   ")) // non-empty, goes in
	if s.Events() != 1 {
		t.Errorf("events = %d, want 1 (two empty lines should be skipped)", s.Events())
	}
}

func TestCaptureSink_Append_AfterClose_Ignored(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	s.Append([]byte(`{"msg":"before"}`))
	s.Close()
	s.Append([]byte(`{"msg":"after"}`))
	if s.Events() != 1 {
		t.Errorf("events = %d, want 1 (post-close append must be dropped)", s.Events())
	}
}

// --------------------------------------------------------------------------
// CaptureSink — ring eviction
// --------------------------------------------------------------------------

func TestCaptureSink_RingEviction(t *testing.T) {
	t.Parallel()
	// Budget = 200 bytes.  Each line payload is 50 bytes of JSON
	// (+ 1 newline = 51 bytes per record).  After filling up, the sink
	// must discard the oldest lines so the newest survive.
	const maxBytes = 200
	s := hmlog.NewCaptureSink(maxBytes, false)

	// Build a line that is exactly 50 chars.
	base := `{"n":"` + strings.Repeat("x", 40) + `"}`
	for range 10 {
		s.Append([]byte(base))
	}

	// Total events must be 10.
	if got := s.Events(); got != 10 {
		t.Errorf("events = %d, want 10", got)
	}

	// Buffer must not exceed maxBytes.
	if got := s.Bytes(); got > maxBytes {
		t.Errorf("bytes = %d, exceeds maxBytes %d", got, maxBytes)
	}

	// At least one line must still be present.
	snap := s.Snapshot()
	if len(snap) == 0 {
		t.Fatal("snapshot is empty after ring eviction")
	}
}

// --------------------------------------------------------------------------
// CaptureSink — Snapshot is a detached copy
// --------------------------------------------------------------------------

func TestCaptureSink_SnapshotIsCopy(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	s.Append([]byte(`{"msg":"first"}`))

	snap1 := s.Snapshot()
	// Mutate the returned slice.
	if len(snap1) > 0 {
		snap1[0] = 'X'
	}

	snap2 := s.Snapshot()
	if len(snap2) > 0 && snap2[0] == 'X' {
		t.Error("snapshot is not a copy: mutation of first snapshot affected second")
	}
}

// --------------------------------------------------------------------------
// CaptureSink — Events counts evictions too
// --------------------------------------------------------------------------

func TestCaptureSink_EventsCountsEvicted(t *testing.T) {
	t.Parallel()
	// Tiny buffer: hold at most ~60 bytes.
	s := hmlog.NewCaptureSink(60, false)
	for range 5 {
		s.Append([]byte(`{"msg":"line"}`))
	}
	if got := s.Events(); got != 5 {
		t.Errorf("events = %d, want 5 (evicted events must still be counted)", got)
	}
}

// --------------------------------------------------------------------------
// CaptureSink — Bytes reflects current buffer size
// --------------------------------------------------------------------------

func TestCaptureSink_Bytes(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	if s.Bytes() != 0 {
		t.Errorf("fresh sink bytes = %d, want 0", s.Bytes())
	}
	line := []byte(`{"msg":"hello"}`)
	s.Append(line)
	// +1 for the trailing newline added by Append.
	want := len(line) + 1
	if got := s.Bytes(); got != want {
		t.Errorf("bytes = %d, want %d", got, want)
	}
}

// --------------------------------------------------------------------------
// CaptureSink — Close lets Snapshot work
// --------------------------------------------------------------------------

func TestCaptureSink_CloseAllowsSnapshot(t *testing.T) {
	t.Parallel()
	s := hmlog.NewCaptureSink(0, false)
	s.Append([]byte(`{"msg":"recorded"}`))
	s.Close()
	snap := s.Snapshot()
	if len(snap) == 0 {
		t.Error("Snapshot after Close must return buffered content")
	}
}

// --------------------------------------------------------------------------
// TeeHandler — nil inner panics
// --------------------------------------------------------------------------

func TestNewTeeHandler_NilInner_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil inner handler, got none")
		}
	}()
	hmlog.NewTeeHandler(nil) //nolint:staticcheck // intentional nil
}

// --------------------------------------------------------------------------
// TeeHandler — without Attach records go only to inner
// --------------------------------------------------------------------------

func TestTeeHandler_WithoutAttach_RecordsGoToInnerOnly(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	logger := slog.New(tee)
	logger.Info("test message")

	// Inner must have received the record.
	if innerBuf.Len() == 0 {
		t.Error("inner handler received nothing")
	}
	// No sink attached — attach a fresh sink and verify it is empty.
	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)
	// The message was logged before Attach, so sink must be empty.
	if sink.Bytes() != 0 {
		t.Errorf("sink has %d bytes before any post-attach log call, want 0", sink.Bytes())
	}
}

// TestTeeHandler_AttachReachesLoggersDerivedBeforeAttach pins that a capture
// started at runtime reaches loggers that were derived earlier via With().
//
// This is the production order: every subsystem takes its logger with
// With("logger", …) during boot, and the operator starts a capture much later
// through the REST endpoint. If each derived handler owns a private copy of
// the sink pointer, Attach on the root reaches none of them and the support
// archive silently contains only records logged through the root logger.
func TestTeeHandler_AttachReachesLoggersDerivedBeforeAttach(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	// Derived at boot, before any capture exists.
	derived := slog.New(tee).With("logger", "client.xmlrpc")
	grandchild := derived.WithGroup("wire").With("interface", "HmIP-RF")

	// Operator starts the capture afterwards.
	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)

	derived.Info("south-bound call")
	grandchild.Info("wire frame")

	if sink.Bytes() == 0 {
		t.Fatal("capture attached at runtime received nothing from loggers derived before the attach")
	}
	got := string(sink.Snapshot())
	if !strings.Contains(got, "south-bound call") {
		t.Errorf("sink is missing the With()-derived logger's record; got %q", got)
	}
	if !strings.Contains(got, "wire frame") {
		t.Errorf("sink is missing the WithGroup()-derived logger's record; got %q", got)
	}

	// Detach must reach the derived handlers too, or the capture never stops.
	tee.Detach()
	before := sink.Bytes()
	derived.Info("after detach")
	if sink.Bytes() != before {
		t.Error("records still reached the sink after Detach")
	}
}

// --------------------------------------------------------------------------
// TeeHandler — Attach + Handle records land in both
// --------------------------------------------------------------------------

func TestTeeHandler_AttachAndHandle(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)

	logger := slog.New(tee)
	logger.Info("tee message", "key", "val")

	// Inner must have the record.
	if innerBuf.Len() == 0 {
		t.Error("inner handler received nothing after Attach")
	}
	// Sink must have received the record as ndjson with expected fields.
	snap := sink.Snapshot()
	if len(snap) == 0 {
		t.Fatal("sink is empty after Attach + log")
	}
	var m map[string]any
	line := bytes.TrimRight(snap, "\n")
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("sink line is not valid JSON: %v — got %q", err, snap)
	}
	for _, field := range []string{"time", "level", "msg"} {
		if _, ok := m[field]; !ok {
			t.Errorf("sink JSON missing field %q", field)
		}
	}
	if m["msg"] != "tee message" {
		t.Errorf("msg = %q, want %q", m["msg"], "tee message")
	}
}

// --------------------------------------------------------------------------
// TeeHandler — Detach returns current sink, nil afterwards
// --------------------------------------------------------------------------

func TestTeeHandler_Detach(t *testing.T) {
	t.Parallel()
	inner := slog.NewJSONHandler(&bytes.Buffer{}, nil)
	tee := hmlog.NewTeeHandler(inner)

	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)

	got := tee.Detach()
	if got != sink {
		t.Errorf("Detach returned %p, want %p", got, sink)
	}
	if second := tee.Detach(); second != nil {
		t.Errorf("second Detach returned non-nil: %p", second)
	}
}

// --------------------------------------------------------------------------
// TeeHandler — WithAttrs inherits sink
// --------------------------------------------------------------------------

func TestTeeHandler_WithAttrs_InheritsSink(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)

	child := slog.New(tee.WithAttrs([]slog.Attr{slog.String("component", "test")}))
	child.Info("with-attrs message")

	if sink.Events() == 0 {
		t.Error("child logger from WithAttrs did not capture into sink")
	}
}

// --------------------------------------------------------------------------
// TeeHandler — WithGroup inherits sink
// --------------------------------------------------------------------------

func TestTeeHandler_WithGroup_InheritsSink(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	sink := hmlog.NewCaptureSink(0, false)
	tee.Attach(sink)

	child := slog.New(tee.WithGroup("grp"))
	child.Info("with-group message")

	if sink.Events() == 0 {
		t.Error("child logger from WithGroup did not capture into sink")
	}
}

// --------------------------------------------------------------------------
// Anonymisation — operator-identifying attrs are hashed, operations
// data (device_address, host, …) stays in clear text
// --------------------------------------------------------------------------

func TestTeeHandler_Anonymise_SubjectAndUsername(t *testing.T) {
	t.Parallel()
	var innerBuf bytes.Buffer
	inner := slog.NewJSONHandler(&innerBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tee := hmlog.NewTeeHandler(inner)

	sink := hmlog.NewCaptureSink(0, true)
	tee.Attach(sink)

	slog.New(tee).Info(
		"anon test",
		"subject", "admin",
		"username", "bob",
		"remote", "10.0.0.5",
		// Operations data — must stay in clear text.
		"device_address", "HEQ123",
		"host", "172.18.4.29",
		"other", "keep",
	)

	snap := sink.Snapshot()
	if len(snap) == 0 {
		t.Fatal("sink empty")
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimRight(snap, "\n"), &m); err != nil {
		t.Fatalf("unmarshal: %v — raw: %q", err, snap)
	}

	// PII fields are hashed.
	for _, k := range []string{"subject", "username", "remote"} {
		got, _ := m[k].(string)
		if !strings.HasPrefix(got, "anon:") {
			t.Errorf("%s = %q, want anon: prefix", k, got)
		}
	}
	// Operations data stays untouched.
	if m["device_address"] != "HEQ123" {
		t.Errorf("device_address = %v, want clear text HEQ123", m["device_address"])
	}
	if m["host"] != "172.18.4.29" {
		t.Errorf("host = %v, want clear text 172.18.4.29", m["host"])
	}
	if m["other"] != "keep" {
		t.Errorf("other attr = %q, want %q", m["other"], "keep")
	}
}

// --------------------------------------------------------------------------
// AnonymiseToken
// --------------------------------------------------------------------------

func TestAnonymiseToken_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := hmlog.AnonymiseToken(""); got != "" {
		t.Errorf("AnonymiseToken(%q) = %q, want empty", "", got)
	}
}

func TestAnonymiseToken_NonEmptyHasAnonPrefix(t *testing.T) {
	t.Parallel()
	got := hmlog.AnonymiseToken("HEQ123")
	if !strings.HasPrefix(got, "anon:") {
		t.Errorf("AnonymiseToken(%q) = %q, want anon: prefix", "HEQ123", got)
	}
}
