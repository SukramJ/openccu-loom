// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box test: it reaches the unexported vendored predicate, so it lives
// in package mdns. It is the only place in this package that imports the
// daemon's netutil, and it does so from a _test file on purpose — the
// non-test import graph of the Matter subtree stays free of it.

package mdns

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/netutil"
)

// sharedInterfaceNames is the input table both copies of the
// virtual-interface predicate are run over. It names one interface per
// prefix the daemon's netutil list carries, plus the real LAN links, VPN
// overlays and near-miss names the policy must NOT drop.
var sharedInterfaceNames = []string{
	// Container / virtualisation bridges — one per known prefix.
	"docker0", "br-1a2b3c4d", "veth9f2c1a", "hassio", "hassio-docker",
	"virbr0", "cni0", "flannel.1", "cali1234abcd", "kube-ipvs0",
	"podman0", "cni-podman0",
	// Case variants: the policy is case-insensitive.
	"Docker0", "VETH1", "HASSIO", "BR-DEADBEEF",
	// Real LAN links, VPN overlays and near misses that must survive.
	"eth0", "en0", "eno1", "enp3s0", "end0", "wlan0", "wlp2s0",
	"wg0", "tun0", "tailscale0", "lo", "bond0", "vlan10",
	"br0", "bridge0", "vmnet1", "utun3", "cal", "kub", "",
}

// TestVendoredVirtualInterfaceFilterAgreesWithNetutil pins the copy of the
// virtual-interface policy this package carries against the daemon's
// original in internal/netutil. The Matter mDNS advertiser and the client
// auto-discovery advertiser publish A-records for the same host, so a
// silent divergence would make one of them announce an address the other
// deliberately suppresses.
//
// Scope: the table catches any edit to a prefix either copy already has,
// and any prefix added here that netutil does not have. A prefix added to
// netutil alone is caught only once this table names an interface that
// matches it — extending the table is part of extending that list.
func TestVendoredVirtualInterfaceFilterAgreesWithNetutil(t *testing.T) {
	t.Parallel()
	for _, name := range sharedInterfaceNames {
		if got, want := isVirtualInterfaceName(name), netutil.IsVirtualInterfaceName(name); got != want {
			t.Errorf("interface %q: vendored isVirtualInterfaceName = %v, netutil.IsVirtualInterfaceName = %v",
				name, got, want)
		}
	}
}

// TestVendoredVirtualInterfacePrefixesAreRecognisedByNetutil derives the
// input from the vendored list itself, so a prefix that exists only here
// is caught even when nobody remembers to extend sharedInterfaceNames.
func TestVendoredVirtualInterfacePrefixesAreRecognisedByNetutil(t *testing.T) {
	t.Parallel()
	for _, p := range virtualIfacePrefixes {
		for _, name := range []string{p, p + "0", strings.ToUpper(p) + "1"} {
			if !netutil.IsVirtualInterfaceName(name) {
				t.Errorf("vendored prefix %q yields %q, which netutil does not treat as virtual", p, name)
			}
		}
	}
}
