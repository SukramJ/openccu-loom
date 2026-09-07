// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Wire-shape tests for the JSON-RPC calls in ccu_extended.go whose argument
// names and reply keys are fixed by the CCU firmware's method table
// (www/api/methods.conf) and the method scripts it dispatches to.

package backends

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCcuGetInstallModePassesInterfaceArgument pins the mandatory `interface`
// argument: the firmware declares Interface.getInstallMode with
// ARGUMENTS {_session_id_ interface} and answers jsonrpc_error 402
// "missing argument (interface)" when it is absent.
func TestCcuGetInstallModePassesInterfaceArgument(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: int(60)}
	b := NewCcuBackendForInterface(hmenum.InterfaceHmIPRF, &fakeCaller{}, j, nil)
	if _, err := b.GetInstallMode(context.Background()); err != nil {
		t.Fatalf("GetInstallMode: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.getInstallMode" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 {
		t.Fatalf("args=%v, want exactly one params map carrying `interface`", args)
	}
	params, ok := args[0].(map[string]any)
	if !ok {
		t.Fatalf("args[0]=%T, want map[string]any", args[0])
	}
	if got := params["interface"]; got != string(hmenum.InterfaceHmIPRF) {
		t.Fatalf("params[interface]=%v, want %q", got, string(hmenum.InterfaceHmIPRF))
	}
}

// TestCcuGetAllRoomsReadsChannelIds pins the reply key: room/getall.tcl emits
// "channelIds" (channel ISE-IDs), never "channels".
func TestCcuGetAllRoomsReadsChannelIds(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"name": "Wohnzimmer", "channelIds": []any{"1234", "1235"}},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	rooms, err := b.GetAllRooms(context.Background())
	if err != nil {
		t.Fatalf("GetAllRooms: %v", err)
	}
	if got := rooms["Wohnzimmer"]; len(got) != 2 || got[0] != "1234" {
		t.Fatalf("rooms[Wohnzimmer]=%v, want the two channel ISE-IDs", got)
	}
}

// TestCcuGetAllFunctionsReadsChannelIds mirrors the room test for
// subsection/getall.tcl, which emits the same "channelIds" key.
func TestCcuGetAllFunctionsReadsChannelIds(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"name": "Heizung", "channelIds": []any{"4711"}},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	fns, err := b.GetAllFunctions(context.Background())
	if err != nil {
		t.Fatalf("GetAllFunctions: %v", err)
	}
	if got := fns["Heizung"]; len(got) != 1 || got[0] != "4711" {
		t.Fatalf("functions[Heizung]=%v, want the channel ISE-ID", got)
	}
}
