// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package audit

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// TestNoopRecorderList exercises the List method on noopRecorder (0% before).
func TestNoopRecorderList(t *testing.T) {
	r := NoopRecorder()
	r.Record(Entry{Action: ActionParamsetWrite})
	if got := r.List(0); got != nil {
		t.Fatalf("NoopRecorder.List should return nil, got %v", got)
	}
}

// TestNoopRecorderRecord verifies Record is a no-op (doesn't panic).
func TestNoopRecorderRecord(t *testing.T) {
	r := NoopRecorder()
	r.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "ABC"})
	// No panic = pass.
}

// TestNewBufferWithClockNilFallsBackToReal verifies nil clock → real clock.
func TestNewBufferWithClockNilFallsBackToReal(t *testing.T) {
	b := NewBufferWithClock(5, nil)
	if b == nil {
		t.Fatal("NewBufferWithClock(5, nil) should not return nil")
	}
	// A Record call should not panic.
	b.Record(Entry{Action: ActionLinkAdd, DeviceAddress: "X"})
}

// TestNewBufferWithClockFake exercises the fake-clock path and verifies
// that the buffer stamps entries with the fake clock's current time.
func TestNewBufferWithClockFake(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	b := NewBufferWithClock(5, fake)
	b.Record(Entry{Action: ActionParamsetWrite})
	entries := b.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Timestamp.Equal(fake.Now()) {
		t.Fatalf("timestamp mismatch: %v vs %v", entries[0].Timestamp, fake.Now())
	}
}

// TestNewChangeLogCappedWithClockNilFallback verifies nil clock is accepted.
func TestNewChangeLogCappedWithClockNilFallback(t *testing.T) {
	cl := NewChangeLogCappedWithClock(10, nil)
	if cl == nil {
		t.Fatal("NewChangeLogCappedWithClock should not return nil")
	}
}

// TestNewChangeLogCappedWithClockFake exercises the fake-clock seam.
func TestNewChangeLogCappedWithClockFake(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	cl := NewChangeLogCappedWithClock(5, fake)
	if cl == nil {
		t.Fatal("NewChangeLogCappedWithClock(fake) should not return nil")
	}
	added := cl.Add("sess1", ChangeEntry{ChannelAddress: "ABC:1"})
	if added.Timestamp.IsZero() {
		t.Fatal("Add should stamp the timestamp")
	}
	if !added.Timestamp.Equal(fake.Now()) {
		t.Fatalf("timestamp mismatch: %v vs %v", added.Timestamp, fake.Now())
	}
}

// TestNewPersistedRecorderWithClockNil exercises the nil-clock fallback.
func TestNewPersistedRecorderWithClockNil(t *testing.T) {
	buf := NewBuffer(5)
	r := NewPersistedRecorderWithClock(buf, nil, nil, nil)
	if r == nil {
		t.Fatal("NewPersistedRecorderWithClock should not return nil")
	}
	r.Record(Entry{Action: ActionParamsetWrite})
}

// TestNewPersistedRecorderWithClockFake exercises the fake-clock path.
func TestNewPersistedRecorderWithClockFake(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	buf := NewBuffer(5)
	r := NewPersistedRecorderWithClock(buf, nil, nil, fake)
	r.Record(Entry{Action: ActionLinkAdd})
	entries := r.List(0)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if !entries[0].Timestamp.Equal(fake.Now()) {
		t.Fatalf("timestamp mismatch: %v", entries[0].Timestamp)
	}
}

// TestDurableSinkStatsNonNilAfterNewDurableSink verifies DurableSinkStats fields
// via NewDurableSink.
func TestDurableSinkStatsNonNilAfterNewDurableSink(t *testing.T) {
	sink, stats, stop := NewDurableSink(func(_ context.Context, _ Entry) error { return nil }, DurableSinkOptions{})
	if sink == nil {
		t.Fatal("sink should not be nil")
	}
	defer stop()
	if stats == nil {
		t.Fatal("stats should not be nil")
	}
	// Exercising SinkErrors on a non-nil stats object.
	if stats.SinkErrors() != 0 {
		t.Fatalf("SinkErrors should start at 0")
	}
}

// TestDurableSinkStatsNil verifies that a nil *DurableSinkStats returns 0 safely.
func TestDurableSinkStatsNil(t *testing.T) {
	var s *DurableSinkStats
	if s.Enqueued() != 0 {
		t.Fatal("nil Enqueued should return 0")
	}
	if s.Dropped() != 0 {
		t.Fatal("nil Dropped should return 0")
	}
	if s.SinkErrors() != 0 {
		t.Fatal("nil SinkErrors should return 0")
	}
}

// TestPersistedRecorderList exercises the List path when buf is non-nil.
func TestPersistedRecorderList(t *testing.T) {
	buf := NewBuffer(10)
	r := NewPersistedRecorder(buf, func(_ context.Context, _ Entry) error { return nil }, nil)
	r.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "A"})
	entries := r.List(0)
	if len(entries) != 1 {
		t.Fatalf("PersistedRecorder.List = %d, want 1", len(entries))
	}
}

// TestPersistedRecorderListNilBuf exercises the branch where buf is nil → returns nil.
func TestPersistedRecorderListNilBuf(t *testing.T) {
	r := NewPersistedRecorder(nil, nil, nil)
	r.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "A"})
	if got := r.List(5); got != nil {
		t.Fatalf("List with nil buf should return nil: %v", got)
	}
}
