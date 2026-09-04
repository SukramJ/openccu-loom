// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	neturl "net/url"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestInterfaceTCPAddrFollowsTheInterfacesOwnPort pins the address the
// recovery coordinator's TCP probe dials.
//
// The probe decides whether the CCU is reachable at all. Dialing a fixed
// port instead of the interface's own means a BidCos-RF-only or a TLS
// central is probed on a port nothing listens on, so recovery never leaves
// the cooldown stage.
func TestInterfaceTCPAddrFollowsTheInterfacesOwnPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cc   config.CentralConfig
		ifc  hmenum.Interface
		want string
	}{
		{
			name: "bidcos plain",
			cc:   config.CentralConfig{Host: "ccu.example"},
			ifc:  hmenum.InterfaceBidCosRF,
			want: "ccu.example:2001",
		},
		{
			name: "bidcos tls",
			cc:   config.CentralConfig{Host: "ccu.example", TLS: true},
			ifc:  hmenum.InterfaceBidCosRF,
			want: "ccu.example:42001",
		},
		{
			name: "hmip with operator port override",
			cc: config.CentralConfig{
				Host:       "ccu.example",
				Interfaces: []config.InterfaceSpec{{Name: string(hmenum.InterfaceHmIPRF), Port: 32010}},
			},
			ifc:  hmenum.InterfaceHmIPRF,
			want: "ccu.example:32010",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := interfaceTCPAddr(tc.cc, tc.ifc); got != tc.want {
				t.Errorf("interfaceTCPAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInterfaceTCPAddrAgreesWithInterfaceURL ties the probe address to the
// endpoint the RPC calls actually use: wireInterface prefers the host it
// parses out of the endpoint and falls back to this helper, so the two must
// name the same port or the fallback silently probes elsewhere.
func TestInterfaceTCPAddrAgreesWithInterfaceURL(t *testing.T) {
	t.Parallel()

	ccs := []config.CentralConfig{
		{Host: "ccu.example"},
		{Host: "ccu.example", TLS: true},
		{Host: "ccu.example", Interfaces: []config.InterfaceSpec{{Name: string(hmenum.InterfaceHmIPRF), Port: 32010}}},
	}
	ifaces := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceVirtualDevices,
	}

	for _, cc := range ccs {
		for _, ifc := range ifaces {
			u, err := interfaceURL(cc, ifc)
			if err != nil {
				t.Fatalf("interfaceURL(%v, %s): %v", cc.TLS, ifc, err)
			}
			parsed, err := neturl.Parse(u)
			if err != nil {
				t.Fatalf("parse %q: %v", u, err)
			}
			if got := interfaceTCPAddr(cc, ifc); got != parsed.Host {
				t.Errorf("tls=%v %s: interfaceTCPAddr = %q, endpoint host = %q", cc.TLS, ifc, got, parsed.Host)
			}
		}
	}
}
