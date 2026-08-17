// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"net/http"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// BasicAuthThrottle throttles repeated failed HTTP Basic credential
// verification by source IP. It is backed by the same per-IP token buckets
// that guard the login POST, so a credential-guessing sweep is limited no
// matter which Resolve-protected route carries the Basic header — not just
// POST /auth/login.
//
// Its two halves sit on opposite sides of the password KDF.
// ReserveBasicAttempt is called by [Middleware.Resolve] immediately BEFORE
// the verification, because the verification itself is the expensive
// operation the throttle exists to bound — a bcrypt compare at cost 12 costs
// hundreds of milliseconds of CPU, so a throttle downstream of it protects
// nothing. Budget is the read-only peek [GuardBasicAuth] uses afterwards to
// turn a spent budget into a 429.
type BasicAuthThrottle interface {
	// Budget reports whether the source of r has a verification token left —
	// WITHOUT consuming one — and the integer Retry-After seconds when it
	// does not.
	Budget(r *http.Request) (ok bool, retryAfter int)
	// ReserveBasicAttempt consumes one token for a Basic verification that is
	// about to run. ok is false when the source is out of budget, and the
	// caller must then skip the verification entirely. refund returns the
	// token and is called when the credential verified — a valid credential
	// must cost nothing. refund is nil when ok is false.
	ReserveBasicAttempt(r *http.Request) (refund func(), ok bool)
	// Charge records one failed verification the resolver did not account
	// for. It is the fallback [GuardBasicAuth] uses behind a resolver with no
	// throttle wired, so a mount that misses that wiring is still throttled —
	// after the verification rather than before it.
	Charge(r *http.Request)
}

// GuardBasicAuth answers 429 for a source that has spent its Basic-auth
// budget. It closes the gap where the login limiter only guards POST
// /auth/login while GET /auth/me — and every other route behind Resolve —
// accepts an unlimited stream of `Authorization: Basic` probes (200 reveals a
// correct password, 401 a wrong one).
//
// Mount it immediately AFTER Resolve so it observes the bucket the
// verification just charged:
//
//   - A request that carries no Basic credentials is never a guess: it passes
//     untouched and the bucket is not consulted.
//   - When the source's budget is spent, every Basic attempt — valid or not —
//     is answered 429 rather than the downstream handler revealing whether the
//     credential was good, closing the "exhaust the bucket, then probe for the
//     200" side channel.
//   - Within budget, a valid credential passes and consumes nothing (Resolve
//     refunds it); an invalid one has already cost the source one token.
//
// The accounting itself lives in [Middleware.Resolve] rather than here, so an
// out-of-budget source never reaches the password KDF at all — see
// [BasicAuthThrottle].
//
// A nil throttle disables the guard (test fixtures, builds without the limiter).
func GuardBasicAuth(t BasicAuthThrottle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if t == nil || !hasBasicCredentials(r) {
				next.ServeHTTP(w, r)
				return
			}
			if _, resolved := IdentityFrom(r.Context()); resolved {
				next.ServeHTTP(w, r)
				return
			}
			if basicSchemeDisabled(r.Context()) {
				// Resolve never attempted a verification for this request —
				// the Basic scheme is administratively off (no UserStore
				// wired) — so there is nothing here to rate-limit. Charging
				// anyway would let a client that still sends a stale Basic
				// header (a browser that once answered the challenge, an old
				// integration) deplete the same per-IP bucket POST
				// /auth/login needs, locking that source out of login while
				// Basic itself answers every such request instantly.
				next.ServeHTTP(w, r)
				return
			}
			ok, retry := t.Budget(r)
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				problem.Write(w, http.StatusTooManyRequests,
					problem.New(problem.TypeRateLimited, r, "Too many authentication attempts",
						"too many failed credential attempts — wait "+strconv.Itoa(retry)+"s and retry"))
				return
			}
			if !basicAttemptAccounted(r.Context()) {
				// The resolver in front of this guard has no throttle wired, so
				// nothing charged the failed verification it just ran. Charge it
				// here rather than leave the sweep unbounded — the source is
				// still limited, it just paid the key derivation first.
				t.Charge(r)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// keyBasicAccounted marks a request whose Basic verification the resolver
// already put through the throttle. It exists so the two accounting points
// cannot both charge the same attempt: [Middleware.Resolve] charges before the
// verification (where it bounds the cost), and [GuardBasicAuth] only falls
// back to charging after it when the resolver had no throttle to consult.
const keyBasicAccounted ctxKey = "basic-attempt-accounted"

// markBasicAttemptAccounted returns a context recording that the throttle has
// already seen this request's Basic verification.
func markBasicAttemptAccounted(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyBasicAccounted, true)
}

// basicAttemptAccounted reports whether the resolver already charged this
// request's Basic verification.
func basicAttemptAccounted(ctx context.Context) bool {
	accounted, _ := ctx.Value(keyBasicAccounted).(bool)
	return accounted
}

// keyBasicSchemeDisabled marks a request that carried a Basic header
// [Middleware.resolve] did not even attempt to verify because no
// UserStore is wired (the operator turned Basic auth off). It is
// distinct from keyBasicAccounted: an unaccounted attempt still means
// "Basic ran, nothing charged it yet, charge it now" — this means
// "Basic never ran at all, there is nothing to charge."
const keyBasicSchemeDisabled ctxKey = "basic-scheme-disabled"

// markBasicSchemeDisabled returns a context recording that the request's
// Basic header was never attempted because the scheme is administratively
// off.
func markBasicSchemeDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyBasicSchemeDisabled, true)
}

// basicSchemeDisabled reports whether the resolver skipped this request's
// Basic header because no UserStore is wired.
func basicSchemeDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(keyBasicSchemeDisabled).(bool)
	return disabled
}

// hasBasicCredentials reports whether r carries a parseable HTTP Basic
// Authorization header. It is the guard's "is this a credential attempt?"
// test — a request without Basic credentials is not a guess and is never
// throttled.
func hasBasicCredentials(r *http.Request) bool {
	_, _, ok := r.BasicAuth()
	return ok
}
