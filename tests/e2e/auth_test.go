// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// The auth-quartet exercises every authentication backend the daemon
// supports in v1.0:
//
//   - HTTP Basic
//   - Form-based session login (issues `openccu_loom_session` cookie)
//   - API token (Authorization: Bearer)
//   - OpenID Connect authorization code with PKCE — against the
//     in-process MockOP wired by the harness.
//
// Each test brings up its own daemon with the matching auth mode,
// drives one full happy-path flow, and asserts that /api/v1/auth/me
// reports a valid Identity. The negative path ("no credentials → 401")
// is covered by the OpenAPI walker via the documented 401 response.

// ─────────────────────────────────────────────────────────────────
// Basic
// ─────────────────────────────────────────────────────────────────

func TestAuthBasic(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthBasic})

	// /auth/me is mounted on the REST API; sending Basic credentials
	// should return 200 with an Identity that names our admin user.
	credential := harness.AdminUser + ":" + harness.AdminPass
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(credential))

	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", authHeader)
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/me with Basic: %v", err)
	}
	defer resp.Body.Close()
	assertIdentity(t, resp, "basic")

	// And without credentials we MUST be rejected.
	reqAnon, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	respAnon, err := h.REST().Do(reqAnon)
	if err != nil {
		t.Fatalf("GET /auth/me anon: %v", err)
	}
	defer respAnon.Body.Close()
	if respAnon.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(respAnon.Body)
		t.Fatalf("anon /auth/me: status=%d body=%s, want 401", respAnon.StatusCode, body)
	}
}

// ─────────────────────────────────────────────────────────────────
// Session
// ─────────────────────────────────────────────────────────────────

func TestAuthSession(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})

	rest := h.REST()
	if err := rest.LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// The cookie jar carries `openccu_loom_session` automatically.
	req, _ := rest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp, err := rest.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/me with session cookie: %v", err)
	}
	defer resp.Body.Close()
	assertIdentity(t, resp, "session")

	// Verify the session cookie is actually present in the jar.
	u, _ := url.Parse(rest.Base())
	var seen bool
	for _, ck := range rest.HTTPClient().Jar.Cookies(u) {
		if ck.Name == "openccu_loom_session" && ck.Value != "" {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("expected openccu_loom_session cookie after login, got none")
	}
}

// ─────────────────────────────────────────────────────────────────
// API Token
// ─────────────────────────────────────────────────────────────────

func TestAuthToken(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthToken})

	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+harness.AdminToken)
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/me with bearer: %v", err)
	}
	defer resp.Body.Close()
	assertIdentity(t, resp, "bearer")

	// A bogus token must be rejected with 401.
	bogus, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	bogus.Header.Set("Authorization", "Bearer not-a-real-token")
	respBogus, err := h.REST().Do(bogus)
	if err != nil {
		t.Fatalf("GET /auth/me with bogus bearer: %v", err)
	}
	defer respBogus.Body.Close()
	if respBogus.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus bearer: status=%d, want 401", respBogus.StatusCode)
	}
}

// ─────────────────────────────────────────────────────────────────
// OIDC (against MockOP)
// ─────────────────────────────────────────────────────────────────

func TestAuthOIDC(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthOIDC})
	if h.OP() == nil {
		t.Fatalf("MockOP not started — AuthOIDC option not honoured by harness")
	}

	rest := h.REST()
	// Disable redirect-following on the cookie-jar-bearing client so
	// we can inspect the daemon's 303 → /authorize hand-off.
	hc := rest.HTTPClient()
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { hc.CheckRedirect = prev }()

	// Step 1: GET /api/v1/auth/oidc/start. The daemon mints a state,
	// stores the verifier, and redirects to MockOP's /authorize.
	startReq, _ := rest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil)
	startResp, err := rest.Do(startReq)
	if err != nil {
		t.Fatalf("GET /oidc/start: %v", err)
	}
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("/oidc/start: status=%d, want 303", startResp.StatusCode)
	}
	loc := startResp.Header.Get("Location")
	if !strings.HasPrefix(loc, h.OP().IssuerURL()+"/authorize") {
		t.Fatalf("/oidc/start: Location=%q, want %s/authorize prefix", loc, h.OP().IssuerURL())
	}
	authorizeURL, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" {
		t.Fatalf("/oidc/start: redirect carries no `state`")
	}

	// Step 2: skip the user-agent step. Mint an auth code directly
	// from MockOP — the test acts as the IdP-side redirect would.
	code := h.OP().IssueAuthCode("e2e-admin", "admin")

	// Step 3: GET /api/v1/auth/oidc/callback?code=...&state=...
	// The daemon exchanges the code (calling MockOP /token), parses
	// the ID token, issues a session cookie, and redirects to /app/.
	cb := "/api/v1/auth/oidc/callback?code=" + url.QueryEscape(code) +
		"&state=" + url.QueryEscape(state)
	cbReq, _ := rest.NewRequest(http.MethodGet, cb, nil)
	cbResp, err := rest.Do(cbReq)
	if err != nil {
		t.Fatalf("GET /oidc/callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(cbResp.Body)
		t.Fatalf("/oidc/callback: status=%d body=%s, want 303", cbResp.StatusCode, body)
	}

	// The daemon MUST have set `openccu_loom_session` on the
	// callback response. Verify by inspecting Set-Cookie directly —
	// the cookie jar would also receive it.
	var session string
	for _, ck := range cbResp.Cookies() {
		if ck.Name == "openccu_loom_session" && ck.Value != "" {
			session = ck.Value
			break
		}
	}
	if session == "" {
		t.Fatalf("/oidc/callback: no session cookie set")
	}

	// Step 4: re-enable redirect-follow and check /auth/me with the
	// session cookie issued by the OIDC flow.
	hc.CheckRedirect = prev
	meReq, _ := rest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meResp, err := rest.Do(meReq)
	if err != nil {
		t.Fatalf("GET /auth/me after OIDC: %v", err)
	}
	defer meResp.Body.Close()
	assertIdentity(t, meResp, "")
}

// ─────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────

// assertIdentity asserts that resp returned 200 with a JSON envelope
// matching the Identity schema (subject + role). When wantScheme is
// non-empty the scheme field is also checked.
func assertIdentity(t *testing.T, resp *http.Response, wantScheme string) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/auth/me: status=%d body=%s, want 200", resp.StatusCode, body)
	}
	var ident struct {
		Subject string `json:"subject"`
		Role    string `json:"role"`
		Scheme  string `json:"scheme"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ident); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	if ident.Subject == "" {
		t.Errorf("/auth/me: empty subject in %+v", ident)
	}
	if ident.Role == "" {
		t.Errorf("/auth/me: empty role in %+v", ident)
	}
	if wantScheme != "" && ident.Scheme != wantScheme {
		t.Errorf("/auth/me: scheme=%q, want %q", ident.Scheme, wantScheme)
	}
}
