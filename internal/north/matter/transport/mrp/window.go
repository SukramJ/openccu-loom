// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mrp

import "sync"

// windowSize is the Matter-mandated count of recently-received
// counters tracked for duplicate detection. Per Core Spec §4.5.4.1 a
// 32-entry sliding window is tracked, plus the highest-seen counter
// itself (kept separately). Mirrors matter.js
// packages/protocol/src/protocol/MessageReceptionState.ts
// MSG_COUNTER_WINDOW_SIZE.
const windowSize = 32

// maxCounter32 is the largest 32-bit message counter. The rollover
// variant folds distances around it, so a counter on the far side of the
// wrap is measured by its short modular distance rather than its
// numeric one. Mirrors matter.js MAX_COUNTER_VALUE_32BIT.
const maxCounter32 = int64(0xFFFFFFFF)

// Window is the sliding-window duplicate detector for inbound message
// counters. It tracks the highest counter seen (max) separately and a
// 32-bit bitmap where bit i records whether counter (max-(i+1)) has
// been received — so the full window covers max-1 .. max-32.
//
// Two variants exist, mirroring matter.js MessageReceptionState:
//
//   - [NewWindow] — the rollover variant used for unsecured sessions,
//     mirroring matter.js MessageReceptionStateUnencryptedWithRollover.
//     Only the 32 counters directly below max count as "behind";
//     everything further back is a restarted free-running counter and
//     rolls the window forward onto it. See [Window.diff].
//   - [NewWindowNoRollover] — the no-rollover variant used for secure
//     sessions, mirroring matter.js
//     MessageReceptionStateEncryptedWithoutRollover (NodeSession.ts:118).
//     Diffs are plain subtraction: a counter that appears to roll over
//     is rejected as a duplicate, because a secure session MUST
//     re-establish before its counter wraps rather than reuse a nonce.
//
// The two variants also seed the bitmap differently when they anchor on
// their first counter — see [Window.initialBitmap].
//
// Concurrency: Window is safe for concurrent use; lookups and updates
// are serialised through an internal mutex.
type Window struct {
	mu sync.Mutex
	// initialBitmap seeds bitmap when the window anchors on its first
	// counter. Mirrors matter.js MessageReceptionState.ts
	// initialBitmap: all-1s for encrypted messages, so every sub-anchor
	// counter counts as already-received (Core Spec §4.5.4.1 replay
	// protection); 0 for unencrypted messages, where duplicate
	// detection is not a security control and a message reordered just
	// below the first one seen must stay acceptable.
	initialBitmap uint32
	max           uint32 // highest counter seen (tracked separately from the bitmap)
	bitmap        uint32 // bit i set ⇒ counter (max-(i+1)) was received
	primed        bool   // false until the first counter is recorded
	rollover      bool   // true ⇒ fold distances at ±windowSize; false ⇒ plain subtraction
}

// NewWindow returns a fresh rollover duplicate-detection window for
// unsecured sessions. The bitmap starts empty, mirroring matter.js
// MessageReceptionStateUnencryptedWithRollover.
func NewWindow() *Window { return &Window{rollover: true, initialBitmap: 0} }

// NewWindowNoRollover returns a fresh no-rollover window for secure
// sessions. A secure-session counter that wraps is rejected rather than
// accepted, and the bitmap anchors full so no counter below the first
// one seen is ever accepted, matching matter.js
// MessageReceptionStateEncryptedWithoutRollover.
func NewWindowNoRollover() *Window { return &Window{rollover: false, initialBitmap: ^uint32(0)} }

// diff computes the signed distance between counter c and the current
// max.
//
// For the no-rollover variant it is plain subtraction (matter.js
// MessageReceptionStateEncryptedWithoutRollover.calculateDiff).
//
// For the rollover variant it folds at ±[windowSize], mirroring matter.js
// MessageReceptionStateUnencryptedWithRollover.calculateDiff. Only the 32
// counters immediately below max are read as "behind"; anything further
// back is reported as a large forward distance so [Window.Accept] rolls
// the window onto it. A free-running unsecured counter restarts whenever
// its peer reboots, and treating that restart as a 4-billion-message
// replay would blank the peer until it climbed back over the old maximum.
// The symmetric case — a counter just below max but on the far side of
// the 2^32 wrap — folds back into the window instead of reading as a
// forward jump.
func (w *Window) diff(c uint32) int64 {
	d := int64(c) - int64(w.max)
	if !w.rollover {
		return d
	}
	switch {
	case d > 0 && maxCounter32-d < windowSize:
		d -= maxCounter32 + 1 // pre-wrap counter: just behind max, not far ahead
	case d < -windowSize:
		d += maxCounter32 + 1 // peer restarted its counter: roll the window forward onto c
	}
	return d
}

// Accept records a received counter and reports whether it is fresh
// (returns true) or a duplicate / out-of-window (returns false — the
// caller drops the message). Mirrors matter.js
// MessageReceptionState.updateMessageCounter.
func (w *Window) Accept(c uint32) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.primed {
		w.max = c
		w.bitmap = w.initialBitmap
		w.primed = true
		return true
	}
	if c == w.max {
		return false // equals the maximum ⇒ duplicate
	}

	d := w.diff(c)
	if d < 0 {
		// Numerically (or modularly) behind the maximum. -d-1 ∈ [0, 31].
		if d < -windowSize {
			return false // beyond the window ⇒ duplicate
		}
		bit := uint32(1) << (-d - 1)
		if w.bitmap&bit != 0 {
			return false // already seen within the window ⇒ duplicate
		}
		w.bitmap |= bit
		return true
	}

	// Ahead of the maximum: advance the window and record the old max.
	// d ∈ [1, windowSize] here; d-1 ∈ [0, 31].
	if d <= windowSize {
		var shifted uint32
		if d < windowSize {
			shifted = w.bitmap << d
		}
		w.bitmap = shifted | (uint32(1) << (d - 1))
	} else {
		w.bitmap = 0 // jump larger than the window: no prior counters known
	}
	w.max = c
	return true
}
