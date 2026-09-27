// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestOpenCCULiteBackendCapabilities pins the openccu-lite profile: the
// box proxies XML-RPC to the CCU's interface processes and pushes events,
// so device, link, install-mode and service-message-suppression work is
// available, while everything that lives in ReGa, the WebUI JSON-RPC or
// the CCU's system scripts is not. The lite kind is never chosen from an
// interface id.
func TestOpenCCULiteBackendCapabilities(t *testing.T) {
	if got := backends.KindOpenCCULite.String(); got != "openccu-lite" {
		t.Fatalf("KindOpenCCULite.String() = %q, want openccu-lite", got)
	}
	caps := backends.CapabilityFor(backends.KindOpenCCULite)
	available := map[string]bool{
		"RPCCallback": caps.RPCCallback, "PingPong": caps.PingPong, "ListDevices": caps.ListDevices,
		"FirmwareUpdate": caps.FirmwareUpdate, "ConfigRestore": caps.ConfigRestore,
		"ReplaceDevice": caps.ReplaceDevice, "SearchDevices": caps.SearchDevices,
		"TeamAssignment": caps.TeamAssignment, "DeleteDevice": caps.DeleteDevice,
		"InstallMode": caps.InstallMode, "InstallModeLocal": caps.InstallModeLocal,
		"LinkOperations":         caps.LinkOperations,
		"SuppressServiceMessage": caps.SuppressServiceMessage, "ValueListRead": caps.ValueListRead,
		"VirtualKey": caps.VirtualKey, "Metadata": caps.Metadata,
	}
	absent := map[string]bool{
		"GetAllPrograms": caps.GetAllPrograms, "GetAllSysvars": caps.GetAllSysvars,
		"CommunicationTest": caps.CommunicationTest, "AlarmMessages": caps.AlarmMessages,
		"Backup": caps.Backup, "CreateSystemVariable": caps.CreateSystemVariable,
		"DeleteSystemVariable": caps.DeleteSystemVariable, "ExecuteProgram": caps.ExecuteProgram,
		"InboxDevices": caps.InboxDevices, "SetProgramState": caps.SetProgramState,
		"SetSystemVariable": caps.SetSystemVariable, "Functions": caps.Functions,
		"Rooms": caps.Rooms, "Rename": caps.Rename, "IseIDLookup": caps.IseIDLookup,
		"RequiresPeriodicRefresh": caps.RequiresPeriodicRefresh,
		"ServiceMessages":         caps.ServiceMessages,
	}
	for name, v := range available {
		if !v {
			t.Errorf("openccu-lite must advertise %s", name)
		}
	}
	for name, v := range absent {
		if v {
			t.Errorf("openccu-lite must not advertise %s", name)
		}
	}
	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceHmIPRF, hmenum.InterfaceBidCosRF, hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices, hmenum.InterfaceCUxD,
	} {
		if backends.KindFor(iface) == backends.KindOpenCCULite {
			t.Errorf("KindFor(%s) selects openccu-lite; the kind comes from the system type only", iface)
		}
	}
}
