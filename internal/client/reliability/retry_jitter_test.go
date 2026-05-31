// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// These tests pin the lower bound of applyJitter so a future refactor cannot
// accidentally produce a negative sleep duration (retry jitter ±20 % on
// small backoffs < 100 ms must never go negative). All tests operate directly
// on the unexported applyJitter / nextDelay helpers (package-internal test
// file). The monotonicity test drives Retrier.Do via a recordingClock that
// captures each NewTimer duration without blocking.

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// deterministicRng returns a seeded *rand.Rand for reproducible iteration.
func deterministicRng(seed uint64) *rand.Rand {
	src := rand.NewPCG(seed, 0xcafebabe)
	return rand.New(src) //nolint:gosec // tests only, not security-sensitive
}

// TestRetryJitterLowerBoundNeverNegative samples applyJitter 1 000 times for
// each of the canonical "small backoff" values and asserts no negative result.
//
// The invariant: applyJitter(d, frac, rng) >= 0 for all d >= 0.
//
// Production clamp (return d when out < 0) makes this trivially true for
// non-negative d, but we pin it explicitly so any future edit to the clamp
// is caught immediately.
func TestRetryJitterLowerBoundNeverNegative(t *testing.T) {
	t.Parallel()

	backoffs := []time.Duration{
		1 * time.Millisecond,
		10 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		1 * time.Second,
	}
	fracs := []struct {
		label string
		val   float64
	}{
		{"0p1", 0.1},
		{"0p2", 0.2},
		{"0p5", 0.5},
	}

	const iterations = 1000

	for _, d := range backoffs {
		for _, fr := range fracs {
			d, fr := d, fr
			t.Run(d.String()+"_frac_"+fr.label, func(t *testing.T) {
				t.Parallel()
				rng := deterministicRng(uint64(d) ^ uint64(fr.val*1e9)) //nolint:gosec // G115: duration values in test are non-negative and bounded to test ranges
				for i := range iterations {
					got := applyJitter(d, fr.val, rng)
					if got < 0 {
						t.Fatalf("iteration %d: applyJitter(%v, %v) = %v — negative duration", i, d, fr.val, got)
					}
				}
			})
		}
	}
}

// TestRetryJitterUpperBoundDoesNotExceedConfigured asserts that jitter with
// frac=0.2 never produces a delay above 1.2 × backoff across 1 000 samples.
// Mirrors the specification "±20 %" upper bound.
func TestRetryJitterUpperBoundDoesNotExceedConfigured(t *testing.T) {
	t.Parallel()

	backoffs := []time.Duration{
		1 * time.Millisecond,
		10 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		1 * time.Second,
	}
	const frac = 0.2
	const iterations = 1000

	for _, d := range backoffs {
		d := d
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			rng := deterministicRng(uint64(d) + 0x1234) //nolint:gosec // G115: duration values in test are non-negative and bounded to test ranges
			// Add a tiny epsilon for float64 rounding: the maximum raw
			// offset is frac * d and Float64 returns [0.0, 1.0), so the
			// supremum of offset is frac*d (exclusive). We still allow
			// an epsilon of 1 ns to guard against truncation artefacts
			// in the time.Duration conversion.
			maxAllowed := time.Duration(float64(d)*(1+frac)) + 1
			for i := range iterations {
				got := applyJitter(d, frac, rng)
				if got > maxAllowed {
					t.Fatalf("iteration %d: applyJitter(%v, 0.2) = %v — exceeds upper bound %v", i, d, got, maxAllowed)
				}
			}
		})
	}
}

// TestRetryJitterDistributionIsRoughlyUniform draws 10 000 samples for
// backoff = 100 ms with frac = 0.2, buckets them into 10 equal-width bins
// across [80 ms, 120 ms), and asserts that each bin received at least 50
// samples. This rules out a degenerate implementation that always returns
// the lower or upper bound.
func TestRetryJitterDistributionIsRoughlyUniform(t *testing.T) {
	t.Parallel()

	const (
		d          = 100 * time.Millisecond
		frac       = 0.2
		iterations = 10_000
		numBuckets = 10
		minPerBin  = 50
	)

	lo := time.Duration(float64(d) * (1 - frac)) // 80 ms
	hi := time.Duration(float64(d) * (1 + frac)) // 120 ms
	width := (hi - lo) / numBuckets

	buckets := make([]int, numBuckets)
	rng := deterministicRng(0xdeadbeef)

	for range iterations {
		got := applyJitter(d, frac, rng)
		idx := int((got - lo) / width)
		if idx < 0 {
			idx = 0
		}
		if idx >= numBuckets {
			idx = numBuckets - 1
		}
		buckets[idx]++
	}

	for i, count := range buckets {
		if count < minPerBin {
			binLo := lo + time.Duration(i)*width
			binHi := lo + time.Duration(i+1)*width
			t.Errorf("bucket %d [%v, %v): %d samples — want >=%d (distribution not uniform enough)",
				i, binLo, binHi, count, minPerBin)
		}
	}
}

// TestRetryBackoffMonotonicWithoutJitter verifies that when Jitter = 0 the
// delays passed to the clock do not decrease across successive retry
// attempts. Uses recordingClock (see below) to capture delays without
// sleeping real time.
func TestRetryBackoffMonotonicWithoutJitter(t *testing.T) {
	t.Parallel()

	rec := newRecordingClock()
	r := NewRetrier(RetryConfig{
		MaxAttempts: 5,
		Initial:     10 * time.Millisecond,
		Max:         200 * time.Millisecond,
		Multiplier:  2,
		Jitter:      -1, // explicit "no jitter" for deterministic timing
		Clock:       rec,
	})

	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("transient")
	})
	if err == nil {
		t.Fatal("expected exhaustion error, got nil")
	}

	delays := rec.Delays()
	if len(delays) == 0 {
		t.Fatal("no delays captured — recordingClock not exercised")
	}
	// With Jitter=0 and Multiplier=2 the sequence is 10ms, 20ms, 40ms, 80ms.
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("delay[%d]=%v < delay[%d]=%v — backoff regressed without jitter",
				i, delays[i], i-1, delays[i-1])
		}
	}
}

// TestRetryBackoffAtZeroBackoffNoJitterApplied tests the edge case where
// d = 0. NewRetrier normalises non-positive Initial, so we call applyJitter
// directly.
func TestRetryBackoffAtZeroBackoffNoJitterApplied(t *testing.T) {
	t.Parallel()

	rng := deterministicRng(42)

	// d = 0 → delta = 0 → offset = 0 → out = 0 — must return 0.
	if got := applyJitter(0, 0.2, rng); got != 0 {
		t.Errorf("applyJitter(0, 0.2) = %v — want 0", got)
	}

	// d = 0, frac = 0 — shortest path.
	if got := applyJitter(0, 0, rng); got != 0 {
		t.Errorf("applyJitter(0, 0) = %v — want 0", got)
	}

	// d = 1 ns (absolute minimum) must be non-negative.
	if got := applyJitter(1, 0.2, rng); got < 0 {
		t.Errorf("applyJitter(1ns, 0.2) = %v — negative duration", got)
	}
}

// TestRetrySmallBackoff10msMillisecondPrecisionPreserved checks that for the
// canonical "small backoff" of 10 ms with ±20 % jitter every produced value
// falls in the closed interval [8 ms, 12 ms] across 10 000 samples.
func TestRetrySmallBackoff10msMillisecondPrecisionPreserved(t *testing.T) {
	t.Parallel()

	const (
		d          = 10 * time.Millisecond
		frac       = 0.2
		iterations = 10_000
		lo         = 8 * time.Millisecond
		hi         = 12 * time.Millisecond
	)

	rng := deterministicRng(0x5eed)
	for i := range iterations {
		got := applyJitter(d, frac, rng)
		if got < lo || got > hi {
			t.Fatalf("iteration %d: applyJitter(10ms, 0.2) = %v — outside [%v, %v]", i, got, lo, hi)
		}
	}
}

// TestRetryJitterFallbackClampsToBaseNotZero verifies the production safety
// net: when arithmetic would produce a negative out, applyJitter returns the
// base duration d rather than 0.
//
// White-box: we deliberately pass frac > 1 (outside API contract) to force
// the negative path. frac=1.5 means delta = 1.5*d, so the minimum of
// (d + offset) is d - 1.5*d = -0.5*d which is negative.
func TestRetryJitterFallbackClampsToBaseNotZero(t *testing.T) {
	t.Parallel()

	const (
		d       = 10 * time.Millisecond
		bigFrac = 1.5
	)

	rng := deterministicRng(0xbad)
	clampFired := false
	for range 10_000 {
		got := applyJitter(d, bigFrac, rng)
		if got < 0 {
			t.Fatalf("applyJitter returned negative %v — clamp not working", got)
		}
		if got == 0 {
			t.Fatalf("applyJitter returned 0 — clamp should return d=%v, not 0", d)
		}
		if got == d {
			clampFired = true
		}
	}
	if !clampFired {
		t.Log("note: frac=1.5 did not trigger the negative clamp in this run (RNG luck)")
	}
}

// ── recordingClock ────────────────────────────────────────────────────────────
//
// recordingClock implements clock.Clock. Every NewTimer(d) call appends d
// to an internal slice, then immediately fires the timer so the retrier never
// blocks real time. Now() advances by d on each NewTimer call so the retrier
// sees a plausible wall-clock progression.

type recordingClock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newRecordingClock() *recordingClock {
	return &recordingClock{
		now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// Delays returns a snapshot of the captured timer durations in order.
func (c *recordingClock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.delays))
	copy(out, c.delays)
	return out
}

// Now implements clock.Clock.
func (c *recordingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// NewTimer implements clock.Clock. The returned timer fires immediately
// (buffered channel) so Retrier.Do never parks on a real timer.
func (c *recordingClock) NewTimer(d time.Duration) clock.Timer {
	c.mu.Lock()
	c.delays = append(c.delays, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return &instantTimer{d: d}
}

// Sleep implements clock.Clock.
func (c *recordingClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// After implements clock.Clock.
func (c *recordingClock) After(d time.Duration) <-chan time.Time {
	return c.NewTimer(d).C()
}

// instantTimer is a clock.Timer that fires immediately.
type instantTimer struct {
	d    time.Duration
	once sync.Once
	ch   chan time.Time
}

func (t *instantTimer) C() <-chan time.Time {
	t.once.Do(func() {
		t.ch = make(chan time.Time, 1)
		t.ch <- time.Now()
	})
	return t.ch
}

func (t *instantTimer) Stop() bool { return false }
