// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

// Fake-clock composite backoff timeline tests for [Retrier].
//
// Goal: drive a Retrier through N failing attempts with a fake clock and
// assert the cumulative backoff sequence matches the exponential envelope
// within the documented ±20 % jitter band.
//
// Uses recordingClock (defined in retry_jitter_test.go) so each NewTimer
// fires immediately — the entire retry schedule runs without sleeping real
// time. The raw delay values captured by recordingClock are then checked
// against the deterministic expected schedule (no-jitter variant) and the
// jitter-band variant.

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

// TestRetryBackoffTimelineMatchesExponentialEnvelopeNoJitter drives a 5-attempt
// Retrier (Jitter=-1, so no jitter) and checks that each captured delay equals
// the deterministic exponential schedule exactly:
//
//	attempt 1→2 : Initial          (e.g. 10 ms)
//	attempt 2→3 : Initial×Mult     (e.g. 20 ms)
//	attempt 3→4 : Initial×Mult²    (e.g. 40 ms)
//	attempt 4→5 : Initial×Mult³    (e.g. 80 ms)
func TestRetryBackoffTimelineMatchesExponentialEnvelopeNoJitter(t *testing.T) {
	t.Parallel()

	const (
		initial    = 10 * time.Millisecond
		mult       = 2.0
		maxBackoff = time.Second
		attempts   = 5
	)

	rec := newRecordingClock()
	r := NewRetrier(RetryConfig{
		MaxAttempts: attempts,
		Initial:     initial,
		Max:         maxBackoff,
		Multiplier:  mult,
		Jitter:      -1, // explicit "no jitter" for deterministic comparison
		Clock:       rec,
	})

	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("transient")
	})

	delays := rec.Delays()
	// We expect exactly attempts-1 inter-attempt delays.
	if len(delays) != attempts-1 {
		t.Fatalf("expected %d delays, got %d: %v", attempts-1, len(delays), delays)
	}

	// Build expected sequence: 10, 20, 40, 80 ms (capped at 1s).
	expected := make([]time.Duration, attempts-1)
	d := initial
	for i := range expected {
		expected[i] = d
		d = nextDelay(d, mult, maxBackoff)
	}

	for i, got := range delays {
		if got != expected[i] {
			t.Errorf("delay[%d]: got %v, want %v", i, got, expected[i])
		}
	}
}

// TestRetryBackoffTimelineWithJitterFallsWithinBand drives a 5-attempt Retrier
// with the default ±20 % jitter and asserts every captured delay lies within
// [0.8×schedule, 1.2×schedule+1ns]. The deterministic RNG makes the test
// reproducible across runs.
func TestRetryBackoffTimelineWithJitterFallsWithinBand(t *testing.T) {
	t.Parallel()

	const (
		initial    = 10 * time.Millisecond
		mult       = 2.0
		maxBackoff = time.Second
		jitter     = 0.2
		attempts   = 5
	)

	rec := newRecordingClock()
	src := rand.NewPCG(0xdeadbeef, 0xcafebabe)
	rng := rand.New(src) //nolint:gosec // test only

	r := NewRetrier(RetryConfig{
		MaxAttempts: attempts,
		Initial:     initial,
		Max:         maxBackoff,
		Multiplier:  mult,
		Jitter:      jitter,
		Rand:        rng,
		Clock:       rec,
	})

	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("transient")
	})

	delays := rec.Delays()
	if len(delays) != attempts-1 {
		t.Fatalf("expected %d delays, got %d: %v", attempts-1, len(delays), delays)
	}

	// Build the deterministic base schedule and check each delay against the
	// ±20 % band.
	d := initial
	for i, got := range delays {
		lo := time.Duration(float64(d) * (1 - jitter))
		// Add 1 ns epsilon for float64→Duration truncation artefacts (matches
		// the upper-bound check in retry_jitter_test.go).
		hi := time.Duration(float64(d)*(1+jitter)) + 1

		if got < lo || got > hi {
			t.Errorf("delay[%d] (base=%v): got %v, want [%v, %v]", i, d, got, lo, hi)
		}
		d = nextDelay(d, mult, maxBackoff)
	}
}

// TestRetryBackoffTimelineCapAtMax verifies that once the exponential schedule
// would exceed Max, all subsequent delays are capped at Max (no-jitter variant
// for a clean comparison).
func TestRetryBackoffTimelineCapAtMax(t *testing.T) {
	t.Parallel()

	const (
		initial    = 100 * time.Millisecond
		mult       = 4.0
		maxBackoff = 200 * time.Millisecond
		attempts   = 5 // schedule: 100, 200 (cap), 200 (cap), 200 (cap)
	)

	rec := newRecordingClock()
	r := NewRetrier(RetryConfig{
		MaxAttempts: attempts,
		Initial:     initial,
		Max:         maxBackoff,
		Multiplier:  mult,
		Jitter:      -1,
		Clock:       rec,
	})

	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("transient")
	})

	delays := rec.Delays()
	if len(delays) != attempts-1 {
		t.Fatalf("expected %d delays, got %d: %v", attempts-1, len(delays), delays)
	}

	// First delay is initial (100ms); subsequent delays must be capped at Max.
	if delays[0] != initial {
		t.Errorf("delay[0]: got %v, want %v", delays[0], initial)
	}
	for i := 1; i < len(delays); i++ {
		if delays[i] != maxBackoff {
			t.Errorf("delay[%d]: got %v, want capped at %v", i, delays[i], maxBackoff)
		}
	}
}

// TestRetryBackoffTimelineMonotonicGrowthUpToCap verifies that delays grow
// monotonically up to the cap and never decrease before the cap is reached.
// No jitter so the growth curve is exact.
func TestRetryBackoffTimelineMonotonicGrowthUpToCap(t *testing.T) {
	t.Parallel()

	const (
		initial    = 5 * time.Millisecond
		mult       = 3.0
		maxBackoff = 500 * time.Millisecond
		attempts   = 7 // schedule: 5, 15, 45, 135, 405, 500 ms
	)

	rec := newRecordingClock()
	r := NewRetrier(RetryConfig{
		MaxAttempts: attempts,
		Initial:     initial,
		Max:         maxBackoff,
		Multiplier:  mult,
		Jitter:      -1,
		Clock:       rec,
	})

	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return errors.New("transient")
	})

	delays := rec.Delays()
	if len(delays) != attempts-1 {
		t.Fatalf("expected %d delays, got %d", attempts-1, len(delays))
	}

	// Check that each delay is >= the previous one (monotonic growth up to cap).
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("backoff regressed: delay[%d]=%v < delay[%d]=%v",
				i, delays[i], i-1, delays[i-1])
		}
	}

	// Every delay must be capped at maxBackoff.
	for i, d := range delays {
		if d > maxBackoff {
			t.Errorf("delay[%d]=%v exceeds Max=%v", i, d, maxBackoff)
		}
	}
}
