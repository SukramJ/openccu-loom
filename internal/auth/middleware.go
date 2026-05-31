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
		if id, ok := m.resolve(r); ok {
			ctx := context.WithValue(r.Context(), keyIdentity, id)
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

func (m *Middleware) resolve(r *http.Request) (Identity, bool) {
	// Bearer first — lets operators use tokens for CI/automation.
	if m.Tokens != nil {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if id, err := m.Tokens.AuthenticateToken(r.Context(), token); err == nil {
				return id, true
			}
		}
	}
	if m.Users != nil {
		if user, pass, ok := r.BasicAuth(); ok {
			if id, err := m.Users.AuthenticateBasic(r.Context(), user, pass); err == nil {
				return id, true
			} else if !errors.Is(err, ErrUnauthenticated) {
				// Other errors propagate as unresolved — they'll be
				// re-surfaced as 401 in Require below.
				return Identity{}, false
			}
		}
	}
	return Identity{}, false
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
