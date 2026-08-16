// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

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
// [SchemeBearer] passes through: a browser never attaches a bearer
// token by itself, so a malicious page cannot produce an authenticated
// request, and demanding the double-submit token would only break
// header-auth clients (curl, CI, ops automation).
//
// [SchemeBasic] is ambient authority as soon as a browser has cached
// the credentials for this origin — it then replays the Authorization
// header on requests another site triggered, exactly like a session
// cookie. Basic therefore keeps the exemption only for requests that
// cannot have been ambient-authenticated by a browser; see
// [csrfExempt].
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
			if id, ok := IdentityFrom(r.Context()); ok && csrfExempt(r, id.Scheme) {
				next.ServeHTTP(w, r)
				return
			}
			// A raw `Authorization: Bearer` header is exempt even when it did
			// not resolve to a known identity (e.g. the inbound-webhook token,
			// which is validated by a route-scoped middleware rather than the
			// global token store). The double-submit defence is about ambient
			// cookie credentials; a browser cannot set the Authorization header
			// on a cross-origin form/simple request, so a Bearer request is not
			// a CSRF vector. An invalid token still fails auth downstream (401),
			// so skipping the CSRF check here grants nothing.
			if hasBearerAuthHeader(r) {
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

// hasBearerAuthHeader reports whether the request carries a non-empty
// `Authorization: Bearer <token>` header. Such requests are CSRF-exempt
// regardless of whether the token resolved to a known identity — see the
// rationale at the call site.
func hasBearerAuthHeader(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	return strings.HasPrefix(h, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")) != ""
}

// csrfExempt reports whether an authenticated request bypasses the
// double-submit token check.
//
// The distinction is not "header credential vs cookie" but "can a page
// on another site cause this credential to be attached without knowing
// it". A bearer token cannot: nothing in the browser adds it on its own.
// Basic credentials can: once the user has answered the browser's Basic
// prompt for this origin, the browser replays the Authorization header
// on requests that any other site initiates — a cross-site form POST
// then arrives fully authenticated. Exempting Basic unconditionally
// would leave every mutating endpoint forgeable for exactly the
// operators who use the browser prompt.
//
// Basic therefore keeps the exemption only while the request carries no
// evidence of a browser having sent it from another site
// ([csrfBrowserCrossSite]). Scripts (curl, CI, ops automation) send no
// such markers and stay exempt; a browser-initiated cross-site request
// must present the double-submit token, which a foreign origin cannot
// read.
func csrfExempt(r *http.Request, s Scheme) bool {
	switch s {
	case SchemeBearer:
		// A browser never attaches a bearer token by itself.
		return true
	case SchemeBasic:
		return !csrfBrowserCrossSite(r)
	case SchemeIngress:
		// Per-request, proxy-trusted (network + X-Ingress-Path), not a
		// browser-ambient cookie — the double-submit defence does not apply.
		return true
	case SchemeSession, SchemeOIDC:
		// Both ride the browser-ambient session cookie, whichever authority
		// minted the identity — the double-submit defence applies to both.
		return false
	}
	return false
}

// csrfBrowserCrossSite reports whether r shows evidence of having been
// initiated by a browser from a site other than the daemon's own origin.
//
// `Sec-Fetch-Site` is the primary signal: browsers set it on every
// request and page script cannot (it is a forbidden header name), so its
// value is trustworthy where it exists. `Origin` is the fallback for
// clients too old to send fetch metadata — browsers attach it to every
// unsafe-method request, and an Origin whose host is neither the request
// host nor the proxy-forwarded host identifies another site.
//
// Absence of both means a non-browser client: no ambient authority is in
// play and the request stays exempt.
func csrfBrowserCrossSite(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site", "same-site":
		return true
	case "same-origin", "none":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || strings.EqualFold(origin, "null") {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// An Origin the daemon cannot resolve to a host cannot be proven
		// same-origin, so it counts as another site.
		return true
	}
	return !csrfSameOriginHost(u.Host, r)
}

// csrfSameOriginHost reports whether originHost addresses the same host
// the request itself was sent to. Behind a reverse proxy the browser's
// Origin names the external host while r.Host names the internal one, so
// the first entry of an X-Forwarded-Host chain counts as well.
func csrfSameOriginHost(originHost string, r *http.Request) bool {
	if strings.EqualFold(originHost, r.Host) {
		return true
	}
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		if first := strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0]); first != "" &&
			strings.EqualFold(originHost, first) {
			return true
		}
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
