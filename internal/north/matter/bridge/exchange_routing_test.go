// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestExchangeRouting_ExchangeSrcsRoundTrip locks in the Store /
// LoadAndDelete / Delete behaviour [Bridge.owedInboundAck] /
// [Bridge.emitStandaloneAck] / [Bridge.dischargeOwedAck] rely on: a
// stored target is retrievable exactly once via LoadAndDelete, and an
// explicit Delete removes it without requiring a load first.
func TestExchangeRouting_ExchangeSrcsRoundTrip(t *testing.T) {
	var r exchangeRouting
	key := mrp.ExchangeKey{SessionID: 1, ExchangeID: 7}
	want := exchangeReplyTarget{
		src:       &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 5540},
		sessionID: 1,
	}

	r.exchangeSrcs.Store(key, want)

	raw, ok := r.exchangeSrcs.LoadAndDelete(key)
	if !ok {
		t.Fatal("LoadAndDelete: not found")
	}
	got, ok := raw.(exchangeReplyTarget)
	if !ok {
		t.Fatalf("LoadAndDelete: value type = %T, want exchangeReplyTarget", raw)
	}
	if got.sessionID != want.sessionID {
		t.Errorf("got = %+v, want %+v", got, want)
	}

	// A second LoadAndDelete on the same key must miss — the first
	// call already consumed the entry.
	if _, ok := r.exchangeSrcs.LoadAndDelete(key); ok {
		t.Error("second LoadAndDelete unexpectedly found an entry")
	}

	// Delete is a no-op on a missing key — must not panic.
	r.exchangeSrcs.Delete(key)
}

// TestExchangeRouting_TimedDeadlinesSessionScoped verifies the
// (sessionID, exchangeID) composite key keeps two sessions that reuse
// the same exchangeID from colliding — the invariant
// [Bridge.checkTimedGate] depends on to reject a cross-session replay.
func TestExchangeRouting_TimedDeadlinesSessionScoped(t *testing.T) {
	var r exchangeRouting
	now := time.Now()
	keyA := timedKey{sessionID: 1, exchangeID: 42}
	keyB := timedKey{sessionID: 2, exchangeID: 42}

	r.timedDeadlines.Store(keyA, now.Add(10*time.Second))

	if _, ok := r.timedDeadlines.Load(keyB); ok {
		t.Fatal("session B unexpectedly sees session A's deadline")
	}
	raw, ok := r.timedDeadlines.LoadAndDelete(keyA)
	if !ok {
		t.Fatal("session A: deadline not found")
	}
	if _, ok := raw.(time.Time); !ok {
		t.Fatalf("value type = %T, want time.Time", raw)
	}
}

// TestExchangeRouting_SubTargetsRoundTrip locks in the Store / Load /
// Delete cycle [Bridge.captureSubTarget] / [Bridge.reportSubscription]
// / [Bridge.closeSubscriptionByCounter] rely on to route ongoing
// reports back to the subscribing peer.
func TestExchangeRouting_SubTargetsRoundTrip(t *testing.T) {
	var r exchangeRouting
	const subID = uint32(99)
	want := subTarget{sessionID: 3, exchangeID: 5, fabricIndex: 1}

	if _, ok := r.subTargets.Load(subID); ok {
		t.Fatal("unexpected hit on empty table")
	}

	r.subTargets.Store(subID, want)
	raw, ok := r.subTargets.Load(subID)
	if !ok {
		t.Fatal("Load: not found after Store")
	}
	got, ok := raw.(subTarget)
	if !ok {
		t.Fatalf("value type = %T, want subTarget", raw)
	}
	if got.sessionID != want.sessionID || got.exchangeID != want.exchangeID || got.fabricIndex != want.fabricIndex {
		t.Errorf("got = %+v, want %+v", got, want)
	}

	r.subTargets.Delete(subID)
	if _, ok := r.subTargets.Load(subID); ok {
		t.Error("entry still present after Delete")
	}
}

// TestExchangeRouting_StatusResponseWaitsSwap locks in the Swap /
// LoadAndDelete semantics [Bridge.armStatusResponseWait] /
// [Bridge.disarmStatusResponseWait] / [Bridge.signalStatusResponseRX]
// depend on: arming a second waiter on the same key returns the prior
// channel (via `loaded=true`) so the caller can release it, and a
// consumed wait cannot be found again.
func TestExchangeRouting_StatusResponseWaitsSwap(t *testing.T) {
	var r exchangeRouting
	key := mrp.ExchangeKey{SessionID: 4, ExchangeID: 9}

	first := make(chan struct{})
	if _, loaded := r.statusResponseWaits.Swap(key, first); loaded {
		t.Fatal("first arm: unexpectedly loaded a prior entry")
	}

	second := make(chan struct{})
	prev, loaded := r.statusResponseWaits.Swap(key, second)
	if !loaded {
		t.Fatal("second arm: expected to observe the first channel")
	}
	if prevCh, ok := prev.(chan struct{}); !ok || prevCh != first {
		t.Errorf("swapped-out value = %v, want the first channel", prev)
	}

	raw, ok := r.statusResponseWaits.LoadAndDelete(key)
	if !ok {
		t.Fatal("LoadAndDelete: entry missing after second arm")
	}
	if ch, ok := raw.(chan struct{}); !ok || ch != second {
		t.Error("LoadAndDelete returned the wrong channel")
	}

	if _, ok := r.statusResponseWaits.LoadAndDelete(key); ok {
		t.Error("entry still present after consumption")
	}
}
