// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

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

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TestAuthURLIncludesSuppliedNonce proves the nonce round-trips into
// the authorization request when the caller supplies one — the
// pending-auth session binds it so [Client.VerifyIDToken] can later
// check the ID token echoes the same value (OIDC Core §3.1.2.1).
func TestAuthURLIncludesSuppliedNonce(t *testing.T) {
	c := &Client{
		cfg:       Config{ClientID: "openccu-loom", RedirectURL: "http://localhost/cb", Scopes: []string{"openid"}},
		providers: &Providers{AuthorizationEndpoint: "http://idp.example/auth"},
	}
	pkce, _ := NewPKCEPair()
	u := c.AuthURL("state-xyz", pkce, "nonce-abc")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	if got := parsed.Query().Get("nonce"); got != "nonce-abc" {
		t.Fatalf("nonce = %q, want %q", got, "nonce-abc")
	}
}

// TestNewNonceIsRandomAndNonEmpty guards against a degenerate nonce
// generator that would make the anti-replay check trivially
// bypassable.
func TestNewNonceIsRandomAndNonEmpty(t *testing.T) {
	a, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	b, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("nonce must not be empty")
	}
	if a == b {
		t.Fatal("two consecutive nonces must not collide")
	}
}

func TestPKCEPair(t *testing.T) {
	p, err := NewPKCEPair()
	if err != nil {
		t.Fatalf("pkce: %v", err)
	}
	if len(p.Verifier) < 43 {
		t.Fatalf("short verifier: %s", p.Verifier)
	}
	if p.Method != "S256" {
		t.Fatalf("method=%s", p.Method)
	}
	if len(p.Challenge) < 43 {
		t.Fatalf("challenge: %s", p.Challenge)
	}
}

func TestDiscoverParsesWellKnown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			// The metadata issuer must equal the configured issuer (the
			// httptest host, a loopback URL), and endpoints must be https or
			// loopback — otherwise Discover rejects them.
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"scopes_supported":["openid","email"]}`,
				issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p, err := Discover(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if p.AuthorizationEndpoint != srv.URL+"/auth" {
		t.Fatalf("auth endpoint: %s", p.AuthorizationEndpoint)
	}
}

// TestDiscoverRejectsNonHTTPSIssuer proves a plain-http, non-loopback issuer
// is rejected before any network call is made — a forged or downgraded
// discovery URL must never be dereferenced.
func TestDiscoverRejectsNonHTTPSIssuer(t *testing.T) {
	_, err := Discover(context.Background(), http.DefaultClient, "http://idp.example.com")
	if err == nil {
		t.Fatal("expected error for non-https, non-loopback issuer")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error should mention https, got %v", err)
	}
}

// TestDiscoverRejectsIssuerMismatch proves the metadata's own "issuer" must
// equal the configured issuer (RFC 8414 §3.3) — otherwise a compromised or
// misconfigured endpoint could redirect token/JWKS traffic elsewhere.
func TestDiscoverRejectsIssuerMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":"https://evil.example","authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				issuer+"/auth", issuer+"/token", issuer+"/jwks")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := Discover(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for issuer mismatch")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error should mention issuer mismatch, got %v", err)
	}
}

// TestDiscoverRejectsNonHTTPSEndpoint proves that even with a matching
// issuer, an advertised endpoint that is neither https nor loopback is
// rejected — every hop of the flow (auth, token, JWKS) must stay off
// cleartext outside local development.
func TestDiscoverRejectsNonHTTPSEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":"http://plain.example/token","jwks_uri":%q}`,
				issuer, issuer+"/auth", issuer+"/jwks")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := Discover(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for non-https, non-loopback token endpoint")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error should mention https, got %v", err)
	}
}

func TestClientAuthURLAndExchange(t *testing.T) {
	var tokenCalled int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			issuer := "http://" + r.Host
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				issuer, issuer+"/auth", issuer+"/token", issuer+"/jwks")
		case "/token":
			tokenCalled++
			_ = r.ParseForm()
			if r.Form.Get("code") != "abc" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			idToken := fakeIDToken(map[string]any{"sub": "alice", "role": "operator"})
			_, _ = fmt.Fprintf(w, `{"access_token":"at","id_token":%q,"token_type":"Bearer","expires_in":3600}`, idToken)
		}
	}))
	defer srv.Close()

	c, err := New(context.Background(), Config{
		Issuer:      srv.URL,
		ClientID:    "openccu-loom",
		RedirectURL: "http://localhost/cb",
	}, srv.Client())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	pkce, _ := NewPKCEPair()
	u := c.AuthURL("state-xyz", pkce)
	parsed, _ := url.Parse(u)
	if parsed.Query().Get("client_id") != "openccu-loom" || parsed.Query().Get("state") != "state-xyz" {
		t.Fatalf("auth URL: %s", u)
	}
	if parsed.Query().Get("nonce") != "" {
		t.Fatalf("auth URL must omit nonce when none was supplied: %s", u)
	}

	tok, err := c.Exchange(context.Background(), "abc", pkce.Verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("access token: %s", tok.AccessToken)
	}
	// The ID-token contents (signature + claims) are covered by the
	// VerifyIDToken tests; here we only assert the role/subject mapping.
	id := c.IdentityFrom(&IDClaims{Subject: "alice", Role: "operator"})
	if id.Subject != "alice" || id.Role != auth.RoleOperator {
		t.Fatalf("identity: %+v", id)
	}
	if tokenCalled != 1 {
		t.Fatalf("token endpoint hits=%d", tokenCalled)
	}
}

// fakeIDToken builds a JWT with the given payload and an unsigned
// signature segment — enough to populate the token-endpoint response
// in tests that do not exercise verification.
func fakeIDToken(payload map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	b, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(b)
	return strings.Join([]string{header, body, "sig"}, ".")
}
