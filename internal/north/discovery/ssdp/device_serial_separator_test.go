// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ssdp

import "testing"

// TestSerialFromUDNSeparatorEdges pins how serialFrom treats the last "-"
// in a UDN: absent, leading, trailing and repeated.
func TestSerialFromUDNSeparatorEdges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ udn, want string }{
		{"uuid:upnp-BasicDevice-1_0-ABC123", "ABC123"},
		{"uuid:XYZ", "XYZ"},
		{"-ABC", "ABC"},
		{"uuid:abc-", "abc-"},
		{"uuid:abc- ", "abc-"},
		{"uuid:a-b- c", "c"},
	} {
		if got := serialFrom(tc.udn, "fallback-desc"); got != tc.want {
			t.Errorf("serialFrom(%q) = %q, want %q", tc.udn, got, tc.want)
		}
	}
}
