// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net"
	"net/http"
	"strconv"
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
	loginLimiterCap  = 4096             // hard ceiling on live per-IP buckets
	loginLimiterIdle = 10 * time.Minute // reclaim prefers buckets idle longer than this
)

// LoginRateLimiter is a per-client-IP token bucket guarding the pre-auth
// login POST against brute-force sweeps. The per-identity [RateLimit]
// middleware cannot cover this surface: it keys on a resolved auth identity,
// which is absent before login, so every unauthenticated attempt shares one
// "anonymous" bucket — a global throttle, not per-source brute-force
// protection. Keyed by client IP; the backing table is hard-bounded at
// loginLimiterCap buckets, so a source rotating through an address range —
// an IPv6 /64 offers 2^64 of them, all able to complete a handshake — cannot
// grow it. Rotation still buys the attacker a fresh burst per address; the
// bound is about the daemon's memory, not about defeating rotation.
type LoginRateLimiter struct {
	store *keyedLimiterStore
}

// NewLoginRateLimiter builds a limiter allowing loginRatePerSec sustained
// requests per IP with a small burst.
func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		store: newKeyedLimiterStore(rate.Limit(loginRatePerSec), loginRateBurst, loginLimiterCap, loginLimiterIdle),
	}
}

// allow reports whether a request from ip may proceed, and — when denied —
// the integer seconds the caller should wait before a token frees up.
func (l *LoginRateLimiter) allow(ip string) (ok bool, retryAfter int) {
	lim := l.store.get(ip)
	if lim.Allow() {
		return true, 0
	}
	return false, retryAfterSeconds(lim)
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
