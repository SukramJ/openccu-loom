// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestKeyedLimiterStoreBoundsFreshKeys pins the table's hard ceiling. A
// stream of distinct keys arriving inside the idle window used to grow
// the map without limit — the idle sweep found nothing to delete and the
// new bucket was inserted anyway — so the login limiter's per-IP table
// was an unauthenticated memory sink for anyone rotating source
// addresses.
func TestKeyedLimiterStoreBoundsFreshKeys(t *testing.T) {
	t.Parallel()
	const capacity = 64
	s := newKeyedLimiterStore(rate.Limit(1), 5, capacity, 10*time.Minute)

	for i := range capacity * 10 {
		s.get("key-" + strconv.Itoa(i))
		if n := len(s.buckets); n > capacity {
			t.Fatalf("after %d fresh keys: %d buckets, want <= %d", i+1, n, capacity)
		}
	}
	if len(s.buckets) == 0 {
		t.Fatal("store evicted everything; a fresh key must still get a bucket")
	}
}

// TestKeyedLimiterStoreKeepsRecentKeys pins that reclaiming sheds the
// oldest entries: a key touched on every insert must survive a flood of
// fresh ones, so an active caller keeps its accumulated bucket.
func TestKeyedLimiterStoreKeepsRecentKeys(t *testing.T) {
	t.Parallel()
	const capacity = 32
	s := newKeyedLimiterStore(rate.Limit(1), 5, capacity, 10*time.Minute)

	hot := s.get("hot")
	for i := range capacity * 4 {
		s.get("cold-" + strconv.Itoa(i))
		s.get("hot")
	}
	if got := s.get("hot"); got != hot {
		t.Fatal("the continuously used key lost its limiter to the reclaim pass")
	}
}

// TestKeyedLimiterStoreEvictsIdleFirst pins the cheap half of the
// reclaim: buckets past the idle window go before any live one is
// touched.
func TestKeyedLimiterStoreEvictsIdleFirst(t *testing.T) {
	t.Parallel()
	const capacity = 8
	s := newKeyedLimiterStore(rate.Limit(1), 5, capacity, time.Millisecond)

	for i := range capacity {
		s.get("old-" + strconv.Itoa(i))
	}
	// Age every bucket past the idle window without waiting on the clock.
	stale := time.Now().Add(-time.Hour)
	for _, e := range s.buckets {
		e.lastUse = stale
	}
	s.get("fresh")

	if n := len(s.buckets); n != 1 {
		t.Fatalf("buckets = %d, want 1 (every idle bucket reclaimed)", n)
	}
	if _, ok := s.buckets["fresh"]; !ok {
		t.Fatal("the new key is missing from the table")
	}
}
