// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package netutil

import "testing"

// TestIsVirtualInterfaceName verifies the container/virtualisation-bridge
// name classification shared by the client-discovery mDNS advertiser and
// the Matter bridge's mDNS advertiser: both must agree on which host
// interfaces carry addresses that LAN peers cannot route to.
func TestIsVirtualInterfaceName(t *testing.T) {
	t.Parallel()

	virtual := []string{"docker0", "br-1a2b3c", "veth123", "hassio", "virbr0", "cni0", "podman0"}
	physical := []string{"eth0", "en0", "end0", "wlan0", "br0", "wg0", "tailscale0", "tun0"}

	for _, n := range virtual {
		if !IsVirtualInterfaceName(n) {
			t.Errorf("IsVirtualInterfaceName(%q) = false, want true (container/bridge)", n)
		}
	}
	for _, n := range physical {
		if IsVirtualInterfaceName(n) {
			t.Errorf("IsVirtualInterfaceName(%q) = true, want false (real LAN / VPN link)", n)
		}
	}
}
