// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Guard: no source in this package may name a JSON-RPC method the CCU
// firmware does not implement.

package backends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// absentJSONRPCMethods lists CCU JSON-RPC method names this package must not
// name. None of them exists in the firmware's method table, so homematic.cgi
// answers jsonrpc_error 401 ("method not found") for every one — the call can
// never succeed on any CCU.
//
// Verified against ../OpenCCU-Base/www/api/methods.conf (the stock method
// table homematic.cgi's getMethod consults) with an anchored match on the
// method-name line; the names the daemon does use on live paths
// (Interface.setValue, Room.getAll, Program.get, …) match there.
var absentJSONRPCMethods = []string{
	"Interface.setInstallMode\"",
	"Interface.acceptNewDevice",
	"Interface.acceptDevice",
	"Interface.triggerFirmwareUpdate",
	"Interface.getAllDeviceData",
	"Program.assignProgramIDs",
	"Program.deleteProgramID",
	"Program.readProgram",
	"Program.updateProgram",
	"Program.setActive",
	"Program.getByID",
	"Metadata.setMetadata",
	"Metadata.getMetadata",
	"Metadata.deleteMetadata",
	"Room.getChannelIDs",
	"Function.getChannelIDs",
	"Device.getIseIDByAddress",
	"Alarm.acknowledge",
	"Message.acknowledge",
	"Message.getAll",
	"System.runFirmwareUpdate",
}

// assertNoAbsentJSONRPCMethod fails when any Go source in dir names a method
// from [absentJSONRPCMethods].
func assertNoAbsentJSONRPCMethod(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range absentJSONRPCMethods {
			if strings.Contains(string(src), m) {
				t.Errorf("%s names %q, a JSON-RPC method the CCU's method table does not contain: the call answers jsonrpc_error 401 on every CCU", name, strings.TrimSuffix(m, "\""))
			}
		}
	}
}

// TestBackendsNamesNoAbsentJSONRPCMethod pins the package's wire vocabulary to
// the firmware's method table.
func TestBackendsNamesNoAbsentJSONRPCMethod(t *testing.T) {
	t.Parallel()
	assertNoAbsentJSONRPCMethod(t, ".")
}
