// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCUxDUsesBINRPCBackend locks the SPECIFICATION §9.3 rule that
// CUxD is served by the BIN-RPC-speaking CuxdBackend, not by the
// CCU's XML-RPC backend.
func TestCUxDUsesBINRPCBackend(t *testing.T) {
	if kind := backends.KindFor(hmenum.InterfaceCUxD); kind != backends.KindCUxD {
		t.Fatalf("CUxD resolves to %s, want KindCUxD", kind)
	}
	caps := backends.CapabilityFor(backends.KindCUxD)
	if !caps.RPCCallback {
		t.Fatal("CUxD must support RPC callback (BIN-RPC)")
	}
	if !caps.PingPong {
		t.Fatal("CUxD must support ping/pong")
	}
}

// TestXMLRPCInterfacesUseCCUBackend pins CCU-native interfaces to the
// CcuBackend kind.
func TestXMLRPCInterfacesUseCCUBackend(t *testing.T) {
	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceHmIPRF, hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired, hmenum.InterfaceVirtualDevices,
	} {
		if backends.KindFor(iface) != backends.KindCCU {
			t.Errorf("%s → %s, want KindCCU", iface, backends.KindFor(iface))
		}
	}
}

// TestJSONRPCOnlyInterfacesEmpty verifies that no interface is classified
// as "JSON-RPC only / pull-only" — CCU-Jack was removed.
func TestJSONRPCOnlyInterfacesEmpty(t *testing.T) {
	if len(hmenum.JSONRPCOnlyInterfaces) != 0 {
		t.Fatalf("JSONRPCOnlyInterfaces must be empty, got %d entries", len(hmenum.JSONRPCOnlyInterfaces))
	}
}

// TestHomegearBackendCapabilities pins the SPECIFICATION §9.2 statement:
// Homegear is XML-RPC-only. Push (RPC callback), ping/pong, and
// ListDevices work; firmware update / programs / sysvars (CCU-side)
// are not supported.
func TestHomegearBackendCapabilities(t *testing.T) {
	caps := backends.CapabilityFor(backends.KindHomegear)
	if !caps.RPCCallback {
		t.Fatal("Homegear must support RPC callback")
	}
	// PingPong is a CCU-specific liveness extension; Homegear does not
	// expose it. Pinning it here would force the daemon to schedule a
	// ping path the backend cannot service, so the contract pins the
	// absence.
	if caps.PingPong {
		t.Fatal("Homegear must NOT advertise ping/pong (CCU-specific extension)")
	}
	if !caps.ListDevices {
		t.Fatal("Homegear must support ListDevices")
	}
	if caps.FirmwareUpdate {
		t.Fatal("Homegear is XML-RPC-only and must NOT advertise firmware updates")
	}
	if caps.GetAllPrograms || caps.GetAllSysvars {
		t.Fatal("Homegear has no CCU-side program/sysvar surface")
	}
	if caps.RequiresPeriodicRefresh {
		t.Fatal("Homegear pushes — must not require periodic refresh")
	}
}
