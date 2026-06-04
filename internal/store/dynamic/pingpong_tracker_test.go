// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// pingpong_tracker_test.go — tests for PingPongCombinedTracker and
// PongTracker.CleanupTracker.

package dynamic

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// PongTracker.CleanupTracker — TTL and size enforcement
// ---------------------------------------------------------------------------

func TestPongTrackerCleanupTrackerTTL(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	now := time.Now()

	// Add two tokens: one old, one fresh.
	pt.Add("old-token", now.Add(-120*time.Second))
	pt.Add("fresh-token", now)

	removed := pt.CleanupTracker(60*time.Second, 0)

	if removed != 1 {
		t.Errorf("CleanupTracker removed %d, want 1 (only old-token)", removed)
	}
	if pt.Contains("old-token") {
		t.Error("old-token must have been removed by TTL")
	}
	if !pt.Contains("fresh-token") {
		t.Error("fresh-token must still be present")
	}
}

func TestPongTrackerCleanupTrackerSizeLimit(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	base := time.Now()

	// Add 5 tokens with distinct, ascending timestamps.
	for i := range 5 {
		pt.Add(string(rune('a'+i)), base.Add(time.Duration(i)*time.Second))
	}

	// Cap at 3 — oldest 2 should be evicted.
	removed := pt.CleanupTracker(0, 3)

	if pt.Len() != 3 {
		t.Errorf("Len after size-limit cleanup = %d, want 3", pt.Len())
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	// Tokens 'a' and 'b' were oldest; they must be gone.
	if pt.Contains("a") {
		t.Error("oldest token 'a' must have been evicted")
	}
	if pt.Contains("b") {
		t.Error("2nd oldest token 'b' must have been evicted")
	}
	// Tokens 'c', 'd', 'e' must survive.
	for _, tok := range []string{"c", "d", "e"} {
		if !pt.Contains(tok) {
			t.Errorf("token %q must still be present after size-limit eviction", tok)
		}
	}
}

func TestPongTrackerCleanupTrackerNoOp(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	pt.Add("tok", time.Now())

	// Both limits disabled (maxAge=0, maxSize=0).
	removed := pt.CleanupTracker(0, 0)

	if removed != 0 {
		t.Errorf("CleanupTracker with both disabled removed %d, want 0", removed)
	}
	if pt.Len() != 1 {
		t.Errorf("Len = %d, want 1", pt.Len())
	}
}

func TestPongTrackerCleanupTrackerEmpty(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	removed := pt.CleanupTracker(60*time.Second, 100)
	if removed != 0 {
		t.Errorf("CleanupTracker on empty tracker removed %d, want 0", removed)
	}
}

// ---------------------------------------------------------------------------
// PingPongCombinedTracker — basic construction and lifecycle
// ---------------------------------------------------------------------------

func TestPingPongCombinedTrackerNew(t *testing.T) {
	t.Parallel()
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID: "BidCos-RF",
	})
	if tr == nil {
		t.Fatal("NewPingPongCombinedTracker must not return nil")
	}
	if tr.AllowedDelta() != 15 {
		t.Errorf("default AllowedDelta=%d want 15 (PING_PONG_MISMATCH_COUNT, const.py:316)", tr.AllowedDelta())
	}
	if tr.Size() != 0 {
		t.Errorf("initial Size=%d want 0", tr.Size())
	}
	if tr.Journal() == nil {
		t.Error("Journal must not be nil")
	}
}

func TestPingPongCombinedTrackerHandleSendPing(t *testing.T) {
	t.Parallel()
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID:  "BidCos-RF",
		AllowedDelta: 10,
	})
	ts := time.Now()
	tr.HandleSendPing("BidCos-RF", ts)

	if tr.pending.Len() != 1 {
		t.Errorf("pending.Len()=%d want 1 after HandleSendPing", tr.pending.Len())
	}
	if tr.Size() != 1 {
		t.Errorf("Size()=%d want 1 after HandleSendPing", tr.Size())
	}
}

func TestPingPongCombinedTrackerHandleReceivedPongMatches(t *testing.T) {
	t.Parallel()
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID:  "BidCos-RF",
		AllowedDelta: 10,
	})
	ts := time.Now()
	// Send a ping, then receive the matching pong.
	tr.HandleSendPing("BidCos-RF", ts)
	tr.HandleReceivedPong("BidCos-RF", ts)

	if tr.pending.Len() != 0 {
		t.Errorf("pending.Len()=%d want 0 after matching pong", tr.pending.Len())
	}
}

func TestPingPongCombinedTrackerHandleReceivedPongUnknown(t *testing.T) {
	t.Parallel()
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID:  "BidCos-RF",
		AllowedDelta: 10,
	})
	// Receive a pong without a preceding ping.
	tr.HandleReceivedPong("BidCos-RF", time.Now())

	if tr.unknown.Len() != 1 {
		t.Errorf("unknown.Len()=%d want 1 for orphan pong", tr.unknown.Len())
	}
}

func TestPingPongCombinedTrackerClear(t *testing.T) {
	t.Parallel()
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{InterfaceID: "BidCos-RF"})
	tr.HandleSendPing("BidCos-RF", time.Now())
	tr.Clear()

	if tr.Size() != 0 {
		t.Errorf("Size()=%d want 0 after Clear()", tr.Size())
	}
}

func TestPingPongCombinedTrackerConnectionIssueSkipsTracking(t *testing.T) {
	t.Parallel()
	hasIssue := true
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID:        "BidCos-RF",
		HasConnectionIssue: func() bool { return hasIssue },
	})
	tr.HandleSendPing("BidCos-RF", time.Now())

	if tr.pending.Len() != 0 {
		t.Errorf("pending.Len()=%d want 0 when HasConnectionIssue=true", tr.pending.Len())
	}
}

func TestPingPongCombinedTrackerThresholdPublish(t *testing.T) {
	t.Parallel()
	var publishCount atomic.Int32
	tr := NewPingPongCombinedTracker(PingPongCombinedConfig{
		InterfaceID:  "BidCos-RF",
		AllowedDelta: 2,
		OnPublish: func(kind hmenum.PingPongMismatchType, count int) {
			publishCount.Add(1)
		},
	})
	// Send 3 pings (> threshold of 2). Each HandleSendPing may call OnPublish.
	for i := range 3 {
		tr.HandleSendPing("BidCos-RF", time.Now().Add(time.Duration(i)*time.Millisecond))
	}

	if publishCount.Load() == 0 {
		t.Error("OnPublish must have been called at least once when pending > AllowedDelta")
	}
}

// ---------------------------------------------------------------------------
// PongTracker stats: RecordEviction and ResetStats
// ---------------------------------------------------------------------------

func TestPongTrackerRecordEviction(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	pt.RecordEviction(2)
	if got := pt.Evictions(); got != 2 {
		t.Errorf("Evictions()=%d want 2", got)
	}
	pt.RecordEviction(0) // 0 → 1
	if got := pt.Evictions(); got != 3 {
		t.Errorf("Evictions after RecordEviction(0)=%d want 3", got)
	}
}

func TestPongTrackerResetStats(t *testing.T) {
	t.Parallel()
	pt := NewPongTracker()
	pt.RecordEviction(5)
	pt.ResetStats()
	if got := pt.Evictions(); got != 0 {
		t.Errorf("Evictions after ResetStats=%d want 0", got)
	}
}
