// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newVerifyClient boots a fake IdP (discovery + JWKS) and returns a
// Client wired against it, plus the signing key and key id the IdP
// publishes. The returned issuer is what VerifyIDToken pins against.
func newVerifyClient(t *testing.T) (client *Client, priv *rsa.PrivateKey, kid, issuer string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	kid = "vk1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iss := "http://" + r.Host
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
				iss, iss+"/auth", iss+"/token", iss+"/jwks")
		case "/jwks":
			n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","n":%q,"e":%q}]}`, kid, n, e)
		}
	}))
	t.Cleanup(srv.Close)

	client, err = New(context.Background(), Config{
		Issuer: srv.URL, ClientID: "openccu-loom", RedirectURL: "http://localhost/cb",
	}, srv.Client())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client, priv, kid, client.providers.Issuer
}

func TestVerifyIDTokenHappyPath(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom",
		"iat": now, "exp": now + 3600, "role": "operator",
	})
	claims, err := client.VerifyIDToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject = %q", claims.Subject)
	}
}

func TestVerifyIDTokenAudienceArray(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	// A multi-audience ID token must carry azp = this client (OIDC Core
	// §3.1.3.7); a real Keycloak token always does.
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": []string{"other", "openccu-loom"},
		"azp": "openccu-loom", "exp": now + 3600,
	})
	if _, err := client.VerifyIDToken(context.Background(), tok); err != nil {
		t.Fatalf("audience array must be accepted: %v", err)
	}
}

// TestVerifyIDTokenRejectsAzpMismatch proves that an "azp" claim present but
// naming a different client is rejected even when aud already includes this
// client — azp pins which party the token was actually issued to (OIDC Core
// §3.1.3.7).
func TestVerifyIDTokenRejectsAzpMismatch(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": []string{"openccu-loom"},
		"azp": "someoneelse", "exp": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "azp") {
		t.Fatalf("expected azp mismatch rejection, got %v", err)
	}
}

// TestVerifyIDTokenAcceptsSingleAudienceWithoutAzp proves azp is optional
// when the token carries a single audience that already names this client.
func TestVerifyIDTokenAcceptsSingleAudienceWithoutAzp(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": []string{"openccu-loom"}, "exp": now + 3600,
	})
	if _, err := client.VerifyIDToken(context.Background(), tok); err != nil {
		t.Fatalf("single-audience token without azp must be accepted: %v", err)
	}
}

// TestVerifyIDTokenRejectsMultiAudienceWithoutAzp proves a multi-audience
// token that omits azp is rejected — without azp there is no way to tell
// which party the token was actually minted for.
func TestVerifyIDTokenRejectsMultiAudienceWithoutAzp(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": []string{"other", "openccu-loom"}, "exp": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "azp") {
		t.Fatalf("expected multi-audience-without-azp rejection, got %v", err)
	}
}

// TestVerifyIDTokenAcceptsMultiAudienceWithAzp proves a multi-audience token
// is accepted once azp names this client, matching the existing
// TestVerifyIDTokenAudienceArray happy path.
func TestVerifyIDTokenAcceptsMultiAudienceWithAzp(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": []string{"other", "openccu-loom"},
		"azp": "openccu-loom", "exp": now + 3600,
	})
	if _, err := client.VerifyIDToken(context.Background(), tok); err != nil {
		t.Fatalf("multi-audience token with matching azp must be accepted: %v", err)
	}
}

func TestVerifyIDTokenRejectsExpired(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom", "exp": now - 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired rejection, got %v", err)
	}
}

func TestVerifyIDTokenRejectsMissingExp(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom",
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "exp") {
		t.Fatalf("expected missing-exp rejection, got %v", err)
	}
}

// TestVerifyIDTokenRejectsNotYetValid covers an ID token whose "nbf" claim
// is still in the future beyond the clock-skew leeway — OIDC Core §3.1.3.7
// forbids accepting a token before its not-before time.
func TestVerifyIDTokenRejectsNotYetValid(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom",
		"exp": now + 3600, "nbf": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "not yet valid") {
		t.Fatalf("expected not-yet-valid rejection, got %v", err)
	}
}

func TestVerifyIDTokenRejectsWrongAudience(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "another-client", "exp": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("expected audience rejection, got %v", err)
	}
}

func TestVerifyIDTokenRejectsWrongIssuer(t *testing.T) {
	client, priv, kid, _ := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": "http://evil.example", "sub": "alice", "aud": "openccu-loom", "exp": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok)
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("expected issuer rejection, got %v", err)
	}
}

// TestVerifyIDTokenRejectsForeignSignature proves the signature is
// actually checked end-to-end: a token signed by a key the IdP does
// not publish must be rejected even though every claim is valid.
func TestVerifyIDTokenRejectsForeignSignature(t *testing.T) {
	client, _, kid, issuer := newVerifyClient(t)
	foreign, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	now := time.Now().Unix()
	tok := signJWT(t, foreign, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom", "exp": now + 3600,
	})
	if _, err := client.VerifyIDToken(context.Background(), tok); err == nil {
		t.Fatal("token signed by a foreign key must be rejected")
	}
}

// TestVerifyIDTokenAcceptsMatchingNonce proves that supplying the
// expected nonce accepts a token whose "nonce" claim matches it
// (OIDC Core §3.1.2.1 / §3.1.3.7 step 11).
func TestVerifyIDTokenAcceptsMatchingNonce(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom", "exp": now + 3600,
		"nonce": "expected-nonce",
	})
	if _, err := client.VerifyIDToken(context.Background(), tok, "expected-nonce"); err != nil {
		t.Fatalf("matching nonce must be accepted: %v", err)
	}
}

// TestVerifyIDTokenRejectsNonceMismatch covers a captured ID token
// replayed into a session that expects a different nonce: the IdP
// returned a nonce claim, but it does not equal the value the caller
// bound to its pending-auth session.
func TestVerifyIDTokenRejectsNonceMismatch(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom", "exp": now + 3600,
		"nonce": "attacker-nonce",
	})
	_, err := client.VerifyIDToken(context.Background(), tok, "expected-nonce")
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected nonce mismatch rejection, got %v", err)
	}
}

// TestVerifyIDTokenRejectsMissingNonceClaim covers an IdP that omits
// the nonce claim entirely even though the authentication request
// carried one — OIDC Core §3.1.3.7 step 11 requires the claim to be
// present and to match when a nonce was sent.
func TestVerifyIDTokenRejectsMissingNonceClaim(t *testing.T) {
	client, priv, kid, issuer := newVerifyClient(t)
	now := time.Now().Unix()
	tok := signJWT(t, priv, kid, map[string]any{
		"iss": issuer, "sub": "alice", "aud": "openccu-loom", "exp": now + 3600,
	})
	_, err := client.VerifyIDToken(context.Background(), tok, "expected-nonce")
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected missing-nonce rejection, got %v", err)
	}
}
