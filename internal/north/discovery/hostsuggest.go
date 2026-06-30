// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package discovery holds cross-cutting helpers shared by the LAN-discovery
// surfaces (SSDP/UPnP, mDNS). HostSuggester turns the raw address SSDP reports
// for a discovered CCU into the address an operator should actually configure.
package discovery

import (
	"context"
	"net"
	"net/netip"
	"strings"
)

// dockerCIDR is the private range Docker (and therefore the Home Assistant
// add-on networks) hands out container addresses from — 172.16.0.0/12. A CCU
// reachable only on such an address is almost certainly a co-located HA add-on
// whose IP rotates across restarts; LAN CCUs live on 192.168/16 or 10/8, which
// are deliberately left untouched. See docs/adr/0046-ssdp-ccu-discovery.md.
var dockerCIDR = netip.MustParsePrefix("172.16.0.0/12")

// HostSuggester picks the address to pre-fill when an operator adopts a
// discovered CCU. The raw SSDP host is the device-description URL's host —
// usually a bare IP that may be unstable (DHCP lease, rotating docker IP).
//
// Resolution order (first match wins), mirroring the operator intent recorded
// in ADR 0046:
//  1. The raw host is the daemon's own IP (CCU and daemon share a host) →
//     "localhost", which survives any address change on that host.
//  2. The daemon runs supervised (HA add-on) and the raw host is a docker-range
//     IP → the CCU is a co-located add-on with a rotating IP, so reverse-resolve
//     the IP to its stable container hostname and use that.
//  3. Otherwise the raw host is returned unchanged.
//
// A host that is already a name (not an IP) is always returned unchanged, as is
// any case where the reverse lookup yields nothing.
type HostSuggester struct {
	// Supervised is true only when the daemon runs as the supervised HA add-on
	// (build stamp / OPENCCU_LOOM_SUPERVISOR); the docker-hostname rule applies
	// only there.
	Supervised bool
	// LocalIPs are the daemon's own interface addresses (all non-loopback IPs,
	// including docker/bridge ones, so a shared-host CCU is detected even when
	// both run inside the same container network).
	LocalIPs []netip.Addr
	// LookupAddr reverse-resolves an IP to its hostnames. Injectable for tests;
	// nil falls back to the system resolver.
	LookupAddr func(ctx context.Context, addr string) ([]string, error)
}

// NewHostSuggester builds a suggester from the live host: it enumerates the
// daemon's interface IPs and uses the system resolver for reverse lookups.
func NewHostSuggester(supervised bool) *HostSuggester {
	return &HostSuggester{
		Supervised: supervised,
		LocalIPs:   localInterfaceIPs(),
		LookupAddr: net.DefaultResolver.LookupAddr,
	}
}

// Suggest returns the address an operator should configure for a CCU whose SSDP
// host is rawHost. It never returns an empty string: on any miss it returns
// rawHost unchanged.
func (h *HostSuggester) Suggest(ctx context.Context, rawHost string) string {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return rawHost
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host // already a hostname — nothing to improve
	}
	for _, local := range h.LocalIPs {
		if local == ip {
			return "localhost"
		}
	}
	if h.Supervised && dockerCIDR.Contains(ip.Unmap()) {
		if name := h.reverseName(ctx, host); name != "" {
			return name
		}
	}
	return host
}

// reverseName returns the first usable hostname a reverse lookup yields for ip,
// trimmed of the trailing dot, or "" when nothing resolves or the result is
// itself an address.
func (h *HostSuggester) reverseName(ctx context.Context, ip string) string {
	lookup := h.LookupAddr
	if lookup == nil {
		lookup = net.DefaultResolver.LookupAddr
	}
	names, err := lookup(ctx, ip)
	if err != nil {
		return ""
	}
	for _, n := range names {
		n = strings.TrimSuffix(strings.TrimSpace(n), ".")
		if n == "" {
			continue
		}
		// A PTR record that points back at an address is no improvement.
		if _, err := netip.ParseAddr(n); err == nil {
			continue
		}
		return n
	}
	return ""
}

// localInterfaceIPs returns every non-loopback unicast IP bound to the host's
// interfaces. Unlike the mDNS advertise filter this keeps bridge/docker
// addresses, because a CCU sharing the daemon's container would present one.
func localInterfaceIPs() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			if ip, ok := netip.AddrFromSlice(ipnet.IP); ok {
				out = append(out, ip.Unmap())
			}
		}
	}
	return out
}
