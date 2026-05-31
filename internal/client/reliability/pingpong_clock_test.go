// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// P1-1: PingPongTracker honours the injected clock.Clock interface.
// Tests advance virtual time instead of relying on real wall-clock
// deltas, making timing-sensitive assertions fully deterministic.
// Mirrors the approach used in throttle_clock_test.go.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPingPongDefaultClockIsReal verifies that a nil Clock field in
// PingPongConfig results in the production clock.Real being wired onto
// the tracker — not a nil pointer that would panic on first use.
func TestPingPongDefaultClockIsReal(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	if tr.clk == nil {
		t.Fatal("expected clk to be non-nil after construction")
	}
	if _, ok := tr.clk.(clock.Real); !ok {
		t.Fatalf("expected clock.Real, got %T", tr.clk)
	}
}

// TestPingPongRTTUsesInjectedClock verifies that the RTT computed by
// RecordPong is derived from the injected fake clock rather than the
// real wall clock. Advancing the fake by exactly 50 ms between Ping
// and Pong must produce an RTT of exactly 50 ms.
func TestPingPongRTTUsesInjectedClock(t *testing.T) {
	t.Parallel()
	// Start the fake clock at a pinned moment so log timestamps are readable.
	fake := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	tr := NewPingPongTracker(PingPongConfig{Clock: fake})

	tr.RecordPing("ping-1")
	// Advance virtual time by exactly 50 ms — no real sleep required.
	fake.Advance(50 * time.Millisecond)
	matched, rtt := tr.RecordPong("ping-1")

	if !matched {
		t.Fatal("RecordPong should have matched the outstanding ping")
	}
	const wantRTT = 50 * time.Millisecond
	if rtt != wantRTT {
		t.Fatalf("rtt=%v, want %v — clock injection not honoured", rtt, wantRTT)
	}
}

// TestPingPongSweepEvictsExpired verifies that Sweep surfaces a
// PingPongMismatchPending mismatch with the correct When timestamp
// when the fake clock is advanced past PendingTTL. The mismatch When
// field must equal the time at which RecordPing was called, not the
// sweep time.
func TestPingPongSweepEvictsExpired(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := clock.NewFake(start)
	const ttl = 200 * time.Millisecond
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		Clock:      fake,
	})

	// Record the ping — the tracker stores start as the sent timestamp.
	tr.RecordPing("expired-ping")

	// Advance past TTL without matching the pong.
	fake.Advance(ttl + 1*time.Millisecond)
	mismatches := tr.Sweep()

	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %d", len(mismatches))
	}
	m := mismatches[0]
	if m.Kind != hmenum.PingPongMismatchPending {
		t.Fatalf("mismatch kind=%v, want PingPongMismatchPending", m.Kind)
	}
	if m.ID != "expired-ping" {
		t.Fatalf("mismatch ID=%q, want %q", m.ID, "expired-ping")
	}
	// When must be the Ping timestamp (start), not the sweep time.
	if !m.When.Equal(start) {
		t.Fatalf("mismatch.When=%v, want %v (ping timestamp)", m.When, start)
	}
}

// TestPingPongMismatchHookFiresOutsideLock verifies that the mismatch
// hook installed via SetMismatchHook is called by Sweep after the
// tracker lock is released. The hook may therefore call back into
// tracker methods (e.g. Stats) without deadlocking.
func TestPingPongMismatchHookFiresOutsideLock(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 100 * time.Millisecond
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		Clock:      fake,
	})

	// Track how many times the hook fires and whether Stats() re-entered
	// without deadlock.
	var hookCalls atomic.Int32
	var statsOK atomic.Bool

	tr.SetMismatchHook(func(m Mismatch) {
		hookCalls.Add(1)
		// Calling Stats() from inside the hook must not deadlock — the
		// hook is fired after Sweep drops the mutex.
		s := tr.Stats()
		if s.TotalSent >= 0 { // trivially true; proves no panic/deadlock
			statsOK.Store(true)
		}
	})

	tr.RecordPing("hook-ping")
	fake.Advance(ttl + 1*time.Millisecond)
	tr.Sweep()

	if hookCalls.Load() != 1 {
		t.Fatalf("hook called %d times, want 1", hookCalls.Load())
	}
	if !statsOK.Load() {
		t.Fatal("Stats() inside hook deadlocked or panicked")
	}
}

// ---------------------------------------------------------------------------
// C19 — PingPongTracker.Clear() tests
// ---------------------------------------------------------------------------

// TestPingPongClearEmptiesTables verifies that Clear empties the pending and
// unknown tables and resets all counters.
func TestPingPongClearEmptiesTables(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tr := NewPingPongTracker(PingPongConfig{
		Clock:       fake,
		JournalSize: 16,
	})

	tr.RecordPing("ping-1")
	tr.RecordPong("orphan-pong") // unknown — no matching PING

	if tr.PendingCount() != 1 {
		t.Fatalf("PendingCount before Clear=%d, want 1", tr.PendingCount())
	}
	if tr.UnknownCount() != 1 {
		t.Fatalf("UnknownCount before Clear=%d, want 1", tr.UnknownCount())
	}

	tr.Clear()

	if tr.PendingCount() != 0 {
		t.Errorf("PendingCount after Clear=%d, want 0", tr.PendingCount())
	}
	if tr.UnknownCount() != 0 {
		t.Errorf("UnknownCount after Clear=%d, want 0", tr.UnknownCount())
	}

	s := tr.Stats()
	if s.TotalSent != 0 {
		t.Errorf("TotalSent after Clear=%d, want 0", s.TotalSent)
	}
	if s.TotalReceived != 0 {
		t.Errorf("TotalReceived after Clear=%d, want 0", s.TotalReceived)
	}
}

// TestPingPongClearPreservesJournal verifies that the diagnostic journal is
// not purged by Clear — history is retained for post-mortem analysis.
func TestPingPongClearPreservesJournal(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tr := NewPingPongTracker(PingPongConfig{
		Clock:       fake,
		JournalSize: 16,
	})

	tr.RecordPing("p1")
	fake.Advance(10 * time.Millisecond)
	tr.RecordPong("p1")

	journalBefore := tr.Journal()
	if len(journalBefore) == 0 {
		t.Fatal("journal should be non-empty before Clear")
	}

	tr.Clear()

	journalAfter := tr.Journal()
	if len(journalAfter) != len(journalBefore) {
		t.Errorf("journal length changed after Clear: %d → %d", len(journalBefore), len(journalAfter))
	}
}

// TestPingPongClearThenRecord verifies the tracker is fully functional after
// Clear — new PINGs can be sent and matched.
func TestPingPongClearThenRecord(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tr := NewPingPongTracker(PingPongConfig{
		Clock: fake,
	})

	tr.RecordPing("old-ping")
	tr.Clear()

	tr.RecordPing("new-ping")
	fake.Advance(5 * time.Millisecond)
	matched, rtt := tr.RecordPong("new-ping")

	if !matched {
		t.Error("RecordPong should match after Clear+RecordPing")
	}
	if rtt != 5*time.Millisecond {
		t.Errorf("rtt=%v, want 5ms", rtt)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("PendingCount after match=%d, want 0", tr.PendingCount())
	}
}

// TestPingPongSeverityAfterClear verifies severity resets to "ok" after Clear
// even when the previous state was degraded.
//
// Severity is "degraded" when the live pending-table size reaches
// MismatchThreshold. A pending PING that has not yet been swept keeps
// the table populated and the severity elevated. Clear empties the
// pending table unconditionally — severity must return to "ok".
func TestPingPongSeverityAfterClear(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 30 * time.Second
	tr := NewPingPongTracker(PingPongConfig{
		Clock:             fake,
		PendingTTL:        ttl,
		MismatchThreshold: 1,
	})

	// Record a PING without matching it. The pending table has 1 entry
	// which equals MismatchThreshold → Stats.Severity must be "degraded".
	tr.RecordPing("unmatched")

	if s := tr.Stats(); s.Severity != "degraded" {
		t.Fatalf("severity before Clear=%q, want degraded (pending=%d)", s.Severity, s.Pending)
	}

	tr.Clear()

	if s := tr.Stats(); s.Severity != "ok" {
		t.Errorf("severity after Clear=%q, want ok", s.Severity)
	}
}
