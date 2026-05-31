// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RateLimitConfig parameterises the per-identity token-bucket.
// Zero values yield a sensible default (10 rps, burst 30); use
// [RateLimitConfig.Effective] to materialise the active settings.
type RateLimitConfig struct {
	// RequestsPerSecond is the steady-state token-refill rate.
	// Defaults to 10.
	RequestsPerSecond float64
	// Burst is the bucket size — how many tokens can accumulate
	// during an idle window. Defaults to 30.
	Burst int
	// SkipPaths is the set of request paths that bypass rate
	// limiting entirely. Defaults to a small allow-list covering
	// liveness / readiness / info endpoints; an explicit non-nil
	// slice (even empty) overrides the default.
	SkipPaths []string
}

// Effective applies defaults to any zero-value field. Returned by
// value so the constant case stays cheap.
func (c RateLimitConfig) Effective() RateLimitConfig {
	if c.RequestsPerSecond <= 0 {
		c.RequestsPerSecond = 10
	}
	if c.Burst <= 0 {
		c.Burst = 30
	}
	if c.SkipPaths == nil {
		c.SkipPaths = []string{
			"/api/v1/info",
			"/api/v1/health",
		}
	}
	return c
}

// rateLimitIdleTTL is how long an idle per-identity limiter survives
// before garbage-collection. Two minutes is long enough to absorb
// brief HA-style reconnect bursts, short enough that idle limiters
// for one-off CI runs don't accumulate forever.
const rateLimitIdleTTL = 2 * time.Minute

// RateLimit returns a middleware that enforces a per-identity
// token-bucket on the REST surface. Unauthenticated requests share
// a single bucket keyed by the literal "anonymous"; authenticated
// requests get one bucket per [auth.Identity.Subject].
//
// On overflow the middleware writes RFC 9457 problem+json with
// `code: rate_limited`, HTTP 429, and a `Retry-After` header set
// to the integer number of seconds the caller should wait before
// the bucket has a token again — matches the contract the
// `Problem` schema description in openapi.yaml documents.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	eff := cfg.Effective()
	skip := make(map[string]struct{}, len(eff.SkipPaths))
	for _, p := range eff.SkipPaths {
		skip[p] = struct{}{}
	}
	limiters := newLimiterStore(rate.Limit(eff.RequestsPerSecond), eff.Burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			lim := limiters.get(identityKey(r))
			if !lim.Allow() {
				retry := retryAfter(lim)
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				problem.Write(w, http.StatusTooManyRequests,
					problem.New(problem.TypeRateLimited, r, "Too many requests",
						"per-identity rate limit exceeded — wait "+strconv.Itoa(retry)+"s and retry"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// identityKey resolves the per-request bucket key. Authenticated
// requests are keyed by the resolved subject; anonymous traffic
// shares one bucket so a misbehaving non-auth caller cannot exhaust
// the limiter store by rotating client addresses.
func identityKey(r *http.Request) string {
	if r == nil {
		return "anonymous"
	}
	if id, ok := auth.IdentityFrom(r.Context()); ok && id.Subject != "" {
		return id.Subject
	}
	return "anonymous"
}

// retryAfter computes the integer seconds the client must wait
// before the bucket regains a token. Always returns ≥ 1 so the
// header value is meaningful.
func retryAfter(lim *rate.Limiter) int {
	reserve := lim.Reserve()
	d := reserve.Delay()
	reserve.Cancel()
	if d <= 0 {
		return 1
	}
	return int(math.Ceil(d.Seconds()))
}

// limiterStore is a bounded per-identity limiter cache with
// idle-eviction. Map access is guarded by a single mutex; GC runs
// inline on each get when the table grows beyond a soft cap.
type limiterStore struct {
	limit rate.Limit
	burst int

	mu       sync.Mutex
	limiters map[string]*identityLimiter
}

type identityLimiter struct {
	lim     *rate.Limiter
	lastUse time.Time
}

func newLimiterStore(limit rate.Limit, burst int) *limiterStore {
	return &limiterStore{
		limit:    limit,
		burst:    burst,
		limiters: make(map[string]*identityLimiter),
	}
}

// get returns (or creates) the limiter for key. Opportunistically
// evicts limiters that have been idle longer than [rateLimitIdleTTL].
func (s *limiterStore) get(key string) *rate.Limiter {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.limiters[key]; ok {
		entry.lastUse = now
		return entry.lim
	}
	// Inline GC when the map grows. Cheap O(n) sweep — n is bounded
	// by active identity count, typically single digits in HA setups.
	if len(s.limiters) > 256 {
		for k, e := range s.limiters {
			if now.Sub(e.lastUse) > rateLimitIdleTTL {
				delete(s.limiters, k)
			}
		}
	}
	lim := rate.NewLimiter(s.limit, s.burst)
	s.limiters[key] = &identityLimiter{lim: lim, lastUse: now}
	return lim
}
