// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// PingPongTracker timing tests run in synctest bubbles: they advance the
// bubble's fake clock instead of relying on real wall-clock deltas, which
// makes timing-sensitive assertions fully deterministic. Mirrors the
// approach used in throttle_clock_test.go.

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPingPongRTTEqualsElapsedBubbleTime verifies that the RTT computed by
// RecordPong is the elapsed time between Ping and Pong. Advancing the
// bubble clock by exactly 50 ms between them must produce an RTT of
// exactly 50 ms.
func TestPingPongRTTEqualsElapsedBubbleTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := NewPingPongTracker(PingPongConfig{})

		tr.RecordPing("ping-1")
		// Advance bubble time by exactly 50 ms — no real sleep happens.
		synctest.Sleep(50 * time.Millisecond)
		matched, rtt := tr.RecordPong("ping-1")

		if !matched {
			t.Fatal("RecordPong should have matched the outstanding ping")
		}
		const wantRTT = 50 * time.Millisecond
		if rtt != wantRTT {
			t.Fatalf("rtt=%v, want %v", rtt, wantRTT)
		}
	})
}

// TestPingPongSweepEvictsExpired verifies that Sweep surfaces a
// PingPongMismatchPending mismatch with the correct When timestamp
// when the bubble clock is advanced past PendingTTL. The mismatch When
// field must equal the time at which RecordPing was called, not the
// sweep time.
func TestPingPongSweepEvictsExpired(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		const ttl = 200 * time.Millisecond
		tr := NewPingPongTracker(PingPongConfig{
			PendingTTL: ttl,
		})

		// Record the ping — the tracker stores start as the sent timestamp.
		tr.RecordPing("expired-ping")

		// Advance past TTL without matching the pong.
		synctest.Sleep(ttl + 1*time.Millisecond)
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
	})
}

// TestPingPongMismatchHookFiresOutsideLock verifies that the mismatch
// hook installed via SetMismatchHook is called by Sweep after the
// tracker lock is released. The hook may therefore call back into
// tracker methods (e.g. Stats) without deadlocking.
func TestPingPongMismatchHookFiresOutsideLock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const ttl = 100 * time.Millisecond
		tr := NewPingPongTracker(PingPongConfig{
			PendingTTL: ttl,
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
		synctest.Sleep(ttl + 1*time.Millisecond)
		tr.Sweep()

		if hookCalls.Load() != 1 {
			t.Fatalf("hook called %d times, want 1", hookCalls.Load())
		}
		if !statsOK.Load() {
			t.Fatal("Stats() inside hook deadlocked or panicked")
		}
	})
}

// ---------------------------------------------------------------------------
// C19 — PingPongTracker.Clear() tests
// ---------------------------------------------------------------------------

// TestPingPongClearEmptiesTables verifies that Clear empties the pending and
// unknown tables and resets all counters.
func TestPingPongClearEmptiesTables(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{
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
	synctest.Test(t, func(t *testing.T) {
		tr := NewPingPongTracker(PingPongConfig{
			JournalSize: 16,
		})

		tr.RecordPing("p1")
		synctest.Sleep(10 * time.Millisecond)
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
	})
}

// TestPingPongClearThenRecord verifies the tracker is fully functional after
// Clear — new PINGs can be sent and matched.
func TestPingPongClearThenRecord(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := NewPingPongTracker(PingPongConfig{})

		tr.RecordPing("old-ping")
		tr.Clear()

		tr.RecordPing("new-ping")
		synctest.Sleep(5 * time.Millisecond)
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
	})
}

// TestPingPongSeverityAfterClear verifies severity resets to "ok" after Clear
// even when the previous state was degraded.
//
// Severity is "degraded" when the live pending-table size is over
// MismatchThreshold. A pending PING that has not yet been swept keeps
// the table populated and the severity elevated. Clear empties the
// pending table unconditionally — severity must return to "ok".
func TestPingPongSeverityAfterClear(t *testing.T) {
	t.Parallel()
	const ttl = 30 * time.Second
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        ttl,
		MismatchThreshold: 1,
	})

	// Record two PINGs without matching them. The pending table has 2
	// entries, over MismatchThreshold → Stats.Severity must be "degraded".
	tr.RecordPing("unmatched-1")
	tr.RecordPing("unmatched-2")

	if s := tr.Stats(); s.Severity != "degraded" {
		t.Fatalf("severity before Clear=%q, want degraded (pending=%d)", s.Severity, s.Pending)
	}

	tr.Clear()

	if s := tr.Stats(); s.Severity != "ok" {
		t.Errorf("severity after Clear=%q, want ok", s.Severity)
	}
}
