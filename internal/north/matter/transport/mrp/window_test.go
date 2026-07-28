// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import "testing"

// TestWindowTracksFullThirtyTwoCounters — the reception window covers
// max-1 .. max-32 (32 historical counters); only within that span does a
// counter get duplicate-checked against the bitmap. Mirrors matter.js
// MessageReceptionState.ts (bitmap lookup only when
// diff >= -MSG_COUNTER_WINDOW_SIZE).
func TestWindowTracksFullThirtyTwoCounters(t *testing.T) {
	w := NewWindow()
	if !w.Accept(100) {
		t.Fatal("prime Accept(100) should be fresh")
	}
	// A counter exactly 32 behind the maximum is still inside the window.
	if !w.Accept(68) {
		t.Fatal("Accept(68) = max-32 should be fresh (inside the 32-window)")
	}
	if w.Accept(68) {
		t.Fatal("Accept(68) again should be a duplicate")
	}
	// One step further back leaves the window. On the unsecured variant
	// that is a restarted peer counter, not a replay, so the window rolls
	// forward onto it — see TestRolloverWindowRollsBackOnPeerRestart. The
	// secure variant rejects the same counter
	// (TestNoRolloverWindowRejectsStaleAfterAdvance).
	if !w.Accept(67) {
		t.Fatal("Accept(67) = max-33 should roll the unsecured window back, not be dropped")
	}
	if w.Accept(67) {
		t.Fatal("Accept(67) again should be a duplicate (it is the new maximum)")
	}
}

// TestNoRolloverWindowRejectsWrappedCounter — a secure-session window
// must reject a counter that appears to roll over (plain subtraction,
// no ±2^31 fold). Mirrors matter.js
// MessageReceptionStateEncryptedWithoutRollover — secure sessions
// re-establish before the counter wraps.
func TestNoRolloverWindowRejectsWrappedCounter(t *testing.T) {
	w := NewWindowNoRollover()
	if !w.Accept(0xFFFFFFF0) {
		t.Fatal("prime near the top of the counter space should be fresh")
	}
	if w.Accept(0x00000005) {
		t.Fatal("a wrapped counter must be rejected on a no-rollover (secure) window")
	}
}

// TestNoRolloverWindowSeedsFullBitmap — a secure-session window anchors
// on the first counter it sees and marks every sub-maximum counter as
// already-received, so a message below the anchor is a duplicate even
// though the window has never observed it. Mirrors matter.js
// MessageReceptionStateEncryptedWithoutRollover.initialBitmap (all-1s,
// packages/protocol/src/protocol/MessageReceptionState.ts) and the
// Core Spec §4.5.4.1 replay rule for encrypted messages.
func TestNoRolloverWindowSeedsFullBitmap(t *testing.T) {
	w := NewWindowNoRollover()
	if !w.Accept(100) {
		t.Fatal("prime Accept(100) should be fresh")
	}
	if w.Accept(99) {
		t.Fatal("Accept(99) = anchor-1 must be a duplicate on a secure window (bitmap seeded all-1s)")
	}
	if w.Accept(68) {
		t.Fatal("Accept(68) = anchor-32 must be a duplicate on a secure window (bitmap seeded all-1s)")
	}
	// Advancing past the anchor stays fresh — the seed only closes the
	// backwards direction.
	if !w.Accept(101) {
		t.Fatal("Accept(101) ahead of the anchor should be fresh")
	}
	if w.Accept(100) {
		t.Fatal("Accept(100) after advancing should be a duplicate")
	}
}

// TestRolloverWindowSeedsEmptyBitmap — the unsecured / PASE window
// starts with an empty bitmap, so a legitimately reordered message just
// below the first counter observed is still accepted. Mirrors matter.js
// MessageReceptionStateUnencryptedWithRollover.initialBitmap (0):
// unencrypted duplicate detection is not a security control, and CHIP
// has never seeded a full window there.
func TestRolloverWindowSeedsEmptyBitmap(t *testing.T) {
	w := NewWindow()
	if !w.Accept(100) {
		t.Fatal("prime Accept(100) should be fresh")
	}
	if !w.Accept(99) {
		t.Fatal("Accept(99) = anchor-1 must stay acceptable on an unsecured window (bitmap seeded empty)")
	}
	if w.Accept(99) {
		t.Fatal("Accept(99) again should be a duplicate")
	}
}

// TestRolloverWindowRollsBackOnPeerRestart — on the unsecured window a
// counter more than 32 behind the maximum is NOT a duplicate: it means
// the peer restarted its free-running counter, so the window rolls back
// onto the new counter. Mirrors matter.js
// MessageReceptionStateUnencryptedWithRollover, whose class comment
// spells this out: "Messages with a counter behind the window are likely
// caused by a node rebooting and are thus processed as rolling back the
// window to the current location."
func TestRolloverWindowRollsBackOnPeerRestart(t *testing.T) {
	w := NewWindow()
	if !w.Accept(1000) {
		t.Fatal("prime Accept(1000) should be fresh")
	}
	// Far behind the window — a restarted peer, not a replay.
	if !w.Accept(10) {
		t.Fatal("Accept(10) far behind the maximum must roll the window back, not be dropped")
	}
	// The window now tracks the restarted trajectory.
	if !w.Accept(11) {
		t.Fatal("Accept(11) after the rollback should be fresh")
	}
	if w.Accept(10) {
		t.Fatal("Accept(10) again should be a duplicate inside the rolled-back window")
	}
}

// TestNoRolloverWindowRejectsStaleAfterAdvance — the secure window keeps
// the strict reading: anything more than 32 behind the maximum is a
// duplicate, never a rollback. Plain subtraction, no fold. Mirrors
// matter.js MessageReceptionStateEncryptedWithoutRollover. Distinct from
// TestNoRolloverWindowRejectsStale, which covers the same rule while the
// window still carries its seeded all-1s bitmap: here the bitmap has been
// cleared by the jump, so the stale-distance check is what decides.
func TestNoRolloverWindowRejectsStaleAfterAdvance(t *testing.T) {
	w := NewWindowNoRollover()
	if !w.Accept(100) {
		t.Fatal("prime Accept(100) should be fresh")
	}
	// Advance past the anchor so the seeded all-1s bitmap is cleared and
	// the stale-rejection path is what actually decides.
	if !w.Accept(200) {
		t.Fatal("Accept(200) ahead of the anchor should be fresh")
	}
	if !w.Accept(168) {
		t.Fatal("Accept(168) = max-32 should be fresh (inside the 32-window)")
	}
	if w.Accept(167) {
		t.Fatal("Accept(167) = max-33 must be a duplicate on a secure window (no rollback)")
	}
}

// TestRolloverWindowAcceptsWrappedCounter — the unsecured rollover
// window treats a counter that wrapped past 2^32 as a fresh, advancing
// counter, and still recognises the pre-wrap counter as being behind the
// new maximum. Mirrors matter.js
// MessageReceptionStateUnencryptedWithRollover.calculateDiff.
func TestRolloverWindowAcceptsWrappedCounter(t *testing.T) {
	w := NewWindow()
	if !w.Accept(0xFFFFFFF0) {
		t.Fatal("prime near the top of the counter space should be fresh")
	}
	if !w.Accept(0x00000005) {
		t.Fatal("a wrapped counter must be accepted (fresh) on a rollover window")
	}
	// Now the maximum has advanced to 0x05; a replay of the pre-wrap
	// counter is behind the window and rejected.
	if w.Accept(0xFFFFFFF0) {
		t.Fatal("replay of the pre-wrap counter should be rejected after advance")
	}
}
