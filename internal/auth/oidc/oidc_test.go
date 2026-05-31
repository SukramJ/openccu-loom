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
			_, _ = fmt.Fprintf(w, `{"issuer":"%s","authorization_endpoint":"%s/auth","token_endpoint":"%s/token","jwks_uri":"%s/jwks","scopes_supported":["openid","email"]}`,
				"https://example.com", "https://example.com", "https://example.com", "https://example.com")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p, err := Discover(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if p.AuthorizationEndpoint != "https://example.com/auth" {
		t.Fatalf("auth endpoint: %s", p.AuthorizationEndpoint)
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

	tok, err := c.Exchange(context.Background(), "abc", pkce.Verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("access token: %s", tok.AccessToken)
	}
	claims, err := DecodeIDToken(tok.IDToken)
	if err != nil {
		t.Fatalf("id token decode: %v", err)
	}
	id := c.IdentityFrom(claims)
	if id.Subject != "alice" || id.Role != auth.RoleOperator {
		t.Fatalf("identity: %+v", id)
	}
	if tokenCalled != 1 {
		t.Fatalf("token endpoint hits=%d", tokenCalled)
	}
}

func TestDecodeIDTokenRejectsMalformed(t *testing.T) {
	if _, err := DecodeIDToken("not-a-jwt"); err == nil {
		t.Fatal("expected error")
	}
}

// fakeIDToken builds a JWT with the given payload, an unsigned
// signature segment. Sufficient for our non-verifying MVP decoder.
func fakeIDToken(payload map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	b, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(b)
	return strings.Join([]string{header, body, "sig"}, ".")
}
