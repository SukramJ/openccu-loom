// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package diagevent

import (
	"sync"
	"testing"
	"time"
)

func at(sec int) time.Time { return time.Date(2026, 8, 15, 12, 0, sec, 0, time.UTC) }

// TestTheRingKeepsTheNewestAndDropsTheOldest pins the property the
// whole thing exists for.
//
// A pairing attempt that fails produces its trace in the seconds around
// the failure. A buffer that fills up and then refuses new entries keeps
// the boot noise and drops exactly the part an operator opened the page
// to see.
func TestTheRingKeepsTheNewestAndDropsTheOldest(t *testing.T) {
	t.Parallel()

	r := NewRing(3)
	for i := range 5 {
		r.Record(Event{At: at(i), Kind: KindPairing, Message: string(rune('a' + i))})
	}

	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("ring holds %d events, want its capacity of 3", len(got))
	}
	// Newest first: an operator reads the top of the list.
	for i, want := range []string{"e", "d", "c"} {
		if got[i].Message != want {
			t.Errorf("entry %d = %q, want %q (newest first)", i, got[i].Message, want)
		}
	}
}

// TestAnUnwiredRingIsSilentRatherThanFatal keeps the diagnostic from
// becoming a liability.
//
// The ring is optional: a daemon built without it, or a test that never
// wires one, still runs the wire paths that record into it. Recording
// through a nil ring has to be a no-op, because the alternative is a
// panic on a Matter receive path taken for every packet.
func TestAnUnwiredRingIsSilentRatherThanFatal(t *testing.T) {
	t.Parallel()

	var r *Ring
	r.Record(Event{Kind: KindPairing, Message: "ignored"})
	if got := r.Snapshot(); len(got) != 0 {
		t.Errorf("a nil ring returned %d events", len(got))
	}
}

// TestRecordingIsSafeUnderConcurrentWireTraffic pins the property that
// makes it usable where it has to be used.
//
// The recording points sit on the receive path, which is concurrent by
// construction: several exchanges are decoded at once. A ring that needs
// the caller to serialise access would either be wired in the wrong
// places or corrupt under load.
func TestRecordingIsSafeUnderConcurrentWireTraffic(t *testing.T) {
	t.Parallel()

	r := NewRing(64)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Record(Event{At: at(i), Kind: KindSession, Message: "concurrent"})
			_ = r.Snapshot()
		}(i)
	}
	wg.Wait()

	if got := len(r.Snapshot()); got != 32 {
		t.Errorf("ring holds %d events after 32 concurrent records, want 32", got)
	}
}

// TestACapacityBelowOneStillProducesAUsableRing guards the wiring
// mistake that would otherwise disable the diagnostic silently: a
// zero-valued capacity from an unset config field.
func TestACapacityBelowOneStillProducesAUsableRing(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{0, -1} {
		r := NewRing(capacity)
		r.Record(Event{At: at(0), Kind: KindPairing, Message: "kept"})
		if got := r.Snapshot(); len(got) != 1 {
			t.Errorf("NewRing(%d) dropped the only event; a misconfigured capacity must not "+
				"silently switch the diagnostic off", capacity)
		}
	}
}

// TestSnapshotIsACopy keeps a reader from mutating the ring's own
// storage — the REST handler serialises what it gets while the receive
// path keeps recording into it.
func TestSnapshotIsACopy(t *testing.T) {
	t.Parallel()

	r := NewRing(4)
	r.Record(Event{At: at(0), Kind: KindPairing, Message: "original"})
	snap := r.Snapshot()
	snap[0].Message = "mutated"

	if again := r.Snapshot(); again[0].Message != "original" {
		t.Errorf("mutating a snapshot changed the ring: %q", again[0].Message)
	}
}
