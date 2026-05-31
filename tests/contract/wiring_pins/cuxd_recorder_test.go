// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_CUxDWiring_InstallsSessionHook pins that cuxd_wiring.go sets the
// SessionRecorderHook field on the InterfaceClient Config. Without it CUxD
// BIN-RPC traffic is never forwarded to the session recorder, so a recorded
// replay would silently omit every CUxD call.
func TestPin_CUxDWiring_InstallsSessionHook(t *testing.T) {
	contract.MustFindStructLiteralField(
		t,
		"internal/central/adapter/cuxd_wiring.go",
		"Config", "SessionRecorderHook",
	)
}

// TestPin_CUxDWiring_RecordsAsBINRPC pins that the CUxD session hook records
// under session.RPCTypeBIN, not RPCTypeXML. CUxD speaks BIN-RPC; tagging its
// trace as XML would make a replay unable to tell the two transports apart
// (the divergence this distinct RPCType exists to prevent).
func TestPin_CUxDWiring_RecordsAsBINRPC(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/cuxd_wiring.go",
		"session", "RPCTypeBIN",
	)
}

// TestPin_CUxDWiring_ForwardsToRecordSession pins that the hook actually calls
// the cache coordinator's RecordSession — the bridge from a CCU call to the
// recorder.
func TestPin_CUxDWiring_ForwardsToRecordSession(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/cuxd_wiring.go",
		"cache", "RecordSession",
	)
}
