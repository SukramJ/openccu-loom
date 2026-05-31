// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestPrimaryClientCandidateInterfaces(t *testing.T) {
	want := map[hmenum.Interface]struct{}{
		hmenum.InterfaceHmIPRF:      {},
		hmenum.InterfaceBidCosRF:    {},
		hmenum.InterfaceBidCosWired: {},
	}
	for iface := range want {
		if _, ok := hmenum.PrimaryClientCandidateInterfaces[iface]; !ok {
			t.Errorf("PrimaryClientCandidateInterfaces missing %s", iface)
		}
	}
	if len(hmenum.PrimaryClientCandidateInterfaces) != len(want) {
		t.Errorf("PrimaryClientCandidateInterfaces size = %d, want %d",
			len(hmenum.PrimaryClientCandidateInterfaces), len(want))
	}
}

func TestInterfaceRPCServerType_XMLRPCInterfaces(t *testing.T) {
	// XML-RPC interfaces must map to RPCServerTypeXMLRPC.
	xmlrpc := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceVirtualDevices,
	}
	for _, iface := range xmlrpc {
		got, ok := hmenum.InterfaceRPCServerType[iface]
		if !ok {
			t.Errorf("InterfaceRPCServerType missing %s", iface)
			continue
		}
		if got != hmenum.RPCServerTypeXMLRPC {
			t.Errorf("InterfaceRPCServerType[%s] = %q, want %q", iface, got, hmenum.RPCServerTypeXMLRPC)
		}
	}
}

func TestInterfaceRPCServerType_CUxDIsNone(t *testing.T) {
	// CUxD uses BIN-RPC in openccu-loom, so its server type is None.
	got := hmenum.InterfaceRPCServerType[hmenum.InterfaceCUxD]
	if got != hmenum.RPCServerTypeNone {
		t.Errorf("InterfaceRPCServerType[CUxD] = %q, want %q", got, hmenum.RPCServerTypeNone)
	}
}
