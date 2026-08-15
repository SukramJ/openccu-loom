// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPingPongPublishHookFiresAboveThreshold verifies that — when the pending
// count crosses [MismatchThreshold] the publish hook fires with kind=Pending.
func TestPingPongPublishHookFiresAboveThreshold(t *testing.T) {
	t.Parallel()

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        time.Minute,
		UnknownTTL:        time.Minute,
		MismatchThreshold: 3,
		Clock:             clock.NewFake(time.Now()),
	})

	var (
		mu    sync.Mutex
		calls []struct {
			Kind  hmenum.PingPongMismatchType
			Count int
		}
	)
	tr.SetPublishHook(func(kind hmenum.PingPongMismatchType, count int) {
		mu.Lock()
		calls = append(calls, struct {
			Kind  hmenum.PingPongMismatchType
			Count int
		}{kind, count})
		mu.Unlock()
	})

	for i := range 6 {
		tr.RecordPing(string(rune('A' + i)))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("publish hook never fired even though pending count exceeded threshold=3 with 6 pings")
	}
	for _, c := range calls {
		if c.Kind != hmenum.PingPongMismatchPending {
			t.Errorf("expected kind=Pending, got %s", c.Kind)
		}
		if c.Count <= 3 {
			t.Errorf("publish count %d must be > threshold 3", c.Count)
		}
	}
}

// TestPingPongPublishHookFiresOnUnknownAboveThreshold verifies the
// unknown-side threshold path: orphan PONGs (no matching PING) cross
// the threshold and the hook fires with kind=Unknown.
func TestPingPongPublishHookFiresOnUnknownAboveThreshold(t *testing.T) {
	t.Parallel()

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        time.Minute,
		UnknownTTL:        time.Minute,
		MismatchThreshold: 2,
	})

	var got atomic.Int32
	tr.SetPublishHook(func(kind hmenum.PingPongMismatchType, _ int) {
		if kind == hmenum.PingPongMismatchUnknown {
			got.Add(1)
		}
	})

	// Send 4 orphan PONGs — each lands in the Unknown table.
	for i := range 4 {
		tr.RecordPong(string(rune('X' + i)))
	}

	if got.Load() == 0 {
		t.Fatal("publish hook never fired for unknown-overflow")
	}
}

// TestPingPongConnectionIssueGate verifies that RecordPing skips tracking
// when the configured gate returns true.
func TestPingPongConnectionIssueGate(t *testing.T) {
	t.Parallel()

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: time.Minute,
		UnknownTTL: time.Minute,
	})

	var blocked atomic.Bool
	tr.SetConnectionIssueGate(blocked.Load)

	// Gate closed → ping not tracked.
	blocked.Store(true)
	tr.RecordPing("A")
	if got := tr.PendingCount(); got != 0 {
		t.Fatalf("PendingCount=%d, want 0 (gate closed)", got)
	}

	// Gate open → ping tracked.
	blocked.Store(false)
	tr.RecordPing("B")
	if got := tr.PendingCount(); got != 1 {
		t.Fatalf("PendingCount=%d, want 1 (gate open)", got)
	}
}

// TestPingPongGateInstallIsSafeAgainstConcurrentRecordPing reproduces the
// production window: the client is published into the central's client
// registry before the ping/pong wiring installs the connection-issue gate, so
// the periodic connection check can be inside RecordPing while the gate is
// still being assigned. Run under -race, an unsynchronised read of the func
// field fails here.
func TestPingPongGateInstallIsSafeAgainstConcurrentRecordPing(t *testing.T) {
	t.Parallel()

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL: time.Minute,
		UnknownTTL: time.Minute,
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			tr.RecordPing(strconv.Itoa(i))
		}
	}()
	go func() {
		defer wg.Done()
		var blocked atomic.Bool
		for range 200 {
			tr.SetConnectionIssueGate(blocked.Load)
		}
	}()
	wg.Wait()
}

// TestPingPongPublishHookOnRecordPongDrop verifies that when a matched PONG
// drops the pending count but it remains above threshold, the hook still
// fires.
func TestPingPongPublishHookOnRecordPongDrop(t *testing.T) {
	t.Parallel()

	tr := NewPingPongTracker(PingPongConfig{
		PendingTTL:        time.Minute,
		UnknownTTL:        time.Minute,
		MismatchThreshold: 2,
	})

	// Build pending up to 5 (above threshold=2).
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		tr.RecordPing(id)
	}

	var firedPendingDrop atomic.Int32
	tr.SetPublishHook(func(kind hmenum.PingPongMismatchType, count int) {
		if kind == hmenum.PingPongMismatchPending {
			firedPendingDrop.Add(1)
		}
		_ = count
	})

	// Match one — pending drops 5 → 4, still > 2 threshold.
	tr.RecordPong("a")
	if firedPendingDrop.Load() == 0 {
		t.Fatal("publish hook should fire when matched PONG drops pending but it stays above threshold")
	}
}
