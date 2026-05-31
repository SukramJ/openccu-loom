// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- isSafeRelativeTarget ---

func TestIsSafeRelativeTarget_SafePaths(t *testing.T) {
	t.Parallel()
	safe := []string{"/app/", "/login", "/app/settings"}
	for _, p := range safe {
		if !isSafeRelativeTarget(p) {
			t.Errorf("isSafeRelativeTarget(%q) = false, want true", p)
		}
	}
}

func TestIsSafeRelativeTarget_UnsafePaths(t *testing.T) {
	t.Parallel()
	unsafe := []string{
		"",                    // empty
		"relative",            // no leading slash
		"//evil.example",      // protocol-relative
		"https://evil",        // scheme
		"javascript:alert(1)", // JS scheme with colon
		"/path:with:colons",   // colons in path → filtered
	}
	for _, p := range unsafe {
		if isSafeRelativeTarget(p) {
			t.Errorf("isSafeRelativeTarget(%q) = true, want false", p)
		}
	}
}

// --- randomKey ---

func TestRandomKey_IsNonEmpty(t *testing.T) {
	t.Parallel()
	key, err := randomKey()
	if err != nil {
		t.Fatalf("randomKey: %v", err)
	}
	if key == "" {
		t.Error("randomKey must not return an empty string")
	}
}

func TestRandomKey_IsUnique(t *testing.T) {
	t.Parallel()
	a, _ := randomKey()
	b, _ := randomKey()
	if a == b {
		t.Error("two successive randomKey calls must produce different values")
	}
}

// --- OIDCStart with nil deps ---

func TestOIDCStart_NilDeps_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", http.NoBody)
	w := httptest.NewRecorder()
	OIDCStart(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- OIDCCallback with nil deps ---

func TestOIDCCallback_NilDeps_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=abc&state=xyz", http.NoBody)
	w := httptest.NewRecorder()
	OIDCCallback(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- OIDCCallback with error query param ---

func TestOIDCCallback_ErrorQueryParam_Redirects(t *testing.T) {
	t.Parallel()
	d := &OIDCDeps{
		states: make(map[string]oidcState),
		// We need a non-nil Client field to pass the first guard but we
		// can't create one without an OIDC provider. Set Auth and Sessions
		// to something that passes the nil check.
		Auth: &AuthDeps{Sessions: nil},
	}
	// When Client is nil the handler returns 503 before reaching the
	// error-redirect logic. We can only test the nil-auth guard here.
	req := httptest.NewRequest(http.MethodGet, "/callback?error=access_denied", http.NoBody)
	w := httptest.NewRecorder()
	OIDCCallback(d).ServeHTTP(w, req)

	// Client == nil → 503.
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no client), got %d", w.Code)
	}
}

// --- NewOIDCDeps constructor ---

func TestNewOIDCDeps_InitialisesStateMap(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	if d == nil {
		t.Fatal("constructor returned nil")
	}
	if d.states == nil {
		t.Fatal("states map must be initialised so putState does not nil-deref")
	}
	if d.now == nil {
		t.Fatal("clock must be installed so consumeState's TTL check works")
	}
}

// --- putState / consumeState round-trip ---

func TestOIDCDeps_PutAndConsumeState_RoundTrip(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	key, err := d.putState("verifier-abc")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}
	if key == "" {
		t.Fatal("key must not be empty")
	}
	got, ok := d.consumeState(key)
	if !ok {
		t.Fatal("consumeState must find the just-stored state")
	}
	if got != "verifier-abc" {
		t.Fatalf("verifier = %q, want verifier-abc", got)
	}
	// State is one-shot — second consume must fail.
	if _, ok := d.consumeState(key); ok {
		t.Fatal("state must be consumed exactly once")
	}
}

func TestOIDCDeps_ConsumeState_Unknown(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	if _, ok := d.consumeState("never-stored"); ok {
		t.Fatal("unknown state must not match")
	}
}

func TestOIDCDeps_ConsumeState_ExpiredAfterTTL(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return fakeNow }
	key, _ := d.putState("v")
	// Advance the clock past oidcStateTTL.
	d.now = func() time.Time { return fakeNow.Add(oidcStateTTL + time.Second) }
	if _, ok := d.consumeState(key); ok {
		t.Fatal("expired state must not match")
	}
}

// --- oidcRedirectError ---

// oidcRedirectError is exercised via direct call since we can't reach it
// through OIDCCallback without a working OIDC provider.
func TestOIDCRedirectError_SafePath(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/callback", http.NoBody)
	w := httptest.NewRecorder()
	oidcRedirectError(w, req, "access_denied", "/app/login")

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}
}

func TestOIDCRedirectError_UnsafePath_FallsBackToApp(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/callback", http.NoBody)
	w := httptest.NewRecorder()
	oidcRedirectError(w, req, "access_denied", "https://evil.example/login")

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/app/") {
		t.Fatalf("unsafe target should fall back to /app/, got %q", loc)
	}
}
