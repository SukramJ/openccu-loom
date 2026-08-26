// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ssdp

import (
	"net"
	"strings"
)

// virtualIfacePrefixes mirrors the mDNS advertiser's list: interface-name
// prefixes that belong to container / virtualisation bridges rather than a
// real LAN link. M-SEARCH must leave via a real LAN interface so it reaches
// the CCU; sending out of a `docker0` / `hassio` bridge would never get there.
// Kept in sync with internal/north/discovery/mdns/interfaces.go by intent.
var virtualIfacePrefixes = []string{
	"docker", "br-", "veth", "hassio", "virbr",
	"cni", "flannel", "cali", "kube", "podman",
}

func isVirtualInterfaceName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// multicastSourceIPs returns the IPv4 addresses of every up, multicast-capable,
// non-loopback, non-virtual interface — the source IPs to send an M-SEARCH
// from. Returns the unspecified address (0.0.0.0) as a single fallback when no
// concrete LAN address is found, so discovery still works on a simple host
// where interface enumeration yields nothing usable.
func multicastSourceIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []net.IP{net.IPv4zero}
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			iface.Flags&net.FlagMulticast == 0 ||
			isVirtualInterfaceName(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ipnet.IP.IsGlobalUnicast() {
				continue
			}
			ips = append(ips, ip4)
		}
	}
	if len(ips) == 0 {
		return []net.IP{net.IPv4zero}
	}
	return ips
}
