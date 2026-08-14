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
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/httpx"
)

// providerTimeout bounds one call to the identity provider — discovery,
// a JWKS refresh, a code exchange. A provider that accepts the
// connection and never answers would otherwise hold a login request (or
// a JWKS refresh serialising every token verification behind it) open
// with no deadline of its own.
const providerTimeout = 30 * time.Second

// defaultHTTPClient is the client used when a caller passes none. It
// owns its transport (see [internal/httpx]) rather than sharing the
// process-wide default with unrelated callers, and carries
// [providerTimeout] as its request deadline.
var defaultHTTPClient = sync.OnceValue(func() *http.Client {
	return httpx.NewClient(providerTimeout)
})

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

// New constructs a Client. httpClient may be nil, in which case the
// package's own bounded client is used.
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
		httpClient = defaultHTTPClient()
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
//
// loom:reachable:reason="minted by the REST OIDC start handler; the OIDC surface is wired conditionally so the production callgraph does not reach it"
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
	Issuer          string   `json:"iss,omitempty"`
	Subject         string   `json:"sub"`
	Audience        Audience `json:"aud,omitempty"`
	AuthorizedParty string   `json:"azp,omitempty"`
	Expiry          int64    `json:"exp,omitempty"`
	IssuedAt        int64    `json:"iat,omitempty"`
	NotBefore       int64    `json:"nbf,omitempty"`
	Email           string   `json:"email,omitempty"`
	EmailVerified   bool     `json:"email_verified,omitempty"`
	Name            string   `json:"name,omitempty"`
	PreferredUser   string   `json:"preferred_username,omitempty"`
	Role            string   `json:"role,omitempty"`
	Roles           []any    `json:"roles,omitempty"`
	Nonce           string   `json:"nonce,omitempty"`
	// Raw is the full decoded claim set. It backs role resolution against a
	// configurable — and possibly nested, e.g. Keycloak's
	// "realm_access.roles" — RoleClaim. [Verify] populates it; it is nil
	// when a caller constructs IDClaims directly (the typed Role / Roles
	// fields are the fallback in that case).
	Raw map[string]any `json:"-"`
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
	// OIDC Core §3.1.3.7: an "azp" claim, when present, must be this client;
	// and a multi-audience token must carry azp so a token minted for another
	// party (that merely also lists this client in aud) cannot be replayed.
	if claims.AuthorizedParty != "" && claims.AuthorizedParty != c.cfg.ClientID {
		return nil, fmt.Errorf("oidc: azp %q is not client %q", claims.AuthorizedParty, c.cfg.ClientID)
	}
	if len(claims.Audience) > 1 && claims.AuthorizedParty == "" {
		return nil, errors.New("oidc: multi-audience ID token without azp")
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

// IdentityFrom builds an [auth.Identity] from ID-token claims. The role is
// read from the configured RoleClaim (default "role"), resolved against the
// raw claim set so it supports a plain string, a string array, and a dotted
// path into nested objects (e.g. Keycloak's "realm_access.roles"). When the
// claim yields several role names the highest-privilege match wins
// (admin > operator > viewer); an unmapped or absent claim yields Viewer.
func (c *Client) IdentityFrom(claims *IDClaims) auth.Identity {
	subject := claims.PreferredUser
	if subject == "" {
		subject = claims.Subject
	}
	return auth.Identity{
		Subject: subject,
		Scheme:  auth.SchemeSession,
		Role:    c.roleFromClaims(claims),
	}
}

// roleFromClaims resolves the auth.Role from the configured RoleClaim. It
// reads the raw claim set first; when that yields nothing (a caller built
// IDClaims without the raw map, or a provider populated only the typed
// fields) it falls back to the well-known top-level "role" string and
// "roles" array so pre-existing behaviour is preserved.
func (c *Client) roleFromClaims(claims *IDClaims) auth.Role {
	claim := c.cfg.RoleClaim
	if claim == "" {
		claim = "role"
	}
	names := claimStrings(claims.Raw, claim)
	if len(names) == 0 {
		if claims.Role != "" {
			names = append(names, claims.Role)
		}
		for _, r := range claims.Roles {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
	}
	return highestRole(names)
}

// highestRole maps role names to the most-privileged [auth.Role] they denote,
// so a user carrying several roles is granted the strongest one.
func highestRole(names []string) auth.Role {
	best := auth.RoleViewer
	for _, n := range names {
		switch strings.ToLower(strings.TrimSpace(n)) {
		case "admin", "administrator":
			return auth.RoleAdmin // highest — nothing can beat it
		case "operator":
			best = auth.RoleOperator
		}
	}
	return best
}

// claimStrings returns the string value(s) at a dotted path in the raw claim
// set. Each path segment descends one nested JSON object; the leaf may be a
// string or an array of strings (anything else yields nothing). This lets a
// RoleClaim address a top-level string ("role"), a top-level array
// ("groups"), or a nested array ("realm_access.roles").
func claimStrings(raw map[string]any, path string) []string {
	if raw == nil {
		return nil
	}
	var cur any = raw
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[p]; !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}
