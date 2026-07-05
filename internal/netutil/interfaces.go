// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package netutil holds small network-introspection helpers shared by
// the daemon's mDNS advertisers (client auto-discovery and the Matter
// bridge). Both publish A/AAAA records and must agree on which host
// interfaces carry addresses that LAN peers can actually route to.
package netutil

import "strings"

// virtualIfacePrefixes are interface-name prefixes that belong to
// container / virtualisation bridges rather than a real LAN link. Their
// addresses must NOT end up in advertised A-records: a client that
// resolves the daemon to e.g. the Home Assistant `hassio` bridge gateway
// (172.30.232.1) or a `docker0` address cannot route to the daemon from
// its own namespace. Matching is case-insensitive prefix.
//
// Deliberately conservative — only names that are unambiguously container/
// hypervisor bridges. Real LAN links (eth*, en*, wlan*, eno*, enp*, end*)
// and VPN overlays (wg*, tun*, tailscale*) are left untouched. The "br-"
// form targets Docker's `br-<hex>` user-network bridges (a plain "br0" is
// not matched).
var virtualIfacePrefixes = []string{
	"docker",
	"br-",
	"veth",
	"hassio",
	"virbr",
	"cni",
	"flannel",
	"cali",
	"kube",
	"podman",
	"cni-podman",
}

// IsVirtualInterfaceName reports whether name looks like a container /
// virtualisation bridge whose addresses are not routable from peers.
//
// loom:reachable:reason="statically called by the client-discovery and Matter mDNS advertisers (internal/north/discovery/mdns, internal/north/matter/mdns); those advertiser paths sit behind the daemon wiring the RTA entry-point analysis already tolerates as unreachable for the caller packages"
func IsVirtualInterfaceName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
