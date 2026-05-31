// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package audit

import (
	"sync"
	"testing"
	"time"
)

// ChangeLog is the session-scoped FIFO mirror of
// 's ConfigChangeLog (change_log.py).

func TestAddChangeAppendsEntry(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	e := cl.Add("s1", ChangeEntry{
		EntryID:        "s1",
		ChannelAddress: "ABC0:1",
		ParamsetKey:    "MASTER",
		Changes:        map[string]ParamChange{"P": {Old: 1, New: 2}},
	})
	if e.ChannelAddress != "ABC0:1" {
		t.Fatalf("returned entry has wrong ChannelAddress: %s", e.ChannelAddress)
	}
	got, total, ok := cl.GetEntries("s1", "", 0)
	if !ok {
		t.Fatal("GetEntries: ok=false for known session")
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("total=%d len=%d", total, len(got))
	}
	if got[0].ChannelAddress != "ABC0:1" {
		t.Fatalf("wrong ChannelAddress: %s", got[0].ChannelAddress)
	}
}

func TestGetEntriesReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Add("s1", ChangeEntry{ChannelAddress: "A"})

	got, _, _ := cl.GetEntries("s1", "", 0)
	got[0].ChannelAddress = "MUTATED"

	again, _, _ := cl.GetEntries("s1", "", 0)
	if again[0].ChannelAddress != "A" {
		t.Fatalf("GetEntries did not return a defensive copy: %s", again[0].ChannelAddress)
	}
}

func TestGetEntriesUnknownSession(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	got, total, ok := cl.GetEntries("no-such-session", "", 0)
	if ok {
		t.Fatal("expected ok=false for unknown session")
	}
	if got != nil || total != 0 {
		t.Fatalf("expected nil/0, got %v/%d", got, total)
	}
}

func TestGetEntriesNewestFirst(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	for _, addr := range []string{"A", "B", "C"} {
		cl.Add("s1", ChangeEntry{ChannelAddress: addr})
	}
	got, total, _ := cl.GetEntries("s1", "", 0)
	if total != 3 || len(got) != 3 {
		t.Fatalf("total=%d len=%d", total, len(got))
	}
	// Newest-first: C, B, A
	if got[0].ChannelAddress != "C" || got[2].ChannelAddress != "A" {
		t.Fatalf("wrong order: %v", got)
	}
}

func TestGetEntriesLimitRespected(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	for _, addr := range []string{"A", "B", "C", "D"} {
		cl.Add("s1", ChangeEntry{ChannelAddress: addr})
	}
	got, total, _ := cl.GetEntries("s1", "", 2)
	if total != 4 {
		t.Fatalf("total should be 4, got %d", total)
	}
	if len(got) != 2 {
		t.Fatalf("limited slice should have 2, got %d", len(got))
	}
	// Newest-first with limit=2: D, C
	if got[0].ChannelAddress != "D" || got[1].ChannelAddress != "C" {
		t.Fatalf("wrong limited result: %v", got)
	}
}

func TestGetEntriesChannelAddressFilter(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Add("s1", ChangeEntry{ChannelAddress: "ABC0:1"})
	cl.Add("s1", ChangeEntry{ChannelAddress: "ABC0:2"})
	cl.Add("s1", ChangeEntry{ChannelAddress: "XYZ0:1"})

	got, total, _ := cl.GetEntries("s1", "ABC0", 0)
	if total != 2 || len(got) != 2 {
		t.Fatalf("filter total=%d len=%d", total, len(got))
	}
	for _, e := range got {
		if len(e.ChannelAddress) < 4 || e.ChannelAddress[:4] != "ABC0" {
			t.Fatalf("unexpected ChannelAddress after filter: %s", e.ChannelAddress)
		}
	}
}

func TestClearByEntryIDRemovesMatching(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Add("s1", ChangeEntry{EntryID: "e1", ChannelAddress: "A"})
	cl.Add("s1", ChangeEntry{EntryID: "e2", ChannelAddress: "B"})
	cl.Add("s1", ChangeEntry{EntryID: "e1", ChannelAddress: "C"})

	removed := cl.ClearByEntryID("s1", "e1")
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	got, total, _ := cl.GetEntries("s1", "", 0)
	if total != 1 || got[0].EntryID != "e2" {
		t.Fatalf("remaining entries wrong: %+v", got)
	}
}

func TestClearByEntryIDUnknownSessionIsNoop(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	if n := cl.ClearByEntryID("no-such", "e1"); n != 0 {
		t.Fatalf("expected 0 removed, got %d", n)
	}
}

func TestDiscardClearsSession(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Add("s1", ChangeEntry{ChannelAddress: "A"})
	cl.Discard("s1")

	_, _, ok := cl.GetEntries("s1", "", 0)
	if ok {
		t.Fatal("session still present after Discard")
	}
}

func TestDiscardIsIdempotent(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Discard("nonexistent")
	cl.Discard("nonexistent") // must not panic
}

func TestSessionsListsActiveOnes(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	cl.Add("s1", ChangeEntry{})
	cl.Add("s2", ChangeEntry{})
	cl.Add("s3", ChangeEntry{})
	cl.Discard("s2")

	ids := cl.Sessions()
	set := make(map[string]bool)
	for _, id := range ids {
		set[id] = true
	}
	if !set["s1"] || !set["s3"] {
		t.Fatalf("missing active sessions: %v", ids)
	}
	if set["s2"] {
		t.Fatal("discarded session still listed")
	}
}

func TestMaxEntriesPerSessionCaps(t *testing.T) {
	t.Parallel()
	cl := NewChangeLogCapped(3)
	for _, addr := range []string{"A", "B", "C", "D", "E"} {
		cl.Add("s1", ChangeEntry{ChannelAddress: addr})
	}
	got, total, _ := cl.GetEntries("s1", "", 0)
	if total != 3 || len(got) != 3 {
		t.Fatalf("cap not enforced: total=%d len=%d", total, len(got))
	}
	// After capping to 3, newest are C D E → newest-first: E, D, C
	if got[0].ChannelAddress != "E" || got[2].ChannelAddress != "C" {
		t.Fatalf("wrong entries after cap: %v", got)
	}
}

func TestZeroCapFallsBackToDefault(t *testing.T) {
	t.Parallel()
	cl := NewChangeLogCapped(0)
	if cl.maxEntries != DefaultMaxEntriesPerSession {
		t.Fatalf("maxEntries=%d, want %d", cl.maxEntries, DefaultMaxEntriesPerSession)
	}
}

func TestAddAutoTimestampsZeroEntry(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC()
	cl := NewChangeLog()
	e := cl.Add("s1", ChangeEntry{})
	if e.Timestamp.Before(before) || e.Timestamp.After(time.Now().UTC()) {
		t.Fatalf("timestamp out of expected window: %v", e.Timestamp)
	}
}

func TestConcurrentAddAndRead(t *testing.T) {
	t.Parallel()
	cl := NewChangeLog()
	const goroutines = 20
	const entries = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < entries; i++ {
				cl.Add("shared", ChangeEntry{ChannelAddress: "X"})
				cl.GetEntries("shared", "", 10) //nolint:errcheck // return values intentionally discarded
			}
		}()
	}
	wg.Wait()
	_, total, ok := cl.GetEntries("shared", "", 0)
	if !ok {
		t.Fatal("session missing after concurrent writes")
	}
	// Total is capped at DefaultMaxEntriesPerSession.
	if total > DefaultMaxEntriesPerSession {
		t.Fatalf("cap not enforced under concurrency: total=%d", total)
	}
}

func TestBuildChangeDiff(t *testing.T) {
	t.Parallel()
	old := map[string]any{"A": 1, "B": "x", "C": true}
	newMap := map[string]any{"A": 2, "B": "x", "D": 99}
	diff := BuildChangeDiff(old, newMap)

	if _, ok := diff["B"]; ok {
		t.Fatal("unchanged param B must not appear in diff")
	}
	if d, ok := diff["A"]; !ok || d.Old != 1 || d.New != 2 {
		t.Fatalf("diff A wrong: %+v", diff["A"])
	}
	if d, ok := diff["D"]; !ok || d.Old != nil || d.New != 99 {
		t.Fatalf("diff D wrong (new param): %+v", diff["D"])
	}
}
