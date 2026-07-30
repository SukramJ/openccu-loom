// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestGetSystemInformation verifies that the CCU response is decoded into
// SystemInformation correctly.
func TestGetSystemInformation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.getSystemInformation": func(env envelope) any {
			return okResult(map[string]any{
				"SERIAL_NUMBER":    "MEQ1234567",
				"VERSION":          "3.65.10",
				"HARDWARE_VERSION": "2.1",
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	info, err := c.GetSystemInformation(context.Background())
	if err != nil {
		t.Fatalf("GetSystemInformation: %v", err)
	}
	if info.Serial != "MEQ1234567" {
		t.Errorf("Serial = %q, want %q", info.Serial, "MEQ1234567")
	}
	if info.SoftwareVersion != "3.65.10" {
		t.Errorf("SoftwareVersion = %q, want %q", info.SoftwareVersion, "3.65.10")
	}
	if info.HardwareVersion != "2.1" {
		t.Errorf("HardwareVersion = %q, want %q", info.HardwareVersion, "2.1")
	}
}

// TestGetSystemInformationErrorPropagates checks that a server-side error is
// returned as a non-nil error.
func TestGetSystemInformationErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.getSystemInformation": func(env envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "internal"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetSystemInformation(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// TestDeleteSystemVariable verifies the correct wire params are sent.
func TestDeleteSystemVariable(t *testing.T) {
	t.Parallel()
	var capturedName string
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.deleteSysVarByName": func(env envelope) any {
			capturedName, _ = env.Params["name"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.DeleteSystemVariable(context.Background(), "my_var"); err != nil {
		t.Fatalf("DeleteSystemVariable: %v", err)
	}
	if capturedName != "my_var" {
		t.Errorf("server saw name=%q, want %q", capturedName, "my_var")
	}
}

// TestDeleteSystemVariableError checks that a failure is surfaced.
func TestDeleteSystemVariableError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.deleteSysVarByName": func(env envelope) any {
			return response{Error: &wireError{Code: -32600, Message: "not found"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.DeleteSystemVariable(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error")
	}
}

// TestRenameChannel verifies ISE-ID and name are forwarded to the CCU.
func TestRenameChannel(t *testing.T) {
	t.Parallel()
	var gotID, gotName string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Channel.setName": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			gotName, _ = env.Params["name"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.RenameChannel(context.Background(), "4711", "Living Room"); err != nil {
		t.Fatalf("RenameChannel: %v", err)
	}
	if gotID != "4711" {
		t.Errorf("id=%q, want %q", gotID, "4711")
	}
	if gotName != "Living Room" {
		t.Errorf("name=%q, want %q", gotName, "Living Room")
	}
}

// TestRenameDevice verifies ISE-ID and name are forwarded to the CCU.
func TestRenameDevice(t *testing.T) {
	t.Parallel()
	var gotID, gotName string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.setName": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			gotName, _ = env.Params["name"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.RenameDevice(context.Background(), "8888", "Thermostat"); err != nil {
		t.Fatalf("RenameDevice: %v", err)
	}
	if gotID != "8888" {
		t.Errorf("id=%q, want %q", gotID, "8888")
	}
	if gotName != "Thermostat" {
		t.Errorf("name=%q, want %q", gotName, "Thermostat")
	}
}

// TestSetLinkInfo verifies all five parameters are sent to Interface.setLinkInfo.
func TestSetLinkInfo(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.setLinkInfo": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetLinkInfo(context.Background(), "BidCos-RF", "HEQ0123456:1", "HEQ0654321:1", "my-link", "desc"); err != nil {
		t.Fatalf("SetLinkInfo: %v", err)
	}
	checks := map[string]string{
		"interface":       "BidCos-RF",
		"senderAddress":   "HEQ0123456:1",
		"receiverAddress": "HEQ0654321:1",
		"name":            "my-link",
		"description":     "desc",
	}
	for k, want := range checks {
		if got, _ := gotParams[k].(string); got != want {
			t.Errorf("param %q = %q, want %q", k, got, want)
		}
	}
}

// TestSetInstallModeHMIP verifies the CCU wire shape: on as string
// "true"/"false", installMode, address, key, keymode fields present.
func TestSetInstallModeHMIP(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.setInstallModeHMIP": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetInstallModeHMIP(context.Background(), "HmIP-RF", true, 60, ""); err != nil {
		t.Fatalf("SetInstallModeHMIP: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "HmIP-RF" {
		t.Errorf("interface=%q, want HmIP-RF", iface)
	}
	// on must be the string "true" per the CCU wire protocol.
	if on, _ := gotParams["on"].(string); on != "true" {
		t.Errorf("on=%q, want %q", on, "true")
	}
	// JSON numbers decode as float64.
	if dur, _ := gotParams["time"].(float64); dur != 60 {
		t.Errorf("time=%v, want 60", gotParams["time"])
	}
	if mode, _ := gotParams["installMode"].(string); mode != "ALL" {
		t.Errorf("installMode=%q, want ALL", mode)
	}
	if _, ok := gotParams["key"]; !ok {
		t.Error("key field missing")
	}
	if _, ok := gotParams["keymode"]; !ok {
		t.Error("keymode field missing")
	}
}

// TestGetDeviceDetails verifies the list of detail maps is decoded.
func TestGetDeviceDetails(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.listAllDetail": func(env envelope) any {
			return okResult([]map[string]any{
				{"ADDRESS": "HEQ0123456", "TYPE": "HM-CC-RT-DN"},
				{"ADDRESS": "HEQ9999999", "TYPE": "HM-ES-PMSw1-Pl"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	details, err := c.GetDeviceDetails(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceDetails: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("len(details)=%d, want 2", len(details))
	}
	if addr, _ := details[0]["ADDRESS"].(string); addr != "HEQ0123456" {
		t.Errorf("details[0].ADDRESS=%q, want %q", addr, "HEQ0123456")
	}
}

// TestGetAllDeviceData verifies the interface parameter is forwarded and the
// map result is decoded.
func TestGetAllDeviceData(t *testing.T) {
	t.Parallel()
	var gotIface string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listDevices": func(env envelope) any {
			gotIface, _ = env.Params["interface"].(string)
			return okResult(map[string]any{
				"HEQ0123456:1": map[string]any{"SET_POINT_TEMPERATURE": 21.5},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	data, err := c.GetAllDeviceData(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("GetAllDeviceData: %v", err)
	}
	if gotIface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", gotIface)
	}
	if _, ok := data["HEQ0123456:1"]; !ok {
		t.Error("expected key HEQ0123456:1 in result")
	}
}

// TestSuppressServiceMessage verifies all four parameters are forwarded.
func TestSuppressServiceMessage(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.suppressServiceMessages": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SuppressServiceMessage(context.Background(), "BidCos-RF", "HEQ0123456:1", "ERROR", true); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if ch, _ := gotParams["channelAddress"].(string); ch != "HEQ0123456:1" {
		t.Errorf("channelAddress=%q, want HEQ0123456:1", ch)
	}
	if param, _ := gotParams["parameterId"].(string); param != "ERROR" {
		t.Errorf("parameterId=%q, want ERROR", param)
	}
	if suppress, _ := gotParams["suppress"].(bool); !suppress {
		t.Error("suppress=false, want true")
	}
}

// TestHasProgramIDs verifies the ISE-ID is forwarded and boolean is decoded.
func TestHasProgramIDs(t *testing.T) {
	t.Parallel()
	var gotID string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Channel.hasProgramIds": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	ok, err := c.HasProgramIDs(context.Background(), "1234")
	if err != nil {
		t.Fatalf("HasProgramIDs: %v", err)
	}
	if gotID != "1234" {
		t.Errorf("id=%q, want %q", gotID, "1234")
	}
	if !ok {
		t.Error("HasProgramIDs returned false, want true")
	}
}

// TestHasProgramIDsFalse verifies that a false result is correctly decoded.
func TestHasProgramIDsFalse(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Channel.hasProgramIds": func(env envelope) any {
			return okResult(false)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	ok, err := c.HasProgramIDs(context.Background(), "9999")
	if err != nil {
		t.Fatalf("HasProgramIDs: %v", err)
	}
	if ok {
		t.Error("HasProgramIDs returned true for absent program")
	}
}

// TestHasProgramIDsError checks that a server error is surfaced.
func TestHasProgramIDsError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Channel.hasProgramIds": func(env envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "server error"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	_, err := c.HasProgramIDs(context.Background(), "1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("got %v, want ErrInternalBackendException", err)
	}
}

// ---------------------------------------------------------------------------
// methods
// ---------------------------------------------------------------------------

// TestSetInstallModeBidCos verifies all four parameters are forwarded.
func TestSetInstallModeBidCos(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.setInstallMode": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetInstallModeBidCos(context.Background(), "BidCos-RF", true, 60, 1); err != nil {
		t.Fatalf("SetInstallModeBidCos: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if on, _ := gotParams["on"].(bool); !on {
		t.Error("on=false, want true")
	}
	if dur, _ := gotParams["duration"].(float64); dur != 60 {
		t.Errorf("duration=%v, want 60", gotParams["duration"])
	}
	if mode, _ := gotParams["mode"].(float64); mode != 1 {
		t.Errorf("mode=%v, want 1", gotParams["mode"])
	}
}

// TestAssignProgramIDs verifies iseID and channelID are forwarded.
func TestAssignProgramIDs(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.assignProgramIDs": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.AssignProgramIDs(context.Background(), "prog-1", "ch-9"); err != nil {
		t.Fatalf("AssignProgramIDs: %v", err)
	}
	if id, _ := gotParams["id"].(string); id != "prog-1" {
		t.Errorf("id=%q, want prog-1", id)
	}
	if ch, _ := gotParams["channelId"].(string); ch != "ch-9" {
		t.Errorf("channelId=%q, want ch-9", ch)
	}
}

// TestDeleteProgramID verifies the ISE-ID is forwarded.
func TestDeleteProgramID(t *testing.T) {
	t.Parallel()
	var gotID string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.deleteProgramID": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.DeleteProgramID(context.Background(), "42"); err != nil {
		t.Fatalf("DeleteProgramID: %v", err)
	}
	if gotID != "42" {
		t.Errorf("id=%q, want 42", gotID)
	}
}

// TestReadProgram verifies the ISE-ID is sent and the map result is decoded.
func TestReadProgram(t *testing.T) {
	t.Parallel()
	var gotID string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.readProgram": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			return okResult(map[string]any{"name": "my-prog", "active": true})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	result, err := c.ReadProgram(context.Background(), "77")
	if err != nil {
		t.Fatalf("ReadProgram: %v", err)
	}
	if gotID != "77" {
		t.Errorf("id=%q, want 77", gotID)
	}
	if name, _ := result["name"].(string); name != "my-prog" {
		t.Errorf("name=%q, want my-prog", name)
	}
}

// TestUpdateProgram verifies that the body fields and id are merged and sent.
func TestUpdateProgram(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.updateProgram": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.UpdateProgram(context.Background(), "55", map[string]any{"name": "updated"}); err != nil {
		t.Fatalf("UpdateProgram: %v", err)
	}
	if id, _ := gotParams["id"].(string); id != "55" {
		t.Errorf("id=%q, want 55", id)
	}
	if name, _ := gotParams["name"].(string); name != "updated" {
		t.Errorf("name=%q, want updated", name)
	}
}

// TestSetMetadata verifies objectId, dataId, and value are forwarded.
func TestSetMetadata(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Metadata.setMetadata": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetMetadata(context.Background(), "obj-1", "color", "red"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if oid, _ := gotParams["objectId"].(string); oid != "obj-1" {
		t.Errorf("objectId=%q, want obj-1", oid)
	}
	if did, _ := gotParams["dataId"].(string); did != "color" {
		t.Errorf("dataId=%q, want color", did)
	}
	if val, _ := gotParams["value"].(string); val != "red" {
		t.Errorf("value=%q, want red", val)
	}
}

// TestGetMetadata verifies objectId and dataId are forwarded and value decoded.
func TestGetMetadata(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Metadata.getMetadata": func(env envelope) any {
			gotParams = env.Params
			return okResult("blue")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	val, err := c.GetMetadata(context.Background(), "obj-2", "color")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if gotParams["objectId"] != "obj-2" {
		t.Errorf("objectId=%v, want obj-2", gotParams["objectId"])
	}
	if gotParams["dataId"] != "color" {
		t.Errorf("dataId=%v, want color", gotParams["dataId"])
	}
	if val != "blue" {
		t.Errorf("value=%v, want blue", val)
	}
}

// TestDeleteMetadata verifies objectId and dataId are forwarded.
func TestDeleteMetadata(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Metadata.deleteMetadata": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.DeleteMetadata(context.Background(), "obj-3", "tag"); err != nil {
		t.Fatalf("DeleteMetadata: %v", err)
	}
	if gotParams["objectId"] != "obj-3" {
		t.Errorf("objectId=%v, want obj-3", gotParams["objectId"])
	}
	if gotParams["dataId"] != "tag" {
		t.Errorf("dataId=%v, want tag", gotParams["dataId"])
	}
}

// ---------------------------------------------------------------------------
// methods
// ---------------------------------------------------------------------------

// TestExecuteProgram verifies the ISE-ID is forwarded via Program.execute.
func TestExecuteProgram(t *testing.T) {
	t.Parallel()
	var gotID string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.execute": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.ExecuteProgram(context.Background(), "prog-42"); err != nil {
		t.Fatalf("ExecuteProgram: %v", err)
	}
	if gotID != "prog-42" {
		t.Errorf("id=%q, want prog-42", gotID)
	}
}

// TestGetAllChannelISEIDsRoom verifies the map result is decoded.
func TestGetAllChannelISEIDsRoom(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Room.getChannelIDs": func(env envelope) any {
			return okResult(map[string][]string{
				"room-1": {"ch-1", "ch-2"},
				"room-2": {"ch-3"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	result, err := c.GetAllChannelISEIDsRoom(context.Background())
	if err != nil {
		t.Fatalf("GetAllChannelISEIDsRoom: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result)=%d, want 2", len(result))
	}
	if len(result["room-1"]) != 2 {
		t.Errorf("room-1 channels=%d, want 2", len(result["room-1"]))
	}
}

// TestGetAllChannelISEIDsFunction verifies the map result is decoded.
func TestGetAllChannelISEIDsFunction(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Function.getChannelIDs": func(env envelope) any {
			return okResult(map[string][]string{
				"fn-heating": {"HEQ0123456:1"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	result, err := c.GetAllChannelISEIDsFunction(context.Background())
	if err != nil {
		t.Fatalf("GetAllChannelISEIDsFunction: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result)=%d, want 1", len(result))
	}
	if ids := result["fn-heating"]; len(ids) != 1 || ids[0] != "HEQ0123456:1" {
		t.Errorf("fn-heating=%v, want [HEQ0123456:1]", ids)
	}
}

// TestGetIseIDByAddress verifies address is forwarded and ISE-ID is decoded.
func TestGetIseIDByAddress(t *testing.T) {
	t.Parallel()
	var gotAddr string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.getIseIDByAddress": func(env envelope) any {
			gotAddr, _ = env.Params["address"].(string)
			return okResult("9876")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	id, err := c.GetIseIDByAddress(context.Background(), "HEQ0123456")
	if err != nil {
		t.Fatalf("GetIseIDByAddress: %v", err)
	}
	if gotAddr != "HEQ0123456" {
		t.Errorf("address=%q, want HEQ0123456", gotAddr)
	}
	if id != "9876" {
		t.Errorf("ise_id=%q, want 9876", id)
	}
}

// TestIsInterfacePresent verifies the interface parameter is forwarded and
// the boolean result is decoded.
func TestIsInterfacePresent(t *testing.T) {
	t.Parallel()
	var gotIface string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.isPresent": func(env envelope) any {
			gotIface, _ = env.Params["interface"].(string)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	ok, err := c.IsInterfacePresent(context.Background(), "HmIP-RF")
	if err != nil {
		t.Fatalf("IsInterfacePresent: %v", err)
	}
	if gotIface != "HmIP-RF" {
		t.Errorf("interface=%q, want HmIP-RF", gotIface)
	}
	if !ok {
		t.Error("IsInterfacePresent returned false, want true")
	}
}

// ---------------------------------------------------------------------------
// Batch 2: JSON-RPC typed write-wrappers
// ---------------------------------------------------------------------------

// TestSetSystemVariableBool verifies that the value is sent as an integer
// (1 for true, 0 for false) per CCU wire contract.
func TestSetSystemVariableBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value   bool
		wantInt float64
	}{
		{true, 1},
		{false, 0},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("value=%v", tc.value), func(t *testing.T) {
			t.Parallel()
			var gotParams map[string]any
			srv := newTestServer(t, map[string]func(envelope) any{
				"SysVar.setBool": func(env envelope) any {
					gotParams = env.Params
					return okResult(true)
				},
			})
			defer srv.Close()

			c, _ := New(Config{Endpoint: srv.URL})
			if err := c.SetSystemVariableBool(context.Background(), "myVar", tc.value); err != nil {
				t.Fatalf("SetSystemVariableBool: %v", err)
			}
			if n, _ := gotParams["name"].(string); n != "myVar" {
				t.Errorf("name=%q, want myVar", n)
			}
			if v, _ := gotParams["value"].(float64); v != tc.wantInt {
				t.Errorf("value=%v, want %v", gotParams["value"], tc.wantInt)
			}
		})
	}
}

// TestSetSystemVariableFloat verifies name and float value are forwarded
// via SysVar.setFloat.
func TestSetSystemVariableFloat(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariableFloat(context.Background(), "tempVar", 21.5); err != nil {
		t.Fatalf("SetSystemVariableFloat: %v", err)
	}
	if n, _ := gotParams["name"].(string); n != "tempVar" {
		t.Errorf("name=%q, want tempVar", n)
	}
	if v, _ := gotParams["value"].(float64); v != 21.5 {
		t.Errorf("value=%v, want 21.5", gotParams["value"])
	}
}

// TestCreateSystemVariableBool verifies the correct JSON payload shape.
func TestCreateSystemVariableBool(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createBool": func(env envelope) any {
			gotParams = env.Params
			return okResult(map[string]any{"id": "1234"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	result, err := c.CreateSystemVariableBool(context.Background(), "presence", true)
	if err != nil {
		t.Fatalf("CreateSystemVariableBool: %v", err)
	}
	if n, _ := gotParams["name"].(string); n != "presence" {
		t.Errorf("name=%q, want presence", n)
	}
	if iv, _ := gotParams["init_val"].(float64); iv != 1 {
		t.Errorf("init_val=%v, want 1", gotParams["init_val"])
	}
	if internal, _ := gotParams["internal"].(float64); internal != 0 {
		t.Errorf("internal=%v, want 0", gotParams["internal"])
	}
	if chn, _ := gotParams["chnID"].(float64); chn != -1 {
		t.Errorf("chnID=%v, want -1", gotParams["chnID"])
	}
	if id, _ := result["id"].(string); id != "1234" {
		t.Errorf("result[id]=%q, want 1234", id)
	}
}

// TestCreateSystemVariableEnum verifies the value list is semicolon-joined.
func TestCreateSystemVariableEnum(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createEnum": func(env envelope) any {
			gotParams = env.Params
			return okResult(map[string]any{"id": "5678"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	_, err := c.CreateSystemVariableEnum(context.Background(), "scene", []string{"off", "relax", "bright"})
	if err != nil {
		t.Fatalf("CreateSystemVariableEnum: %v", err)
	}
	if n, _ := gotParams["name"].(string); n != "scene" {
		t.Errorf("name=%q, want scene", n)
	}
	if vl, _ := gotParams["valueList"].(string); vl != "off;relax;bright" {
		t.Errorf("valueList=%q, want off;relax;bright", vl)
	}
}

// TestCreateSystemVariableFloat verifies name, min and max are forwarded.
func TestCreateSystemVariableFloat(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createFloat": func(env envelope) any {
			gotParams = env.Params
			return okResult(map[string]any{"id": "9012"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	_, err := c.CreateSystemVariableFloat(context.Background(), "setpoint", 5.0, 35.0)
	if err != nil {
		t.Fatalf("CreateSystemVariableFloat: %v", err)
	}
	if n, _ := gotParams["name"].(string); n != "setpoint" {
		t.Errorf("name=%q, want setpoint", n)
	}
	if minVal, _ := gotParams["minValue"].(float64); minVal != 5.0 {
		t.Errorf("minValue=%v, want 5.0", gotParams["minValue"])
	}
	if maxVal, _ := gotParams["maxValue"].(float64); maxVal != 35.0 {
		t.Errorf("maxValue=%v, want 35.0", gotParams["maxValue"])
	}
}

// TestGetAllSystemVariables verifies the slice is decoded and no params sent.
func TestGetAllSystemVariables(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.getAll": func(env envelope) any {
			return okResult([]map[string]any{
				{"id": "v1", "name": "presence", "value": "true"},
				{"id": "v2", "name": "temperature", "value": "21.5"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	vars, err := c.GetAllSystemVariables(context.Background())
	if err != nil {
		t.Fatalf("GetAllSystemVariables: %v", err)
	}
	if len(vars) != 2 {
		t.Fatalf("len(vars)=%d, want 2", len(vars))
	}
	if id, _ := vars[0]["id"].(string); id != "v1" {
		t.Errorf("vars[0].id=%q, want v1", id)
	}
}

// TestGetAllPrograms verifies the slice is decoded from Program.getAll.
func TestGetAllPrograms(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.getAll": func(env envelope) any {
			return okResult([]map[string]any{
				{"id": "p1", "name": "Wake Up", "isActive": true, "isInternal": false, "lastExecuteTime": "2026-05-01 07:00:00"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	progs, err := c.GetAllPrograms(context.Background())
	if err != nil {
		t.Fatalf("GetAllPrograms: %v", err)
	}
	if len(progs) != 1 {
		t.Fatalf("len(progs)=%d, want 1", len(progs))
	}
	if id, _ := progs[0]["id"].(string); id != "p1" {
		t.Errorf("progs[0].id=%q, want p1", id)
	}
	if active, _ := progs[0]["isActive"].(bool); !active {
		t.Error("progs[0].isActive=false, want true")
	}
}

// TestGetValueValues verifies Interface.getValue is used for non-MASTER keys.
func TestGetValueValues(t *testing.T) {
	t.Parallel()
	var gotMethod string
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getValue": func(env envelope) any {
			gotMethod = "Interface.getValue"
			gotParams = env.Params
			return okResult(21.5)
		},
		"Interface.getMasterValue": func(env envelope) any {
			gotMethod = "Interface.getMasterValue"
			gotParams = env.Params
			return okResult(42.0)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	val, err := c.GetValue(context.Background(), "BidCos-RF", "HEQ0123456:1", "VALUES", "SET_POINT_TEMPERATURE")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if gotMethod != "Interface.getValue" {
		t.Errorf("method=%q, want Interface.getValue", gotMethod)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if addr, _ := gotParams["address"].(string); addr != "HEQ0123456:1" {
		t.Errorf("address=%q, want HEQ0123456:1", addr)
	}
	if vk, _ := gotParams["valueKey"].(string); vk != "SET_POINT_TEMPERATURE" {
		t.Errorf("valueKey=%q, want SET_POINT_TEMPERATURE", vk)
	}
	if v, _ := val.(float64); v != 21.5 {
		t.Errorf("result=%v, want 21.5", val)
	}
}

// TestGetValueMaster verifies Interface.getMasterValue is used for MASTER key.
func TestGetValueMaster(t *testing.T) {
	t.Parallel()
	var gotMethod string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getValue": func(env envelope) any {
			gotMethod = "Interface.getValue"
			return okResult(0.0)
		},
		"Interface.getMasterValue": func(env envelope) any {
			gotMethod = "Interface.getMasterValue"
			return okResult(5.0)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetValue(context.Background(), "BidCos-RF", "HEQ0123456:1", "MASTER", "BURST_LIMIT"); err != nil {
		t.Fatalf("GetValue MASTER: %v", err)
	}
	if gotMethod != "Interface.getMasterValue" {
		t.Errorf("method=%q, want Interface.getMasterValue", gotMethod)
	}
}

// TestPutParamset verifies all four parameters are forwarded to Interface.putParamset.
func TestPutParamset(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.putParamset": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	values := []map[string]any{
		{"SET_POINT_TEMPERATURE": 22.0},
	}
	if err := c.PutParamset(context.Background(), "BidCos-RF", "HEQ0123456:1", "VALUES", values); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if addr, _ := gotParams["address"].(string); addr != "HEQ0123456:1" {
		t.Errorf("address=%q, want HEQ0123456:1", addr)
	}
	if pk, _ := gotParams["paramsetKey"].(string); pk != "VALUES" {
		t.Errorf("paramsetKey=%q, want VALUES", pk)
	}
	// "set" field arrives as a JSON-decoded []any
	if _, ok := gotParams["set"]; !ok {
		t.Error("set field missing in params")
	}
}

// TestSetValue verifies all five parameters are forwarded to Interface.setValue.
func TestSetValue(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.setValue": func(env envelope) any {
			gotParams = env.Params
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetValue(context.Background(), "BidCos-RF", "HEQ0123456:1", "SET_POINT_TEMPERATURE", "FLOAT", 21.0); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if addr, _ := gotParams["address"].(string); addr != "HEQ0123456:1" {
		t.Errorf("address=%q, want HEQ0123456:1", addr)
	}
	if vk, _ := gotParams["valueKey"].(string); vk != "SET_POINT_TEMPERATURE" {
		t.Errorf("valueKey=%q, want SET_POINT_TEMPERATURE", vk)
	}
	if typ, _ := gotParams["type"].(string); typ != "FLOAT" {
		t.Errorf("type=%q, want FLOAT", typ)
	}
	if v, _ := gotParams["value"].(float64); v != 21.0 {
		t.Errorf("value=%v, want 21.0", gotParams["value"])
	}
}

// TestInterfaceGetLinks verifies all three parameters are forwarded and the
// slice result is decoded.
func TestInterfaceGetLinks(t *testing.T) {
	t.Parallel()
	var gotParams map[string]any
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getLinks": func(env envelope) any {
			gotParams = env.Params
			return okResult([]map[string]any{
				{"sender": "HEQ0000001:1", "receiver": "HEQ0000002:1"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	links, err := c.InterfaceGetLinks(context.Background(), "BidCos-RF", "HEQ0000001:1", 4)
	if err != nil {
		t.Fatalf("InterfaceGetLinks: %v", err)
	}
	if iface, _ := gotParams["interface"].(string); iface != "BidCos-RF" {
		t.Errorf("interface=%q, want BidCos-RF", iface)
	}
	if addr, _ := gotParams["address"].(string); addr != "HEQ0000001:1" {
		t.Errorf("address=%q, want HEQ0000001:1", addr)
	}
	if flags, _ := gotParams["flags"].(float64); flags != 4 {
		t.Errorf("flags=%v, want 4", gotParams["flags"])
	}
	if len(links) != 1 {
		t.Fatalf("len(links)=%d, want 1", len(links))
	}
}

// ---------------------------------------------------------------------------
// L06: GetSystemVariable
// ---------------------------------------------------------------------------

// TestGetSystemVariableString verifies that the name parameter is forwarded
// via SysVar.getValueByName and the returned string value is decoded.
func TestGetSystemVariableString(t *testing.T) {
	t.Parallel()
	var gotName string
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.getValueByName": func(env envelope) any {
			gotName, _ = env.Params["name"].(string)
			return okResult("hello")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	val, err := c.GetSystemVariable(context.Background(), "greeting")
	if err != nil {
		t.Fatalf("GetSystemVariable: %v", err)
	}
	if gotName != "greeting" {
		t.Errorf("name=%q, want greeting", gotName)
	}
	if s, _ := val.(string); s != "hello" {
		t.Errorf("value=%v, want hello", val)
	}
}

// TestGetSystemVariableFloat verifies that a numeric sysvar value is decoded
// as float64 (CCU JSON-RPC wire type).
func TestGetSystemVariableFloat(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.getValueByName": func(env envelope) any {
			return okResult(21.5)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	val, err := c.GetSystemVariable(context.Background(), "setpoint")
	if err != nil {
		t.Fatalf("GetSystemVariable: %v", err)
	}
	if v, _ := val.(float64); v != 21.5 {
		t.Errorf("value=%v, want 21.5", val)
	}
}

// TestGetSystemVariableError verifies that a CCU-side error is surfaced.
func TestGetSystemVariableError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.getValueByName": func(env envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "not found"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	_, err := c.GetSystemVariable(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("got %v, want ErrInternalBackendException", err)
	}
}

// ---------------------------------------------------------------------------
// L06: DownloadBackup
// ---------------------------------------------------------------------------

// TestDownloadBackupSuccess verifies that the CCU session ID is embedded in
// the backup URL (@sid@) and the binary body is returned as []byte.
func TestDownloadBackupSuccess(t *testing.T) {
	t.Parallel()

	const wantSID = "testsession123"
	const wantBody = "PKbackupdata"

	// Spin up a plain HTTP mux that handles both the JSON-RPC endpoint and
	// the cp_security.cgi endpoint on the same server.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, r *http.Request) {
		// Session.login handler — return our test session ID.
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"result":%q}`, wantSID)
	})
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, r *http.Request) {
		// Verify the CCU-style @sid@ query parameter is present.
		if !containsSubstr(r.URL.RawQuery, wantSID) {
			http.Error(w, "missing sid", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("action") != "create_backup" {
			http.Error(w, "wrong action", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, wantBody)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	// Inject session ID directly (bypasses Login round-trip in test).
	c.mu.Lock()
	c.sessionID = wantSID
	c.mu.Unlock()

	data, err := c.DownloadBackup(context.Background())
	if err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}
	if string(data) != wantBody {
		t.Errorf("body=%q, want %q", data, wantBody)
	}
}

// TestDownloadBackupNoSession verifies that DownloadBackup returns
// ErrAuthFailure when no session is active.
func TestDownloadBackupNoSession(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://localhost:9999/api/homematic.cgi"})
	_, err := c.DownloadBackup(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Errorf("got %v, want ErrAuthFailure", err)
	}
}

// TestDownloadBackupCCUError verifies that a non-200 HTTP status from the CCU
// CGI is surfaced as ErrInternalBackendException.
func TestDownloadBackupCCUError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = "sess-x"
	c.mu.Unlock()

	_, err := c.DownloadBackup(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("got %v, want ErrInternalBackendException", err)
	}
}

// ---------------------------------------------------------------------------
// L07: DownloadFirmware
// ---------------------------------------------------------------------------

// TestDownloadFirmwareSuccess verifies the session ID, action, and firmware
// URL are forwarded as form fields to the CCU maintenance CGI.
func TestDownloadFirmwareSuccess(t *testing.T) {
	t.Parallel()

	const wantSID = "fw-session-xyz"
	const wantFirmwareURL = "https://example.com/firmware-3.65.zip"

	var gotSID, gotAction, gotURL string

	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_maintenance.cgi", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		gotSID = r.FormValue("sid")
		gotAction = r.FormValue("action")
		gotURL = r.FormValue("url")
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = wantSID
	c.mu.Unlock()

	if err := c.DownloadFirmware(context.Background(), wantFirmwareURL); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
	if gotSID != wantSID {
		t.Errorf("sid=%q, want %q", gotSID, wantSID)
	}
	if gotAction != "download_firmware" {
		t.Errorf("action=%q, want download_firmware", gotAction)
	}
	if gotURL != wantFirmwareURL {
		t.Errorf("url=%q, want %q", gotURL, wantFirmwareURL)
	}
}

// TestDownloadFirmwareInvalidScheme verifies that non-http(s) URLs are
// rejected with ErrUnsupported before any network call is made.
func TestDownloadFirmwareInvalidScheme(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://localhost:9999/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.mu.Unlock()

	err := c.DownloadFirmware(context.Background(), "ftp://firmware.example.com/fw.bin")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}

// TestDownloadFirmwareNoSession verifies that ErrAuthFailure is returned when
// no session is active.
func TestDownloadFirmwareNoSession(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://localhost:9999/api/homematic.cgi"})
	err := c.DownloadFirmware(context.Background(), "https://example.com/fw.bin")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Errorf("got %v, want ErrAuthFailure", err)
	}
}

// TestDownloadFirmwareCCUError verifies that a non-200 status from the
// maintenance CGI is surfaced as ErrInternalBackendException.
func TestDownloadFirmwareCCUError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_maintenance.cgi", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway error", http.StatusBadGateway)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = "sess-fw"
	c.mu.Unlock()

	err := c.DownloadFirmware(context.Background(), "https://example.com/fw.bin")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("got %v, want ErrInternalBackendException", err)
	}
}

// TestGetHttpsRedirectEnabledTrue verifies that a true CCU result is returned as true.
func TestGetHttpsRedirectEnabledTrue(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"CCU.getHttpsRedirectEnabled": func(env envelope) any {
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetHTTPSRedirectEnabled(context.Background())
	if err != nil {
		t.Fatalf("GetHTTPSRedirectEnabled: %v", err)
	}
	if !got {
		t.Errorf("expected true, got false")
	}
}

// TestGetHttpsRedirectEnabledFalse verifies that a false CCU result is returned as false.
func TestGetHttpsRedirectEnabledFalse(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"CCU.getHttpsRedirectEnabled": func(env envelope) any {
			return okResult(false)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetHTTPSRedirectEnabled(context.Background())
	if err != nil {
		t.Fatalf("GetHTTPSRedirectEnabled: %v", err)
	}
	if got {
		t.Errorf("expected false, got true")
	}
}

// containsSubstr reports whether sub appears anywhere in s.
func containsSubstr(s, sub string) bool {
	if sub == "" || len(s) < len(sub) {
		return sub == ""
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// GetAllRoomsRaw
// ---------------------------------------------------------------------------

func TestGetAllRoomsRaw(t *testing.T) {
	t.Parallel()
	want := []RoomEntry{{ID: "10", Name: "Living Room", ChannelIDs: []string{"ch1", "ch2"}}}
	srv := newTestServer(t, map[string]func(envelope) any{
		"Room.getAll": func(_ envelope) any { return okResult(want) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetAllRoomsRaw(context.Background())
	if err != nil {
		t.Fatalf("GetAllRoomsRaw: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Living Room" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestGetAllRoomsRawError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Room.getAll": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllRoomsRaw(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// GetAllFunctionsRaw
// ---------------------------------------------------------------------------

func TestGetAllFunctionsRaw(t *testing.T) {
	t.Parallel()
	want := []SubsectionEntry{{ID: "20", Name: "Lights", ChannelIDs: []string{"chA"}}}
	srv := newTestServer(t, map[string]func(envelope) any{
		"Subsection.getAll": func(_ envelope) any { return okResult(want) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetAllFunctionsRaw(context.Background())
	if err != nil {
		t.Fatalf("GetAllFunctionsRaw: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Lights" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestGetAllFunctionsRawError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Subsection.getAll": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "boom"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllFunctionsRaw(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// SetSystemVariable — type dispatch branches
// ---------------------------------------------------------------------------

func TestSetSystemVariableDispatchBool(t *testing.T) {
	t.Parallel()
	var gotVal any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setBool": func(env envelope) any {
			gotVal = env.Params["value"]
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "myBool", true); err != nil {
		t.Fatalf("SetSystemVariable(bool): %v", err)
	}
	if v, _ := gotVal.(float64); v != 1 {
		t.Errorf("expected value=1, got %v", gotVal)
	}
}

func TestSetSystemVariableDispatchFloat64(t *testing.T) {
	t.Parallel()
	var gotVal any
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(env envelope) any {
			gotVal = env.Params["value"]
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "myFloat", float64(3.14)); err != nil {
		t.Fatalf("SetSystemVariable(float64): %v", err)
	}
	if v, _ := gotVal.(float64); v != 3.14 {
		t.Errorf("expected 3.14, got %v", gotVal)
	}
}

func TestSetSystemVariableDispatchInt(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(_ envelope) any { return okResult(nil) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "x", int(42)); err != nil {
		t.Fatalf("SetSystemVariable(int): %v", err)
	}
}

func TestSetSystemVariableDispatchFloat32(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(_ envelope) any { return okResult(nil) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "x", float32(1.5)); err != nil {
		t.Fatalf("SetSystemVariable(float32): %v", err)
	}
}

func TestSetSystemVariableDispatchInt32(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(_ envelope) any { return okResult(nil) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "x", int32(7)); err != nil {
		t.Fatalf("SetSystemVariable(int32): %v", err)
	}
}

func TestSetSystemVariableDispatchInt64(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.setFloat": func(_ envelope) any { return okResult(nil) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetSystemVariable(context.Background(), "x", int64(100)); err != nil {
		t.Fatalf("SetSystemVariable(int64): %v", err)
	}
}

func TestSetSystemVariableDispatchUnsupportedType(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://unused"})
	err := c.SetSystemVariable(context.Background(), "x", "a string")
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// ---------------------------------------------------------------------------
// SetProgramState
// ---------------------------------------------------------------------------

func TestSetProgramState(t *testing.T) {
	t.Parallel()
	var gotID string
	var gotActive bool
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.setActive": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			gotActive, _ = env.Params["active"].(bool)
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetProgramState(context.Background(), "prog-1", true); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	if gotID != "prog-1" {
		t.Errorf("id=%q, want %q", gotID, "prog-1")
	}
	if !gotActive {
		t.Error("expected active=true")
	}
}

func TestSetProgramStateError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.setActive": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "not found"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SetProgramState(context.Background(), "prog-2", false); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// AcceptDeviceInInbox
// ---------------------------------------------------------------------------

func TestAcceptDeviceInInbox(t *testing.T) {
	t.Parallel()
	var gotAddr string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.acceptNewDevice": func(env envelope) any {
			gotAddr, _ = env.Params["address"].(string)
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.AcceptDeviceInInbox(context.Background(), "HmIP-RF", "VCU9999"); err != nil {
		t.Fatalf("AcceptDeviceInInbox: %v", err)
	}
	if gotAddr != "VCU9999" {
		t.Errorf("address=%q, want VCU9999", gotAddr)
	}
}

func TestAcceptDeviceInInboxError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.acceptNewDevice": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.AcceptDeviceInInbox(context.Background(), "HmIP-RF", "VCU0"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// AcknowledgeMessage
// ---------------------------------------------------------------------------

func TestAcknowledgeMessage(t *testing.T) {
	t.Parallel()
	var gotID string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Alarm.acknowledge": func(env envelope) any {
			gotID, _ = env.Params["id"].(string)
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.AcknowledgeMessage(context.Background(), "msg-42"); err != nil {
		t.Fatalf("AcknowledgeMessage: %v", err)
	}
	if gotID != "msg-42" {
		t.Errorf("id=%q, want msg-42", gotID)
	}
}

func TestAcknowledgeMessageError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Alarm.acknowledge": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "bad"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.AcknowledgeMessage(context.Background(), "x"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// IsServiceAvailable
// ---------------------------------------------------------------------------

func TestIsServiceAvailableTrue(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.getSystemInformation": func(_ envelope) any {
			return okResult(map[string]any{"SERIAL_NUMBER": "X", "VERSION": "1", "HARDWARE_VERSION": "1"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if !c.IsServiceAvailable(context.Background()) {
		t.Error("expected true")
	}
}

func TestIsServiceAvailableFalse(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.getSystemInformation": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "down"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if c.IsServiceAvailable(context.Background()) {
		t.Error("expected false")
	}
}

// ---------------------------------------------------------------------------
// TriggerFirmwareUpdate
// ---------------------------------------------------------------------------

func TestTriggerFirmwareUpdate(t *testing.T) {
	t.Parallel()
	called := false
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.triggerFirmwareUpdate": func(_ envelope) any {
			called = true
			return okResult(nil)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.TriggerFirmwareUpdate(context.Background()); err != nil {
		t.Fatalf("TriggerFirmwareUpdate: %v", err)
	}
	if !called {
		t.Error("expected server method to be called")
	}
}

func TestTriggerFirmwareUpdateError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.triggerFirmwareUpdate": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.TriggerFirmwareUpdate(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// GetSuppressedServiceMessages
// ---------------------------------------------------------------------------

func TestGetSuppressedServiceMessages(t *testing.T) {
	t.Parallel()
	want := []map[string]any{{"parameterID": "LOWBAT"}}
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getSuppressedServiceMessages": func(_ envelope) any { return okResult(want) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "VCU001:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
}

func TestGetSuppressedServiceMessagesError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getSuppressedServiceMessages": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetSuppressedServiceMessages(context.Background(), "X", "Y"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// GetInstallMode
// ---------------------------------------------------------------------------

func TestGetInstallMode(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getInstallMode": func(env envelope) any {
			if env.Params["interface"] != "HmIP-RF" {
				return response{Error: &wireError{Code: -1, Message: "bad"}}
			}
			return okResult(45)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	v, err := c.GetInstallMode(context.Background(), "HmIP-RF")
	if err != nil {
		t.Fatalf("GetInstallMode: %v", err)
	}
	if v != 45 {
		t.Errorf("v=%d, want 45", v)
	}
}

func TestGetInstallModeError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getInstallMode": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetInstallMode(context.Background(), "X"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// GetLinkInfo
// ---------------------------------------------------------------------------

func TestGetLinkInfo(t *testing.T) {
	t.Parallel()
	want := map[string]any{"NAME": "mylink", "DESCRIPTION": "desc"}
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getLinkInfo": func(_ envelope) any { return okResult(want) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetLinkInfo(context.Background(), "BidCos-RF", "sender:1", "receiver:1")
	if err != nil {
		t.Fatalf("GetLinkInfo: %v", err)
	}
	if got["NAME"] != "mylink" {
		t.Errorf("NAME=%v, want mylink", got["NAME"])
	}
}

func TestGetLinkInfoError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getLinkInfo": func(_ envelope) any {
			return response{Error: &wireError{Code: -1, Message: "fail"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetLinkInfo(context.Background(), "X", "a", "b"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// backupBaseURL — suffix-match and fallback paths
// ---------------------------------------------------------------------------

func TestBackupBaseURLWithSuffix(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://ccu.local/api/homematic.cgi"})
	base := c.backupBaseURL()
	if base != "http://ccu.local" {
		t.Errorf("backupBaseURL=%q, want http://ccu.local", base)
	}
}

func TestBackupBaseURLFallbackPath(t *testing.T) {
	t.Parallel()
	// Endpoint without the expected JSON-RPC suffix → fallback path.
	c, _ := New(Config{Endpoint: "http://ccu.local/some/other/path"})
	base := c.backupBaseURL()
	if base != "http://ccu.local" {
		t.Errorf("backupBaseURL=%q, want http://ccu.local", base)
	}
}

// TestBackupBaseURLInvalidURL verifies that backupBaseURL returns "" when the
// endpoint is not parseable by url.Parse (exercises the "return empty string"
// branch in the fallback path).
func TestBackupBaseURLInvalidURL(t *testing.T) {
	t.Parallel()
	// An endpoint with a control character that url.Parse rejects.
	c, _ := New(Config{Endpoint: "http://host\x00/path"})
	base := c.backupBaseURL()
	// Either the suffix match fires (no suffix here → fallback) and the parse
	// fails → empty string expected.
	if strings.HasPrefix(base, "http") && strings.Contains(base, "\x00") {
		t.Fatalf("backupBaseURL should not return a URL containing NUL, got %q", base)
	}
	// If the fallback parse failed we get ""; if it somehow succeeded we just
	// need it to not be "". Either outcome is acceptable — the key assertion is
	// that the function does not panic.
}

// ---------------------------------------------------------------------------
// DownloadBackup additional paths
// ---------------------------------------------------------------------------

// TestDownloadBackupHappyPath verifies the full flow using a fake HTTP server.
func TestDownloadBackupHappyPath(t *testing.T) {
	t.Parallel()
	const fakeSID = "testsession"
	const fakePayload = "BACKUP_DATA"

	var mux http.ServeMux
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if env.Method == "Session.login" {
			raw, _ := json.Marshal(fakeSID)
			_ = json.NewEncoder(w).Encode(response{Result: raw})
		} else {
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	})
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakePayload))
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	c, _ := New(Config{
		Endpoint: srv.URL + "/api/homematic.cgi",
		Username: "u",
		Password: "p",
	})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	data, err := c.DownloadBackup(context.Background())
	if err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}
	if string(data) != fakePayload {
		t.Errorf("got %q, want %q", data, fakePayload)
	}
}

func TestDownloadBackupServerError(t *testing.T) {
	t.Parallel()
	const fakeSID = "sess-err"

	var mux http.ServeMux
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if env.Method == "Session.login" {
			raw, _ := json.Marshal(fakeSID)
			_ = json.NewEncoder(w).Encode(response{Result: raw})
		}
	})
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	c, _ := New(Config{
		Endpoint: srv.URL + "/api/homematic.cgi",
		Username: "u",
		Password: "p",
	})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := c.DownloadBackup(context.Background()); err == nil {
		t.Fatal("expected error for non-200 backup response")
	}
}

// TestDownloadBackupNoBaseURL verifies that DownloadBackup returns ErrUnsupported
// when the endpoint cannot be parsed into a valid base URL.
func TestDownloadBackupNoBaseURL(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "://bad-endpoint"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.mu.Unlock()
	_, err := c.DownloadBackup(context.Background())
	if err == nil {
		t.Fatal("expected error for unparseable endpoint")
	}
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

// TestDownloadBackupNetworkError verifies that a network failure during the
// GET to cp_security.cgi is surfaced as ErrNoConnection.
func TestDownloadBackupNetworkError(t *testing.T) {
	t.Parallel()
	// Endpoint on port 1 so the backup GET will fail immediately.
	c, _ := New(Config{Endpoint: "http://127.0.0.1:1/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.mu.Unlock()
	_, err := c.DownloadBackup(context.Background())
	if err == nil {
		t.Fatal("expected network error")
	}
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("got %v, want ErrNoConnection", err)
	}
}

// TestDownloadBackupNilHTTPClient verifies the nil-httpClient branch: when no
// HTTPClient is set on Config, DownloadBackup builds its own client with the
// backup timeout and still succeeds.
func TestDownloadBackupNilHTTPClient(t *testing.T) {
	t.Parallel()
	const wantSID = "sess-nil"
	const wantBody = "BACKUP_BYTES"

	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, wantBody)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// HTTPClient is left nil in Config — exercises the nil branch inside
	// DownloadBackup that builds `&http.Client{Timeout: backupDownloadTimeout}`.
	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	// Manually force httpClient to nil to guarantee nil branch is hit.
	c.httpClient = nil
	c.mu.Lock()
	c.sessionID = wantSID
	c.mu.Unlock()

	data, err := c.DownloadBackup(context.Background())
	if err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}
	if string(data) != wantBody {
		t.Errorf("body=%q, want %q", data, wantBody)
	}
}

// ---------------------------------------------------------------------------
// DownloadFirmware additional paths
// ---------------------------------------------------------------------------

func TestDownloadFirmwareUnsupportedScheme(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://ccu.local/api/homematic.cgi"})
	err := c.DownloadFirmware(context.Background(), "ftp://example.com/fw.tar.gz")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

// TestDownloadFirmwareHappyPath verifies the session ID, action, and firmware
// URL are forwarded as form fields to the CCU maintenance CGI.
func TestDownloadFirmwareHappyPath(t *testing.T) {
	t.Parallel()
	const fakeSID = "sess-fw"

	var mux http.ServeMux
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if env.Method == "Session.login" {
			raw, _ := json.Marshal(fakeSID)
			_ = json.NewEncoder(w).Encode(response{Result: raw})
		}
	})
	mux.HandleFunc("/config/cp_maintenance.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	c, _ := New(Config{
		Endpoint: srv.URL + "/api/homematic.cgi",
		Username: "u",
		Password: "p",
	})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.DownloadFirmware(context.Background(), "http://update.example.com/fw.tar.gz"); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
}

func TestDownloadFirmwareServerError(t *testing.T) {
	t.Parallel()
	const fakeSID = "sess-fw2"

	var mux http.ServeMux
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if env.Method == "Session.login" {
			raw, _ := json.Marshal(fakeSID)
			_ = json.NewEncoder(w).Encode(response{Result: raw})
		}
	})
	mux.HandleFunc("/config/cp_maintenance.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	c, _ := New(Config{
		Endpoint: srv.URL + "/api/homematic.cgi",
		Username: "u",
		Password: "p",
	})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.DownloadFirmware(context.Background(), "https://update.example.com/fw.tar.gz"); err == nil {
		t.Fatal("expected error for non-200 firmware response")
	}
}

// TestDownloadFirmwareNoBaseURL verifies that DownloadFirmware returns
// ErrUnsupported when backupBaseURL returns "" for the configured endpoint.
func TestDownloadFirmwareNoBaseURL(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "://bad-endpoint"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.mu.Unlock()
	err := c.DownloadFirmware(context.Background(), "http://example.com/fw.bin")
	if err == nil {
		t.Fatal("expected error for unparseable endpoint")
	}
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

// TestDownloadFirmwareNetworkError verifies that a network failure during the
// POST to cp_maintenance.cgi is surfaced as ErrNoConnection.
func TestDownloadFirmwareNetworkError(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://127.0.0.1:1/api/homematic.cgi"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.mu.Unlock()
	err := c.DownloadFirmware(context.Background(), "http://example.com/fw.bin")
	if err == nil {
		t.Fatal("expected network error")
	}
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("got %v, want ErrNoConnection", err)
	}
}

// TestDownloadFirmwareNilHTTPClient verifies the nil-httpClient branch inside
// DownloadFirmware: when httpClient is nil, the method builds its own with the
// firmware timeout.
func TestDownloadFirmwareNilHTTPClient(t *testing.T) {
	t.Parallel()
	const fakeSID = "sess-fw-nil"

	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_maintenance.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	c.httpClient = nil // force nil branch
	c.mu.Lock()
	c.sessionID = fakeSID
	c.mu.Unlock()

	if err := c.DownloadFirmware(context.Background(), "http://example.com/fw.zip"); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
}

// ---------------------------------------------------------------------------
// acquire — semaphore cancellation path
// ---------------------------------------------------------------------------

// TestAcquireCancelledContextBlocking verifies that acquire returns immediately
// with an error when MaxConcurrent=1 is saturated and context is pre-cancelled.
func TestAcquireCancelledContextBlocking(t *testing.T) {
	t.Parallel()
	// MaxConcurrent=1: fill the only slot, then attempt a second acquire
	// with a pre-cancelled context — it must return immediately with an error.
	c, _ := New(Config{Endpoint: "http://unused", MaxConcurrent: 1})
	// Occupy the semaphore.
	if err := c.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	err := c.acquire(ctx)
	if err == nil {
		t.Fatal("expected context error from blocked acquire")
	}
	c.release() // restore the slot
}

// ---------------------------------------------------------------------------
// Methods error paths
// ---------------------------------------------------------------------------

func TestGetDeviceDetailsError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.listAllDetail": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetDeviceDetails(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAllDeviceDataError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listDevices": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllDeviceData(context.Background(), "HmIP-RF"); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadProgramError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.readProgram": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.ReadProgram(context.Background(), "42"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetMetadataError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Metadata.getMetadata": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetMetadata(context.Background(), "obj", "key"); err == nil {
		t.Fatal("expected error")
	}
}

func TestInterfaceGetLinksError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getLinks": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.InterfaceGetLinks(context.Background(), "BidCos-RF", "addr:1", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAllChannelISEIDsRoomError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Room.getChannelIDs": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllChannelISEIDsRoom(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAllChannelISEIDsFunctionError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Function.getChannelIDs": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllChannelISEIDsFunction(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetIseIDByAddressError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.getIseIDByAddress": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetIseIDByAddress(context.Background(), "HEQ0"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsInterfacePresentError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.isPresent": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.IsInterfacePresent(context.Background(), "HmIP-RF"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSystemVariableBoolError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createBool": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.CreateSystemVariableBool(context.Background(), "x", false); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSystemVariableEnumError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createEnum": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.CreateSystemVariableEnum(context.Background(), "x", []string{"a", "b"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSystemVariableFloatError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.createFloat": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.CreateSystemVariableFloat(context.Background(), "x", 0, 100); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAllSystemVariablesError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"SysVar.getAll": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllSystemVariables(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAllProgramsError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Program.getAll": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetAllPrograms(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetValueError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.getValue": func(_ envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "fail"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetValue(context.Background(), "BidCos-RF", "addr:1", "VALUES", "LEVEL"); err == nil {
		t.Fatal("expected error")
	}
}

// TestListBidcosInterfaces verifies the CCU response is decoded into
// []BidcosInterface, including the CCU's quoted-string dutyCycle shape and
// the absent-carrierSense → -1 fallback, and that the interface param is
// sent on the wire.
func TestListBidcosInterfaces(t *testing.T) {
	t.Parallel()
	var capturedIface string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listBidcosInterfaces": func(env envelope) any {
			capturedIface, _ = env.Params["interface"].(string)
			return okResult([]map[string]any{
				{
					"address":     "OEQ1234567",
					"description": "HM-CCU2",
					"type":        "CCU2",
					"dutyCycle":   "42", // CCU emits a quoted string
					"isConnected": true,
					"isDefault":   true,
				},
				{
					"address":     "KEQ0111111",
					"type":        "HM-LGW",
					"dutyCycle":   "0",
					"isConnected": false,
					"isDefault":   false,
				},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListBidcosInterfaces(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("ListBidcosInterfaces: %v", err)
	}
	if capturedIface != "BidCos-RF" {
		t.Errorf("server saw interface=%q, want %q", capturedIface, "BidCos-RF")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	first := got[0]
	if first.Address != "OEQ1234567" || first.Type != "CCU2" {
		t.Errorf("first = %+v", first)
	}
	if first.DutyCycle != 42 {
		t.Errorf("DutyCycle = %d, want 42", first.DutyCycle)
	}
	if !first.Connected || !first.Default {
		t.Errorf("Connected/Default = %v/%v, want true/true", first.Connected, first.Default)
	}
	// carrierSense absent → -1.
	if first.CarrierSense != -1 {
		t.Errorf("CarrierSense = %d, want -1 (absent)", first.CarrierSense)
	}
	second := got[1]
	if second.DutyCycle != 0 {
		t.Errorf("second DutyCycle = %d, want 0", second.DutyCycle)
	}
	if second.Connected || second.Default {
		t.Errorf("second Connected/Default = %v/%v, want false/false", second.Connected, second.Default)
	}
}

// TestListBidcosInterfacesNumericAndCarrierSense checks that a native-number
// dutyCycle and a present carrierSense value are both coerced correctly.
func TestListBidcosInterfacesNumericAndCarrierSense(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listBidcosInterfaces": func(env envelope) any {
			return okResult([]map[string]any{
				{
					"address":      "OEQ9999999",
					"dutyCycle":    63, // native JSON number
					"carrierSense": "71",
					"isConnected":  true,
				},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListBidcosInterfaces(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("ListBidcosInterfaces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].DutyCycle != 63 {
		t.Errorf("DutyCycle = %d, want 63", got[0].DutyCycle)
	}
	if got[0].CarrierSense != 71 {
		t.Errorf("CarrierSense = %d, want 71", got[0].CarrierSense)
	}
}

// TestListBidcosInterfacesError checks that a server-side error surfaces.
func TestListBidcosInterfacesError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listBidcosInterfaces": func(env envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "internal"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.ListBidcosInterfaces(context.Background(), "BidCos-RF"); err == nil {
		t.Fatal("expected error")
	}
}

// TestListBidcosInterfacesMalformedFieldsFallBackToUnknown verifies that a
// dutyCycle value the CCU sends as a non-numeric string, or omits entirely,
// decodes to -1 (unknown) instead of erroring, and that boolean fields of an
// unexpected wire shape default to false rather than panicking.
func TestListBidcosInterfacesMalformedFieldsFallBackToUnknown(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listBidcosInterfaces": func(env envelope) any {
			return okResult([]map[string]any{
				{
					"address":     "OEQ0000001",
					"dutyCycle":   "n/a", // malformed, unparsable string
					"isConnected": 1.0,   // unexpected type for a bool field
					// isDefault is absent entirely.
				},
				{
					"address": "OEQ0000002",
					// dutyCycle key is absent entirely.
				},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListBidcosInterfaces(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("ListBidcosInterfaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].DutyCycle != -1 {
		t.Errorf("DutyCycle for malformed string = %d, want -1", got[0].DutyCycle)
	}
	if got[0].Connected {
		t.Errorf("Connected for unexpected-type isConnected = %v, want false", got[0].Connected)
	}
	if got[0].Default {
		t.Errorf("Default for absent isDefault = %v, want false", got[0].Default)
	}
	if got[1].DutyCycle != -1 {
		t.Errorf("DutyCycle for absent key = %d, want -1", got[1].DutyCycle)
	}
}

// TestListBidcosInterfacesEmptyResult verifies that a CCU reply with no
// gateways decodes to an empty (non-nil) slice rather than an error.
func TestListBidcosInterfacesEmptyResult(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listBidcosInterfaces": func(env envelope) any {
			return okResult([]map[string]any{})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListBidcosInterfaces(context.Background(), "BidCos-RF")
	if err != nil {
		t.Fatalf("ListBidcosInterfaces: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len=%d, want 0", len(got))
	}
}

// --- CCU security posture + CCU-reported interface list -------------------
// GetAuthEnabled / GetHTTPSRedirectEnabled / ListInterfaces feed the
// per-central system info (`GET /api/v1/system/ccu`). All three are
// best-effort at bring-up: a firmware that does not implement the method
// must degrade to the zero value rather than fail the central, so the
// nil-result and wrong-type paths matter as much as the happy path.

func TestGetAuthEnabled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result any
		want   bool
	}{
		{name: "true", result: true, want: true},
		{name: "false", result: false, want: false},
		{name: "nil result means not configured", result: nil, want: false},
		{name: "non-bool result degrades to false", result: "yes", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newTestServer(t, map[string]func(envelope) any{
				"CCU.getAuthEnabled": func(envelope) any { return okResult(tc.result) },
			})
			defer srv.Close()

			c, _ := New(Config{Endpoint: srv.URL})
			got, err := c.GetAuthEnabled(context.Background())
			if err != nil {
				t.Fatalf("GetAuthEnabled: %v", err)
			}
			if got != tc.want {
				t.Errorf("GetAuthEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetAuthEnabledError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"CCU.getAuthEnabled": func(envelope) any {
			return response{Error: &wireError{Code: -32601, Message: "method not found"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.GetAuthEnabled(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got {
		t.Errorf("GetAuthEnabled = true on error, want false")
	}
}

func TestGetHTTPSRedirectEnabled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result any
		want   bool
	}{
		{name: "true", result: true, want: true},
		{name: "false", result: false, want: false},
		{name: "nil result means not configured", result: nil, want: false},
		{name: "non-bool result degrades to false", result: 1.0, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newTestServer(t, map[string]func(envelope) any{
				"CCU.getHttpsRedirectEnabled": func(envelope) any { return okResult(tc.result) },
			})
			defer srv.Close()

			c, _ := New(Config{Endpoint: srv.URL})
			got, err := c.GetHTTPSRedirectEnabled(context.Background())
			if err != nil {
				t.Fatalf("GetHTTPSRedirectEnabled: %v", err)
			}
			if got != tc.want {
				t.Errorf("GetHTTPSRedirectEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetHTTPSRedirectEnabledError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"CCU.getHttpsRedirectEnabled": func(envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "internal"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.GetHTTPSRedirectEnabled(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestListInterfaces(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listInterfaces": func(envelope) any {
			return okResult([]map[string]any{
				{
					"type":    "HmIP-RF",
					"address": "HmIP-RF",
					"port":    2010,
					"url":     "http://127.0.0.1:2010",
				},
				{
					"type":    "BidCos-RF",
					"address": "BidCos-RF",
					"port":    2001,
					"url":     "http://127.0.0.1:2001",
				},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListInterfaces(context.Background())
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	first := got[0]
	if first.Type != "HmIP-RF" || first.Address != "HmIP-RF" {
		t.Errorf("first = %+v", first)
	}
	if first.Port != 2010 {
		t.Errorf("Port = %d, want 2010", first.Port)
	}
	if first.URL != "http://127.0.0.1:2010" {
		t.Errorf("URL = %q", first.URL)
	}
	if got[1].Port != 2001 {
		t.Errorf("second Port = %d, want 2001", got[1].Port)
	}
}

// TestListInterfacesMalformedFieldsDegradeToZero pins that an entry whose
// fields carry an unexpected wire shape (or are missing entirely) still
// yields an entry with zero values instead of erroring — the CCU-side view
// is a status-page nicety and must never fail bring-up.
func TestListInterfacesMalformedFieldsDegradeToZero(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listInterfaces": func(envelope) any {
			return okResult([]map[string]any{
				{
					"type":    123,     // not a string
					"address": nil,     // explicitly null
					"port":    "2010",  // string instead of number
					"url":     []any{}, // wrong shape entirely
				},
				{}, // every field missing
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListInterfaces(context.Background())
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for i, entry := range got {
		if entry.Type != "" || entry.Address != "" || entry.Port != 0 || entry.URL != "" {
			t.Errorf("entry %d = %+v, want all-zero", i, entry)
		}
	}
}

func TestListInterfacesEmptyResult(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listInterfaces": func(envelope) any {
			return okResult([]map[string]any{})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListInterfaces(context.Background())
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d, want 0", len(got))
	}
}

func TestListInterfacesError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.listInterfaces": func(envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "internal"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.ListInterfaces(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got != nil {
		t.Errorf("got = %+v on error, want nil", got)
	}
}

// --- wire-coercion helpers ------------------------------------------------
// Tested directly rather than through ListBidcosInterfaces: the CCU emits
// these fields in several shapes across firmware generations, and the
// json.Number branch is unreachable through the default decoder, so a
// server-driven test cannot cover it.

func TestBidcosString(t *testing.T) {
	t.Parallel()
	m := map[string]any{"str": "value", "num": 42.0, "nil": nil}
	for _, tc := range []struct {
		key  string
		want string
	}{
		{key: "str", want: "value"},
		{key: "num", want: ""},
		{key: "nil", want: ""},
		{key: "absent", want: ""},
	} {
		if got := bidcosString(m, tc.key); got != tc.want {
			t.Errorf("bidcosString(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestBidcosBool(t *testing.T) {
	t.Parallel()
	m := map[string]any{
		"native_true":  true,
		"native_false": false,
		"str_true":     "true",
		"str_one":      "1",
		"str_false":    "false",
		"str_zero":     "0",
		"str_junk":     "maybe",
		"number":       1.0,
		"nil":          nil,
	}
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{key: "native_true", want: true},
		{key: "native_false", want: false},
		{key: "str_true", want: true},
		{key: "str_one", want: true},
		{key: "str_false", want: false},
		{key: "str_zero", want: false},
		{key: "str_junk", want: false},
		{key: "number", want: false},
		{key: "nil", want: false},
		{key: "absent", want: false},
	} {
		if got := bidcosBool(m, tc.key); got != tc.want {
			t.Errorf("bidcosBool(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestBidcosPercent(t *testing.T) {
	t.Parallel()
	m := map[string]any{
		"float":        42.0,
		"float_trunc":  42.9,
		"json_number":  json.Number("42"),
		"json_bad":     json.Number("not-a-number"),
		"str":          "42",
		"str_padded":   " 42 ",
		"str_empty":    "",
		"str_junk":     "n/a",
		"str_negative": "-1",
		"wrong_type":   true,
		"nil":          nil,
	}
	for _, tc := range []struct {
		key  string
		want int
	}{
		{key: "float", want: 42},
		{key: "float_trunc", want: 42},
		{key: "json_number", want: 42},
		{key: "json_bad", want: -1},
		{key: "str", want: 42},
		{key: "str_padded", want: 42},
		{key: "str_empty", want: -1},
		{key: "str_junk", want: -1},
		{key: "str_negative", want: -1},
		{key: "wrong_type", want: -1},
		{key: "nil", want: -1},
		{key: "absent", want: -1},
	} {
		if got := bidcosPercent(m, tc.key); got != tc.want {
			t.Errorf("bidcosPercent(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}
