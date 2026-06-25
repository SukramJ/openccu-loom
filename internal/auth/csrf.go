// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// CSRFCookieName is the double-submit cookie for CSRF tokens.
const CSRFCookieName = "openccu_loom_csrf"

// CSRFHeaderName is the matching header the client echoes back.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFFormField is the form field the HTMX/plain-HTML path uses.
const CSRFFormField = "_csrf"

type csrfCtxKey struct{}

// CSRFToken fetches the active token from ctx. Handlers render it
// into forms through `.Data.CSRFToken`.
func CSRFToken(ctx context.Context) string {
	v, _ := ctx.Value(csrfCtxKey{}).(string)
	return v
}

// CSRFMiddleware implements the double-submit pattern:
//   - every response carries (or refreshes) a CSRF cookie
//   - mutating requests must echo the cookie value in either
//     `X-CSRF-Token` or the form field `_csrf`
//
// loom:reachable:reason="wired into the REST router middleware chain for SPA mutation protection"
//
// Safe methods (GET/HEAD/OPTIONS) pass through unchanged.
//
// Per-request credential schemes — [SchemeBasic] and [SchemeBearer] —
// also pass through: those carry their credential in a per-request
// Authorization header that browsers never auto-include on cross-
// origin requests, so they cannot be forged by a malicious page.
// CSRF protection is fundamentally about ambient credentials (session
// cookies); enforcing it on header-auth scripts (curl, the chip-tool
// test harness, ops automation) would block legitimate clients while
// adding no real protection. OWASP CSRF Cheat Sheet §"Token-based
// mitigation" — "CSRF tokens are not needed for endpoints that do not
// use cookies for authentication".
func CSRFMiddleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(CSRFCookieName); err == nil {
				token = c.Value
			}
			if token == "" {
				t, err := randomID()
				if err != nil {
					problem.Write(w, http.StatusInternalServerError,
						problem.New(problem.TypeInternal, r, "CSRF mint failed", err.Error()))
					return
				}
				token = t
				// CSRF token cookie is intentionally readable by JS
				// (HttpOnly=false) so the SPA can echo it back via the
				// double-submit header. SameSite=Lax + Secure (when
				// behind HTTPS) keep the token bound to the origin.
				http.SetCookie(w, &http.Cookie{ //nolint:gosec // double-submit CSRF cookie must be JS-readable; see #20
					Name:     CSRFCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: false,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
				})
			}
			ctx := context.WithValue(r.Context(), csrfCtxKey{}, token)
			r = r.WithContext(ctx)

			if csrfIsSafe(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			//nolint:contextcheck // r carries a context derived from r.Context() via WithValue above; reading it back is not a new detached context
			if id, ok := IdentityFrom(r.Context()); ok && csrfSchemeExempt(id.Scheme) {
				next.ServeHTTP(w, r)
				return
			}
			submitted := r.Header.Get(CSRFHeaderName)
			if submitted == "" {
				// Cap the form body before parsing — a malicious POST
				// with a multi-GB body would otherwise pin one
				// goroutine in r.ParseForm. 64 KiB is well above what
				// any legitimate CSRF-protected form sends.
				r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
				_ = r.ParseForm()
				submitted = r.FormValue(CSRFFormField)
			}
			if submitted == "" || subtle.ConstantTimeCompare([]byte(token), []byte(submitted)) != 1 {
				problem.Write(w, http.StatusForbidden,
					problem.New(problem.TypeForbidden, r, "CSRF check failed", "token mismatch"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfSchemeExempt reports whether an authenticated scheme bypasses
// the double-submit token check. Per-request credential schemes —
// Authorization: Basic and Authorization: Bearer — are not in scope
// because browsers do not auto-include those headers on cross-origin
// requests. Only ambient credentials (session cookies) require the
// double-submit defence.
func csrfSchemeExempt(s Scheme) bool {
	switch s {
	case SchemeBasic, SchemeBearer:
		return true
	case SchemeIngress:
		// Per-request, proxy-trusted (network + X-Ingress-Path), not a
		// browser-ambient cookie — the double-submit defence does not apply.
		return true
	case SchemeSession:
		return false
	}
	return false
}

func csrfIsSafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
