// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the JSON-RPC extended operations on CcuBackend defined in ccu.go
// (GetAllPrograms, SetProgramState, GetSystemUpdateInfo, GetInboxDevices,
// SetSystemVariable, CreateSystemVariableBool/Enum/Float).

package backends

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// GetAllPrograms
// ---------------------------------------------------------------------------

func TestCcuGetAllProgramsNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetAllPrograms(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetAllProgramsReturnsSlice(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: []any{
		map[string]any{"id": "42", "name": "Licht"},
	}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetAllPrograms(context.Background())
	if err != nil {
		t.Fatalf("GetAllPrograms: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	if out[0]["name"] != "Licht" {
		t.Fatalf("name=%v, want Licht", out[0]["name"])
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Program.getAll" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 0 {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestCcuGetAllProgramsJSONError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("rpc fail")
	j := &fakeCaller{err: sentinel}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	_, err := b.GetAllPrograms(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SetProgramState
// ---------------------------------------------------------------------------

func TestCcuSetProgramStateNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	err := b.SetProgramState(context.Background(), "42", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuSetProgramStateDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetProgramState(context.Background(), "42", true); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "Program.setActive" {
		t.Fatalf("method=%s", method)
	}
	if len(args) != 1 {
		t.Fatalf("arg count=%d, want 1", len(args))
	}
	params, ok2 := args[0].(map[string]any)
	if !ok2 {
		t.Fatalf("args[0] not map: %v", args[0])
	}
	if params["id"] != "42" || params["active"] != true {
		t.Fatalf("params=%v", params)
	}
}

func TestCcuSetProgramStateFalse(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetProgramState(context.Background(), "7", false); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	_, args, ok := loadArgs(j)
	if !ok {
		t.Fatal("no args stored")
	}
	params := args[0].(map[string]any)
	if params["active"] != false {
		t.Fatalf("active=%v, want false", params["active"])
	}
}

// ---------------------------------------------------------------------------
// GetSystemUpdateInfo
// ---------------------------------------------------------------------------

func TestCcuGetSystemUpdateInfoNoRega(t *testing.T) {
	t.Parallel()
	// Without a ScriptRunner the operation must return ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	_, err := b.GetSystemUpdateInfo(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetSystemUpdateInfoReturnsMap(t *testing.T) {
	t.Parallel()
	// With a ScriptRunner wired in the result is routed through ReGa.
	sr := &fakeScriptRunner{rawJSON: `{"current_firmware":"3.77.5","update_available":true}`}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(sr)
	m, err := b.GetSystemUpdateInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemUpdateInfo: %v", err)
	}
	if m["current_firmware"] != "3.77.5" {
		t.Fatalf("current_firmware=%v", m["current_firmware"])
	}
}

// TestCcuCapabilitiesReflectRunnerAfterInitialize locks the wiring-order fix:
// production calls Initialize() before SetScriptRunner(), so the rega-dependent
// Backup capability must be derived at call time, not frozen at probe time.
// Otherwise CreateBackupAndDownload short-circuits on a stale Backup=false and
// silently produces an empty result.
func TestCcuCapabilitiesReflectRunnerAfterInitialize(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)

	// Probe runs before the script runner is wired (production order).
	if err := b.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if caps := b.Capabilities(); caps.Backup {
		t.Fatal("without a runner: Backup=true, want false")
	}

	// Wiring the runner afterwards must flip the capability on.
	b.SetScriptRunner(&fakeScriptRunner{})
	if caps := b.Capabilities(); !caps.Backup {
		t.Fatal("after SetScriptRunner: Backup=false, want true")
	}
}

// ---------------------------------------------------------------------------
// GetInboxDevices
// ---------------------------------------------------------------------------

func TestCcuGetInboxDevicesNoRega(t *testing.T) {
	t.Parallel()
	// Without a ScriptRunner the operation must return ErrUnsupported.
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.GetInboxDevices(context.Background(), "HmIP-RF")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuGetInboxDevicesDispatch(t *testing.T) {
	t.Parallel()
	// GetInboxDevices routes through the ReGa script engine; wire a
	// ScriptRunner that returns a central-wide inbox list in snake_case.
	sr := &fakeScriptRunner{
		rawJSON: `[{"id":"d1","address":"AABBCCDD","name":"WRC2","type":"HmIP-WRC2","interface":"HmIP-RF"}]`,
	}
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	b.SetScriptRunner(sr)
	out, err := b.GetInboxDevices(context.Background(), "HmIP-RF")
	if err != nil {
		t.Fatalf("GetInboxDevices: %v", err)
	}
	if len(out) != 1 || out[0]["address"] != "AABBCCDD" {
		t.Fatalf("out=%v", out)
	}
	if sr.called.Load() != 1 {
		t.Fatalf("ScriptRunner.RunJSON call count = %d, want 1", sr.called.Load())
	}
}

// ---------------------------------------------------------------------------
// SetSystemVariable
// ---------------------------------------------------------------------------

func TestCcuSetSystemVariableNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	err := b.SetSystemVariable(context.Background(), "var1", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuSetSystemVariableBoolTrue(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myBool", true); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.setBool" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "myBool" || params["value"] != 1 {
		t.Fatalf("params=%v", params)
	}
}

func TestCcuSetSystemVariableBoolFalse(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myBool", false); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	_, args, ok := loadArgs(j)
	if !ok {
		t.Fatal("no args")
	}
	params := args[0].(map[string]any)
	if params["value"] != 0 {
		t.Fatalf("value=%v, want 0", params["value"])
	}
}

func TestCcuSetSystemVariableFloat64(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myFloat", float64(3.14)); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.setFloat" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["value"].(float64) != 3.14 {
		t.Fatalf("value=%v", params["value"])
	}
}

func TestCcuSetSystemVariableFloat32(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myFloat", float32(1.5)); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "SysVar.setFloat" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuSetSystemVariableInt(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myInt", int(7)); err != nil {
		t.Fatalf("SetSystemVariable int: %v", err)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.setFloat" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["value"].(float64) != 7 {
		t.Fatalf("value=%v", params["value"])
	}
}

func TestCcuSetSystemVariableInt32(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myInt", int32(5)); err != nil {
		t.Fatalf("SetSystemVariable int32: %v", err)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "SysVar.setFloat" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuSetSystemVariableInt64(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	if err := b.SetSystemVariable(context.Background(), "myInt", int64(99)); err != nil {
		t.Fatalf("SetSystemVariable int64: %v", err)
	}
	method, _, ok := loadArgs(j)
	if !ok || method != "SysVar.setFloat" {
		t.Fatalf("method=%s", method)
	}
}

func TestCcuSetSystemVariableUnsupportedType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	err := b.SetSystemVariable(context.Background(), "x", []byte("bad"))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported for unsupported type, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// CreateSystemVariableBool
// ---------------------------------------------------------------------------

func TestCcuCreateSystemVariableBoolNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.CreateSystemVariableBool(context.Background(), "bv", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuCreateSystemVariableBoolTrueDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"iseID": "100"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.CreateSystemVariableBool(context.Background(), "boolVar", true)
	if err != nil {
		t.Fatalf("CreateSystemVariableBool: %v", err)
	}
	if out["iseID"] != "100" {
		t.Fatalf("out=%v", out)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.createBool" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "boolVar" {
		t.Fatalf("name=%v", params["name"])
	}
	if params["init_val"] != 1 {
		t.Fatalf("init_val=%v, want 1", params["init_val"])
	}
}

func TestCcuCreateSystemVariableBoolFalseDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"iseID": "101"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	_, err := b.CreateSystemVariableBool(context.Background(), "boolVar2", false)
	if err != nil {
		t.Fatalf("CreateSystemVariableBool false: %v", err)
	}
	_, args, ok := loadArgs(j)
	if !ok {
		t.Fatal("no args")
	}
	params := args[0].(map[string]any)
	if params["init_val"] != 0 {
		t.Fatalf("init_val=%v, want 0", params["init_val"])
	}
}

// ---------------------------------------------------------------------------
// CreateSystemVariableEnum
// ---------------------------------------------------------------------------

func TestCcuCreateSystemVariableEnumNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.CreateSystemVariableEnum(context.Background(), "e", []string{"a", "b"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuCreateSystemVariableEnumDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"iseID": "200"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.CreateSystemVariableEnum(context.Background(), "myEnum", []string{"Off", "On", "Auto"})
	if err != nil {
		t.Fatalf("CreateSystemVariableEnum: %v", err)
	}
	if out["iseID"] != "200" {
		t.Fatalf("out=%v", out)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.createEnum" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "myEnum" {
		t.Fatalf("name=%v", params["name"])
	}
	if params["valList"] != "Off;On;Auto" {
		t.Fatalf("valList=%v, want Off;On;Auto", params["valList"])
	}
}

func TestCcuCreateSystemVariableEnumSingleValue(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	_, err := b.CreateSystemVariableEnum(context.Background(), "single", []string{"Only"})
	if err != nil {
		t.Fatalf("CreateSystemVariableEnum single: %v", err)
	}
	_, args, ok := loadArgs(j)
	if !ok {
		t.Fatal("no args")
	}
	params := args[0].(map[string]any)
	if params["valList"] != "Only" {
		t.Fatalf("valList=%v, want Only", params["valList"])
	}
}

// ---------------------------------------------------------------------------
// CreateSystemVariableFloat
// ---------------------------------------------------------------------------

func TestCcuCreateSystemVariableFloatNoJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil)
	_, err := b.CreateSystemVariableFloat(context.Background(), "f", 0, 100)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCcuCreateSystemVariableFloatDispatch(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: map[string]any{"iseID": "300"}}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.CreateSystemVariableFloat(context.Background(), "temp", -50.0, 100.0)
	if err != nil {
		t.Fatalf("CreateSystemVariableFloat: %v", err)
	}
	if out["iseID"] != "300" {
		t.Fatalf("out=%v", out)
	}
	method, args, ok := loadArgs(j)
	if !ok || method != "SysVar.createFloat" {
		t.Fatalf("method=%s", method)
	}
	params := args[0].(map[string]any)
	if params["name"] != "temp" {
		t.Fatalf("name=%v", params["name"])
	}
	if params["minValue"].(float64) != -50.0 || params["maxValue"].(float64) != 100.0 {
		t.Fatalf("minValue=%v maxValue=%v", params["minValue"], params["maxValue"])
	}
}

// ---------------------------------------------------------------------------
// toSliceOfMaps (indirect coverage via nil / wrong-type paths)
// ---------------------------------------------------------------------------

func TestCcuToSliceOfMapsNilInput(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: nil}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	out, err := b.GetAllPrograms(context.Background())
	if err != nil {
		t.Fatalf("GetAllPrograms nil: %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil slice, got %v", out)
	}
}

func TestCcuToSliceOfMapsWrongType(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{reply: "not a slice"}
	b := NewCcuBackend(&fakeCaller{}, j, nil)
	_, err := b.GetAllPrograms(context.Background())
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}
