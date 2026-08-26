// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns

import (
	"net"

	"github.com/SukramJ/openccu-loom/internal/netutil"
)

// isVirtualInterfaceName reports whether name looks like a container /
// virtualisation bridge whose addresses are not routable from peers.
// Shared with the Matter bridge's mDNS advertiser via
// [netutil.IsVirtualInterfaceName] so both advertisers agree on which
// interfaces carry LAN-routable addresses.
func isVirtualInterfaceName(name string) bool {
	return netutil.IsVirtualInterfaceName(name)
}

// ifaceAddrs is the testable shape of one network interface: its name,
// up/loopback flags and unicast IPs. Decoupled from net.Interface so
// filterAdvertiseIPs can be unit-tested without real interfaces.
type ifaceAddrs struct {
	name     string
	up       bool
	loopback bool
	ips      []net.IP
}

// filterAdvertiseIPs returns the routable IPs to put in the mDNS A/AAAA
// records: global-unicast addresses on up, non-loopback, non-virtual
// interfaces — IPv4 first so a client that picks the first resolved
// address lands on an IPv4 LAN address. Container/bridge interfaces are
// dropped entirely so their internal addresses never get advertised.
func filterAdvertiseIPs(ifaces []ifaceAddrs) []string {
	var v4, v6 []string
	for _, ifc := range ifaces {
		if !ifc.up || ifc.loopback || isVirtualInterfaceName(ifc.name) {
			continue
		}
		for _, ip := range ifc.ips {
			// IsGlobalUnicast() already excludes loopback, link-local,
			// multicast and the unspecified address; RFC1918 LAN ranges
			// stay (they are global-unicast in Go's classification).
			if ip == nil || !ip.IsGlobalUnicast() {
				continue
			}
			if ip.To4() != nil {
				v4 = append(v4, ip.String())
			} else {
				v6 = append(v6, ip.String())
			}
		}
	}
	return append(v4, v6...)
}

// routableAdvertiseIPs reads the host's interfaces and returns the IPs
// safe to advertise (see filterAdvertiseIPs). Returns nil when interface
// enumeration fails or nothing routable is found, so the caller can fall
// back to the library's auto-detection rather than advertise nothing.
func routableAdvertiseIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	infos := make([]ifaceAddrs, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		info := ifaceAddrs{
			name:     iface.Name,
			up:       iface.Flags&net.FlagUp != 0,
			loopback: iface.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				info.ips = append(info.ips, ipnet.IP)
			}
		}
		infos = append(infos, info)
	}
	return filterAdvertiseIPs(infos)
}
