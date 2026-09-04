// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns

import "strings"

// virtualIfacePrefixes are interface-name prefixes that belong to
// container / virtualisation bridges rather than a real LAN link. Their
// addresses must NOT end up in advertised A-records: a commissioner that
// resolves the bridge to e.g. the Home Assistant `hassio` bridge gateway
// (172.30.232.1) or a `docker0` address cannot route to the daemon from
// its own namespace, and Apple's Matter daemon walks the published
// address list in order and gives up on an unreachable entry rather than
// moving on. Matching is case-insensitive prefix.
//
// Deliberately conservative — only names that are unambiguously container/
// hypervisor bridges. Real LAN links (eth*, en*, wlan*, eno*, enp*, end*)
// and VPN overlays (wg*, tun*, tailscale*) are left untouched. The "br-"
// form targets Docker's `br-<hex>` user-network bridges (a plain "br0" is
// not matched).
//
// The daemon's client-discovery advertiser applies the identical policy
// from [github.com/SukramJ/openccu-loom/internal/netutil]. This copy exists
// so the Matter subtree carries no dependency on the surrounding daemon;
// the price is that the two lists can drift apart in silence, which is
// what TestVendoredVirtualInterfaceFilterAgreesWithNetutil is for.
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

// isVirtualInterfaceName reports whether name looks like a container /
// virtualisation bridge whose addresses are not routable from peers. It is
// the default [Zeroconf.InterfaceFilter].
func isVirtualInterfaceName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
