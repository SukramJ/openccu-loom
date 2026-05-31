// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observability

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// TestSetClock replaces the tracing clock with a fake, runs a span, and
// restores the original clock.
func TestSetClock(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	prev := SetClock(fake)
	defer SetClock(prev)

	sp, _ := StartSpan(context.Background(), "timed_op", nil)
	if !sp.StartedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("StartedAt = %v, want fake clock time", sp.StartedAt)
	}
	// Advance the fake clock by 50 ms, then End the span.
	fake.Advance(50 * time.Millisecond)
	sp.End()
	if sp.DurationMS() < 40 {
		t.Fatalf("DurationMS = %v, want ~50", sp.DurationMS())
	}
}

// TestSetClockNilRestoresReal verifies that passing nil resets to the real clock.
func TestSetClockNilRestoresReal(t *testing.T) {
	prev := SetClock(nil)
	defer SetClock(prev)

	// After reset, now() should return something close to time.Now().
	sp, _ := StartSpan(context.Background(), "real_time", nil)
	diff := time.Since(sp.StartedAt)
	if diff < 0 || diff > 5*time.Second {
		t.Fatalf("StartedAt too far from real now: %v", diff)
	}
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
