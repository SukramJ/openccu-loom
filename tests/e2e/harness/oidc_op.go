// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// MockOP is the harness's in-process OIDC OpenID Provider used by
// the AuthOIDC test mode. It serves the four endpoints the daemon's
// OIDC client needs (discovery, JWKS, authorize, token), signs ID
// tokens with an in-memory RS256 keypair, and exposes IssueAuthCode
// so the auth-flow test can skip the user-agent redirect dance.
//
// The daemon verifies the ID-token RS256 signature against this
// mock's JWKS (see internal/auth/oidc/client.go::VerifyIDToken), so
// the mock signs with a real in-memory keypair and serves the
// matching JWKS at /jwks.
type MockOP interface {
	IssuerURL() string
	// IssueAuthCode mints an auth code. nonce is the value from the
	// daemon's authorize redirect; a real OP MUST echo it into the ID
	// token's nonce claim (OIDC Core §3.1.3.7 step 11), and the daemon
	// rejects tokens where it is missing or mismatching. Pass "" only
	// to simulate a non-conforming OP.
	IssueAuthCode(sub, role, nonce string) string
	Stop() error
}

// startMockOP spins up the mock OP on a free loopback port and
// registers a t.Cleanup that stops it.
func startMockOP(t *testing.T) MockOP {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("oidc-op: generate key: %v", err)
	}

	op := &mockOP{
		priv:  priv,
		kid:   "harness-key-1",
		codes: map[string]codeEntry{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", op.handleDiscovery)
	mux.HandleFunc("/jwks", op.handleJWKS)
	mux.HandleFunc("/authorize", op.handleAuthorize)
	mux.HandleFunc("/token", op.handleToken)

	// httptest.NewServer is hermetic, picks a free port, and gives
	// us the URL — exactly what we need.
	op.srv = httptest.NewServer(mux)
	op.issuer = op.srv.URL

	t.Cleanup(func() { _ = op.Stop() })
	return op
}

type codeEntry struct {
	subject string
	role    string
	nonce   string
	created time.Time
}

type mockOP struct {
	srv    *httptest.Server
	issuer string

	priv *rsa.PrivateKey
	kid  string

	mu    sync.Mutex
	codes map[string]codeEntry
}

func (o *mockOP) IssuerURL() string { return o.issuer }

func (o *mockOP) Stop() error {
	if o == nil || o.srv == nil {
		return nil
	}
	o.srv.Close()
	return nil
}

// IssueAuthCode mints a short-lived authorization code that the
// /token endpoint accepts. Tests use it to bypass the browser
// redirect: they receive `code=<this>` and POST it to the daemon's
// callback handler, which exchanges it via the mock /token endpoint.
//
// The role parameter populates the "role" claim of the issued
// ID token; pass "admin" / "operator" / "" (viewer fallback).
// nonce is echoed into the ID token's nonce claim when non-empty,
// mirroring a conforming OP.
func (o *mockOP) IssueAuthCode(sub, role, nonce string) string {
	if sub == "" {
		sub = "harness-user"
	}
	codeBytes := make([]byte, 24)
	if _, err := rand.Read(codeBytes); err != nil {
		panic(err) // test-only path
	}
	code := base64.RawURLEncoding.EncodeToString(codeBytes)
	o.mu.Lock()
	o.codes[code] = codeEntry{subject: sub, role: role, nonce: nonce, created: time.Now()}
	o.mu.Unlock()
	return code
}

// ─── HTTP handlers ───────────────────────────────────────────────

func (o *mockOP) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                o.issuer,
		"authorization_endpoint":                o.issuer + "/authorize",
		"token_endpoint":                        o.issuer + "/token",
		"jwks_uri":                              o.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	writeJSON(w, http.StatusOK, doc)
}

func (o *mockOP) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := o.priv.PublicKey
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": o.kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}
	writeJSON(w, http.StatusOK, jwks)
}

// handleAuthorize accepts a browser-style request, mints a code, and
// 302-redirects back to redirect_uri with code+state. The auth-flow
// test does NOT walk this path — it calls IssueAuthCode directly —
// but the endpoint is wired so a future browser-based test can use
// it without changes.
func (o *mockOP) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	state := q.Get("state")
	if redirect == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	code := o.IssueAuthCode("harness-user", "admin", q.Get("nonce"))
	target := redirect + "?code=" + code + "&state=" + state
	http.Redirect(w, r, target, http.StatusFound)
}

func (o *mockOP) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	o.mu.Lock()
	entry, ok := o.codes[code]
	if ok {
		delete(o.codes, code) // one-shot
	}
	o.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
	if time.Since(entry.created) > 5*time.Minute {
		http.Error(w, "expired code", http.StatusBadRequest)
		return
	}

	now := time.Now().Unix()
	claims := map[string]any{
		"iss":   o.issuer,
		"sub":   entry.subject,
		"aud":   r.PostForm.Get("client_id"),
		"iat":   now,
		"exp":   now + 600,
		"name":  entry.subject,
		"email": entry.subject + "@harness.local",
		"role":  entry.role,
	}
	// A conforming OP echoes the authorize request's nonce into the ID
	// token (OIDC Core §3.1.3.7 step 11); the daemon rejects the token
	// otherwise.
	if entry.nonce != "" {
		claims["nonce"] = entry.nonce
	}
	idToken := o.signJWT(claims)

	resp := map[string]any{
		"access_token": "harness-access-" + code,
		"id_token":     idToken,
		"token_type":   "Bearer",
		"expires_in":   600,
		"scope":        "openid profile email",
	}
	writeJSON(w, http.StatusOK, resp)
}

// signJWT produces an RS256 JWT with the in-memory keypair.
func (o *mockOP) signJWT(claims map[string]any) string {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": o.kid}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, o.priv, crypto.SHA256, sum[:])
	if err != nil {
		// SignPKCS1v15 only fails on a malformed key — we generated
		// the key ourselves, so this is a programming error if it
		// ever happens.
		panic(fmt.Sprintf("oidc-op: sign: %v", err))
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// writeJSON serialises body with Content-Type: application/json.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Compile-time check that httptest is wired correctly. Without this
// the import would be flagged when the file is the only consumer.
var _ = (*net.TCPAddr)(nil)
