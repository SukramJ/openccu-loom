// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestNewCounter_SeedsWithin28BitRange verifies that NewCounter's
// crypto/rand-derived seed always lands in [1, 2^28], never using the
// full 32-bit range. matter.js MessageCounter.ts:60 computes
// `(crypto.randomUint32 >>> 4) + 1`; a full-width random seed could
// begin within a handful of messages of the 32-bit ceiling, which
// [mrp.Counter.NextNoRollover] would then refuse almost immediately.
func TestNewCounter_SeedsWithin28BitRange(t *testing.T) {
	t.Parallel()

	const ceiling = 1 << 28
	for i := range 1000 {
		c, err := mrp.NewCounter()
		if err != nil {
			t.Fatalf("iteration %d: NewCounter: %v", i, err)
		}
		seed := c.Peek()
		if seed < 1 {
			t.Fatalf("iteration %d: seed=%d, want >= 1", i, seed)
		}
		if seed > ceiling {
			t.Fatalf("iteration %d: seed=%d, want <= 2^28 (%d)", i, seed, ceiling)
		}
	}
}

// TestCounter_NextNoRollover_ReturnsSequentialValues verifies that
// repeated NextNoRollover calls behave like Next for a counter far
// from the 32-bit ceiling: current value returned, then advanced by 1.
func TestCounter_NextNoRollover_ReturnsSequentialValues(t *testing.T) {
	t.Parallel()

	c := mrp.NewCounterFromSeed(10)
	for i, want := range []uint32{10, 11, 12} {
		got, err := c.NextNoRollover()
		if err != nil {
			t.Fatalf("call %d: NextNoRollover: unexpected error %v", i, err)
		}
		if got != want {
			t.Fatalf("call %d: NextNoRollover = %d, want %d", i, got, want)
		}
	}
}

// TestCounter_NextNoRollover_ExhaustsAtCeiling pins the exact
// exhaustion boundary: seeded one below the 32-bit ceiling, the first
// call still succeeds (returning the pre-ceiling value and advancing
// to 0xFFFFFFFF), and every call from then on returns
// [mrp.ErrCounterExhausted] without advancing further — the counter
// is refused, never wrapped. Mirrors matter.js NodeSession.ts:111 +
// MessageCounter.ts:64-67 (a secure session's counter closes the
// session instead of rolling over).
func TestCounter_NextNoRollover_ExhaustsAtCeiling(t *testing.T) {
	t.Parallel()

	c := mrp.NewCounterFromSeed(0xFFFFFFFE)

	got, err := c.NextNoRollover()
	if err != nil {
		t.Fatalf("first call: unexpected error %v", err)
	}
	if got != 0xFFFFFFFE {
		t.Fatalf("first call: got %d, want 0xFFFFFFFE", got)
	}
	if peek := c.Peek(); peek != 0xFFFFFFFF {
		t.Fatalf("after first call: Peek=%d, want 0xFFFFFFFF", peek)
	}

	// Every subsequent call must fail with ErrCounterExhausted and
	// leave the counter pinned at the ceiling (never wraps to 0).
	for i := range 3 {
		got, err := c.NextNoRollover()
		if !errors.Is(err, mrp.ErrCounterExhausted) {
			t.Fatalf("call %d after exhaustion: err=%v, want ErrCounterExhausted", i, err)
		}
		if got != 0 {
			t.Fatalf("call %d after exhaustion: got=%d, want 0", i, got)
		}
		if peek := c.Peek(); peek != 0xFFFFFFFF {
			t.Fatalf("call %d after exhaustion: Peek=%d, want 0xFFFFFFFF (must not wrap)", i, peek)
		}
	}
}

// TestCounter_NextNoRollover_ExhaustedFromTheStart verifies that a
// counter already seeded at the ceiling refuses on the very first
// call.
func TestCounter_NextNoRollover_ExhaustedFromTheStart(t *testing.T) {
	t.Parallel()

	c := mrp.NewCounterFromSeed(0xFFFFFFFF)
	got, err := c.NextNoRollover()
	if !errors.Is(err, mrp.ErrCounterExhausted) {
		t.Fatalf("err=%v, want ErrCounterExhausted", err)
	}
	if got != 0 {
		t.Fatalf("got=%d, want 0", got)
	}
}
