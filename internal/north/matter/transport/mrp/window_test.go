// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import "testing"

// TestWindowTracksFullThirtyTwoCounters — the reception window covers
// max-1 .. max-32 (32 historical counters), rejecting only counters
// more than 32 behind the maximum. Mirrors matter.js
// MessageReceptionState.ts (reject only when diff < -MSG_COUNTER_WINDOW_SIZE).
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
	// A counter 33 behind the maximum is beyond the window.
	if w.Accept(67) {
		t.Fatal("Accept(67) = max-33 should be rejected (beyond the 32-window)")
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

// TestRolloverWindowAcceptsWrappedCounter — the unsecured rollover
// window treats a wrap-around as a fresh, advancing counter (±2^31
// modular fold). Mirrors matter.js
// MessageReceptionStateEncryptedWithRollover.
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
