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

// Budget reports whether the source of r has a credential-verification token
// left in its per-IP bucket — WITHOUT consuming one — and the Retry-After
// seconds when it does not. It backs the Basic-auth guard
// ([auth.GuardBasicAuth]), which turns a spent budget into a 429.
func (l *LoginRateLimiter) Budget(r *http.Request) (ok bool, retryAfter int) {
	lim := l.store.get(loginClientIP(r))
	if lim.Tokens() >= 1 {
		return true, 0
	}
	return false, retryAfterSeconds(lim)
}

// ReserveBasicAttempt takes one token from the source's bucket for a Basic
// credential verification that is about to run, and returns the refund to
// call when the credential turned out to be valid — a successful
// verification must cost nothing, so the token goes back.
//
// Taking the token BEFORE the verification is the whole point: the bcrypt
// compare behind it is the expensive operation, and accounting for it
// afterwards let any number of concurrent attempts pass the same peek and run
// the KDF in parallel. A reservation is per-attempt, so concurrency cannot
// outrun it. The buckets are shared with the login POST, so a Basic-guessing
// sweep and a login sweep deplete one budget.
func (l *LoginRateLimiter) ReserveBasicAttempt(r *http.Request) (refund func(), ok bool) {
	now := time.Now()
	res := l.store.get(loginClientIP(r)).ReserveN(now, 1)
	// A positive delay means the token only becomes available in the future:
	// the bucket is empty now, so the attempt must not run. Cancelling undoes
	// the reservation, or a refused attempt would push the next one further
	// out.
	if !res.OK() || res.Delay() > 0 {
		res.Cancel()
		return nil, false
	}
	// Cancel at the instant the token was taken, not at the instant of the
	// refund: a reservation whose act time has already passed is treated as
	// consumed and Cancel() then restores nothing, so a valid credential
	// would silently spend the source's budget after all.
	return func() { res.CancelAt(now) }, true
}

// Charge records one failed credential verification from the source of r that
// nothing accounted for beforehand. It backs the Basic-auth guard's fallback
// for a resolver with no throttle wired ([auth.GuardBasicAuth]); the wired
// path uses [LoginRateLimiter.ReserveBasicAttempt] instead, which charges
// before the verification rather than after it.
func (l *LoginRateLimiter) Charge(r *http.Request) {
	_ = l.store.get(loginClientIP(r)).Allow()
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
