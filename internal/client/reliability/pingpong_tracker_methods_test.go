// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// Tests for :
// - HasConnectionIssue
// - Size
// - AllowedDelta
// - Journal property delegation
// - RetryReconcilePong
// - ScheduleUnknownPongRetry

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// ─── HasConnectionIssue ───────────────────────────────────────────────

func TestPingPongHasConnectionIssueNoGate(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	if tr.HasConnectionIssue() {
		t.Fatal("HasConnectionIssue without gate must return false")
	}
}

func TestPingPongHasConnectionIssueGateTrue(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	tr.SetConnectionIssueGate(func() bool { return true })
	if !tr.HasConnectionIssue() {
		t.Fatal("HasConnectionIssue with gate=true must return true")
	}
}

func TestPingPongHasConnectionIssueGateFalse(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	tr.SetConnectionIssueGate(func() bool { return false })
	if tr.HasConnectionIssue() {
		t.Fatal("HasConnectionIssue with gate=false must return false")
	}
}

// ─── Size ─────────────────────────────────────────────────────────────

func TestPingPongSizeIsZeroOnNew(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	if tr.Size() != 0 {
		t.Fatalf("Size=%d on new tracker, want 0", tr.Size())
	}
}

func TestPingPongSizePendingPlusUnknown(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: time.Minute,
		UnknownTTL: time.Minute,
	})

	tr.RecordPing("p1")
	tr.RecordPing("p2")
	// Orphan pong — ends up in unknown.
	tr.RecordPong("u1")

	// pending=2, unknown=1 → Size=3.
	if got := tr.Size(); got != 3 {
		t.Fatalf("Size=%d, want 3 (2 pending + 1 unknown)", got)
	}
}

func TestPingPongSizeAfterClear(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{PendingTTL: time.Minute})
	tr.RecordPing("x")
	tr.Clear()
	if got := tr.Size(); got != 0 {
		t.Fatalf("Size=%d after Clear, want 0", got)
	}
}

// ─── AllowedDelta ─────────────────────────────────────────────────────

func TestPingPongAllowedDelta(t *testing.T) {
	t.Parallel()
	const delta = 7
	tr := NewPingPongTracker(PingPongConfig{MismatchThreshold: delta})
	if got := tr.AllowedDelta(); got != delta {
		t.Fatalf("AllowedDelta=%d, want %d", got, delta)
	}
}

func TestPingPongAllowedDeltaDefault(t *testing.T) {
	t.Parallel()
	// Zero MismatchThreshold is filled with the parity default of 15
	// (PING_PONG_MISMATCH_COUNT, const.py:316). Negative values opt
	// out of escalation entirely.
	tr := NewPingPongTracker(PingPongConfig{})
	if got := tr.AllowedDelta(); got != defaultPingPongMismatchThreshold {
		t.Fatalf("AllowedDelta=%d on zero cfg, want %d (parity default)", got, defaultPingPongMismatchThreshold)
	}
}

// ─── RetryReconcilePong ───────────────────────────────────────────────

// TestRetryReconcilePongMatchesLatePing verifies that if a PONG arrived as
// unknown and the corresponding PING arrives shortly after, RetryReconcilePong
// removes both from their tables.
func TestRetryReconcilePongMatchesLatePing(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Now())
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: time.Minute,
		UnknownTTL: time.Minute,
		Clock:      fake,
	})

	// The PONG arrives first (unknown), then the PING.
	tr.RecordPong("late-tok") // → unknown
	tr.RecordPing("late-tok") // → pending (PING was late)

	if tr.UnknownCount() != 1 {
		t.Fatalf("UnknownCount=%d before retry, want 1", tr.UnknownCount())
	}
	if tr.PendingCount() != 1 {
		t.Fatalf("PendingCount=%d before retry, want 1", tr.PendingCount())
	}

	tr.RetryReconcilePong("late-tok")

	if tr.UnknownCount() != 0 {
		t.Errorf("UnknownCount=%d after retry, want 0", tr.UnknownCount())
	}
	if tr.PendingCount() != 0 {
		t.Errorf("PendingCount=%d after retry, want 0", tr.PendingCount())
	}
}

// TestRetryReconcilePongTokenNotPending verifies that RetryReconcilePong
// is a no-op (leaves unknown table unchanged) when the token is not in pending.
func TestRetryReconcilePongTokenNotPending(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{
		UnknownTTL: time.Minute,
	})

	tr.RecordPong("orphan") // → unknown, no matching PING exists

	tr.RetryReconcilePong("orphan") // PING still absent — no reconcile

	// Unknown entry should remain (not yet expired).
	if tr.UnknownCount() != 1 {
		t.Errorf("UnknownCount=%d after failed retry, want 1", tr.UnknownCount())
	}
}

// TestRetryReconcilePongEvictsExpiredEntries verifies that
// RetryReconcilePong purges expired entries from both tables as a
// side-effect (mirrors Python _cleanup_tracker call at start of _retry_reconcile_pong).
func TestRetryReconcilePongEvictsExpiredEntries(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(time.Now())
	const ttl = 50 * time.Millisecond
	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: ttl,
		UnknownTTL: ttl,
		Clock:      fake,
	})

	tr.RecordPing("stale-p")
	tr.RecordPong("stale-u") // → unknown

	fake.Advance(ttl + time.Millisecond)

	// Trigger retry for a different token — should still evict stale entries.
	tr.RetryReconcilePong("nonexistent-token")

	if tr.PendingCount() != 0 {
		t.Errorf("PendingCount=%d after retry eviction, want 0", tr.PendingCount())
	}
	if tr.UnknownCount() != 0 {
		t.Errorf("UnknownCount=%d after retry eviction, want 0", tr.UnknownCount())
	}
}

// ─── ScheduleUnknownPongRetry ─────────────────────────────────────────

// TestScheduleUnknownPongRetryCallsScheduleFn verifies that the schedule
// function is called once with the right token and delay.
func TestScheduleUnknownPongRetryCallsScheduleFn(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{UnknownTTL: time.Minute})
	tr.RecordPong("u1") // → unknown

	var scheduledToken string
	var scheduledDelay time.Duration
	scheduleFn := func(token string, delay time.Duration, retry func(string)) {
		scheduledToken = token
		scheduledDelay = delay
		// Immediately execute the retry to test cleanup.
		retry(token)
	}

	tr.ScheduleUnknownPongRetry("u1", 15*time.Second, scheduleFn)

	if scheduledToken != "u1" {
		t.Fatalf("scheduled token=%q, want u1", scheduledToken)
	}
	if scheduledDelay != 15*time.Second {
		t.Fatalf("scheduled delay=%v, want 15s", scheduledDelay)
	}
}

// TestScheduleUnknownPongRetryCoalesces verifies that a second schedule
// call for the same token while one is already pending is a no-op.
func TestScheduleUnknownPongRetryCoalesces(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{UnknownTTL: time.Minute})

	var calls atomic.Int32
	// Schedule function that does NOT invoke the retry — simulates async scheduling.
	noopSchedule := func(_ string, _ time.Duration, _ func(string)) {
		calls.Add(1)
	}

	tr.ScheduleUnknownPongRetry("tok", time.Second, noopSchedule)
	tr.ScheduleUnknownPongRetry("tok", time.Second, noopSchedule) // duplicate

	if calls.Load() != 1 {
		t.Fatalf("schedule called %d times, want 1 (coalesced)", calls.Load())
	}
}

// TestScheduleUnknownPongRetryNilScheduleIsNoop verifies that passing
// a nil schedule function is safe and does not panic.
func TestScheduleUnknownPongRetryNilScheduleIsNoop(t *testing.T) {
	t.Parallel()
	tr := NewPingPongTracker(PingPongConfig{})
	// Should not panic.
	tr.ScheduleUnknownPongRetry("tok", time.Second, nil)
}
