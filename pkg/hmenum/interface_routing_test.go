// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

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
