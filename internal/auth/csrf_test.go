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
	if csrfSchemeExempt(SchemeOIDC) {
		t.Error("SchemeOIDC is cookie-borne and must not be CSRF-exempt")
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
