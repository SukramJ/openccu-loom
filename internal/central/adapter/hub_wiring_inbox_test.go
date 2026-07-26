// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "testing"

// TestIsVirtualInboxDevice pins the pairing-inbox filter: CCU-internal virtual
// devices (heating-group backing devices with an "INT" address, and anything on
// the VirtualDevices interface) are never physical pairing candidates and must
// be kept out of the inbox, while real devices pass through.
func TestIsVirtualInboxDevice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		address string
		iface   string
		want    bool
	}{
		{"heating group INT address", "INT0000012", "HmIP-RF", true},
		{"virtual devices interface", "0001D3C9", "VirtualDevices", true},
		{"physical HmIP device", "00021BE9957782", "HmIP-RF", false},
		{"physical BidCos device", "OEQ1234567", "BidCos-RF", false},
		{"physical device on HmIP-RF, colon channel", "ABC123:1", "HmIP-RF", false},
	}
	for _, tc := range cases {
		if got := isVirtualInboxDevice(tc.address, tc.iface); got != tc.want {
			t.Errorf("%s: isVirtualInboxDevice(%q, %q) = %v, want %v",
				tc.name, tc.address, tc.iface, got, tc.want)
		}
	}
}
