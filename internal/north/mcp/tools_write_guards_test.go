// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// tools_write_guards_test.go pins the two write-path guards the MCP tools
// share with their REST/WS siblings: set_datapoint coerces the incoming
// value against the parameter descriptor before the wire (so an ENUM label
// becomes its index and a JSON number lands as the descriptor's type), and
// write_paramset honours the strict edit lock for MASTER configuration
// writes.

package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeChannelDeviceFixture builds a device carrying one channel with an ENUM
// VALUES parameter (BEHAVIOUR, VALUE_LIST [ANALOG_OUTPUT, DIGITAL_OUTPUT])
// and an INTEGER VALUES parameter (LEVEL, 0..100), so set_datapoint can
// resolve a descriptor to coerce against. The device is owned by "ccu1".
func makeChannelDeviceFixture() *fakeDevices {
	dev := device.New(device.Config{
		Address:     "ENUMDEV",
		Model:       "HmIP-MIO",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF",
	})
	ch := dev.AddChannel("ENUMDEV:1", 1, "MULTI_MODE_INPUT_TRANSMITTER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ENUMDEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "BEHAVIOUR",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			ValueList:  []string{"ANALOG_OUTPUT", "DIGITAL_OUTPUT"},
		},
	}))
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ENUMDEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("100"),
		},
	}))
	devs := newFakeDevices()
	devs.add(dev, "ccu1")
	return devs
}

// TestSetDatapoint_CoercesEnumLabelToIndex pins that set_datapoint maps an
// ENUM option label to its integer index before the wire — the CCU expects
// the index, not the label token Home Assistant hands back.
func TestSetDatapoint_CoercesEnumLabelToIndex(t *testing.T) {
	devs := makeChannelDeviceFixture()
	writer := &fakeWriter{}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Writer:      writer,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu1",
		"address":      "ENUMDEV:1",
		"parameter":    "BEHAVIOUR",
		"value":        "DIGITAL_OUTPUT",
	})
	if res.IsError {
		t.Fatalf("set_datapoint returned error: %v", res.Content)
	}
	iv, ok := writer.last.value.(int)
	if !ok || iv != 1 {
		t.Fatalf("writer received %T(%v); the enum label must be coerced to index 1", writer.last.value, writer.last.value)
	}
}

// TestSetDatapoint_CoercesJSONNumberToInteger pins that a JSON number (which
// arrives as float64 over MCP) is coerced to the descriptor's INTEGER type,
// rather than reaching the CCU as a float.
func TestSetDatapoint_CoercesJSONNumberToInteger(t *testing.T) {
	devs := makeChannelDeviceFixture()
	writer := &fakeWriter{}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Writer:      writer,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu1",
		"address":      "ENUMDEV:1",
		"parameter":    "LEVEL",
		"value":        21,
	})
	if res.IsError {
		t.Fatalf("set_datapoint returned error: %v", res.Content)
	}
	iv, ok := writer.last.value.(int)
	if !ok || iv != 21 {
		t.Fatalf("writer received %T(%v); a JSON number for an INTEGER must be coerced to int 21", writer.last.value, writer.last.value)
	}
}

// fakeEditLocks records the last Verify call and returns a preset verdict.
type fakeEditLocks struct {
	key   string
	token string
	allow bool
}

func (f *fakeEditLocks) Verify(key, token string) bool {
	f.key = key
	f.token = token
	return f.allow
}

// TestWriteParamset_RefusedWhileEditLockHeld pins that a MASTER paramset
// write over MCP is refused while an edit session holds the lock: MCP
// presents no token, so the shared verifier reports the lock is not held by
// this caller and the write must never reach the paramset backend.
func TestWriteParamset_RefusedWhileEditLockHeld(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	locks := &fakeEditLocks{allow: false} // a session holds the lock; MCP does not

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for a MASTER write while the edit lock is held")
	}
	if len(ps.putCalls) != 0 {
		t.Errorf("PutParamset must not run when the edit lock is held, got %d calls", len(ps.putCalls))
	}
	if locks.key != "channel:ADDR001:MASTER" {
		t.Errorf("edit-lock key: got %q, want channel:ADDR001:MASTER", locks.key)
	}
}

// TestWriteParamset_ProceedsWithEditToken pins the counterpart: a MASTER
// write succeeds when the presented token holds the lock.
func TestWriteParamset_ProceedsWithEditToken(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	locks := &fakeEditLocks{allow: true}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
		"edit_token":   "tok-123",
	})
	if res.IsError {
		t.Fatalf("write_paramset with a valid edit token returned error: %v", res.Content)
	}
	if len(ps.putCalls) != 1 {
		t.Fatalf("expected 1 PutParamset call with a valid token, got %d", len(ps.putCalls))
	}
	if locks.token != "tok-123" {
		t.Errorf("edit-lock token: got %q, want tok-123", locks.token)
	}
}

// TestWriteParamset_ValuesNotGatedByEditLock pins that a VALUES write is not
// subject to the edit lock — device control stays live even while a
// configuration edit session is open.
func TestWriteParamset_ValuesNotGatedByEditLock(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	locks := &fakeEditLocks{allow: false}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "VALUES",
		"values":       map[string]any{"STATE": true},
	})
	if res.IsError {
		t.Fatalf("a VALUES write must not be gated by the edit lock: %v", res.Content)
	}
	if len(ps.putCalls) != 1 {
		t.Fatalf("expected 1 PutParamset call for the ungated VALUES write, got %d", len(ps.putCalls))
	}
	if locks.key != "" {
		t.Errorf("the edit-lock verifier must not be consulted for a VALUES write, but it saw key %q", locks.key)
	}
}
