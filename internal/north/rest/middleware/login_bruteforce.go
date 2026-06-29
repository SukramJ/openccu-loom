// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// Login brute-force speed-bump defaults: a small burst so an operator who
// fat-fingers the form a few times is unaffected, then a slow sustained
// refill so an automated sweep is throttled to roughly one attempt per
// second per source address.
const (
	loginRateBurst   = 5
	loginRatePerSec  = 1.0
	loginLimiterCap  = 4096             // soft cap before an idle sweep runs
	loginLimiterIdle = 10 * time.Minute // evict buckets idle longer than this
)

// LoginRateLimiter is a per-client-IP token bucket guarding the pre-auth
// login POST against brute-force sweeps. The per-identity [RateLimit]
// middleware cannot cover this surface: it keys on a resolved auth identity,
// which is absent before login, so every unauthenticated attempt shares one
// "anonymous" bucket — a global throttle, not per-source brute-force
// protection. Keyed by client IP with idle eviction so a source rotating
// through addresses cannot grow the table without bound.
type LoginRateLimiter struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*loginIPBucket
}

type loginIPBucket struct {
	lim     *rate.Limiter
	lastUse time.Time
}

// NewLoginRateLimiter builds a limiter allowing loginRatePerSec sustained
// requests per IP with a small burst.
func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		limit:   rate.Limit(loginRatePerSec),
		burst:   loginRateBurst,
		buckets: make(map[string]*loginIPBucket),
	}
}

// allow reports whether a request from ip may proceed, and — when denied —
// the integer seconds the caller should wait before a token frees up.
func (l *LoginRateLimiter) allow(ip string) (ok bool, retryAfter int) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.buckets[ip]
	if !found {
		if len(l.buckets) > loginLimiterCap {
			for k, e := range l.buckets {
				if now.Sub(e.lastUse) > loginLimiterIdle {
					delete(l.buckets, k)
				}
			}
		}
		b = &loginIPBucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[ip] = b
	}
	b.lastUse = now
	if b.lim.Allow() {
		return true, 0
	}
	res := b.lim.Reserve()
	d := res.Delay()
	res.Cancel()
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return false, secs
}

// Middleware wraps next with the per-IP limiter. On overflow it writes an
// RFC 9457 problem+json with `code: rate_limited`, HTTP 429, and a
// Retry-After header — the same contract the per-identity limiter uses, so
// the SPA's login form can surface a consistent "try again later" message.
func (l *LoginRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retry := l.allow(loginClientIP(r))
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				problem.Write(w, http.StatusTooManyRequests,
					problem.New(problem.TypeRateLimited, r, "Too many login attempts",
						"too many login attempts — wait "+strconv.Itoa(retry)+"s and retry"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// loginClientIP is the best-effort source address used as the rate-limit key.
// It deliberately uses RemoteAddr only — honouring X-Forwarded-For without a
// configured trusted-proxy allow-list would let an attacker bypass the limit
// by spoofing the header. Behind a reverse proxy all clients therefore share
// the proxy's bucket; the generous burst keeps that from harming small
// single-proxy deployments.
func loginClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
