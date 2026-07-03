// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestWireREST_SessionCookieSecureFlag guards the session-cookie Secure
// derivation in wireREST: the cookie must be marked Secure whenever the
// deployment terminates TLS directly, or the operator has declared a
// TLS-terminating reverse proxy via CSRFSecure or an https PublicURL —
// otherwise the cookie would ride plaintext requests and a downgraded
// request could leak it.
func TestWireREST_SessionCookieSecureFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		mutate     func(cfg *config.Config)
		wantSecure bool
	}{
		{
			name:       "plain HTTP, no CSRF secure, no public URL",
			mutate:     func(_ *config.Config) {},
			wantSecure: false,
		},
		{
			name: "TLS cert+key configured",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.TLSCertFile = "cert.pem"
				cfg.North.REST.TLSKeyFile = "key.pem"
			},
			wantSecure: true,
		},
		{
			name: "CSRFSecure set explicitly",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.CSRFSecure = true
			},
			wantSecure: true,
		},
		{
			name: "https public URL",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.PublicURL = "https://loom.example.de"
			},
			wantSecure: true,
		},
		{
			name: "http (non-TLS) public URL does not imply secure",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.PublicURL = "http://loom.example.de"
			},
			wantSecure: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			tc.mutate(cfg)

			w := wireREST(context.Background(), restWiringDeps{
				cfg:    cfg,
				logger: slog.New(slog.DiscardHandler),
			})
			if w.auth == nil {
				t.Fatal("wireREST: AuthDeps is nil")
			}
			if w.auth.Secure != tc.wantSecure {
				t.Errorf("AuthDeps.Secure = %v, want %v", w.auth.Secure, tc.wantSecure)
			}
		})
	}
}
