// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// Config describes one OIDC deployment the UI should accept.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string // optional for public clients
	RedirectURL  string
	Scopes       []string
	// RoleClaim is the claim in the ID token that maps to [auth.Role].
	// Defaults to "role" when empty.
	RoleClaim string
}

// Client drives the authorization-code-with-PKCE flow.
type Client struct {
	cfg       Config
	providers *Providers
	http      *http.Client
	jwks      *JWKSCache
}

// New constructs a Client. httpClient may be nil (uses default).
func New(ctx context.Context, cfg Config, httpClient *http.Client) (*Client, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: Issuer + ClientID + RedirectURL required")
	}
	if cfg.RoleClaim == "" {
		cfg.RoleClaim = "role"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	prov, err := Discover(ctx, httpClient, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:       cfg,
		providers: prov,
		http:      httpClient,
		jwks:      NewJWKSCache(prov.JWKSURI, httpClient),
	}, nil
}

// NewNonce mints a cryptographically-random OIDC nonce (OIDC Core
// §3.1.2.1). Callers bind the returned value to the pending-auth
// session alongside the PKCE verifier + state, pass it to
// [Client.AuthURL], and pass the same value back into
// [Client.VerifyIDToken] as expectedNonce so a captured ID token
// cannot be replayed into a different browser session.
func NewNonce() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// AuthURL returns the redirect target for the browser. state + PKCE
// verifier are server-side session state; the caller persists them.
// nonce is optional (variadic so existing callers keep compiling);
// when supplied it is echoed as the "nonce" authorization parameter
// and MUST be passed back into [Client.VerifyIDToken] as the
// expectedNonce so the returned ID token's nonce claim is checked
// against it.
func (c *Client) AuthURL(state string, pkce PKCEPair, nonce ...string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURL)
	q.Set("scope", strings.Join(c.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	if len(nonce) > 0 && nonce[0] != "" {
		q.Set("nonce", nonce[0])
	}
	return c.providers.AuthorizationEndpoint + "?" + q.Encode()
}

// TokenResponse is the subset of the token-endpoint response the
// client reads.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Exchange swaps the authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, code, verifier string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.cfg.RedirectURL)
	form.Set("client_id", c.cfg.ClientID)
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.providers.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc.Exchange: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // teardown after JSON decode
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("oidc.Exchange: status=%d body=%s", resp.StatusCode, body)
	}
	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// IDClaims is the decoded ID token payload the Client reads. It
// carries the registered claims ([Client.VerifyIDToken] validates
// iss / aud / exp) alongside the profile claims the role mapping
// consumes.
type IDClaims struct {
	Issuer        string   `json:"iss,omitempty"`
	Subject       string   `json:"sub"`
	Audience      Audience `json:"aud,omitempty"`
	Expiry        int64    `json:"exp,omitempty"`
	IssuedAt      int64    `json:"iat,omitempty"`
	NotBefore     int64    `json:"nbf,omitempty"`
	Email         string   `json:"email,omitempty"`
	EmailVerified bool     `json:"email_verified,omitempty"`
	Name          string   `json:"name,omitempty"`
	PreferredUser string   `json:"preferred_username,omitempty"`
	Role          string   `json:"role,omitempty"`
	Roles         []any    `json:"roles,omitempty"`
	Nonce         string   `json:"nonce,omitempty"`
}

// Audience models the OIDC "aud" claim. The spec allows it to be
// either a single string or an array of strings, so it decodes both
// shapes into a slice.
type Audience []string

// UnmarshalJSON accepts both the string and the array form of "aud".
func (a *Audience) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = Audience{s}
		return nil
	}
	var ss []string
	if err := json.Unmarshal(b, &ss); err != nil {
		return err
	}
	*a = Audience(ss)
	return nil
}

// contains reports whether want is one of the audiences.
func (a Audience) contains(want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}

// idTokenLeeway absorbs small clock differences between the daemon
// and the IdP when checking the exp / nbf bounds.
const idTokenLeeway = 2 * time.Minute

// VerifyIDToken is the production entry point for the OIDC callbacks.
// It verifies the ID token's RS256 signature against the provider's
// JWKS, then validates the issuer, audience, and expiry. A token that
// is unsigned, signed by an unknown key, issued for a different
// client, or expired is rejected.
//
// expectedNonce is optional (variadic so existing callers keep
// compiling). When the caller supplies a non-empty value — the same
// one it passed to [Client.AuthURL] for this flow — OIDC Core
// §3.1.3.7 step 11 requires the ID token's "nonce" claim to be
// present and equal to it; a missing or mismatching claim is
// rejected. Callers that pass no expectedNonce skip the check, which
// exists only to keep pre-nonce call sites compiling during rollout
// and must not be relied on as a long-term opt-out.
func (c *Client) VerifyIDToken(ctx context.Context, rawIDToken string, expectedNonce ...string) (*IDClaims, error) {
	claims, err := Verify(ctx, rawIDToken, c.jwks)
	if err != nil {
		return nil, err
	}
	if claims.Issuer == "" || claims.Issuer != c.providers.Issuer {
		return nil, fmt.Errorf("oidc: issuer mismatch: got %q want %q", claims.Issuer, c.providers.Issuer)
	}
	if !claims.Audience.contains(c.cfg.ClientID) {
		return nil, fmt.Errorf("oidc: audience %v does not include client %q", []string(claims.Audience), c.cfg.ClientID)
	}
	if claims.Expiry == 0 {
		return nil, errors.New("oidc: ID token missing exp")
	}
	now := time.Now()
	if now.After(time.Unix(claims.Expiry, 0).Add(idTokenLeeway)) {
		return nil, errors.New("oidc: ID token expired")
	}
	if claims.NotBefore != 0 && now.Add(idTokenLeeway).Before(time.Unix(claims.NotBefore, 0)) {
		return nil, errors.New("oidc: ID token not yet valid")
	}
	if len(expectedNonce) > 0 && expectedNonce[0] != "" && claims.Nonce != expectedNonce[0] {
		return nil, errors.New("oidc: ID token nonce mismatch")
	}
	return claims, nil
}

// IdentityFrom builds an [auth.Identity] from ID-token claims. The
// role falls back to Viewer when nothing maps.
func (c *Client) IdentityFrom(claims *IDClaims) auth.Identity {
	subject := claims.PreferredUser
	if subject == "" {
		subject = claims.Subject
	}
	role := auth.RoleViewer
	switch strings.ToLower(claims.Role) {
	case "admin", "administrator":
		role = auth.RoleAdmin
	case "operator":
		role = auth.RoleOperator
	}
	return auth.Identity{Subject: subject, Scheme: auth.SchemeSession, Role: role}
}
