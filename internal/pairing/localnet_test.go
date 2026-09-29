// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package pairing

import "testing"

func TestLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":     true,
		"::1":           true,
		"192.168.1.50":  true,
		"10.0.0.7":      true,
		"172.16.0.9":    true,
		"169.254.10.10": true,
		"fd12::1":       true,  // ULA
		"fe80::1":       true,  // link-local
		"203.0.113.9":   false, // public
		"2001:db8::1":   false, // public v6 (documentation range, not ULA)
		"not-an-ip":     false,
		"":              false,
	} {
		if got := LocalHost(host); got != want {
			t.Errorf("LocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}
