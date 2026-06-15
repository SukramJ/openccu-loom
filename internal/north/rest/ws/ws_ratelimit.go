// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// Per-identity WS command-rate defaults: generous enough that the SPA's burst
// of commands on a page load is never throttled, low enough that a single
// authenticated session cannot saturate the south-bound write path.
const (
	commandRatePerSec = 20
	commandRateBurst  = 60
	commandRateCap    = 4096             // soft cap before an idle sweep runs
	commandRateIdle   = 10 * time.Minute // evict buckets idle longer than this
)

// commandRateLimiter is a per-identity token bucket guarding the WS command
// channel. Once a connection is upgraded the REST per-request rate limiter no
// longer applies, so without this a single authenticated session could fan
// out paramset writes / ReGa executions unbounded. Keyed by auth subject with
// idle eviction so the bucket map cannot grow without bound under a source
// that rotates identities.
type commandRateLimiter struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*cmdBucket
}

type cmdBucket struct {
	lim     *rate.Limiter
	lastUse time.Time
}

func newCommandRateLimiter(rps float64, burst int) *commandRateLimiter {
	return &commandRateLimiter{
		limit:   rate.Limit(rps),
		burst:   burst,
		buckets: make(map[string]*cmdBucket),
	}
}

// allow reports whether a command from key may proceed now.
func (l *commandRateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) > commandRateCap {
			for k, e := range l.buckets {
				if now.Sub(e.lastUse) > commandRateIdle {
					delete(l.buckets, k)
				}
			}
		}
		b = &cmdBucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.lastUse = now
	return b.lim.Allow()
}

// commandRateKey derives the limiter key from the request's auth identity,
// falling back to a shared "anonymous" bucket for unauthenticated callers so a
// non-auth flood cannot exhaust the bucket map by rotating addresses.
func commandRateKey(ctx context.Context) string {
	if id, ok := auth.IdentityFrom(ctx); ok && id.Subject != "" {
		return id.Subject
	}
	return "anonymous"
}
