// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// newOIDCTestHarness boots a fake IdP and a UI router wired against
// it. The fake issues a fixed code → token mapping so the callback
// handler can complete end-to-end.
func newOIDCTestHarness(t *testing.T) (http.Handler, *httptest.Server) {
	t.Helper()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "abc" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			id := fakeJWT(map[string]any{"sub": "alice", "role": "operator"})
			_, _ = fmt.Fprintf(w, `{"access_token":"at","id_token":%q,"token_type":"Bearer","expires_in":3600}`, id)
		}
	}))
	t.Cleanup(idp.Close)

	client, err := oidc.New(context.Background(), oidc.Config{
		Issuer: idp.URL, ClientID: "openccu-loom", RedirectURL: "http://localhost/cb",
	}, idp.Client())
	if err != nil {
		t.Fatalf("oidc client: %v", err)
	}

	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()

	router := NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Auth:     &AuthDeps{Users: users, Sessions: sessions},
		OIDC:     NewOIDCDeps(client),
	})
	return router, idp
}

func TestOIDCStartRedirectsToIdP(t *testing.T) {
	router, idp := newOIDCTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/start", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), idp.URL+"/auth") {
		t.Fatalf("location=%s", loc)
	}
	if loc.Query().Get("code_challenge") == "" {
		t.Fatalf("no PKCE challenge in URL: %s", loc)
	}
	if loc.Query().Get("state") == "" {
		t.Fatalf("no state: %s", loc)
	}
}

func TestOIDCCallbackIssuesSession(t *testing.T) {
	router, _ := newOIDCTestHarness(t)

	// First start the flow to plant state + verifier.
	startReq := httptest.NewRequest(http.MethodGet, "/login/oidc/start", http.NoBody)
	startRR := httptest.NewRecorder()
	router.ServeHTTP(startRR, startReq)
	redir, _ := url.Parse(startRR.Header().Get("Location"))
	state := redir.Query().Get("state")

	cbReq := httptest.NewRequest(http.MethodGet,
		"/login/oidc/callback?code=abc&state="+state, http.NoBody)
	cbRR := httptest.NewRecorder()
	router.ServeHTTP(cbRR, cbReq)

	if cbRR.Code != http.StatusSeeOther || cbRR.Header().Get("Location") != "/" {
		t.Fatalf("status=%d loc=%s body=%s", cbRR.Code, cbRR.Header().Get("Location"), cbRR.Body.String())
	}
	if !strings.Contains(cbRR.Header().Get("Set-Cookie"), auth.SessionCookieName) {
		t.Fatalf("session cookie missing: %s", cbRR.Header().Get("Set-Cookie"))
	}
}

func TestOIDCCallbackRejectsUnknownState(t *testing.T) {
	router, _ := newOIDCTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback?code=abc&state=nope", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther ||
		!strings.Contains(rr.Header().Get("Location"), "bad_state") {
		t.Fatalf("loc=%s", rr.Header().Get("Location"))
	}
}

func TestOIDCStartWithoutConfigReturns503(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	router := NewRouter(Deps{Lang: "en", Catalogs: cats})
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/start", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestLoginPageShowsOIDCWhenEnabled(t *testing.T) {
	router, _ := newOIDCTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "single sign-on") {
		t.Fatalf("OIDC button missing: %s", rr.Body.String())
	}
}

func fakeJWT(payload map[string]any) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	b, _ := json.Marshal(payload)
	p := base64.RawURLEncoding.EncodeToString(b)
	return strings.Join([]string{h, p, "sig"}, ".")
}

// ---------------------------------------------------------------------------
// oidcStateStore — Put / Consume / TTL expiry
// ---------------------------------------------------------------------------

func TestOIDCStateStore_PutConsume(t *testing.T) {
	t.Parallel()
	store := newOIDCStateStore()
	// Put stores a verifier and Consume returns it.
	key, err := store.Put("my-verifier")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, ok := store.Consume(key)
	if !ok {
		t.Fatal("Consume: expected ok=true")
	}
	if v != "my-verifier" {
		t.Fatalf("Consume: got %q, want my-verifier", v)
	}
	// Second Consume on the same key must fail (already consumed).
	_, ok = store.Consume(key)
	if ok {
		t.Fatal("Consume: expected ok=false on second call (already consumed)")
	}
}

func TestOIDCStateStore_ConsumeMissingKey(t *testing.T) {
	t.Parallel()
	store := newOIDCStateStore()
	_, ok := store.Consume("non-existent-key")
	if ok {
		t.Fatal("Consume with unknown key must return false")
	}
}

func TestOIDCStateStore_ConsumeExpired(t *testing.T) {
	t.Parallel()
	store := newOIDCStateStore()
	// Set TTL to 1ms so the entry expires immediately.
	store.TTL = time.Millisecond

	now := time.Now()
	store.now = func() time.Time { return now }

	key, err := store.Put("expired-verifier")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Advance time past TTL.
	store.now = func() time.Time { return now.Add(time.Second) }

	_, ok := store.Consume(key)
	if ok {
		t.Fatal("Consume: expected false for expired entry")
	}
}

// ---------------------------------------------------------------------------
// handleOIDCStart / handleOIDCCallback without OIDC configured → 503
// ---------------------------------------------------------------------------

func TestOIDCCallbackWithoutConfigReturns503(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{Lang: "en", Catalogs: cats})
	req := httptest.NewRequest("GET", "/login/oidc/callback?code=abc&state=xyz", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// handleOIDCCallback — error in IdP response redirects to exchange_failed
// ---------------------------------------------------------------------------

func TestOIDCCallbackBadCodeRedirectsWithExchangeFailed(t *testing.T) {
	router, _ := newOIDCTestHarness(t)

	// Start a flow to get a real state.
	startReq := httptest.NewRequest(http.MethodGet, "/login/oidc/start", http.NoBody)
	startRR := httptest.NewRecorder()
	router.ServeHTTP(startRR, startReq)
	loc := startRR.Header().Get("Location")

	// Extract state from redirect URL.
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location %q: %v", loc, err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in redirect: %s", loc)
	}

	// Use wrong code "bad" — the fake IdP returns 400 for non-"abc" codes.
	cbReq := httptest.NewRequest(http.MethodGet,
		"/login/oidc/callback?code=bad&state="+state, http.NoBody)
	cbRR := httptest.NewRecorder()
	router.ServeHTTP(cbRR, cbReq)

	if cbRR.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body=%s", cbRR.Code, cbRR.Body.String())
	}
	if !strings.Contains(cbRR.Header().Get("Location"), "exchange_failed") {
		t.Fatalf("expected exchange_failed redirect, got %q", cbRR.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// handleOIDCCallback — bad id_token triggers id_token_invalid redirect
// ---------------------------------------------------------------------------

// newOIDCHarnessWithBadToken builds a fake IdP that returns a malformed
// id_token ("notavalidjwt" — no "." — so DecodeIDToken returns an error).
func newOIDCHarnessWithBadToken(t *testing.T) http.Handler {
	t.Helper()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w,
				`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")
		case "/token":
			// malformed id_token: single segment, DecodeIDToken will fail
			_, _ = fmt.Fprintf(w,
				`{"access_token":"at","id_token":"notavalidjwt","token_type":"Bearer","expires_in":3600}`)
		}
	}))
	t.Cleanup(idp.Close)

	client, err := oidc.New(context.Background(), oidc.Config{
		Issuer: idp.URL, ClientID: "openccu-loom", RedirectURL: "http://localhost/cb",
	}, idp.Client())
	if err != nil {
		t.Fatalf("oidc client (bad token harness): %v", err)
	}

	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()
	return NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Auth:     &AuthDeps{Users: users, Sessions: sessions},
		OIDC:     NewOIDCDeps(client),
	})
}

func TestOIDCCallbackBadIDTokenRedirectsWithInvalidToken(t *testing.T) {
	router := newOIDCHarnessWithBadToken(t)

	// Plant state by going through /login/oidc/start.
	startReq := httptest.NewRequest(http.MethodGet, "/login/oidc/start", http.NoBody)
	startRR := httptest.NewRecorder()
	router.ServeHTTP(startRR, startReq)
	parsed, err := url.Parse(startRR.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in redirect")
	}

	// Use code=abc so the fake IdP returns the bad id_token.
	cbReq := httptest.NewRequest(http.MethodGet,
		"/login/oidc/callback?code=abc&state="+state, http.NoBody)
	cbRR := httptest.NewRecorder()
	router.ServeHTTP(cbRR, cbReq)

	if cbRR.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body=%s", cbRR.Code, cbRR.Body.String())
	}
	// The redirect should carry "id_token_invalid".
	if !strings.Contains(cbRR.Header().Get("Location"), "id_token_invalid") {
		t.Fatalf("expected id_token_invalid in redirect, got %q", cbRR.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// handleOIDCCallback — error query parameter redirects to /login?error=<reason>
// ---------------------------------------------------------------------------

func TestOIDCCallbackErrorParamRedirects(t *testing.T) {
	t.Parallel()
	router, _ := newOIDCTestHarness(t)
	req := httptest.NewRequest(http.MethodGet,
		"/login/oidc/callback?error=access_denied", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Location"), "access_denied") {
		t.Fatalf("expected error in redirect, got %q", rr.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// handleOIDCCallback — missing code or state redirects with missing_code
// ---------------------------------------------------------------------------

func TestOIDCCallbackMissingCodeRedirects(t *testing.T) {
	t.Parallel()
	router, _ := newOIDCTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/login/oidc/callback", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Location"), "missing_code") {
		t.Fatalf("expected missing_code in redirect, got %q", rr.Header().Get("Location"))
	}
}
