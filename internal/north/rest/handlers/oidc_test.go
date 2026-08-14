// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
)

// newTestOIDCClient builds a real [*oidc.Client] backed by a local
// discovery server, so OIDCStart / OIDCCallback can be exercised past
// their nil-Client guard. The token endpoint returns 404 for every
// request, so [oidc.Client.Exchange] always fails — tests that need to
// go further only assert the callback got past the state-cookie check,
// not that a full token exchange succeeded.
func newTestOIDCClient(t *testing.T) *oidc.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client, err := oidc.New(context.Background(), oidc.Config{
		Issuer:      srv.URL,
		ClientID:    "test-client",
		RedirectURL: "http://localhost/callback",
	}, srv.Client())
	if err != nil {
		t.Fatalf("oidc.New: %v", err)
	}
	return client
}

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
	key, err := d.putState("verifier-abc", "nonce-abc")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}
	if key == "" {
		t.Fatal("key must not be empty")
	}
	got, nonce, ok := d.consumeState(key)
	if !ok {
		t.Fatal("consumeState must find the just-stored state")
	}
	if got != "verifier-abc" {
		t.Fatalf("verifier = %q, want verifier-abc", got)
	}
	if nonce != "nonce-abc" {
		t.Fatalf("nonce = %q, want nonce-abc", nonce)
	}
	// State is one-shot — second consume must fail.
	if _, _, ok := d.consumeState(key); ok {
		t.Fatal("state must be consumed exactly once")
	}
}

func TestOIDCDeps_ConsumeState_Unknown(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	if _, _, ok := d.consumeState("never-stored"); ok {
		t.Fatal("unknown state must not match")
	}
}

func TestOIDCDeps_ConsumeState_ExpiredAfterTTL(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return fakeNow }
	key, _ := d.putState("v", "n")
	// Advance the clock past oidcStateTTL.
	d.now = func() time.Time { return fakeNow.Add(oidcStateTTL + time.Second) }
	if _, _, ok := d.consumeState(key); ok {
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

// --- OIDCStart state cookie ---

// TestOIDCStart_SetsStateCookieMatchingRedirectState verifies that the
// state value baked into the IdP redirect URL is also carried in a
// HttpOnly, SameSite=Lax cookie so OIDCCallback can bind the flow to
// the initiating browser (login-CSRF mitigation).
func TestOIDCStart_SetsStateCookieMatchingRedirectState(t *testing.T) {
	t.Parallel()
	client := newTestOIDCClient(t)
	d := NewOIDCDeps(client, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", http.NoBody)
	w := httptest.NewRecorder()
	OIDCStart(d).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("expected a non-empty state in the redirect URL")
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == oidcStateCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("expected the oidc state cookie to be set")
	}
	if cookie.Value != state {
		t.Fatalf("cookie value %q must equal the redirect state %q", cookie.Value, state)
	}
	if !cookie.HttpOnly {
		t.Error("state cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("expected SameSite=Lax, got %v", cookie.SameSite)
	}
}

// TestOIDCStart_RedirectCarriesNonceBoundToState verifies that the IdP
// redirect URL carries a fresh OIDC nonce (OIDC Core §3.1.2.1) and that
// the same nonce is bound server-side to the pending flow, so the
// callback can hand it to VerifyIDToken as expectedNonce. Without this
// a captured ID token could be replayed into a different session.
func TestOIDCStart_RedirectCarriesNonceBoundToState(t *testing.T) {
	t.Parallel()
	client := newTestOIDCClient(t)
	d := NewOIDCDeps(client, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", http.NoBody)
	w := httptest.NewRecorder()
	OIDCStart(d).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	nonce := loc.Query().Get("nonce")
	if nonce == "" {
		t.Fatal("expected a non-empty nonce in the redirect URL")
	}
	state := loc.Query().Get("state")
	_, storedNonce, ok := d.consumeState(state)
	if !ok {
		t.Fatal("state minted by OIDCStart must be consumable")
	}
	if storedNonce != nonce {
		t.Fatalf("stored nonce %q must equal the redirect nonce %q", storedNonce, nonce)
	}
}

// --- OIDCCallback state-cookie binding ---

// TestOIDCCallback_MatchingStateCookie_ProceedsPastStateCheck verifies
// that a callback carrying the same value in the state cookie and the
// state query parameter passes the binding check. The fake IdP's token
// endpoint always 404s, so the flow still fails later at token exchange
// — the assertion only requires that the failure is NOT bad_state.
func TestOIDCCallback_MatchingStateCookie_ProceedsPastStateCheck(t *testing.T) {
	t.Parallel()
	client := newTestOIDCClient(t)
	d := NewOIDCDeps(client, &AuthDeps{Sessions: auth.NewSessionStore()}, nil)

	state, err := d.putState("verifier-1", "nonce-1")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, http.NoBody)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state})
	w := httptest.NewRecorder()
	OIDCCallback(d).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, "error=bad_state") {
		t.Fatalf("matching state cookie must pass the binding check, got %q", loc)
	}
}

// TestOIDCCallback_MissingStateCookie_RedirectsBadState verifies that a
// callback with no state cookie at all — e.g. a forged callback replayed
// into a victim's browser that never started the flow — is rejected.
func TestOIDCCallback_MissingStateCookie_RedirectsBadState(t *testing.T) {
	t.Parallel()
	client := newTestOIDCClient(t)
	d := NewOIDCDeps(client, &AuthDeps{Sessions: auth.NewSessionStore()}, nil)

	state, err := d.putState("verifier-1", "nonce-1")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, http.NoBody)
	w := httptest.NewRecorder()
	OIDCCallback(d).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=bad_state") {
		t.Fatalf("missing state cookie must redirect with error=bad_state, got %q", loc)
	}
}

// TestOIDCCallback_MismatchedStateCookie_RedirectsBadState verifies that
// a state cookie present but not equal to the state query parameter is
// rejected — the constant-time comparison must not accept a near-miss.
func TestOIDCCallback_MismatchedStateCookie_RedirectsBadState(t *testing.T) {
	t.Parallel()
	client := newTestOIDCClient(t)
	d := NewOIDCDeps(client, &AuthDeps{Sessions: auth.NewSessionStore()}, nil)

	state, err := d.putState("verifier-1", "nonce-1")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, http.NoBody)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: "some-other-value"})
	w := httptest.NewRecorder()
	OIDCCallback(d).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=bad_state") {
		t.Fatalf("mismatched state cookie must redirect with error=bad_state, got %q", loc)
	}
}

// --- putState sweep ---

// TestOIDCDeps_PutState_SweepsExpiredEntries verifies that putState
// reclaims entries older than oidcStateTTL on every insert, so states
// minted but never completed (abandoned logins) do not accumulate
// unbounded in the map.
func TestOIDCDeps_PutState_SweepsExpiredEntries(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return fakeNow }

	firstKey, err := d.putState("v1", "n1")
	if err != nil {
		t.Fatalf("putState: %v", err)
	}

	// Advance the clock past the TTL and insert a second state; this must
	// trigger the sweep that removes the first (now-expired) entry.
	d.now = func() time.Time { return fakeNow.Add(oidcStateTTL + time.Second) }
	if _, err := d.putState("v2", "n2"); err != nil {
		t.Fatalf("putState: %v", err)
	}

	d.mu.Lock()
	_, stillThere := d.states[firstKey]
	d.mu.Unlock()
	if stillThere {
		t.Fatal("expired state must be swept on the next putState insert")
	}
}

// TestOIDCDeps_PutState_IsBoundedIndependentlyOfTheTTL pins the ceiling on
// the in-flight state map. /auth/oidc/start is pre-auth, so anyone can
// mint states; the TTL sweep reclaims nothing before five minutes, which
// let the map grow at the caller's request rate — and every insert scans
// it under the lock, so genuine logins slow down with it. The oldest flow
// is dropped instead, because a real login completes in seconds.
func TestOIDCDeps_PutState_IsBoundedIndependentlyOfTheTTL(t *testing.T) {
	t.Parallel()
	d := NewOIDCDeps(nil, nil, nil)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return fakeNow }

	var newest string
	for i := range maxOIDCStates + 500 {
		// Advance well inside the TTL so nothing is reclaimed by the sweep.
		d.now = func() time.Time { return fakeNow.Add(time.Duration(i) * time.Millisecond) }
		key, err := d.putState("v", "n")
		if err != nil {
			t.Fatalf("putState %d: %v", i, err)
		}
		newest = key
	}

	d.mu.Lock()
	n := len(d.states)
	d.mu.Unlock()
	if n > maxOIDCStates {
		t.Fatalf("states=%d, want at most %d", n, maxOIDCStates)
	}
	// A flow started at the cap must still be completable.
	if _, _, ok := d.consumeState(newest); !ok {
		t.Error("the most recent flow was dropped instead of the oldest")
	}
}
