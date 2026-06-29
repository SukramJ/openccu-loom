// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestBuildOIDCClient_EnabledUnreachableIssuer_ReturnsNil verifies that
// when OIDC is enabled and an issuer is configured but unreachable, the
// OIDC discovery fails and buildOIDCClient returns nil (and logs a warning).
func TestBuildOIDCClient_EnabledUnreachableIssuer_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Auth.OIDC.Enabled = true
	// 192.0.2.1 is TEST-NET-1 (RFC 5737) — guaranteed unreachable, no real
	// server should ever respond. The 5 s discovery timeout will exhaust
	// quickly because TCP connection is refused or times out immediately.
	cfg.North.REST.Auth.OIDC.Issuer = "https://192.0.2.1:9999/oidc"
	cfg.North.REST.Auth.OIDC.ClientID = "test"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	got := buildOIDCClient(cfg, logger)
	if got != nil {
		t.Errorf("expected nil for unreachable issuer, got %v", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("oidc.discovery")) {
		t.Errorf("expected oidc.discovery warning; got:\n%s", buf.String())
	}
}

// TestBuildOIDCRest_EnabledUnreachableIssuer_ReturnsNil tests the buildOIDCRest
// wrapper.
func TestBuildOIDCRest_EnabledUnreachableIssuer_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Auth.OIDC.Enabled = true
	cfg.North.REST.Auth.OIDC.Issuer = "https://192.0.2.1:9999/oidc"
	logger := slog.New(slog.DiscardHandler)
	got := buildOIDCRest(cfg, logger, nil)
	if got != nil {
		t.Errorf("expected nil when OIDC discovery fails")
	}
}
