// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config_test

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

const (
	liteToken   = "olt_0123456789abcdef0123456789abcdef"
	fingerprint = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

// liteCentralYAML is a minimal configuration around one central body.
func liteCentralYAML(central string) string {
	return `
logging:
  level: info
  format: text
centrals:
  - name: box
    host: box.local
` + central
}

// TestLiteCentralValidation pins every rule a central's system type adds:
// what an openccu-lite central needs, what it must not carry because the
// occulited path would silently ignore it, and that a CCU rejects the
// lite-only fields. Each rejected case names the field in its error.
func TestLiteCentralValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		central string
		wantErr string // "" = valid
	}{
		{"lite minimal", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [HmIP-RF, BidCos-RF]\n", ""},
		{"lite all interfaces", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [HmIP-RF, BidCos-RF, BidCos-Wired, VirtualDevices]\n", ""},
		{"lite pinned", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    tls: true\n    tls_fingerprint: " + fingerprint + "\n    interfaces: [HmIP-RF]\n", ""},
		{"lite json port", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    json_rpc_port: 8443\n    interfaces: [HmIP-RF]\n", ""},
		{"lite without token", "    system_type: openccu-lite\n    interfaces: [HmIP-RF]\n", "api_token: required"},
		{"lite token file alone", "    system_type: openccu-lite\n    api_token_file: /run/occulite/addon-tokens/openccu-loom.api\n    interfaces: [HmIP-RF]\n", ""},
		{"lite token and file", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    api_token_file: /run/x.api\n    interfaces: [HmIP-RF]\n", "cannot be combined with api_token"},
		{"lite relative token file", "    system_type: openccu-lite\n    api_token_file: run/x.api\n    interfaces: [HmIP-RF]\n", "an absolute path is required"},
		{"ccu with token file", "    system_type: ccu\n    api_token_file: /run/x.api\n    interfaces: [HmIP-RF]\n", "only an openccu-lite central uses an API token"},
		{"lite malformed token", "    system_type: openccu-lite\n    api_token: olt_XYZ\n    interfaces: [HmIP-RF]\n", "api_token: not an occulited API token"},
		{"lite upper-case token", "    system_type: openccu-lite\n    api_token: OLT_0123456789ABCDEF0123456789ABCDEF\n    interfaces: [HmIP-RF]\n", "api_token: not an occulited API token"},
		{"lite CUxD", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [HmIP-RF, CUxD]\n", "CUxD is not available on openccu-lite"},
		{"lite unknown interface", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [MyInterface]\n", "is not an openccu-lite interface"},
		{"lite username", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    username: Admin\n    interfaces: [HmIP-RF]\n", "authenticates with api_token"},
		{"lite password", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    password: secret\n    interfaces: [HmIP-RF]\n", "authenticates with api_token"},
		{"lite port", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    port: 2010\n    interfaces: [HmIP-RF]\n", "centrals[0].port: has no meaning"},
		{"lite ports", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    ports: {HmIP-RF: 2010}\n    interfaces: [HmIP-RF]\n", "centrals[0].ports: has no meaning"},
		{"lite interface port", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [{name: HmIP-RF, port: 2010}]\n", "interfaces[0].port: has no meaning"},
		{"lite remote path", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [{name: HmIP-RF, remote_path: /x}]\n", "interfaces[0].remote_path: has no meaning"},
		{"lite rpc type", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    interfaces: [{name: HmIP-RF, rpc_type: xmlrpc}]\n", "interfaces[0].rpc_type: has no meaning"},
		{"lite fingerprint without tls", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    tls_fingerprint: " + fingerprint + "\n    interfaces: [HmIP-RF]\n", "tls_fingerprint: requires tls"},
		{"lite fingerprint and insecure", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    tls: true\n    tls_insecure_skip_verify: true\n    tls_fingerprint: " + fingerprint + "\n    interfaces: [HmIP-RF]\n", "cannot be combined with tls_insecure_skip_verify"},
		{"lite malformed fingerprint", "    system_type: openccu-lite\n    api_token: " + liteToken + "\n    tls: true\n    tls_fingerprint: ABCDEF\n    interfaces: [HmIP-RF]\n", "not a lower-case hex SHA-256"},
		{"ccu default", "    port: 2001\n    interfaces: [HmIP-RF, CUxD]\n", ""},
		{"ccu explicit", "    system_type: ccu\n    username: Admin\n    interfaces: [HmIP-RF]\n", ""},
		{"ccu with token", "    system_type: ccu\n    api_token: " + liteToken + "\n    interfaces: [HmIP-RF]\n", "only an openccu-lite central uses an API token"},
		{"ccu with fingerprint", "    tls: true\n    tls_fingerprint: " + fingerprint + "\n    interfaces: [HmIP-RF]\n", "available for openccu-lite centrals only"},
		{"auto keeps both open", "    system_type: auto\n    username: Admin\n    api_token: " + liteToken + "\n    interfaces: [HmIP-RF]\n", ""},
		{"auto malformed token", "    system_type: auto\n    api_token: nope\n    interfaces: [HmIP-RF]\n", "not an occulited API token"},
		{"unknown system type", "    system_type: homegear\n    interfaces: [HmIP-RF]\n", "system_type: unknown value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Parse([]byte(liteCentralYAML(tc.central)))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("want an error containing %q, got none", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
