// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"math"
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

// keyedLimiterStore is a bounded, per-key [rate.Limiter] cache with
// idle-eviction. Backs both [RateLimit] (keyed by resolved identity)
// and [LoginRateLimiter] (keyed by client IP) — the two call sites
// differ only in their key space, refill rate/burst, and GC
// threshold/idle-window, so those are the constructor parameters.
type keyedLimiterStore struct {
	limit rate.Limit
	burst int
	cap   int
	idle  time.Duration

	mu      sync.Mutex
	buckets map[string]*keyedLimiterEntry
}

// newKeyedLimiterStore builds a store whose limiters refill at limit
// with the given burst. capacity is the soft size threshold that
// triggers an inline idle-sweep on the next get; idle is how long an
// unused bucket survives before that sweep evicts it.
func newKeyedLimiterStore(limit rate.Limit, burst, capacity int, idle time.Duration) *keyedLimiterStore {
	return &keyedLimiterStore{
		limit:   limit,
		burst:   burst,
		cap:     capacity,
		idle:    idle,
		buckets: make(map[string]*keyedLimiterEntry),
	}
}

// get returns (or creates) the limiter for key. Opportunistically
// evicts buckets idle longer than s.idle once the table grows beyond
// s.cap — an inline O(n) sweep bounded by the active key count.
func (s *keyedLimiterStore) get(key string) *rate.Limiter {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.buckets[key]; ok {
		e.lastUse = now
		return e.lim
	}
	if len(s.buckets) > s.cap {
		for k, e := range s.buckets {
			if now.Sub(e.lastUse) > s.idle {
				delete(s.buckets, k)
			}
		}
	}
	lim := rate.NewLimiter(s.limit, s.burst)
	s.buckets[key] = &keyedLimiterEntry{lim: lim, lastUse: now}
	return lim
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
