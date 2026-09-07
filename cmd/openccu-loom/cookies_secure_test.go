// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestCookiesSecureIsOneRuleForSessionAndCSRF pins the CSRF double-submit
// cookie to the session cookie's Secure rule: TLS terminated by the daemon,
// or declared via csrf_secure, or an https public_url. The CSRF cookie used
// to follow csrf_secure alone.
func TestCookiesSecureIsOneRuleForSessionAndCSRF(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(*config.Config)
		want bool
	}{
		{"plain http", func(*config.Config) {}, false},
		{"daemon terminates TLS", func(c *config.Config) { c.North.REST.TLSCertFile, c.North.REST.TLSKeyFile = "cert.pem", "key.pem" }, true},
		{"csrf_secure declared", func(c *config.Config) { c.North.REST.CSRFSecure = true }, true},
		{"https public_url", func(c *config.Config) { c.North.REST.PublicURL = "https://loom.example" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			tc.mut(cfg)
			if got := cookiesSecure(cfg); got != tc.want {
				t.Fatalf("cookiesSecure = %v, want %v", got, tc.want)
			}
		})
	}
}
