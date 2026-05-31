// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// --- Counter ---

// TestCounterMonotonic confirms successive Next() calls return
// strictly increasing values from a deterministic seed.
func TestCounterMonotonic(t *testing.T) {
	c := NewCounterFromSeed(100)
	prev := c.Next()
	for range 10 {
		next := c.Next()
		if next != prev+1 {
			t.Fatalf("got %d after %d, want %d", next, prev, prev+1)
		}
		prev = next
	}
}

// TestCounterWrap covers the 2^32 wrap-around. After wrap the
// successor must be 0, not negative or stuck.
func TestCounterWrap(t *testing.T) {
	c := NewCounterFromSeed(0xFFFFFFFF)
	if c.Next() != 0xFFFFFFFF {
		t.Fatal("first Next did not return seed")
	}
	if c.Next() != 0 {
		t.Fatal("wrap did not produce 0")
	}
}

// TestCounterPeekDoesNotAdvance ensures Peek is non-mutating.
func TestCounterPeekDoesNotAdvance(t *testing.T) {
	c := NewCounterFromSeed(7)
	if c.Peek() != 7 {
		t.Fatal("first Peek did not return seed")
	}
	if c.Peek() != 7 {
		t.Fatal("second Peek mutated the counter")
	}
	c.Next()
	if c.Peek() != 8 {
		t.Fatal("Peek did not reflect the advance")
	}
}

// TestCounterConcurrentAdvance race-tests the atomic increment.
func TestCounterConcurrentAdvance(t *testing.T) {
	c := NewCounterFromSeed(0)
	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N)
	seen := make([]uint32, N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			seen[i] = c.Next()
		}(i)
	}
	wg.Wait()
	uniq := map[uint32]struct{}{}
	for _, v := range seen {
		uniq[v] = struct{}{}
	}
	if len(uniq) != N {
		t.Fatalf("got %d unique values, want %d", len(uniq), N)
	}
}

// --- Window ---

// TestWindowAcceptsFreshAndRejectsDuplicate covers the basic
// bitmap-tracking surface.
func TestWindowAcceptsFreshAndRejectsDuplicate(t *testing.T) {
	w := NewWindow()
	if !w.Accept(100) {
		t.Fatal("first counter rejected")
	}
	if w.Accept(100) {
		t.Fatal("duplicate accepted")
	}
}

// TestWindowAcceptsAdvancing emulates an in-order counter stream.
func TestWindowAcceptsAdvancing(t *testing.T) {
	w := NewWindow()
	for i := uint32(0); i < 10; i++ {
		if !w.Accept(i) {
			t.Fatalf("counter %d rejected", i)
		}
	}
}

// TestWindowAcceptsBackInWindow allows older-but-recent counters
// (out-of-order delivery within the 32-slot window).
func TestWindowAcceptsBackInWindow(t *testing.T) {
	w := NewWindow()
	w.Accept(10)
	if !w.Accept(5) {
		t.Fatal("in-window 5 rejected after seeing 10")
	}
	if w.Accept(5) {
		t.Fatal("duplicate 5 accepted")
	}
}

// TestWindowRejectsStale rejects counters older than 32 slots back.
func TestWindowRejectsStale(t *testing.T) {
	w := NewWindow()
	w.Accept(100)
	if w.Accept(50) {
		t.Fatal("stale 50 accepted (>= windowSize behind 100)")
	}
}

// TestWindowResetsOnBigJump simulates a session re-key where the
// counter trajectory restarts at a much larger value.
func TestWindowResetsOnBigJump(t *testing.T) {
	w := NewWindow()
	w.Accept(10)
	if !w.Accept(1000) {
		t.Fatal("big jump rejected")
	}
	// The pre-jump counters fall outside the new window.
	if w.Accept(15) {
		t.Fatal("pre-jump counter accepted post-reset")
	}
}

// --- Retransmitter ---

func detRand() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

// TestTrackThenAckRemovesEntry — the success path.
func TestTrackThenAckRemovesEntry(t *testing.T) {
	r := NewRetransmitter(func([]byte) error { return nil }, detRand())
	r.Track(42, 1, []byte("hi"), time.Now())
	if r.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", r.Pending())
	}
	if !r.Ack(42) {
		t.Fatal("Ack(42) returned false on a tracked entry")
	}
	if r.Pending() != 0 {
		t.Fatalf("pending after ack=%d, want 0", r.Pending())
	}
}

// TestAckOfUnknownReturnsFalse — duplicate ACKs are harmless.
func TestAckOfUnknownReturnsFalse(t *testing.T) {
	r := NewRetransmitter(func([]byte) error { return nil }, detRand())
	if r.Ack(42) {
		t.Fatal("Ack on empty tracker returned true")
	}
}

// TestTickRetransmitsAfterBackoff confirms that after the configured
// backoff window the entry is re-sent.
func TestTickRetransmitsAfterBackoff(t *testing.T) {
	var sent [][]byte
	send := func(b []byte) error {
		cp := make([]byte, len(b))
		copy(cp, b)
		sent = append(sent, cp)
		return nil
	}
	r := NewRetransmitter(send, detRand())

	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	r.Track(7, 1, []byte("payload"), t0)

	// Tick before the backoff elapses — nothing happens.
	if got := r.Tick(t0.Add(10 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("early tick: got %d results, want 0", len(got))
	}

	// Tick after the (deterministic) backoff. Even with jitter the
	// upper bound is base × margin × (1 + 0.25) = 412.5 ms.
	results := r.Tick(t0.Add(500 * time.Millisecond))
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("retransmit err: %v", results[0].Err)
	}
	if len(sent) != 1 {
		t.Fatalf("send called %d times, want 1", len(sent))
	}
}

// TestTickAbandonsAfterMaxRetransmissions surfaces
// ErrMaxRetransmissionsReached and removes the entry.
func TestTickAbandonsAfterMaxRetransmissions(t *testing.T) {
	send := func([]byte) error { return nil }
	r := NewRetransmitter(send, detRand())
	t0 := time.Now()
	r.Track(7, 1, []byte("payload"), t0)
	// Drive the clock far into the future across MaxRetransmissions
	// retries plus one. Each iteration advances by 10 s — well past
	// any backoff window.
	var lastErr error
	for i := 0; i <= MaxRetransmissions+1; i++ {
		results := r.Tick(t0.Add(time.Duration(i+1) * 10 * time.Second))
		for _, res := range results {
			if res.Err != nil {
				lastErr = res.Err
			}
		}
	}
	if !errors.Is(lastErr, ErrMaxRetransmissionsReached) {
		t.Fatalf("lastErr = %v, want ErrMaxRetransmissionsReached", lastErr)
	}
	if r.Pending() != 0 {
		t.Fatalf("entry not removed after abandon: pending=%d", r.Pending())
	}
}

// TestTickSendErrorPropagates surfaces an underlying SendFunc error.
func TestTickSendErrorPropagates(t *testing.T) {
	wantErr := errors.New("network unreachable")
	send := func([]byte) error { return wantErr }
	r := NewRetransmitter(send, detRand())
	t0 := time.Now()
	r.Track(7, 1, []byte("payload"), t0)
	results := r.Tick(t0.Add(time.Second))
	if len(results) != 1 || !errors.Is(results[0].Err, wantErr) {
		t.Fatalf("got %v, want %v", results, wantErr)
	}
}
