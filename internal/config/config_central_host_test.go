// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// centrals[].host is interpolated into every south-bound URL (XML-RPC /
// JSON-RPC endpoints, the readiness probe). Validation must accept bare
// hostnames and IP literals and reject anything that would reshape those
// URLs — scheme, path, query, credentials, embedded port (the TCP port
// has its own config field).

func centralYAML(host string) string {
	return fmt.Sprintf(`
logging:
  level: info
  format: text
centrals:
  - name: ccu1
    host: %q
    port: 2001
    interfaces:
      - HmIP-RF
`, host)
}

func TestValidate_CentralHostAcceptsHostnamesAndIPs(t *testing.T) {
	t.Parallel()
	valid := []string{
		"192.168.1.1",
		"192.0.2.29",
		"ccu.local",
		"otto.mm-jankowski.de",
		"CCU3",
		"my_ccu",           // nonstandard but common on LANs
		"ccu.example.com.", // FQDN with trailing dot
		"fd00::1",          // bare IPv6 literal
		"[fd00::1]",        // bracketed IPv6 literal
	}
	for _, host := range valid {
		if _, err := config.Parse([]byte(centralYAML(host))); err != nil {
			t.Errorf("host %q must be accepted, got %v", host, err)
		}
	}
}

func TestValidate_CentralHostRejectsURLShapes(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"http://ccu.local",  // scheme
		"https://ccu.local", // scheme
		"ccu.local/path",    // path
		"ccu.local?x=1",     // query
		"ccu.local#frag",    // fragment
		"user@ccu.local",    // credentials
		"ccu.local:8080",    // embedded port — the port field owns this
		"ccu local",         // whitespace
		"-ccu.local",        // label starts with a hyphen
		"ccu..local",        // empty label
	}
	for _, host := range invalid {
		_, err := config.Parse([]byte(centralYAML(host)))
		if err == nil || !strings.Contains(err.Error(), "host") {
			t.Errorf("host %q must be rejected with a host error, got %v", host, err)
		}
	}
}
