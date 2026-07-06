// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDeciderLenEmptyOnNew verifies the memoisation cache starts at zero.
func TestDeciderLenEmptyOnNew(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	if got := d.Len(); got != 0 {
		t.Fatalf("Len()=%d on fresh decider, want 0", got)
	}
}

// TestDeciderLenGrowsAfterUse verifies that Len() reports the number of
// distinct (model, channelType, channelNo, paramset, parameter) tuples that
// have been resolved and memoised.
func TestDeciderLenGrowsAfterUse(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)

	// Two distinct queries → two cache entries.
	_ = d.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	_ = d.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterState)

	if got := d.Len(); got != 2 {
		t.Fatalf("Len()=%d after 2 distinct queries, want 2", got)
	}

	// Repeat the first query — must NOT grow the cache.
	_ = d.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	if got := d.Len(); got != 2 {
		t.Fatalf("Len()=%d after duplicate query, want still 2", got)
	}
}

// TestDeciderLenResetsAfterLoadUnIgnore verifies that LoadUnIgnore invalidates
// the memoisation cache so Len() returns 0 immediately after the call.
func TestDeciderLenResetsAfterLoadUnIgnore(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)

	// Warm the cache.
	_ = d.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	if d.Len() == 0 {
		t.Fatal("cache should be non-empty before reset")
	}

	// LoadUnIgnore must invalidate the cache.
	d.LoadUnIgnore(nil)
	if got := d.Len(); got != 0 {
		t.Fatalf("Len()=%d after LoadUnIgnore, want 0", got)
	}
}

// TestDeciderCacheBounded verifies that the memoisation cache never grows
// past maxCacheEntries: once the bound is hit, the next distinct query
// clears the cache before memoising its own result, rather than growing
// forever.
func TestDeciderCacheBounded(t *testing.T) {
	// Not t.Parallel(): mutates the package-level maxCacheEntries var.
	orig := maxCacheEntries
	maxCacheEntries = 4
	defer func() { maxCacheEntries = orig }()

	d := NewParameterDecider(nil)

	// Fill exactly up to the bound with distinct entries.
	for i := range maxCacheEntries {
		p := hmenum.Parameter("BOUND_P" + string(rune('A'+i)))
		_ = d.IsParameterIgnored("HmIP-STH", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p)
	}
	if got := d.Len(); got != maxCacheEntries {
		t.Fatalf("Len()=%d after filling to bound, want %d", got, maxCacheEntries)
	}

	// One more distinct entry must not push the cache past the bound.
	_ = d.IsParameterIgnored("HmIP-STH", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.Parameter("BOUND_OVERFLOW"))
	if got := d.Len(); got > maxCacheEntries {
		t.Fatalf("Len()=%d after overflow entry, want <= %d", got, maxCacheEntries)
	}

	// Repeating the process for many more distinct entries must never
	// exceed the bound.
	for i := range 50 {
		p := hmenum.Parameter("BOUND_MANY" + string(rune('A'+(i%26))) + string(rune('0'+(i/26))))
		_ = d.IsParameterIgnored("HmIP-STH", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p)
		if got := d.Len(); got > maxCacheEntries {
			t.Fatalf("Len()=%d exceeded bound %d during sustained churn", got, maxCacheEntries)
		}
	}
}

// TestDeciderLenRaceSafe exercises Len() and IsParameterIgnored concurrently
// to prove there is no data race under the -race detector.
func TestDeciderLenRaceSafe(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	const goroutines = 8
	const ops = 100
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			for j := range ops {
				// Vary the parameter so different goroutines race to write
				// different cache entries.
				p := hmenum.Parameter("P" + string(rune('A'+((i+j)%26))))
				_ = d.IsParameterIgnored("HmIP-STH", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p)
			}
		}()
	}

	// Concurrent Len() reads.
	wg.Go(func() {
		for range goroutines * ops {
			_ = d.Len()
		}
	})

	wg.Wait()

	// After all goroutines finish the cache must be non-empty and consistent.
	if d.Len() == 0 {
		t.Fatal("cache must be non-empty after concurrent writes")
	}
}
