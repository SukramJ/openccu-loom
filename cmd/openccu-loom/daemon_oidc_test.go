// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// oidcTestConfig returns a config whose OIDC section is complete but points at
// TEST-NET-1 (RFC 5737), which is guaranteed to have no server behind it.
func oidcTestConfig() *config.Config {
	cfg := config.Default()
	cfg.North.REST.Auth.OIDC.Enabled = true
	cfg.North.REST.Auth.OIDC.Issuer = "https://192.0.2.1:9999/oidc"
	cfg.North.REST.Auth.OIDC.ClientID = "test"
	cfg.North.REST.Auth.OIDC.RedirectURL = "https://loom.example/api/v1/auth/oidc/callback"
	return cfg
}

// TestBuildOIDCClient_UnreachableIssuer_StillReturnsAClient pins that a
// provider outage at boot does not disable SSO. Discovery is deferred to the
// first login, so the client stands up regardless of whether the IdP answers
// while the daemon starts; it rediscovers on its own once the IdP is back.
func TestBuildOIDCClient_UnreachableIssuer_StillReturnsAClient(t *testing.T) {
	t.Parallel()
	got := buildOIDCClient(oidcTestConfig(), slog.New(slog.DiscardHandler))
	if got == nil {
		t.Fatal("an unreachable issuer is transient and must not disable SSO until restart")
	}
}

// TestBuildOIDCRest_UnreachableIssuer_StillReturnsDeps mirrors the above for
// the REST wrapper: the SSO routes stay mounted across a provider outage.
func TestBuildOIDCRest_UnreachableIssuer_StillReturnsDeps(t *testing.T) {
	t.Parallel()
	got := buildOIDCRest(oidcTestConfig(), slog.New(slog.DiscardHandler), nil)
	if got == nil {
		t.Fatal("expected OIDC deps for a configured but currently unreachable issuer")
	}
}

// TestBuildOIDCClient_MisconfiguredIssuer_ReturnsNil pins the other half: a
// configuration error is not transient, so the client is not built at all.
func TestBuildOIDCClient_MisconfiguredIssuer_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := oidcTestConfig()
	cfg.North.REST.Auth.OIDC.Issuer = "http://idp.example/oidc" // discovery requires HTTPS
	if got := buildOIDCClient(cfg, slog.New(slog.DiscardHandler)); got != nil {
		t.Error("a non-HTTPS issuer must not yield a client")
	}
}
