// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
func Discover(ctx context.Context, client *http.Client, issuer string) (*Providers, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
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
	return &p, nil
}
