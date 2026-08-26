// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Providers hold the endpoints a [Client] needs. They are the subset
// of the OIDC discovery document we actually consume.
type Providers struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// Discover loads the provider metadata from `<issuer>/.well-known/openid-configuration`.
//
// The issuer and every discovered endpoint must use https (plain http is
// tolerated only on a loopback host, for local development) so the code
// exchange and ID-token retrieval never run in cleartext. Per RFC 8414 §3.3
// the metadata's own `issuer` must equal the configured issuer, closing a
// forged-discovery redirect of the token/JWKS traffic.
func Discover(ctx context.Context, client *http.Client, issuer string) (*Providers, error) {
	if client == nil {
		client = defaultHTTPClient()
	}
	if err := requireHTTPSURL(issuer); err != nil {
		return nil, err
	}
	wellKnown := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc.Discover: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // discovery read-only
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("oidc.Discover: status=%d body=%s", resp.StatusCode, body)
	}
	var p Providers
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("oidc.Discover: decode: %w", err)
	}
	if p.AuthorizationEndpoint == "" || p.TokenEndpoint == "" {
		return nil, fmt.Errorf("oidc.Discover: endpoints missing for issuer %s", issuer)
	}
	if strings.TrimRight(p.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return nil, fmt.Errorf("oidc.Discover: metadata issuer %q does not match configured issuer %q", p.Issuer, issuer)
	}
	for _, ep := range []string{p.AuthorizationEndpoint, p.TokenEndpoint, p.JWKSURI, p.UserInfoEndpoint} {
		if ep == "" {
			continue
		}
		if err := requireHTTPSURL(ep); err != nil {
			return nil, fmt.Errorf("oidc.Discover: %w", err)
		}
	}
	return &p, nil
}

// requireHTTPSURL rejects a URL that is not https. Plain http is allowed only
// on a loopback host (localhost / 127.0.0.0/8 / ::1) so local development
// against a non-TLS IdP still works; every other host must use https.
func requireHTTPSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("oidc: invalid URL %q: %w", raw, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("oidc: %q must use https (plain http is allowed only on localhost)", raw)
}

// isLoopbackHost reports whether host is localhost or a loopback IP.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
