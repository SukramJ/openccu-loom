// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Extra coverage tests targeting the addr == "" default-address
// branches in listener.go. These assert the pure bindAddr resolution
// rather than binding the well-known MatterPort, which would race any
// other 5540 listener and flake on shared CI runners.

package udp

import (
	"fmt"
	"testing"
)

// TestBindAddr covers every branch of bindAddr: the IPv4/IPv6 defaults
// taken when LocalAddr is empty, and the explicit-address passthrough.
func TestBindAddr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		cfg         Config
		wantNetwork string
		wantAddr    string
	}{
		{
			name:        "default IPv4",
			cfg:         Config{LocalAddr: "", PreferIPv4: true},
			wantNetwork: "udp4",
			wantAddr:    fmt.Sprintf("0.0.0.0:%d", MatterPort),
		},
		{
			name:        "default IPv6 dual-stack",
			cfg:         Config{LocalAddr: "", PreferIPv4: false},
			wantNetwork: "udp",
			wantAddr:    fmt.Sprintf("[::]:%d", MatterPort),
		},
		{
			name:        "explicit addr IPv4",
			cfg:         Config{LocalAddr: "127.0.0.1:0", PreferIPv4: true},
			wantNetwork: "udp4",
			wantAddr:    "127.0.0.1:0",
		},
		{
			name:        "explicit addr dual-stack",
			cfg:         Config{LocalAddr: "[::1]:1234", PreferIPv4: false},
			wantNetwork: "udp",
			wantAddr:    "[::1]:1234",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			network, addr := bindAddr(tc.cfg)
			if network != tc.wantNetwork {
				t.Errorf("network = %q, want %q", network, tc.wantNetwork)
			}
			if addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tc.wantAddr)
			}
		})
	}
}
