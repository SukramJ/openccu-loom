// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns

import (
	"net"
	"slices"
	"testing"
)

func TestIsVirtualInterfaceName(t *testing.T) {
	t.Parallel()
	virtual := []string{"docker0", "br-1a2b3c4d5e6f", "veth9f2a", "hassio", "virbr0", "cni0", "flannel.1", "cali123", "kube-bridge", "podman0"}
	physical := []string{"eth0", "en0", "eno1", "enp3s0", "end0", "wlan0", "wlp2s0", "br0", "wg0", "tun0", "tailscale0", "lo"}

	for _, n := range virtual {
		if !isVirtualInterfaceName(n) {
			t.Errorf("isVirtualInterfaceName(%q) = false, want true (container/bridge)", n)
		}
	}
	for _, n := range physical {
		if isVirtualInterfaceName(n) {
			t.Errorf("isVirtualInterfaceName(%q) = true, want false (real LAN / VPN link)", n)
		}
	}
}

func TestFilterAdvertiseIPs(t *testing.T) {
	t.Parallel()
	ip := net.ParseIP

	tests := []struct {
		name   string
		ifaces []ifaceAddrs
		want   []string
	}{
		{
			name: "hassio bridge gateway is excluded, LAN kept",
			ifaces: []ifaceAddrs{
				{name: "hassio", up: true, ips: []net.IP{ip("172.30.232.1")}},
				{name: "eth0", up: true, ips: []net.IP{ip("192.168.1.50")}},
			},
			want: []string{"192.168.1.50"},
		},
		{
			name: "docker0 and br-<hex> excluded",
			ifaces: []ifaceAddrs{
				{name: "docker0", up: true, ips: []net.IP{ip("172.17.0.1")}},
				{name: "br-1a2b3c4d", up: true, ips: []net.IP{ip("172.18.0.1")}},
				{name: "veth0", up: true, ips: []net.IP{ip("172.19.0.2")}},
				{name: "end0", up: true, ips: []net.IP{ip("10.0.0.5")}},
			},
			want: []string{"10.0.0.5"},
		},
		{
			name: "private LAN in 172.16/12 on a real iface is kept (name-based, not range-based)",
			ifaces: []ifaceAddrs{
				{name: "eth0", up: true, ips: []net.IP{ip("172.20.5.10")}},
			},
			want: []string{"172.20.5.10"},
		},
		{
			name: "loopback, down and link-local dropped",
			ifaces: []ifaceAddrs{
				{name: "lo", up: true, loopback: true, ips: []net.IP{ip("127.0.0.1")}},
				{name: "eth1", up: false, ips: []net.IP{ip("192.168.5.5")}},
				{name: "eth0", up: true, ips: []net.IP{ip("169.254.1.1"), ip("192.168.1.50")}},
			},
			want: []string{"192.168.1.50"},
		},
		{
			name: "IPv4 sorted before IPv6",
			ifaces: []ifaceAddrs{
				{name: "eth0", up: true, ips: []net.IP{ip("2001:db8::1"), ip("192.168.1.50")}},
			},
			want: []string{"192.168.1.50", "2001:db8::1"},
		},
		{
			name:   "only container interfaces → empty (caller falls back)",
			ifaces: []ifaceAddrs{{name: "docker0", up: true, ips: []net.IP{ip("172.17.0.1")}}},
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterAdvertiseIPs(tc.ifaces)
			if !slices.Equal(got, tc.want) {
				t.Errorf("filterAdvertiseIPs() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRoutableAdvertiseIPs is a smoke test: it must not panic and must
// never return a loopback or link-local address.
func TestRoutableAdvertiseIPs(t *testing.T) {
	t.Parallel()
	for _, s := range routableAdvertiseIPs() {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Errorf("routableAdvertiseIPs returned unparseable %q", s)
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
			t.Errorf("routableAdvertiseIPs returned non-routable %q", s)
		}
	}
}
