// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"math"
	"slices"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// keyedLimiterEntry pairs a per-key token bucket with the time it was
// last used, so [keyedLimiterStore.get] can evict idle keys.
type keyedLimiterEntry struct {
	lim     *rate.Limiter
	lastUse time.Time
}

// keyedLimiterStore is a hard-bounded, per-key [rate.Limiter] cache
// with idle-eviction. Backs both [RateLimit] (keyed by resolved
// identity) and [LoginRateLimiter] (keyed by client IP) — the two call
// sites differ only in their key space, refill rate/burst, and
// capacity/idle-window, so those are the constructor parameters.
//
// The table never exceeds cap entries: idle buckets go first, and a
// table of nothing but fresh keys sheds its oldest entries instead of
// growing (see [keyedLimiterStore.reclaimLocked]).
type keyedLimiterStore struct {
	limit rate.Limit
	burst int
	cap   int
	idle  time.Duration

	mu      sync.Mutex
	buckets map[string]*keyedLimiterEntry
}

// newKeyedLimiterStore builds a store whose limiters refill at limit
// with the given burst. capacity is the hard ceiling on the number of
// live buckets; idle is how long an unused bucket survives before the
// reclaim pass prefers it as a victim.
func newKeyedLimiterStore(limit rate.Limit, burst, capacity int, idle time.Duration) *keyedLimiterStore {
	return &keyedLimiterStore{
		limit:   limit,
		burst:   burst,
		cap:     capacity,
		idle:    idle,
		buckets: make(map[string]*keyedLimiterEntry),
	}
}

// get returns (or creates) the limiter for key, keeping the table at or
// below s.cap entries at all times.
func (s *keyedLimiterStore) get(key string) *rate.Limiter {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.buckets[key]; ok {
		e.lastUse = now
		return e.lim
	}
	if len(s.buckets) >= s.cap {
		s.reclaimLocked(now)
	}
	lim := rate.NewLimiter(s.limit, s.burst)
	s.buckets[key] = &keyedLimiterEntry{lim: lim, lastUse: now}
	return lim
}

// reclaimLocked frees room for at least one new bucket. It first drops
// every bucket idle longer than s.idle; if that leaves the table still
// at capacity — every key is fresh, which is exactly what an address
// rotation produces — it evicts the oldest entries by last use.
//
// The idle sweep alone bounded nothing: the real ceiling was (rate of
// distinct keys) x (idle window), both attacker-controlled for the
// IP-keyed login limiter. Evicting an entry only costs its holder a
// fresh burst, which is the same outcome unbounded growth already had,
// while memory is now pinned at s.cap entries.
//
// Eviction is batched so the O(n) pass amortises to a small constant per
// insert instead of running on every request once the table sits at cap.
func (s *keyedLimiterStore) reclaimLocked(now time.Time) {
	for k, e := range s.buckets {
		if now.Sub(e.lastUse) > s.idle {
			delete(s.buckets, k)
		}
	}
	if len(s.buckets) < s.cap {
		return
	}
	batch := max(s.cap/8, 1)
	type aged struct {
		key     string
		lastUse time.Time
	}
	entries := make([]aged, 0, len(s.buckets))
	for k, e := range s.buckets {
		entries = append(entries, aged{key: k, lastUse: e.lastUse})
	}
	slices.SortFunc(entries, func(a, b aged) int { return a.lastUse.Compare(b.lastUse) })
	drop := min(batch, len(entries))
	for _, e := range entries[:drop] {
		delete(s.buckets, e.key)
	}
}

// retryAfterSeconds computes the integer seconds a caller must wait
// before lim regains a token. Always ≥ 1 so a Retry-After header
// built from it is meaningful.
func retryAfterSeconds(lim *rate.Limiter) int {
	reserve := lim.Reserve()
	d := reserve.Delay()
	reserve.Cancel()
	if d <= 0 {
		return 1
	}
	return int(math.Ceil(d.Seconds()))
}
