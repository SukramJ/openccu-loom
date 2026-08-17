// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
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
// The two operations are deliberately split so a successful verification costs
// nothing: Budget only peeks, and Charge is called for a failed attempt alone.
type BasicAuthThrottle interface {
	// Budget reports whether the source of r may attempt a Basic verification
	// — a token is available in its bucket — WITHOUT consuming one, and the
	// integer Retry-After seconds when it may not.
	Budget(r *http.Request) (ok bool, retryAfter int)
	// Charge records one failed Basic verification against the source of r,
	// consuming a token from the shared per-IP bucket.
	Charge(r *http.Request)
}

// GuardBasicAuth throttles per-source HTTP Basic credential guessing on every
// route it wraps. It closes the gap where the login limiter only guards POST
// /auth/login while GET /auth/me — and every other route behind Resolve —
// accepts an unlimited stream of `Authorization: Basic` probes (200 reveals a
// correct password, 401 a wrong one).
//
// Mount it immediately AFTER Resolve so it can read the identity Resolve
// attached:
//
//   - A request that carries no Basic credentials is never a guess: it passes
//     untouched and the bucket is not consulted.
//   - When the source's budget is already spent, every Basic attempt — valid or
//     not — is answered 429 before the downstream handler can reveal whether the
//     credential was good, closing the "exhaust the bucket, then probe for the
//     200" side channel.
//   - Within budget, a valid credential passes and consumes nothing; an invalid
//     one charges the source one token and then falls through to the normal 401.
//
// A nil throttle disables the guard (test fixtures, builds without the limiter).
func GuardBasicAuth(t BasicAuthThrottle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if t == nil || !hasBasicCredentials(r) {
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
			if _, resolved := IdentityFrom(r.Context()); !resolved {
				// Basic header present, budget available, yet Resolve attached no
				// identity: a failed guess. Charge the source, then let the normal
				// unauthenticated flow answer 401.
				t.Charge(r)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hasBasicCredentials reports whether r carries a parseable HTTP Basic
// Authorization header. It is the guard's "is this a credential attempt?"
// test — a request without Basic credentials is not a guess and is never
// throttled.
func hasBasicCredentials(r *http.Request) bool {
	_, _, ok := r.BasicAuth()
	return ok
}
