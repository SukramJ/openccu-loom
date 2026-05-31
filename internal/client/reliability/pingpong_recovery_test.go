// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Item B — PingPongTracker failure-recovery behaviour tests.
//
// Production audit result:
// PingPongTracker does NOT have a dedicated "FAILED state" or a
// "RecoveryCallback" in the production code. The health signal is
// Stats().Severity, which is derived live from the current size of the
// pending / unknown tables compared to MismatchThreshold.
//
// Recovery in the Go implementation happens through Clear(): after a
// reconnect the central calls Clear() to drain stale entries from the
// previous session, and the next RecordPing / RecordPong starts a
// Fresh accounting window. This mirrors
// PingPongTracker.clear() (store/dynamic/ping_pong.py:107).
//
// "Recovery" in these tests therefore means: severity transitions from
// "degraded" / "critical" back to "ok" when the conditions that caused
// the elevation are removed (either via Clear() or via successful
// RecordPong calls that drain the pending table).
//
// Covered scenarios:
// B1. Sweep-Failure → live pending table pushes severity to "degraded".
// B2. RecordPong (heartbeat) after "degraded" → severity returns to "ok".
// B3. Multiple failures in a row → no double-increment, counters consistent.
// B4. Concurrent RecordPing + Sweep → no race (verified by -race flag).

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── B1. Sweep-Failure → Severity transitions to "degraded" ─────────────────

// TestPingPongSweepFailureSetsDegraded verifies that after Sweep evicts
// expired pending entries the cumulative mismatch counter grows and
// Stats().Severity reflects "degraded" when pending-table entries reach
// MismatchThreshold.
//
// The live pending table causes severity elevation even before Sweep — a
// pending PING that has not been matched and whose TTL has not yet elapsed
// already counts as a live anomaly if the table size equals the threshold.
// After Sweep the entries move into the mismatch counter; the pending table
// shrinks and severity falls back to "ok" (the cumulative mismatch counter
// does not affect Severity directly — only the live table sizes do).
func TestPingPongSweepFailureSetsLiveSeverity(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 200 * time.Millisecond
	const threshold = 2

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        ttl,
		MismatchThreshold: threshold,
		Clock:             fake,
	})

	// Send threshold pings — live pending table is exactly at threshold.
	for i := 0; i < threshold; i++ {
		tr.RecordPing(fmt.Sprintf("ping-%d", i))
	}

	s := tr.Stats()
	if s.Pending != threshold {
		t.Fatalf("Pending=%d, want %d", s.Pending, threshold)
	}
	// Severity must be "degraded" (one table at threshold, not both).
	if s.Severity != "degraded" {
		t.Errorf("severity before Sweep=%q, want degraded (pending=%d, threshold=%d)", s.Severity, s.Pending, threshold)
	}

	// Advance clock past TTL; Sweep evicts the entries.
	fake.Advance(ttl + time.Millisecond)
	mismatches := tr.Sweep()

	if len(mismatches) != threshold {
		t.Errorf("Sweep mismatches=%d, want %d", len(mismatches), threshold)
	}
	for _, m := range mismatches {
		if m.Kind != hmenum.PingPongMismatchPending {
			t.Errorf("mismatch kind=%v, want PingPongMismatchPending", m.Kind)
		}
	}

	// After Sweep, pending table is empty → severity drops back to "ok".
	sAfter := tr.Stats()
	if sAfter.Pending != 0 {
		t.Errorf("Pending after Sweep=%d, want 0", sAfter.Pending)
	}
	if sAfter.Severity != "ok" {
		t.Errorf("severity after Sweep=%q, want ok", sAfter.Severity)
	}
	// Cumulative mismatch counter grew.
	if sAfter.MismatchTotal != threshold {
		t.Errorf("MismatchTotal=%d, want %d", sAfter.MismatchTotal, threshold)
	}
}

// ─── B2. RecordPong (heartbeat) after "degraded" → returns to "ok" ───────────

// TestPingPongRecordPongDrainsDegradedState verifies that once the pending
// table reaches the threshold (severity "degraded"), matching pongs against
// those pings drains the table and restores severity to "ok".
//
// This mirrors the recovery path where heartbeat pings are eventually answered
// by pong responses from the CCU — no Clear() required.
func TestPingPongRecordPongDrainsDegradedState(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 30 * time.Second
	const threshold = 3

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        ttl,
		MismatchThreshold: threshold,
		Clock:             fake,
	})

	// Build up to the threshold so severity becomes "degraded".
	ids := make([]string, threshold)
	for i := 0; i < threshold; i++ {
		ids[i] = fmt.Sprintf("hb-%d", i)
		tr.RecordPing(ids[i])
	}
	if s := tr.Stats(); s.Severity != "degraded" {
		t.Fatalf("severity before recovery=%q, want degraded", s.Severity)
	}

	// Advance clock slightly (still within TTL) then match all pings.
	fake.Advance(5 * time.Millisecond)
	for _, id := range ids {
		matched, _ := tr.RecordPong(id)
		if !matched {
			t.Errorf("RecordPong(%q) did not match", id)
		}
	}

	// Pending table is now empty; severity must return to "ok".
	s := tr.Stats()
	if s.Pending != 0 {
		t.Errorf("Pending after pongs=%d, want 0", s.Pending)
	}
	if s.Severity != "ok" {
		t.Errorf("severity after recovery=%q, want ok", s.Severity)
	}
	// MatchedTotal equals threshold.
	if s.MatchedTotal != threshold {
		t.Errorf("MatchedTotal=%d, want %d", s.MatchedTotal, threshold)
	}
}

// ─── B3. Multiple consecutive failures — no double-increment ─────────────────

// TestPingPongMultipleFailuresNoDoubleIncrement sends multiple rounds of
// pings without matching pongs, then sweeps; verifies that each expired ping
// is counted exactly once in MismatchTotal and that the cumulative counter
// grows monotonically.
func TestPingPongMultipleFailuresNoDoubleIncrement(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 100 * time.Millisecond
	const rounds = 3
	const pingsPerRound = 2

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		Clock:      fake,
	})

	var sweepTotal int
	for r := 0; r < rounds; r++ {
		for p := 0; p < pingsPerRound; p++ {
			tr.RecordPing(fmt.Sprintf("r%d-p%d", r, p))
		}
		// Advance past TTL so this round's pings expire.
		fake.Advance(ttl + time.Millisecond)
		mismatches := tr.Sweep()
		sweepTotal += len(mismatches)

		s := tr.Stats()
		wantMismatch := (r + 1) * pingsPerRound
		if s.MismatchTotal != wantMismatch {
			t.Errorf("round %d: MismatchTotal=%d, want %d", r, s.MismatchTotal, wantMismatch)
		}
	}

	if sweepTotal != rounds*pingsPerRound {
		t.Errorf("total sweep mismatches=%d, want %d", sweepTotal, rounds*pingsPerRound)
	}
	// After all sweeps, pending table must be empty.
	if tr.PendingCount() != 0 {
		t.Errorf("PendingCount after all sweeps=%d, want 0", tr.PendingCount())
	}
}

// TestPingPongMismatchHookCalledExactlyOncePerExpiry verifies that the
// mismatch hook installed via SetMismatchHook is called exactly once per
// expired entry across multiple sweep rounds — no double-reporting.
func TestPingPongMismatchHookCalledExactlyOncePerExpiry(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 50 * time.Millisecond
	const totalPings = 6

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		Clock:      fake,
	})

	var hookCalls atomic.Int32
	tr.SetMismatchHook(func(_ Mismatch) {
		hookCalls.Add(1)
	})

	for i := 0; i < totalPings; i++ {
		tr.RecordPing(fmt.Sprintf("ping-%d", i))
	}
	fake.Advance(ttl + time.Millisecond)
	tr.Sweep()
	// A second Sweep on the same entries must not re-report them.
	tr.Sweep()

	if n := hookCalls.Load(); n != totalPings {
		t.Errorf("hook called %d times, want %d (one per expiry)", n, totalPings)
	}
}

// ─── B4. Concurrent RecordPing + Sweep — no race ─────────────────────────────

// TestPingPongConcurrentRecordAndSweep exercises simultaneous RecordPing,
// RecordPong, and Sweep calls from many goroutines. The test is intentionally
// light on assertions (counters must not be negative; the tracker must not
// panic) — the main correctness signal is that -race finds no data races.
func TestPingPongConcurrentRecordAndSweep(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 10 * time.Millisecond

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:  ttl,
		Clock:       fake,
		JournalSize: 64,
	})

	const goroutines = 20
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				id := fmt.Sprintf("g%d-i%d", g, i)
				tr.RecordPing(id)
				// Some goroutines immediately match their pong; others don't.
				if (g+i)%3 != 0 {
					tr.RecordPong(id)
				}
				if i%10 == 0 {
					// Advance the fake clock in small increments; concurrent
					// Advance calls are safe (the Fake uses its own mutex).
					fake.Advance(5 * time.Millisecond)
					tr.Sweep()
				}
			}
		}()
	}

	wg.Wait()
	tr.Sweep() // final sweep to drain pending table

	// Basic sanity: counters must be non-negative and consistent.
	s := tr.Stats()
	if s.TotalSent < 0 {
		t.Errorf("TotalSent negative: %d", s.TotalSent)
	}
	if s.MatchedTotal < 0 {
		t.Errorf("MatchedTotal negative: %d", s.MatchedTotal)
	}
	if s.MismatchTotal < 0 {
		t.Errorf("MismatchTotal negative: %d", s.MismatchTotal)
	}
	// Pending and Unknown must be ≥ 0.
	if s.Pending < 0 {
		t.Errorf("Pending negative: %d", s.Pending)
	}
	if s.Unknown < 0 {
		t.Errorf("Unknown negative: %d", s.Unknown)
	}
}

// TestPingPongSweepAndClearRace exercises concurrent Sweep and Clear calls
// to verify no race condition exists between these two mutating operations.
func TestPingPongSweepAndClearRace(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	const ttl = 5 * time.Millisecond

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		Clock:      fake,
	})

	const N = 10
	var wg sync.WaitGroup
	wg.Add(3)

	// Writer goroutine: continuously add pings.
	go func() {
		defer wg.Done()
		for i := 0; i < N*10; i++ {
			tr.RecordPing(fmt.Sprintf("concurrent-%d", i))
		}
	}()

	// Sweeper goroutine: advance clock and sweep.
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			fake.Advance(ttl + time.Millisecond)
			tr.Sweep()
		}
	}()

	// Clear goroutine: periodically clear.
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			tr.Clear()
		}
	}()

	wg.Wait()
	// No assertion needed beyond "no race, no panic".
}
