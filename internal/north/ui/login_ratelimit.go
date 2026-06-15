// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
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

// loginRateLimiter is a per-client-IP token bucket guarding the pre-auth
// HTMX login POST against brute-force sweeps. The REST per-identity limiter
// (internal/north/rest/middleware) cannot cover this surface: it keys on a
// resolved auth identity (absent before login) and runs on the REST
// listener, not the UI listener. Keyed by client IP with idle eviction so a
// source rotating through addresses cannot grow the table without bound.
type loginRateLimiter struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*ipBucket
}

type ipBucket struct {
	lim     *rate.Limiter
	lastUse time.Time
}

// newLoginRateLimiter builds a limiter allowing loginRatePerSec sustained
// requests per IP with the given burst.
func newLoginRateLimiter(burst int) *loginRateLimiter {
	return &loginRateLimiter{
		limit:   rate.Limit(loginRatePerSec),
		burst:   burst,
		buckets: make(map[string]*ipBucket),
	}
}

// allow reports whether a request from ip may proceed, and — when denied —
// the integer seconds the caller should wait before a token frees up.
func (l *loginRateLimiter) allow(ip string) (ok bool, retryAfter int) {
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
		b = &ipBucket{lim: rate.NewLimiter(l.limit, l.burst)}
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

// guard wraps next with the per-IP limiter. On overflow it sets Retry-After
// and redirects back to the login page with the generic error marker — the
// same response a bad-credentials attempt produces, so the limiter neither
// leaks its presence nor needs a dedicated template branch.
func (l *loginRateLimiter) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, retry := l.allow(clientIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// clientIP is the best-effort source address used as the rate-limit key.
// It deliberately uses RemoteAddr only — honouring X-Forwarded-For without a
// configured trusted-proxy allow-list would let an attacker bypass the limit
// by spoofing the header. Behind a reverse proxy all clients therefore share
// the proxy's bucket; the generous burst keeps that from harming small
// single-proxy deployments.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
