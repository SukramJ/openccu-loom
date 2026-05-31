// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JWKS is the JSON Web Key Set the IdP publishes. Only the fields
// we actually consume are modelled; unknown fields survive round-
// trip thanks to the encoding/json default behaviour.
type JWKS struct {
	Keys []JSONWebKey `json:"keys"`
}

// JSONWebKey is one entry in the set.
type JSONWebKey struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Kid string `json:"kid"`
	Alg string `json:"alg,omitempty"`

	// RSA keys.
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
}

// JWKSCache holds a locally-cached JWKS with a freshness TTL. The
// cache refreshes lazily on every lookup whose key-id is missing —
// this handles key-rollover without explicit invalidation.
type JWKSCache struct {
	URL    string
	TTL    time.Duration
	Client *http.Client

	mu         sync.RWMutex
	keys       map[string]JSONWebKey
	lastLoaded time.Time
	now        func() time.Time
}

// NewJWKSCache constructs a cache. An empty URL disables the
// verifier — callers use this when the OIDC provider's JWKS is not
// reachable (e.g. tests against unsigned fixtures).
//
// loom:reachable:reason="used by OIDC auth middleware to verify JWT signatures against the provider's keyset"
func NewJWKSCache(url string, client *http.Client) *JWKSCache {
	if client == nil {
		client = http.DefaultClient
	}
	return &JWKSCache{URL: url, TTL: 15 * time.Minute, Client: client, now: time.Now, keys: make(map[string]JSONWebKey)}
}

// ErrJWKSUnreachable is returned when the cache could not reach the
// configured JWKS endpoint.
var ErrJWKSUnreachable = errors.New("oidc: JWKS unreachable")

// Key returns the key with the given id; refreshing the JWKS when
// the id is unknown. Caller-supplied context aborts slow fetches.
func (c *JWKSCache) Key(ctx context.Context, kid string) (JSONWebKey, error) {
	c.mu.RLock()
	k, ok := c.keys[kid]
	fresh := ok && c.now().Sub(c.lastLoaded) <= c.TTL
	c.mu.RUnlock()
	if ok && fresh {
		return k, nil
	}
	if err := c.refresh(ctx); err != nil {
		return JSONWebKey{}, err
	}
	c.mu.RLock()
	k, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return JSONWebKey{}, fmt.Errorf("oidc: key id %q not in JWKS", kid)
	}
	return k, nil
}

func (c *JWKSCache) refresh(ctx context.Context) error {
	if c.URL == "" {
		return ErrJWKSUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrJWKSUnreachable, err)
	}
	defer resp.Body.Close() //nolint:errcheck // JWKS read-only
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oidc.JWKS: status=%d body=%s", resp.StatusCode, body)
	}
	var out JWKS
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("oidc.JWKS: decode: %w", err)
	}
	c.mu.Lock()
	c.keys = make(map[string]JSONWebKey, len(out.Keys))
	for _, k := range out.Keys {
		c.keys[k.Kid] = k
	}
	c.lastLoaded = c.now()
	c.mu.Unlock()
	return nil
}

// Verify checks the signature of a compact-serialised JWT. Only
// RS256 is supported — the dominant algorithm across the
// Keycloak / Authelia / Okta stack. Callers asserting other
// algorithms should inspect the header first and route accordingly.
func Verify(ctx context.Context, jwt string, cache *JWKSCache) (*IDClaims, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc.Verify: malformed token")
	}
	header, err := decodeHeader(parts[0])
	if err != nil {
		return nil, err
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("oidc.Verify: unsupported alg %q", header.Alg)
	}
	if cache == nil {
		return nil, errors.New("oidc.Verify: no JWKS cache")
	}
	jwk, err := cache.Key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	pub, err := parseRSAPub(jwk)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc.Verify: decode signature: %w", err)
	}
	signed := parts[0] + "." + parts[1]
	sum := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, fmt.Errorf("oidc.Verify: signature: %w", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims IDClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

// jwtHeader is the subset of the JOSE header we consume.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ,omitempty"`
}

func decodeHeader(seg string) (*jwtHeader, error) {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return nil, fmt.Errorf("oidc.Verify: decode header: %w", err)
	}
	var h jwtHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func parseRSAPub(k JSONWebKey) (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("oidc.Verify: unsupported kty %q", k.Kty)
	}
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("oidc.Verify: decode N: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("oidc.Verify: decode E: %w", err)
	}
	if len(n) == 0 || len(e) == 0 {
		return nil, errors.New("oidc.Verify: empty RSA parameters")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: rsaExp(e),
	}, nil
}

func rsaExp(b []byte) int {
	v := 0
	for _, c := range b {
		v = v<<8 | int(c)
	}
	return v
}
