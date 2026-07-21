// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_CcuBackend_SetScriptRunner_CalledInWiring pins that ccu_wiring.go
// calls SetScriptRunner on the CcuBackend after construction. Without this
// call, ReGa-backed operations (CreateBackupAndDownload, GetServiceMessages
// via ReGa, AcceptDeviceInInbox via ReGa, TriggerFirmwareUpdate via ReGa)
// return ErrUnsupported in production.
func TestPin_CcuBackend_SetScriptRunner_CalledInWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"ccuBackend", "SetScriptRunner",
	)
}

// TestPin_CcuBackend_SetDownloadFirmwareTransport_CalledInWiring pins that
// ccu_wiring.go calls SetDownloadFirmwareTransport on the CcuBackend after
// construction. Without this call, DownloadFirmware and
// CreateBackupAndDownload return ErrUnsupported in production because the
// base URL and session-ID provider are never wired.
func TestPin_CcuBackend_SetDownloadFirmwareTransport_CalledInWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"ccuBackend", "SetDownloadFirmwareTransport",
	)
}

// TestPin_CcuBackend_SetRenameDeviceFn_WiredInCCUWiring pins that
// ccu_wiring.go wires the per-central rename hook via SetRenameDeviceFn.
// Without this call, device and channel renames only mutate the in-memory
// model and are lost on the next device reload — never reaching the CCU's
// Device.setName / Channel.setName JSON-RPC surface.
func TestPin_CcuBackend_SetRenameDeviceFn_WiredInCCUWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"unit", "SetRenameDeviceFn",
	)
}

// TestPin_CcuBackend_GetIseIDByAddress_UsedInCCUWiring pins that the rename
// hook resolves the ReGa ISE-ID before calling setName. Skipping the lookup
// would pass a raw wire address where the CCU expects a numeric ISE-ID.
func TestPin_CcuBackend_GetIseIDByAddress_UsedInCCUWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"renameBackend", "GetIseIDByAddress",
	)
}

// TestPin_wireCUxDInterface_CalledInCCUWiring pins that ccu_wiring.go calls
// wireCUxDInterface for CUxD interfaces.  Removing this call would silently
// fall through to the XML-RPC path, violating the CUxD-must-use-BIN-RPC
// contract.
func TestPin_wireCUxDInterface_CalledInCCUWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"internal/central/adapter", "wireCUxDInterface",
	)
}

// TestPin_SetInstallModeHMIP_InCcuBackend pins that ccu_extended.go passes
// the JSON-RPC method name "Interface.setInstallModeHMIP" for HmIP-RF
// install-mode requests.  HmIP-RF requires this distinct method; falling back
// to the generic Interface.setInstallMode raises an IllegalArgumentException
// on the Java HMIPServer.
func TestPin_SetInstallModeHMIP_InCcuBackend(t *testing.T) {
	contract.MustFindStringLiteralInFile(
		t,
		"internal/client/backends/ccu_extended.go",
		"Interface.setInstallModeHMIP",
	)
}

// TestPin_FactoryInput_JSONRPCField_SetInCCUWiring pins that ccu_wiring.go
// sets the JSONRPC field of backends.FactoryInput.  If omitted, CCU backends
// silently fall back to XML-RPC-only mode, losing JSON-RPC-exclusive
// operations (e.g. install-mode via JSON-RPC).
func TestPin_FactoryInput_JSONRPCField_SetInCCUWiring(t *testing.T) {
	contract.MustFindStructLiteralField(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"FactoryInput", "JSONRPC",
	)
}
