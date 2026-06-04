// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package audit

import (
	"testing"
	"time"
)

// Buffer is the in-memory ring the SPA reads via /api/v1/audit.
// Capacity-bound, newest-first, snapshot-safe.

func TestBufferStoresNewestFirst(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	b.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "A"})
	b.Record(Entry{Action: ActionLinkAdd, DeviceAddress: "B"})
	b.Record(Entry{Action: ActionLinkRemove, DeviceAddress: "C"})

	got := b.List(0)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].DeviceAddress != "C" || got[2].DeviceAddress != "A" {
		t.Fatalf("ordering wrong: %+v", got)
	}
}

func TestBufferEvictsOldestPastCapacity(t *testing.T) {
	t.Parallel()
	b := NewBuffer(2)
	for i, dev := range []string{"A", "B", "C"} {
		b.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: dev, Note: string(rune('0' + i))})
	}
	got := b.List(0)
	if len(got) != 2 {
		t.Fatalf("expected 2 (cap), got %d", len(got))
	}
	if got[0].DeviceAddress != "C" || got[1].DeviceAddress != "B" {
		t.Fatalf("evicted wrong entry: %+v", got)
	}
	if b.Len() != 2 {
		t.Fatalf("Len=%d", b.Len())
	}
}

func TestBufferAutoTimestampsZeroEntries(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC()
	b := NewBuffer(4)
	b.Record(Entry{Action: ActionParamsetWrite})
	got := b.List(0)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Timestamp.Before(before) || got[0].Timestamp.After(time.Now().UTC()) {
		t.Fatalf("timestamp out of expected window: %v", got[0].Timestamp)
	}
}

func TestBufferPreservesExplicitTimestamp(t *testing.T) {
	t.Parallel()
	want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	b := NewBuffer(4)
	b.Record(Entry{Action: ActionParamsetWrite, Timestamp: want})
	got := b.List(0)
	if !got[0].Timestamp.Equal(want) {
		t.Fatalf("ts=%v want %v", got[0].Timestamp, want)
	}
}

func TestBufferListLimitClamps(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	for i := range 5 {
		b.Record(Entry{Action: ActionParamsetWrite, Note: string(rune('0' + i))})
	}
	if got := b.List(2); len(got) != 2 {
		t.Fatalf("limit=2 returned %d", len(got))
	}
	if got := b.List(99); len(got) != 5 {
		t.Fatalf("limit>len returned %d", len(got))
	}
}

func TestBufferListReturnsSnapshot(t *testing.T) {
	t.Parallel()
	b := NewBuffer(10)
	b.Record(Entry{Action: ActionParamsetWrite, DeviceAddress: "A"})
	got := b.List(0)
	got[0].DeviceAddress = "MUTATED"
	again := b.List(0)
	if again[0].DeviceAddress != "A" {
		t.Fatalf("List did not return a snapshot: %+v", again)
	}
}

func TestBufferZeroCapacityFallsBackToDefault(t *testing.T) {
	t.Parallel()
	b := NewBuffer(0)
	for range 600 {
		b.Record(Entry{Action: ActionParamsetWrite})
	}
	if got := b.Len(); got != 500 {
		t.Fatalf("default cap not honored, got %d", got)
	}
}

func TestNoopRecorderDropsEverything(t *testing.T) {
	t.Parallel()
	rec := NoopRecorder()
	rec.Record(Entry{Action: ActionParamsetWrite})
	if got := rec.List(99); len(got) != 0 {
		t.Fatalf("noop returned %d entries", len(got))
	}
}
