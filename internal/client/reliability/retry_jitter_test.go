// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// These tests pin the lower bound of applyJitter so a future refactor cannot
// accidentally produce a negative sleep duration (retry jitter ±20 % on
// small backoffs < 100 ms must never go negative). All tests operate directly
// on the unexported applyJitter / nextDelay helpers (package-internal test
// file). The monotonicity test drives Retrier.Do via an attemptRecorder that
// captures each NewTimer duration without blocking.

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"testing/synctest"
	"time"
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
		idx := max(int((got-lo)/width), 0)
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
// attempts. Uses attemptRecorder (see below) inside a synctest bubble to
// capture the waits without sleeping real time.
func TestRetryBackoffMonotonicWithoutJitter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &attemptRecorder{}
		r := NewRetrier(RetryConfig{
			MaxAttempts: 5,
			Initial:     10 * time.Millisecond,
			Max:         200 * time.Millisecond,
			Multiplier:  2,
			Jitter:      -1, // explicit "no jitter" for deterministic timing
		})

		err := r.Do(context.Background(), rec.fail(errors.New("transient")))
		if err == nil {
			t.Fatal("expected exhaustion error, got nil")
		}

		delays := rec.Delays()
		if len(delays) == 0 {
			t.Fatal("no delays captured — the retrier made no second attempt")
		}
		// With Jitter=0 and Multiplier=2 the sequence is 10ms, 20ms, 40ms, 80ms.
		for i := 1; i < len(delays); i++ {
			if delays[i] < delays[i-1] {
				t.Errorf("delay[%d]=%v < delay[%d]=%v — backoff regressed without jitter",
					i, delays[i], i-1, delays[i-1])
			}
		}
	})
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

// ── attemptRecorder ───────────────────────────────────────────────────────────
//
// attemptRecorder records the bubble time of every attempt a Retrier makes,
// so a test can derive the backoff waits the Retrier actually slept. It must
// be used inside a synctest bubble, where those waits cost no real time.

type attemptRecorder struct {
	mu sync.Mutex
	at []time.Time
}

// fail returns a retry operation that stamps the attempt time and fails
// with err.
func (r *attemptRecorder) fail(err error) func(context.Context, int) error {
	return func(context.Context, int) error {
		r.mu.Lock()
		r.at = append(r.at, time.Now())
		r.mu.Unlock()
		return err
	}
}

// Delays returns the gaps between successive recorded attempts, in order.
func (r *attemptRecorder) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []time.Duration
	for i := 1; i < len(r.at); i++ {
		out = append(out, r.at[i].Sub(r.at[i-1]))
	}
	return out
}
