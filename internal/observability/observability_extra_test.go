// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package observability

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestSpanTimestampsFollowTheClock verifies StartedAt is the creation
// time and DurationMS spans exactly the elapsed time until End.
func TestSpanTimestampsFollowTheClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		sp, _ := StartSpan(context.Background(), "timed_op", nil)
		if !sp.StartedAt.Equal(start) {
			t.Fatalf("StartedAt = %v, want %v", sp.StartedAt, start)
		}
		time.Sleep(50 * time.Millisecond)
		sp.End()
		if got := sp.DurationMS(); got != 50 {
			t.Fatalf("DurationMS = %v, want 50", got)
		}
	})
}

// TestSpanString exercises the String() method on Span.
func TestSpanString(t *testing.T) {
	sp, _ := StartSpan(context.Background(), "my_op", nil)
	s := sp.String()
	if !strings.Contains(s, "my_op") {
		t.Errorf("Span.String() missing op name: %q", s)
	}
	if !strings.Contains(s, sp.TraceID[:8]) {
		t.Errorf("Span.String() missing trace prefix: %q", s)
	}
}

// TestLogRecorderWithLogger exercises ObserveLatency and IncCounter with a
// real (no-op writer) logger — covers the non-nil logger branches.
func TestLogRecorderWithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(newDiscardWriter(), nil))
	r := LogRecorder{Logger: logger}

	// Success path: no error → Debug log (covered but not asserted because
	// we only care that it does not panic).
	r.ObserveLatency("op", ScopeService, 5*time.Millisecond, nil)

	// Error path: non-nil error → Warn log.
	r.ObserveLatency("op", ScopeService, 5*time.Millisecond, errors.New("test error"))

	// Counter path.
	r.IncCounter("op", ScopeService, 3)
}

// discardWriter satisfies io.Writer but throws everything away; used to give
// slog.New a non-nil output so the handler does not become the default.
type discardWriter struct{}

func newDiscardWriter() *discardWriter             { return &discardWriter{} }
func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
