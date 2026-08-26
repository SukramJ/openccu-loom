// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyRS256RoundTrip(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jwks" {
			n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"k1","alg":"RS256","n":%q,"e":%q}]}`, n, e)
		}
	}))
	defer srv.Close()

	cache := NewJWKSCache(srv.URL+"/jwks", srv.Client())
	jwt := signJWT(t, priv, "k1", map[string]any{"sub": "alice"})

	claims, err := Verify(context.Background(), jwt, cache)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject: %+v", claims)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jwks" {
			n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"k1","alg":"RS256","n":%q,"e":%q}]}`, n, e)
		}
	}))
	defer srv.Close()

	cache := NewJWKSCache(srv.URL+"/jwks", srv.Client())
	jwt := signJWT(t, priv, "k1", map[string]any{"sub": "alice"})

	// Tamper the FIRST character of the signature segment. The last
	// character of a RawURLEncoded RSA-2048 signature (342 chars)
	// represents only 2 real bits + 4 padding bits — Go's base64
	// decoder discards the padding, so swapping it with another char
	// whose high 2 bits happen to match (~25 % chance) is a silent
	// no-op and the signature still verifies. Tampering position 0
	// always flips real signature bytes.
	parts := strings.Split(jwt, ".")
	first := parts[2][0]
	replacement := byte('A')
	if first == 'A' {
		replacement = 'B'
	}
	parts[2] = string(replacement) + parts[2][1:]
	tampered := strings.Join(parts, ".")

	if _, err := Verify(context.Background(), tampered, cache); err == nil {
		t.Fatal("tampered signature must fail")
	}
}

func TestVerifyRejectsUnsupportedAlg(t *testing.T) {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	p := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	tok := h + "." + p + ".sig"

	cache := NewJWKSCache("http://unreachable", http.DefaultClient)
	if _, err := Verify(context.Background(), tok, cache); err == nil {
		t.Fatal("HS256 must be rejected")
	}
}

func TestJWKSCacheUnreachable(t *testing.T) {
	cache := NewJWKSCache("", nil)
	if _, err := cache.Key(context.Background(), "k1"); !errors.Is(err, ErrJWKSUnreachable) {
		t.Fatalf("err=%v", err)
	}
}

// signJWT produces an RS256 JWT with a minimal header + the given
// payload, signed by priv.
func signJWT(t *testing.T, priv *rsa.PrivateKey, kid string, payload map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	pb, _ := json.Marshal(payload)
	header := base64.RawURLEncoding.EncodeToString(hb)
	body := base64.RawURLEncoding.EncodeToString(pb)
	signed := header + "." + body
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}
