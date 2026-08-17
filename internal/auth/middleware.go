// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

type ctxKey string

const keyIdentity ctxKey = "identity"

// Middleware bundles Resolve and Require; construct via [NewMiddleware]
// and wire both functions on the chi router.
type Middleware struct {
	Users  UserStore
	Tokens TokenStore
	Realm  string
	// BasicThrottle, when set, bounds how often a single source may pay for
	// an HTTP Basic password verification. It is consulted immediately
	// before the verification because the verification is the cost: the
	// password KDF is deliberately slow, so an unauthenticated caller that
	// reaches it can spend a CPU core per request on any route this
	// middleware resolves credentials for — including routes that need no
	// credential at all. Nil leaves the verification unthrottled.
	BasicThrottle BasicAuthThrottle
}

// NewMiddleware constructs a Middleware for the given stores. Either
// store may be nil — the corresponding scheme is then disabled.
func NewMiddleware(users UserStore, tokens TokenStore) *Middleware {
	return &Middleware{Users: users, Tokens: tokens, Realm: "openccu-loom"}
}

// Resolve inspects the incoming headers and attaches an [Identity]
// when authentication succeeds. It never blocks unauthenticated
// traffic — that is Require's job.
func (m *Middleware) Resolve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok, throttled, basicDisabled := m.resolve(r)
		ctx := r.Context()
		if throttled {
			// Tell the downstream guard this attempt is already accounted for,
			// so the two never charge the same source twice.
			ctx = markBasicAttemptAccounted(ctx)
		}
		if basicDisabled {
			// Tell the downstream guard this Basic header was never
			// attempted at all, so it has nothing to charge either.
			ctx = markBasicSchemeDisabled(ctx)
		}
		if ok {
			ctx = context.WithValue(ctx, keyIdentity, id)
		}
		if throttled || ok || basicDisabled {
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// Require gates downstream handlers: requests without an attached
// [Identity] receive problem+json `unauthorized`.
func (m *Middleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := IdentityFrom(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+m.Realm+`", Bearer`)
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Authentication required", "no valid credentials"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole is a shorthand for Require + role check.
func (m *Middleware) RequireRole(want Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+m.Realm+`", Bearer`)
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Authentication required", "no valid credentials"))
			return
		}
		if !id.HasRole(want) {
			problem.Write(w, http.StatusForbidden,
				problem.New(problem.TypeForbidden, r, "Forbidden", "insufficient role"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolve reports the identity the request's credentials carry, whether one
// was found, whether a Basic verification went through the throttle (so the
// downstream guard knows the attempt is already accounted for), and whether
// the request carried a Basic header that was never attempted because the
// scheme is administratively off (no UserStore wired) — the downstream guard
// must not charge that against the shared per-IP bucket either.
func (m *Middleware) resolve(r *http.Request) (id Identity, ok, throttled, basicDisabled bool) {
	// Bearer first — lets operators use tokens for CI/automation.
	if m.Tokens != nil {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if id, err := m.Tokens.AuthenticateToken(r.Context(), token); err == nil {
				return id, true, false, false
			}
		}
	}
	if m.Users == nil {
		if _, _, hasBasic := r.BasicAuth(); hasBasic {
			return Identity{}, false, false, true
		}
		return Identity{}, false, false, false
	}
	if user, pass, hasBasic := r.BasicAuth(); hasBasic {
		// Charge the attempt before the password KDF runs, not after: the
		// KDF is what the throttle has to bound. A source out of budget
		// is refused here, so it never reaches the verification.
		refund := func() {}
		if m.BasicThrottle != nil {
			cancel, allowed := m.BasicThrottle.ReserveBasicAttempt(r)
			if !allowed {
				return Identity{}, false, true, false
			}
			refund = cancel
			throttled = true
		}
		if id, err := m.Users.AuthenticateBasic(r.Context(), user, pass); err == nil {
			// A credential that verified costs nothing.
			refund()
			return id, true, throttled, false
		} else if !errors.Is(err, ErrUnauthenticated) {
			// Other errors propagate as unresolved — they'll be
			// re-surfaced as 401 in Require below. A store failure is not
			// the caller's doing, so the token goes back.
			refund()
			return Identity{}, false, throttled, false
		}
	}
	return Identity{}, false, throttled, false
}

// IdentityFrom returns the Identity attached to ctx by [Resolve].
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(keyIdentity).(Identity)
	return id, ok
}

// ContextWithIdentity returns a copy of ctx with id attached under the
// well-known identity key. Used by tests and WS upgrade handlers that
// need to inject an already-resolved Identity without going through the
// full HTTP middleware stack.
//
// loom:reachable:reason="used by WS upgrade handler and integration tests to inject pre-resolved identity"
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, keyIdentity, id)
}
