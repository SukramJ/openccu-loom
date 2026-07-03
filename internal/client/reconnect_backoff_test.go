// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"math/rand/v2"
	"testing"
	"time"
)

// TestReconnectDelayIsJittered verifies the reconnect backoff carries bounded
// random jitter so sibling interfaces of one central do not reconnect in
// lockstep at a just-booted CCU (thundering herd). Without jitter every
// interface computes the identical deterministic delay for a given attempt.
func TestReconnectDelayIsJittered(t *testing.T) {
	t.Parallel()

	cfg := ReconnectConfig{
		InitialDelay:  10 * time.Second,
		MaxDelay:      120 * time.Second,
		BackoffFactor: 2,
		Rand:          rand.New(rand.NewPCG(1, 2)),
	}
	const attempt0Base = 10 * time.Second
	lo := time.Duration(float64(attempt0Base) * (1 - reconnectJitterFraction))
	hi := time.Duration(float64(attempt0Base) * (1 + reconnectJitterFraction))

	const n = 2000
	seen := make(map[time.Duration]struct{}, n)
	var sum time.Duration
	for range n {
		d := reconnectDelay(cfg, 0)
		if d < lo || d > hi {
			t.Fatalf("delay %v outside jitter band [%v, %v]", d, lo, hi)
		}
		seen[d] = struct{}{}
		sum += d
	}
	if len(seen) < 100 {
		t.Errorf("delays not jittered: only %d distinct values across %d draws", len(seen), n)
	}
	mean := sum / n
	if mean < time.Duration(float64(attempt0Base)*0.97) || mean > time.Duration(float64(attempt0Base)*1.03) {
		t.Errorf("mean delay %v not centred on base %v", mean, attempt0Base)
	}
}

// TestReconnectDelayBackoffGrowsAndCaps verifies the exponential growth and the
// MaxDelay cap survive the jitter: the jittered delay stays within ±jitter of
// the capped exponential base for each attempt.
func TestReconnectDelayBackoffGrowsAndCaps(t *testing.T) {
	t.Parallel()

	cfg := ReconnectConfig{
		InitialDelay:  2 * time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2,
		Rand:          rand.New(rand.NewPCG(42, 7)),
	}
	// base(attempts) = min(2s * 2^attempts, 30s):
	//   0 -> 2s, 1 -> 4s, 2 -> 8s, 3 -> 16s, 4 -> 30s (capped), 10 -> 30s.
	bases := map[int]time.Duration{0: 2 * time.Second, 1: 4 * time.Second, 2: 8 * time.Second, 3: 16 * time.Second, 4: 30 * time.Second, 10: 30 * time.Second}
	for attempts, base := range bases {
		lo := time.Duration(float64(base) * (1 - reconnectJitterFraction))
		hi := time.Duration(float64(base) * (1 + reconnectJitterFraction))
		for range 200 {
			d := reconnectDelay(cfg, attempts)
			if d < lo || d > hi {
				t.Fatalf("attempts=%d: delay %v outside [%v, %v] (base %v)", attempts, d, lo, hi, base)
			}
		}
	}
}

// TestApplyReconnectJitterNilRandIsSafe verifies the nil-rng path (production
// default) still bounds the jitter using the concurrency-safe top-level source.
func TestApplyReconnectJitterNilRandIsSafe(t *testing.T) {
	t.Parallel()

	const base = 5 * time.Second
	lo := time.Duration(float64(base) * (1 - reconnectJitterFraction))
	hi := time.Duration(float64(base) * (1 + reconnectJitterFraction))
	for range 1000 {
		d := applyReconnectJitter(base, nil)
		if d < lo || d > hi {
			t.Fatalf("nil-rng delay %v outside [%v, %v]", d, lo, hi)
		}
	}
	if got := applyReconnectJitter(0, nil); got != 0 {
		t.Errorf("applyReconnectJitter(0) = %v, want 0", got)
	}
}
