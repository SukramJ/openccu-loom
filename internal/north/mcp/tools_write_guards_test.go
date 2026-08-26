// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
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
// It also backs open_edit_session / close_edit_session with a minimal
// single-slot registry, enough to pin the two tools' request/response
// shape without reimplementing handlers.EditSessions' TTL/pruning.
type fakeEditLocks struct {
	key   string
	token string
	allow bool

	openKey     string
	openSubject string
	openResult  handlers.EditLock
	openOK      bool

	closeKey   string
	closeToken string
	closeOK    bool
}

func (f *fakeEditLocks) Verify(key, token string) bool {
	f.key = key
	f.token = token
	return f.allow
}

func (f *fakeEditLocks) Open(key, subject string) (handlers.EditLock, bool) {
	f.openKey = key
	f.openSubject = subject
	return f.openResult, f.openOK
}

func (f *fakeEditLocks) Close(key, token string) bool {
	f.closeKey = key
	f.closeToken = token
	return f.closeOK
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

// TestOpenEditSession_ReturnsTokenAndKey pins the happy path: a free lock
// is opened with the caller's address/key, and the returned token/expiry
// round-trip through the tool's structured output.
func TestOpenEditSession_ReturnsTokenAndKey(t *testing.T) {
	expires := time.Now().Add(5 * time.Minute).UTC()
	locks := &fakeEditLocks{openOK: true, openResult: handlers.EditLock{Token: "tok-abc", Expires: expires}}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "open_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "MASTER",
	})
	if res.IsError {
		t.Fatalf("open_edit_session returned error: %v", res.Content)
	}
	var out struct {
		Opened  bool      `json:"opened"`
		Token   string    `json:"token"`
		Key     string    `json:"key"`
		Expires time.Time `json:"expires"`
	}
	unmarshalStructured(t, res, &out)
	if !out.Opened || out.Token != "tok-abc" || out.Key != "MASTER" {
		t.Fatalf("out=%+v, want opened=true token=tok-abc key=MASTER", out)
	}
	if !out.Expires.Equal(expires) {
		t.Errorf("expires=%v, want %v", out.Expires, expires)
	}
	if locks.openKey != "channel:ADDR001:MASTER" || locks.openSubject != "mcp" {
		t.Errorf("Open called with (%q, %q), want (channel:ADDR001:MASTER, mcp)", locks.openKey, locks.openSubject)
	}
}

// TestOpenEditSession_ErrorsWhenAlreadyHeld pins that a lock another
// session holds is reported as an error, not a silent false success.
func TestOpenEditSession_ErrorsWhenAlreadyHeld(t *testing.T) {
	locks := &fakeEditLocks{openOK: false}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "open_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "MASTER",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true when the lock is already held")
	}
}

// TestOpenEditSession_RejectsNonMasterKey pins that a key other than
// MASTER is rejected before the registry is ever consulted — VALUES
// writes need no lock, and LINK is unreachable through write_paramset's
// own parseParamsetKey, so a lock for either would be dead weight.
func TestOpenEditSession_RejectsNonMasterKey(t *testing.T) {
	locks := &fakeEditLocks{openOK: true}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "open_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "VALUES",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for a non-MASTER key")
	}
	if locks.openKey != "" {
		t.Errorf("Open must not be called for a rejected key, saw %q", locks.openKey)
	}
}

// TestCloseEditSession_ReleasesLock pins that close_edit_session forwards
// address/key/edit_token to the registry's Close and reports its verdict.
func TestCloseEditSession_ReleasesLock(t *testing.T) {
	locks := &fakeEditLocks{closeOK: true}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "close_edit_session", map[string]any{
		"address":    "ADDR001",
		"key":        "MASTER",
		"edit_token": "tok-abc",
	})
	if res.IsError {
		t.Fatalf("close_edit_session returned error: %v", res.Content)
	}
	var out struct {
		Closed bool `json:"closed"`
	}
	unmarshalStructured(t, res, &out)
	if !out.Closed {
		t.Fatal("out.Closed = false, want true")
	}
	if locks.closeKey != "channel:ADDR001:MASTER" || locks.closeToken != "tok-abc" {
		t.Errorf("Close called with (%q, %q), want (channel:ADDR001:MASTER, tok-abc)", locks.closeKey, locks.closeToken)
	}
}

// TestCloseEditSession_RequiresEditToken pins that a missing edit_token is
// rejected before the registry is consulted, rather than forwarded as an
// empty string (which handlers.EditSessions.Close would just fail on
// anyway, but silently, wasting the round trip).
func TestCloseEditSession_RequiresEditToken(t *testing.T) {
	locks := &fakeEditLocks{closeOK: true}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "close_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "MASTER",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for a missing edit_token")
	}
	if locks.closeKey != "" {
		t.Errorf("Close must not be called without edit_token, saw key %q", locks.closeKey)
	}
}

// TestEditSessionTools_NotRegisteredWithoutEditLocks pins the
// per-dependency gating convention registerWriteTools documents: with no
// EditLocks wired, neither session tool is advertised, matching how
// write_paramset itself disappears without a Paramsets dependency.
func TestEditSessionTools_NotRegisteredWithoutEditLocks(t *testing.T) {
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	names := toolNames(t, cs)
	if names["open_edit_session"] || names["close_edit_session"] {
		t.Fatalf("edit-session tools must not be registered without EditLocks, got %v", names)
	}
}

// TestWriteParamset_MasterWriteReachableViaOpenEditSession is the
// regression guard for the defect this file's tools close: a MASTER
// write over MCP was permanently unreachable because write_paramset's
// edit-lock gate required a token no MCP tool could mint. It runs the
// full sequence — open_edit_session, then write_paramset with the
// returned token — against the real *handlers.EditSessions registry
// (not a fake), the same collaborator the production composition root
// wires as both write_paramset's EditLocks.Verify gate and now these two
// tools' Open/Close backend, so passing here means the fix works through
// production types, not merely with a stub that says it does.
func TestWriteParamset_MasterWriteReachableViaOpenEditSession(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	registry := handlers.NewEditSessions()

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   registry,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	openRes := callTool(t, cs, "open_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "MASTER",
	})
	if openRes.IsError {
		t.Fatalf("open_edit_session returned error: %v", openRes.Content)
	}
	var opened struct {
		Token string `json:"token"`
	}
	unmarshalStructured(t, openRes, &opened)
	if opened.Token == "" {
		t.Fatal("open_edit_session returned an empty token")
	}

	writeRes := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
		"edit_token":   opened.Token,
	})
	if writeRes.IsError {
		t.Fatalf("write_paramset with the minted token returned error: %v", writeRes.Content)
	}
	if len(ps.putCalls) != 1 {
		t.Fatalf("expected 1 PutParamset call, got %d", len(ps.putCalls))
	}

	closeRes := callTool(t, cs, "close_edit_session", map[string]any{
		"address":    "ADDR001",
		"key":        "MASTER",
		"edit_token": opened.Token,
	})
	if closeRes.IsError {
		t.Fatalf("close_edit_session returned error: %v", closeRes.Content)
	}
	var closed struct {
		Closed bool `json:"closed"`
	}
	unmarshalStructured(t, closeRes, &closed)
	if !closed.Closed {
		t.Fatal("close_edit_session did not report the lock as released")
	}

	// The lock is gone, so a second write with the now-stale token must
	// be refused again — closing genuinely released it rather than
	// leaving it live under a different bookkeeping error.
	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 12.0},
		"edit_token":   opened.Token,
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for a MASTER write after the lock was closed")
	}
}
