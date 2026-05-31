// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
	return &Client{cfg: cfg, providers: prov, http: httpClient}, nil
}

// AuthURL returns the redirect target for the browser. state + PKCE
// verifier are server-side session state; the caller persists them.
func (c *Client) AuthURL(state string, pkce PKCEPair) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURL)
	q.Set("scope", strings.Join(c.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
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

// IDClaims is the decoded ID token payload the Client reads. We do
// NOT verify JWT signatures — the Spec §19 flags JWT validation as a
// hardening item. The caller MUST pin a trusted IdP via TLS + Issuer.
type IDClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	Name          string `json:"name,omitempty"`
	PreferredUser string `json:"preferred_username,omitempty"`
	Role          string `json:"role,omitempty"`
	Roles         []any  `json:"roles,omitempty"`
}

// DecodeIDToken parses the JWT payload segment without verifying the
// signature. See [IDClaims] for the unverified-signature caveat.
func DecodeIDToken(token string) (*IDClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("oidc: ID token malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode payload: %w", err)
	}
	var claims IDClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
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
