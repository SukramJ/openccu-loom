// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for all Operations implementations in ccu_extended.go:
// GetInstallMode, SetInstallMode, GetServiceMessages, SuppressServiceMessage,
// GetAlarmMessages, GetAllRooms, GetAllFunctions, RenameDevice, RenameChannel,
// AcceptDeviceInInbox, ExecuteProgram, GetSystemVariable, GetAllSystemVariables,
// GetAllDeviceData, GetDeviceDetails, GetDeviceDescription,
// CreateBackupAndDownload, TriggerFirmwareUpdate, DeleteSystemVariable,
// GetIseIDByAddress, GetLinkInfo, SetLinkInfo, GetSuppressedServiceMessages,
// HasProgramIDs, TestDevice.

package backends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// GetInstallMode
// ---------------------------------------------------------------------------

func TestCcuGetInstallModeNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetInstallMode(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetInstallModeReturnsInt(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: int(60)}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	secs, err := b.GetInstallMode(context.Background())
	if err != nil {
		t.Fatalf("GetInstallMode: %v", err)
	}
	if secs != 60 {
		t.Fatalf("secs=%d, want 60", secs)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "Interface.getInstallMode" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuGetInstallModeReturnsFloat64(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: float64(30)}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	secs, err := b.GetInstallMode(context.Background())
	if err != nil {
		t.Fatalf("GetInstallMode: %v", err)
	}
	if secs != 30 {
		t.Fatalf("secs=%d, want 30", secs)
	}
}

func TestCcuGetInstallModeUnknownTypeIsZero(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: "unexpected"}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	secs, err := b.GetInstallMode(context.Background())
	if err != nil {
		t.Fatalf("GetInstallMode: %v", err)
	}
	if secs != 0 {
		t.Fatalf("secs=%d, want 0", secs)
	}
}

// ---------------------------------------------------------------------------
// SetInstallMode
// ---------------------------------------------------------------------------

func TestCcuSetInstallModeNoXML(t *testing.T) {
	t.Parallel()
	// BidCos-path (default ifaceType) requires the XML caller.
	b := NewCcuBackend(nil, &fakeCaller{}, nil)
	err := b.SetInstallMode(context.Background(), true, 60, 1, "")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuSetInstallModeWithAddressDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, &fakeCaller{}, nil)
	if err := b.SetInstallMode(context.Background(), true, 60, 1, "AABBCCDD"); err != nil {
		t.Fatalf("SetInstallMode: %v", err)
	}
	method, args, ok := loadArgs(x)
	if !ok || method != "setInstallMode" {
		t.Fatalf("method=%s", method)
	}
	// XML-RPC positional args: (on bool, duration int, deviceAddress string)
	if len(args) != 3 {
		t.Fatalf("want 3 positional args, got %d: %v", len(args), args)
	}
	if args[0] != true {
		t.Fatalf("on=%v, want true", args[0])
	}
	if args[1] != 60 {
		t.Fatalf("duration=%v, want 60", args[1])
	}
	if args[2] != "AABBCCDD" {
		t.Fatalf("address=%v, want AABBCCDD", args[2])
	}
}

func TestCcuSetInstallModeWithoutAddress(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, &fakeCaller{}, nil)
	if err := b.SetInstallMode(context.Background(), false, 0, 0, ""); err != nil {
		t.Fatalf("SetInstallMode: %v", err)
	}
	method, args, ok := loadArgs(x)
	if !ok || method != "setInstallMode" {
		t.Fatalf("method=%s", method)
	}
	// XML-RPC positional args: (on bool, duration int, mode int)
	if len(args) != 3 {
		t.Fatalf("want 3 positional args, got %d: %v", len(args), args)
	}
	if args[0] != false {
		t.Fatalf("on=%v, want false", args[0])
	}
}

// ---------------------------------------------------------------------------
// SetInstallModeLocal — keyserver-less HmIP LOCAL teach-in
// ---------------------------------------------------------------------------

// TestCcuBackendSetInstallModeLocalPayload pins both the JSON-RPC method
// name and the exact full param map SetInstallModeLocal sends: every key
// must be present (the CCU-side wrapper dereferences all of them
// unconditionally) and "keymode"/"installMode" carry the exact casing the
// wire contract requires.
func TestCcuBackendSetInstallModeLocalPayload(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackendForInterface(hmenum.InterfaceHmIPRF, &fakeCaller{}, j, nil)
	if err := b.SetInstallModeLocal(context.Background(), 300, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19"); err != nil {
		t.Fatalf("SetInstallModeLocal: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.setInstallModeHMIP" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 {
		t.Fatalf("want 1 arg (params map), got %d: %v", len(args), args)
	}
	params, ok := args[0].(map[string]any)
	if !ok {
		t.Fatalf("params not a map: %T", args[0])
	}
	want := map[string]any{
		"interface":   "HmIP-RF",
		"on":          "true",
		"time":        300,
		"installMode": "LOCAL",
		"address":     "3014F711A061A7D569892A67",
		"key":         "0110C8531D0952D8D73E1194E95B5F19",
		"keymode":     "LOCAL",
	}
	if len(params) != len(want) {
		t.Fatalf("params has %d keys, want %d: %v", len(params), len(want), params)
	}
	for k, v := range want {
		if got := params[k]; got != v {
			t.Errorf("params[%q] = %v, want %v", k, got, v)
		}
	}
}

// TestCcuBackendSetInstallModeLocalNonHmIP verifies that any non-HmIP-RF
// interface refuses the LOCAL teach-in with ErrUnsupported — the CCU's
// setInstallModeHMIP wrapper only exists for HmIP.
func TestCcuBackendSetInstallModeLocalNonHmIP(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackendForInterface(hmenum.InterfaceBidCosRF, &fakeCaller{}, j, nil)
	err := b.SetInstallModeLocal(context.Background(), 60, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	if j.called.Load() != 0 {
		t.Fatal("SetInstallModeLocal must not call JSON-RPC for a non-HmIP interface")
	}
}

// TestCcuBackendSetInstallModeLocalNoJSON verifies that a HmIP-RF backend
// without a wired JSON-RPC caller also refuses with ErrUnsupported.
func TestCcuBackendSetInstallModeLocalNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackendForInterface(hmenum.InterfaceHmIPRF, &fakeCaller{}, nil, nil)
	err := b.SetInstallModeLocal(context.Background(), 60, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

// TestCuxdBackendSetInstallModeLocalUnsupported verifies the CUxD stub:
// install mode (LOCAL or otherwise) is CCU-only.
func TestCuxdBackendSetInstallModeLocalUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SetInstallModeLocal(context.Background(), 60, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

// TestHomegearBackendSetInstallModeLocalUnsupported verifies the Homegear
// stub: no HmIP JSON-RPC surface exists on Homegear.
func TestHomegearBackendSetInstallModeLocalUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	err := b.SetInstallModeLocal(context.Background(), 60, "3014F711A061A7D569892A67", "0110C8531D0952D8D73E1194E95B5F19")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetServiceMessages
// ---------------------------------------------------------------------------

func TestCcuGetServiceMessagesNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetServiceMessages(context.Background(), "")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetServiceMessagesWithType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{map[string]any{"msg": "low battery"}}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetServiceMessages(context.Background(), "LOWBAT")
	if err != nil {
		t.Fatalf("GetServiceMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Message.getAll" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["type"] != "LOWBAT" {
		t.Fatalf("type=%v", params["type"])
	}
}

func TestCcuGetServiceMessagesWithoutType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetServiceMessages(context.Background(), "")
	if err != nil {
		t.Fatalf("GetServiceMessages empty type: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("len=%d, want 0", len(out))
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Message.getAll" {
		t.Fatalf("method=%s", method)
	}
	// Without a type the method is called without params.
	if len(args) != 0 {
		t.Fatalf("expected no args, got %v", args)
	}
}

// ---------------------------------------------------------------------------
// SuppressServiceMessage
// ---------------------------------------------------------------------------

func TestCcuSuppressServiceMessageNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	err := b.SuppressServiceMessage(context.Background(), "ADDR:1", "LOWBAT", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuSuppressServiceMessageDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SuppressServiceMessage(context.Background(), "AABBCCDD:1", "LOWBAT", true); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.suppressServiceMessages" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["channelAddress"] != "AABBCCDD:1" || params["parameterId"] != "LOWBAT" || params["suppress"] != true {
		t.Fatalf("params=%v", params)
	}
}

// ---------------------------------------------------------------------------
// GetAlarmMessages
// ---------------------------------------------------------------------------

func TestCcuGetAlarmMessagesNoRega(t *testing.T) {
	t.Parallel()
	// Without a ScriptRunner the operation must return ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAlarmMessages(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAlarmMessagesDispatch(t *testing.T) {
	t.Parallel()
	// GetAlarmMessages routes through the ReGa script engine; wire a
	// ScriptRunner that returns a list of alarm messages in snake_case.
	sr := &fakeScriptRunner{
		rawJSON: `[{"id":"al1","name":"Smoke Alarm","description":"Kitchen","device_name":"Detector A","address":"ABC:1"}]`,
	}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(sr)
	out, err := b.GetAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("GetAlarmMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	if sr.called.Load() != 1 {
		t.Fatalf("ScriptRunner.RunJSON call count = %d, want 1", sr.called.Load())
	}
}

// ---------------------------------------------------------------------------
// GetAllRooms
// ---------------------------------------------------------------------------

func TestCcuGetAllRoomsNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAllRooms(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAllRoomsDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{
			"name":     "Wohnzimmer",
			"channels": []any{"AABBCCDD:1", "AABBCCDD:2"},
		},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	rooms, err := b.GetAllRooms(context.Background())
	if err != nil {
		t.Fatalf("GetAllRooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("rooms len=%d, want 1", len(rooms))
	}
	if len(rooms["Wohnzimmer"]) != 2 {
		t.Fatalf("channels=%v", rooms["Wohnzimmer"])
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "Room.getAll" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuGetAllRoomsSkipsEmptyName(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"name": "", "channels": []any{"ADDR:1"}},
		map[string]any{"name": "Küche", "channels": []any{"ADDR:2"}},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	rooms, err := b.GetAllRooms(context.Background())
	if err != nil {
		t.Fatalf("GetAllRooms: %v", err)
	}
	if _, ok := rooms[""]; ok {
		t.Fatal("empty-name room must be skipped")
	}
	if _, ok := rooms["Küche"]; !ok {
		t.Fatal("Küche must be present")
	}
}

// ---------------------------------------------------------------------------
// GetAllFunctions
// ---------------------------------------------------------------------------

func TestCcuGetAllFunctionsNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAllFunctions(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAllFunctionsDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{
			"name":     "Heizung",
			"channels": []any{"DDEEFF:1"},
		},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	fns, err := b.GetAllFunctions(context.Background())
	if err != nil {
		t.Fatalf("GetAllFunctions: %v", err)
	}
	if fns["Heizung"][0] != "DDEEFF:1" {
		t.Fatalf("functions=%v", fns)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "Subsection.getAll" {
		t.Fatalf("method=%s", method)
	}
}

// ---------------------------------------------------------------------------
// RenameDevice
// ---------------------------------------------------------------------------

func TestCcuRenameDeviceNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.RenameDevice(context.Background(), 42, "NewName")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuRenameDeviceDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.RenameDevice(context.Background(), 42, "Schalter")
	if err != nil {
		t.Fatalf("RenameDevice: %v", err)
	}
	if !ok2 {
		t.Fatal("RenameDevice should return true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Device.setName" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["id"] != "42" || params["name"] != "Schalter" {
		t.Fatalf("params=%v", params)
	}
}

func TestCcuRenameDeviceError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("not found")
	j := &fakeCaller{err: sentinel}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.RenameDevice(context.Background(), 99, "X")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if ok2 {
		t.Fatal("ok should be false on error")
	}
}

// ---------------------------------------------------------------------------
// RenameChannel
// ---------------------------------------------------------------------------

func TestCcuRenameChannelNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.RenameChannel(context.Background(), 7, "CH")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuRenameChannelDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.RenameChannel(context.Background(), 7, "Kanal")
	if err != nil {
		t.Fatalf("RenameChannel: %v", err)
	}
	if !ok2 {
		t.Fatal("RenameChannel should return true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Channel.setName" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["id"] != "7" || params["name"] != "Kanal" {
		t.Fatalf("params=%v", params)
	}
}

// ---------------------------------------------------------------------------
// AcceptDeviceInInbox
// ---------------------------------------------------------------------------

func TestCcuAcceptDeviceInInboxNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.AcceptDeviceInInbox(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuAcceptDeviceInInboxDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.AcceptDeviceInInbox(context.Background(), "AABBCCDD")
	if err != nil {
		t.Fatalf("AcceptDeviceInInbox: %v", err)
	}
	if !ok2 {
		t.Fatal("ok should be true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.acceptDevice" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["address"] != "AABBCCDD" {
		t.Fatalf("address=%v", params["address"])
	}
}

// ---------------------------------------------------------------------------
// ExecuteProgram
// ---------------------------------------------------------------------------

func TestCcuExecuteProgramNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.ExecuteProgram(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuExecuteProgramDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.ExecuteProgram(context.Background(), "77")
	if err != nil {
		t.Fatalf("ExecuteProgram: %v", err)
	}
	if !ok2 {
		t.Fatal("ok should be true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Program.execute" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["id"] != "77" {
		t.Fatalf("id=%v", params["id"])
	}
}

// ---------------------------------------------------------------------------
// GetSystemVariable
// ---------------------------------------------------------------------------

func TestCcuGetSystemVariableNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetSystemVariable(context.Background(), "sv1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetSystemVariableDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: float64(21.5)}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	val, err := b.GetSystemVariable(context.Background(), "temp")
	if err != nil {
		t.Fatalf("GetSystemVariable: %v", err)
	}
	if val.(float64) != 21.5 {
		t.Fatalf("val=%v", val)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.getValueByName" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "temp" {
		t.Fatalf("name=%v", params["name"])
	}
}

// ---------------------------------------------------------------------------
// GetAllSystemVariables
// ---------------------------------------------------------------------------

func TestCcuGetAllSystemVariablesNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAllSystemVariables(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAllSystemVariablesDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"name": "sv1", "value": "true"},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetAllSystemVariables(context.Background())
	if err != nil {
		t.Fatalf("GetAllSystemVariables: %v", err)
	}
	if len(out) != 1 || out[0]["name"] != "sv1" {
		t.Fatalf("out=%v", out)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "SysVar.getAll" {
		t.Fatalf("method=%s", method)
	}
}

// ---------------------------------------------------------------------------
// GetAllDeviceData
// ---------------------------------------------------------------------------

func TestCcuGetAllDeviceDataNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAllDeviceData(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAllDeviceDataDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{
		"AABBCCDD:1": map[string]any{"STATE": true},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	data, err := b.GetAllDeviceData(context.Background())
	if err != nil {
		t.Fatalf("GetAllDeviceData: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("data len=%d, want 1", len(data))
	}
	if data["AABBCCDD:1"]["STATE"] != true {
		t.Fatalf("STATE=%v", data["AABBCCDD:1"]["STATE"])
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "Interface.getAllDeviceData" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuGetAllDeviceDataBadType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: "not a map"}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	_, err := b.GetAllDeviceData(context.Background())
	if err == nil {
		t.Fatal("expected error for bad type, got nil")
	}
}

// ---------------------------------------------------------------------------
// GetDeviceDetails
// ---------------------------------------------------------------------------

func TestCcuGetDeviceDetailsNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetDeviceDetails(context.Background(), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetDeviceDetailsDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"address": "AABBCCDD", "name": "Thermometer"},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetDeviceDetails(context.Background(), []string{"AABBCCDD"})
	if err != nil {
		t.Fatalf("GetDeviceDetails: %v", err)
	}
	if len(out) != 1 || out[0]["address"] != "AABBCCDD" {
		t.Fatalf("out=%v", out)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "Device.listAllDetail" {
		t.Fatalf("method=%s", method)
	}
}

// ---------------------------------------------------------------------------
// GetDeviceDescription (XML-RPC)
// ---------------------------------------------------------------------------

func TestCcuGetDeviceDescriptionNoXML(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, &fakeCaller{}, nil)
	_, err := b.GetDeviceDescription(context.Background(), "AABBCCDD")
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestCcuGetDeviceDescriptionDispatch(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: map[string]any{"ADDRESS": "AABBCCDD", "TYPE": "HmIP-STH"}}
	b := NewCcuBackend(x, nil, nil)
	desc, err := b.GetDeviceDescription(context.Background(), "AABBCCDD")
	if err != nil {
		t.Fatalf("GetDeviceDescription: %v", err)
	}
	if desc["ADDRESS"] != "AABBCCDD" {
		t.Fatalf("ADDRESS=%v", desc["ADDRESS"])
	}
	method, args, ok := loadArgs(x)
	if !ok || method != "getDeviceDescription" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 || args[0] != "AABBCCDD" {
		t.Fatalf("args=%v", args)
	}
}

func TestCcuGetDeviceDescriptionBadType(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: "not a map"}
	b := NewCcuBackend(x, nil, nil)
	_, err := b.GetDeviceDescription(context.Background(), "AABBCCDD")
	if err == nil {
		t.Fatal("expected error for bad type, got nil")
	}
}

// ---------------------------------------------------------------------------
// CreateBackupAndDownload
// ---------------------------------------------------------------------------

// multiScriptRunner sequences through its replies in order: first call gets
// replies[0], second gets replies[1], and so on. After the last reply, every
// subsequent call returns the last reply.
type multiScriptRunner struct {
	replies []string
	idx     int
}

func (m *multiScriptRunner) Run(_ context.Context, _ hmenum.RegaScript, _ map[string]string) (string, error) {
	if m.idx < len(m.replies) {
		r := m.replies[m.idx]
		m.idx++
		return r, nil
	}
	return m.replies[len(m.replies)-1], nil
}

func (m *multiScriptRunner) RunJSON(ctx context.Context, script hmenum.RegaScript, params map[string]string, v any) error {
	raw, err := m.Run(ctx, script, params)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	return jsonUnmarshal([]byte(raw), v)
}

func TestCcuCreateBackupAndDownloadNoScriptRunner(t *testing.T) {
	t.Parallel()
	// No ScriptRunner wired → ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetDownloadFirmwareTransport("http://ccu", http.DefaultClient, func() string { return "sid" })
	_, err := b.CreateBackupAndDownload(context.Background(), 1, 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuCreateBackupAndDownloadNoTransport(t *testing.T) {
	t.Parallel()
	// No download transport wired → ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(&fakeScriptRunner{rawJSON: `{"success":true}`})
	_, err := b.CreateBackupAndDownload(context.Background(), 1, 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuCreateBackupAndDownloadHappyPath(t *testing.T) {
	t.Parallel()
	const wantBody = "BACKUP_CONTENT"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(wantBody))
	}))
	defer srv.Close()

	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"status":"running","message":""}`,
		`{"status":"completed","file":"/tmp/b.sbk","filename":"b.sbk","size":14}`,
	}}

	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)
	b.SetDownloadFirmwareTransport(srv.URL, srv.Client(), func() string { return "testsid" })

	data, err := b.CreateBackupAndDownload(context.Background(), 10, 1)
	if err != nil {
		t.Fatalf("CreateBackupAndDownload: %v", err)
	}
	if string(data) != wantBody {
		t.Fatalf("data=%q, want %q", data, wantBody)
	}
}

func TestCcuCreateBackupAndDownloadStartFailed(t *testing.T) {
	t.Parallel()
	runner := &fakeScriptRunner{
		rawJSON: `{"success":false,"status":"error","message":"CCU busy"}`,
	}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)
	b.SetDownloadFirmwareTransport("http://ccu", http.DefaultClient, func() string { return "sid" })

	_, err := b.CreateBackupAndDownload(context.Background(), 10, 1)
	if err == nil {
		t.Fatal("expected error when start reports failure")
	}
}

func TestCcuCreateBackupAndDownloadStatusFailed(t *testing.T) {
	t.Parallel()
	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"status":"running","message":""}`,
		`{"status":"failed","file":"","filename":"","size":0}`,
	}}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)
	b.SetDownloadFirmwareTransport("http://ccu", http.DefaultClient, func() string { return "sid" })

	_, err := b.CreateBackupAndDownload(context.Background(), 10, 1)
	if err == nil {
		t.Fatal("expected error when status reports failure")
	}
}

// TestCcuCreateBackupAndDownloadRejectsOversizedResponse verifies that a
// backup archive larger than maxDownloadResponseSize is rejected with an
// error instead of being buffered into memory in full. Not parallel: it
// temporarily lowers the package-level limit, which other tests in this
// package rely on at its production default.
func TestCcuCreateBackupAndDownloadRejectsOversizedResponse(t *testing.T) {
	original := maxDownloadResponseSize
	maxDownloadResponseSize = 4
	defer func() { maxDownloadResponseSize = original }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("BACKUP_CONTENT")) // 14 bytes, over the 4-byte test limit
	}))
	defer srv.Close()

	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"status":"running","message":""}`,
		`{"status":"completed","file":"/tmp/b.sbk","filename":"b.sbk","size":14}`,
	}}

	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)
	b.SetDownloadFirmwareTransport(srv.URL, srv.Client(), func() string { return "testsid" })

	_, err := b.CreateBackupAndDownload(context.Background(), 10, 1)
	if err == nil {
		t.Fatal("expected error for oversized backup archive, got nil")
	}
}

func TestCcuCreateBackupAndDownloadTimeout(t *testing.T) {
	t.Parallel()
	// Status always returns "running" so the loop hits the deadline.
	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"status":"running","message":""}`,
		`{"status":"running","file":"","filename":"","size":0}`,
	}}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)
	b.SetDownloadFirmwareTransport("http://ccu", http.DefaultClient, func() string { return "sid" })

	// maxWaitTime=1s, pollInterval=1s: first poll tick arrives at 1s, then
	// deadline check triggers immediately after.
	_, err := b.CreateBackupAndDownload(context.Background(), 1, 1)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ---------------------------------------------------------------------------
// TriggerFirmwareUpdate
// ---------------------------------------------------------------------------

func TestCcuTriggerFirmwareUpdateNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.TriggerFirmwareUpdate(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuTriggerFirmwareUpdateDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.TriggerFirmwareUpdate(context.Background())
	if err != nil {
		t.Fatalf("TriggerFirmwareUpdate: %v", err)
	}
	if !ok2 {
		t.Fatal("ok should be true on success")
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "System.runFirmwareUpdate" {
		t.Fatalf("method=%s", method)
	}
}

// ---------------------------------------------------------------------------
// RebootCCU
// ---------------------------------------------------------------------------

func TestCcuRebootCCUNoScriptRunner(t *testing.T) {
	t.Parallel()
	// Without a ScriptRunner the reboot has no wire path (no JSON-RPC reboot
	// method exists), so it must return ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	_, err := b.RebootCCU(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuRebootCCUDispatch(t *testing.T) {
	t.Parallel()
	sr := &fakeScriptRunner{rawJSON: `{"success":true,"message":"CCU reboot triggered"}`}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(sr)
	ok, err := b.RebootCCU(context.Background())
	if err != nil {
		t.Fatalf("RebootCCU: %v", err)
	}
	if !ok {
		t.Fatal("ok should be true on success")
	}
	if sr.lastScript != hmenum.RegaScriptRebootCCU {
		t.Fatalf("script = %q, want reboot_ccu", sr.lastScript)
	}
}

func TestCcuRebootCCUScriptError(t *testing.T) {
	t.Parallel()
	sr := &fakeScriptRunner{err: errors.New("rega down")}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(sr)
	if _, err := b.RebootCCU(context.Background()); err == nil {
		t.Fatal("expected error when the ReGa script fails")
	}
}

// ---------------------------------------------------------------------------
// DeleteSystemVariable
// ---------------------------------------------------------------------------

func TestCcuDeleteSystemVariableNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.DeleteSystemVariable(context.Background(), "sv1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuDeleteSystemVariableDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.DeleteSystemVariable(context.Background(), "sv1")
	if err != nil {
		t.Fatalf("DeleteSystemVariable: %v", err)
	}
	if !ok2 {
		t.Fatal("ok should be true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.deleteSysVarByName" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "sv1" {
		t.Fatalf("name=%v", params["name"])
	}
}

// ---------------------------------------------------------------------------
// GetIseIDByAddress
// ---------------------------------------------------------------------------

func TestCcuGetIseIDByAddressNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetIseIDByAddress(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetIseIDByAddressReturnsInt(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: int(1234)}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	id, err := b.GetIseIDByAddress(context.Background(), "AABBCCDD")
	if err != nil {
		t.Fatalf("GetIseIDByAddress: %v", err)
	}
	if id != 1234 {
		t.Fatalf("id=%d, want 1234", id)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.getIseIDByAddress" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["address"] != "AABBCCDD" {
		t.Fatalf("address=%v", params["address"])
	}
}

func TestCcuGetIseIDByAddressReturnsFloat64(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: float64(5678)}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	id, err := b.GetIseIDByAddress(context.Background(), "ADDR")
	if err != nil {
		t.Fatalf("GetIseIDByAddress float64: %v", err)
	}
	if id != 5678 {
		t.Fatalf("id=%d, want 5678", id)
	}
}

func TestCcuGetIseIDByAddressUnknownTypeIsZero(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: "unexpected"}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	id, err := b.GetIseIDByAddress(context.Background(), "ADDR")
	if err != nil {
		t.Fatalf("GetIseIDByAddress unknown: %v", err)
	}
	if id != 0 {
		t.Fatalf("id=%d, want 0", id)
	}
}

// ---------------------------------------------------------------------------
// GetLinkInfo
// ---------------------------------------------------------------------------

func TestCcuGetLinkInfoNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetLinkInfo(context.Background(), "HmIP-RF", "SENDER:1", "RECV:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetLinkInfoDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"name": "Treppe", "description": "auto"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	m, err := b.GetLinkInfo(context.Background(), "HmIP-RF", "SENDER:1", "RECV:1")
	if err != nil {
		t.Fatalf("GetLinkInfo: %v", err)
	}
	if m["name"] != "Treppe" {
		t.Fatalf("name=%v", m["name"])
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.getLinkInfo" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["interface"] != "HmIP-RF" || params["senderAddress"] != "SENDER:1" || params["receiverAddress"] != "RECV:1" {
		t.Fatalf("params=%v", params)
	}
}

func TestCcuGetLinkInfoNilReply(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	m, err := b.GetLinkInfo(context.Background(), "HmIP-RF", "S:1", "R:1")
	if err != nil {
		t.Fatalf("GetLinkInfo nil: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil map, got %v", m)
	}
}

// ---------------------------------------------------------------------------
// SetLinkInfo
// ---------------------------------------------------------------------------

func TestCcuSetLinkInfoNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.SetLinkInfo(context.Background(), "HmIP-RF", "S:1", "R:1", "name", "desc")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuSetLinkInfoDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ok2, err := b.SetLinkInfo(context.Background(), "HmIP-RF", "SENDER:1", "RECV:1", "Treppe", "auto")
	if err != nil {
		t.Fatalf("SetLinkInfo: %v", err)
	}
	if !ok2 {
		t.Fatal("ok should be true on success")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.setLinkInfo" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "Treppe" || params["description"] != "auto" {
		t.Fatalf("params=%v", params)
	}
}

// ---------------------------------------------------------------------------
// GetSuppressedServiceMessages
// ---------------------------------------------------------------------------

func TestCcuGetSuppressedServiceMessagesNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ADDR:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetSuppressedServiceMessagesDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{"LOWBAT", "UNREACH"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ids, err := b.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ADDR:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages: %v", err)
	}
	if len(ids) != 2 || ids[0] != "LOWBAT" || ids[1] != "UNREACH" {
		t.Fatalf("ids=%v", ids)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.getSuppressedServiceMessages" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["interface"] != "HmIP-RF" || params["channelAddress"] != "ADDR:1" {
		t.Fatalf("params=%v", params)
	}
}

func TestCcuGetSuppressedServiceMessagesNilReply(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ids, err := b.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ADDR:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages nil: %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil ids, got %v", ids)
	}
}

func TestCcuGetSuppressedServiceMessagesFiltersEmpty(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{"LOWBAT", "", "UNREACH"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ids, err := b.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ADDR:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids (empty filtered), got %d: %v", len(ids), ids)
	}
}

func TestCcuGetSuppressedServiceMessagesBadListType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: "not a list"}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	ids, err := b.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ADDR:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages bad type: %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil for bad type, got %v", ids)
	}
}

// ---------------------------------------------------------------------------
// HasProgramIDs
// ---------------------------------------------------------------------------

func TestCcuHasProgramIDsNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.HasProgramIDs(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuHasProgramIDsFound(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"id": "42"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	found, err := b.HasProgramIDs(context.Background(), "42")
	if err != nil {
		t.Fatalf("HasProgramIDs: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for non-nil reply")
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Program.getByID" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["id"] != "42" {
		t.Fatalf("id=%v", params["id"])
	}
}

func TestCcuHasProgramIDsNotFound(t *testing.T) {
	t.Parallel()
	// RPC error → not found (nilerr is intentional per the production code)
	j := &fakeCaller{err: errors.New("not found")}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	found, err := b.HasProgramIDs(context.Background(), "99")
	if err != nil {
		t.Fatalf("HasProgramIDs not-found should return nil err, got %v", err)
	}
	if found {
		t.Fatal("expected found=false for error reply")
	}
}

func TestCcuHasProgramIDsNilReply(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	found, err := b.HasProgramIDs(context.Background(), "99")
	if err != nil {
		t.Fatalf("HasProgramIDs nil: %v", err)
	}
	if found {
		t.Fatal("expected found=false for nil reply")
	}
}

// ---------------------------------------------------------------------------
// ListBidcosInterfaces
// ---------------------------------------------------------------------------

func TestCcuListBidcosInterfacesNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	if _, err := b.ListBidcosInterfaces(context.Background(), "BidCos-RF"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuListBidcosInterfacesDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{
			"address":     "OEQ1234567",
			"type":        "CCU2",
			"dutyCycle":   "27",
			"isConnected": true,
			"isDefault":   true,
		},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.ListBidcosInterfaces(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("ListBidcosInterfaces: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	if out[0]["address"] != "OEQ1234567" {
		t.Fatalf("address=%v", out[0]["address"])
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Interface.listBidcosInterfaces" {
		t.Fatalf("method=%s", method)
	}
	params, _ := args[0].(map[string]any)
	if params["interface"] != "BidCos-RF" {
		t.Fatalf("interface param=%v", params["interface"])
	}
}

func TestCcuListBidcosInterfacesError(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{err: errors.New("boom")}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if _, err := b.ListBidcosInterfaces(context.Background(), "BidCos-RF"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

// ---------------------------------------------------------------------------
// TestDevice — per-device communication/function test
// ---------------------------------------------------------------------------

func TestCcuTestDeviceNoRega(t *testing.T) {
	t.Parallel()
	// Without a ScriptRunner the operation must return ErrUnsupported —
	// there is no JSON-RPC method for the com-test, only the ReGa scripts.
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	_, err := b.TestDevice(context.Background(), "AABBCCDD", 1, 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuTestDeviceStartFailureReturnsError(t *testing.T) {
	t.Parallel()
	runner := &fakeScriptRunner{
		rawJSON: `{"success":false,"error":"device unreachable"}`,
	}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)

	_, err := b.TestDevice(context.Background(), "AABBCCDD", 1, 1)
	if err == nil {
		t.Fatal("expected error when start reports failure")
	}
	if !strings.Contains(err.Error(), "device unreachable") {
		t.Errorf("error should surface the CCU-reported message, got: %v", err)
	}
}

func TestCcuTestDeviceStartFailureDefaultMessage(t *testing.T) {
	t.Parallel()
	// No "error" field on a failed start: the backend substitutes a
	// generic message rather than returning an empty error string.
	runner := &fakeScriptRunner{rawJSON: `{"success":false}`}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)

	_, err := b.TestDevice(context.Background(), "AABBCCDD", 1, 1)
	if err == nil {
		t.Fatal("expected error when start reports failure")
	}
	if !strings.Contains(err.Error(), "communication test start failed") {
		t.Errorf("expected default failure message, got: %v", err)
	}
}

// TestCcuTestDeviceHappyPathPassesAfterPolling verifies the poll loop:
// the first poll_com_test reply reports passed:false, the second reports
// passed:true with a completed timestamp — TestDevice must keep polling
// until it sees passed:true and parse CompletedAt with the ReGa
// "%Y-%m-%d %H:%M:%S" layout.
func TestCcuTestDeviceHappyPathPassesAfterPolling(t *testing.T) {
	t.Parallel()
	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"started":"2026-07-22 10:00:00"}`,
		`{"passed":false}`,
		`{"passed":true,"completed":"2026-07-22 10:00:05"}`,
	}}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)

	// maxWaitSecs/pollIntervalSecs small so the two poll ticks (false, then
	// true) fire quickly — the test asserts behaviour, not real timing.
	result, err := b.TestDevice(context.Background(), "AABBCCDD", 1, 0.01)
	if err != nil {
		t.Fatalf("TestDevice: %v", err)
	}
	if !result.Passed {
		t.Fatalf("result=%+v, want Passed=true", result)
	}
	if result.TimedOut {
		t.Fatalf("result=%+v, want TimedOut=false", result)
	}
	if result.StartedAt.IsZero() {
		t.Error("StartedAt must be set")
	}
	wantCompleted := "2026-07-22 10:00:05"
	if got := result.CompletedAt.Format(comTestTimeLayout); got != wantCompleted {
		t.Errorf("CompletedAt=%q, want %q", got, wantCompleted)
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs=%d, want >= 0", result.DurationMs)
	}
}

// TestCcuTestDeviceTimeoutWhenNeverPasses verifies that a device that
// never reports passed:true surfaces TimedOut=true, Passed=false once the
// poll window elapses — small maxWaitSecs/pollIntervalSecs keep the test
// fast (tens of milliseconds) without faking the clock.
func TestCcuTestDeviceTimeoutWhenNeverPasses(t *testing.T) {
	t.Parallel()
	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"started":"2026-07-22 10:00:00"}`,
		`{"passed":false}`,
	}}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)

	start := time.Now()
	result, err := b.TestDevice(context.Background(), "AABBCCDD", 0.05, 0.01)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("TestDevice: %v", err)
	}
	if result.Passed {
		t.Fatalf("result=%+v, want Passed=false", result)
	}
	if !result.TimedOut {
		t.Fatalf("result=%+v, want TimedOut=true", result)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout test took %v, want well under the 2s test-suite budget", elapsed)
	}
}

// TestCcuTestDeviceDefaultsAppliedWhenZeroOrNegative verifies the
// maxWaitSecs<=0 / pollIntervalSecs<=0 fallback to 30s/2s does not panic
// or divide by zero — it exercises the branch via a context that is
// cancelled almost immediately so the test does not actually wait 2s.
func TestCcuTestDeviceDefaultsAppliedWhenZeroOrNegative(t *testing.T) {
	t.Parallel()
	runner := &multiScriptRunner{replies: []string{
		`{"success":true,"started":"2026-07-22 10:00:00"}`,
		`{"passed":false}`,
	}}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(runner)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := b.TestDevice(ctx, "AABBCCDD", 0, -1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded (proving the 2s default poll interval is in effect), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestDevice — CUxD / Homegear always ErrUnsupported (no ReGa surface)
// ---------------------------------------------------------------------------

func TestCuxdBackendTestDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.TestDevice(context.Background(), "CUX0001", 1, 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearBackendTestDeviceUnsupported(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.TestDevice(context.Background(), "HG0001", 1, 1)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
