// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubNext is a minimal http.Handler that records whether it was invoked.
type stubNext struct {
	called bool
}

func (s *stubNext) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.called = true
	w.WriteHeader(http.StatusOK)
}

// applyCSRF wraps stub with CSRFMiddleware(false) and serves r, returning
// the response recorder and whether the stub was reached.
func applyCSRF(r *http.Request) (*httptest.ResponseRecorder, bool) {
	stub := &stubNext{}
	handler := CSRFMiddleware(false)(stub)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)
	return rr, stub.called
}

// TestCSRFExemptsBearerAuthHeader verifies that a POST carrying a non-empty
// Bearer token bypasses the double-submit check even with no CSRF cookie or header.
func TestCSRFExemptsBearerAuthHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	r.Header.Set("Authorization", "Bearer sometoken")

	rr, called := applyCSRF(r)
	if !called {
		t.Fatalf("expected stub to be called (Bearer exemption), got status %d", rr.Code)
	}
}

// TestCSRFEmptyBearerTokenNotExempt confirms that "Authorization: Bearer "
// (empty token) does not trigger the Bearer exemption; the request gets 403.
func TestCSRFEmptyBearerTokenNotExempt(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	r.Header.Set("Authorization", "Bearer ")

	rr, called := applyCSRF(r)
	if called {
		t.Fatal("expected stub NOT to be called for empty Bearer token")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for empty Bearer token, got %d", rr.Code)
	}
}

// TestCSRFNoAuthHeaderMutatingMethodBlocked confirms that a plain POST with
// neither a CSRF double-submit pair nor an Authorization header receives 403.
func TestCSRFNoAuthHeaderMutatingMethodBlocked(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)

	rr, called := applyCSRF(r)
	if called {
		t.Fatal("expected stub NOT to be called for unauthenticated POST without CSRF token")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

// TestCSRFSafeMethodPasses confirms that GET requests are never blocked
// by the CSRF middleware regardless of missing credentials or tokens.
func TestCSRFSafeMethodPasses(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/something", http.NoBody)

	rr, called := applyCSRF(r)
	if !called {
		t.Fatalf("expected stub to be called for GET, got status %d", rr.Code)
	}
}

// TestCSRFFederatedSessionNotExempt pins that a session minted through the
// external identity provider keeps the double-submit defence. It rides the
// same browser-ambient cookie as any other session, so exempting it would
// open every mutating endpoint to cross-site requests.
func TestCSRFFederatedSessionNotExempt(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	if csrfExempt(r, SchemeOIDC) {
		t.Error("SchemeOIDC is cookie-borne and must not be CSRF-exempt")
	}
}

// applyCSRFAs wraps stub with CSRFMiddleware(false), stamps id onto the
// request the way the auth middleware does, and serves it.
func applyCSRFAs(r *http.Request, id Identity) (*httptest.ResponseRecorder, bool) {
	return applyCSRF(r.WithContext(ContextWithIdentity(r.Context(), id)))
}

// TestCSRFBasicCrossSiteBrowserRequestBlocked pins the CSRF defence for the
// case Basic auth is ambient authority: the operator answered the browser's
// Basic prompt once, so the browser replays the Authorization header on a
// request another site triggered. The double-submit token — which a foreign
// origin cannot read — is the only thing that separates that forgery from a
// genuine call, so it must be demanded.
func TestCSRFBasicCrossSiteBrowserRequestBlocked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{
			name:    "fetch metadata says cross-site",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
		},
		{
			name:    "fetch metadata says same-site (a sibling subdomain is still another site)",
			headers: map[string]string{"Sec-Fetch-Site": "same-site"},
		},
		{
			name:    "no fetch metadata, foreign Origin",
			headers: map[string]string{"Origin": "https://evil.example"},
		},
		{
			name:    "no fetch metadata, opaque Origin",
			headers: map[string]string{"Origin": "://"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/system/ccu/x/reboot", http.NoBody)
			r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			rr, called := applyCSRFAs(r, Identity{Subject: "op", Scheme: SchemeBasic})
			if called {
				t.Fatal("cross-site browser request authenticated by cached Basic credentials must not pass CSRF")
			}
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
		})
	}
}

// TestCSRFBasicNonBrowserRequestExempt pins the other half: scripts (curl, CI,
// ops automation) carry no browser markers, nothing attaches their credentials
// for them, and they keep passing without a double-submit token.
func TestCSRFBasicNonBrowserRequestExempt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "no browser markers at all"},
		{name: "same-origin fetch", headers: map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{name: "user-typed navigation", headers: map[string]string{"Sec-Fetch-Site": "none"}},
		{name: "own Origin", headers: map[string]string{"Origin": "http://example.com"}},
		{
			name: "own Origin behind a reverse proxy",
			headers: map[string]string{
				"Origin":           "https://loom.example.net",
				"X-Forwarded-Host": "loom.example.net, inner",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/something", http.NoBody)
			r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			rr, called := applyCSRFAs(r, Identity{Subject: "op", Scheme: SchemeBasic})
			if !called {
				t.Fatalf("expected the request to pass the CSRF gate, got status %d", rr.Code)
			}
		})
	}
}

// TestCSRFBearerCrossSiteStillExempt pins that the scoping applies to Basic
// alone: a bearer token is never attached by the browser itself, so a page on
// another site cannot produce an authenticated request and the exemption that
// keeps header-auth clients working stays intact.
func TestCSRFBearerCrossSiteStillExempt(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	r.Header.Set("Authorization", "Bearer sometoken")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rr, called := applyCSRFAs(r, Identity{Subject: "svc", Scheme: SchemeBearer})
	if !called {
		t.Fatalf("bearer requests must stay exempt, got status %d", rr.Code)
	}
}

// TestCSRFBasicCrossSiteWithTokenPasses confirms the blocked case is a token
// requirement, not a ban: the SPA-style double-submit pair still authorises a
// Basic-authenticated mutation.
func TestCSRFBasicCrossSiteWithTokenPasses(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "token-value"})
	r.Header.Set(CSRFHeaderName, "token-value")
	rr, called := applyCSRFAs(r, Identity{Subject: "op", Scheme: SchemeBasic})
	if !called {
		t.Fatalf("matching double-submit pair must pass, got status %d", rr.Code)
	}
}

// TestHasBearerAuthHeader directly unit-tests the hasBearerAuthHeader helper.
func TestHasBearerAuthHeader(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"Bearer abc123", true},
		{"Bearer    trimmed   ", true}, // non-empty after trim
		{"Bearer ", false},             // empty token
		{"Bearer", false},              // no space, no token
		{"Basic dXNlcjpwYXNz", false},  // wrong scheme
		{"", false},                    // no header at all
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		got := hasBearerAuthHeader(r)
		if got != tc.want {
			t.Errorf("hasBearerAuthHeader(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}
