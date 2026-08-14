// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// captureLastArgs returns the args slice from the last Call on a fakeCaller.
// The call must have happened before this is invoked.
func captureLastArgs(f *fakeCaller) (method string, args []any, ok bool) {
	return loadArgs(f)
}

// ---------------------------------------------------------------------------
// SetValue — rxMode = BURST appended as 5th wire argument
// ---------------------------------------------------------------------------

func TestSetValueWithRxModeBurst(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.SetValue(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParameterState,
		true,
		hmenum.CommandPriorityHigh,
		hmenum.CommandRxModeBurst,
	); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	method, args, ok := captureLastArgs(x)
	if !ok || method != "setValue" {
		t.Fatalf("method=%s", method)
	}
	// Expected wire shape: setValue(address, parameter, value, "BURST")
	if len(args) != 4 {
		t.Fatalf("arg count=%d, want 4 (address, param, value, rxMode); args=%v", len(args), args)
	}
	if args[0] != "0001ABCD:1" {
		t.Fatalf("args[0]=%v, want 0001ABCD:1", args[0])
	}
	if args[1] != string(hmenum.ParameterState) {
		t.Fatalf("args[1]=%v, want STATE", args[1])
	}
	if args[2] != true {
		t.Fatalf("args[2]=%v, want true", args[2])
	}
	if args[3] != "BURST" {
		t.Fatalf("args[3]=%v, want BURST", args[3])
	}
}

// ---------------------------------------------------------------------------
// SetValue — rxMode = Unset → NO 5th argument on the wire
// ---------------------------------------------------------------------------

func TestSetValueWithRxModeUnsetSendsNoArgument(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.SetValue(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParameterState,
		true,
		hmenum.CommandPriorityHigh,
		hmenum.CommandRxModeUnset,
	); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	method, args, ok := captureLastArgs(x)
	if !ok || method != "setValue" {
		t.Fatalf("method=%s", method)
	}
	// Wire must carry ONLY address, parameter, value — no rx_mode argument.
	if len(args) != 3 {
		t.Fatalf("arg count=%d, want 3 (no rx_mode on wire); args=%v", len(args), args)
	}
	if args[0] != "0001ABCD:1" || args[1] != string(hmenum.ParameterState) || args[2] != true {
		t.Fatalf("args=%v", args)
	}
}

// ---------------------------------------------------------------------------
// SetValue — rxMode = WAKEUP appended as 5th wire argument
// ---------------------------------------------------------------------------

func TestSetValueWithRxModeWakeup(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	if err := b.SetValue(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParameterState,
		false,
		hmenum.CommandPriorityHigh,
		hmenum.CommandRxModeWakeup,
	); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	_, args, ok := captureLastArgs(x)
	if !ok {
		t.Fatal("no call recorded")
	}
	if len(args) != 4 || args[3] != "WAKEUP" {
		t.Fatalf("args=%v, want 4 args with args[3]=WAKEUP", args)
	}
}

// ---------------------------------------------------------------------------
// PutParamset — rxMode = WAKEUP appended as 4th wire argument
// ---------------------------------------------------------------------------

func TestPutParamsetWithRxModeWakeup(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	values := map[string]any{"SET_TEMPERATURE": 20.5}
	if err := b.PutParamset(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParamsetKeyMaster,
		values,
		hmenum.CommandPriorityLow,
		hmenum.CommandRxModeWakeup,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	method, args, ok := captureLastArgs(x)
	if !ok || method != "putParamset" {
		t.Fatalf("method=%s", method)
	}
	// Expected wire shape: putParamset(address, paramset, values, "WAKEUP")
	if len(args) != 4 {
		t.Fatalf("arg count=%d, want 4 (address, paramset, values, rxMode); args=%v", len(args), args)
	}
	if args[0] != "0001ABCD:1" {
		t.Fatalf("args[0]=%v", args[0])
	}
	if args[1] != "MASTER" {
		t.Fatalf("args[1]=%v, want MASTER", args[1])
	}
	if args[3] != "WAKEUP" {
		t.Fatalf("args[3]=%v, want WAKEUP", args[3])
	}
}

// ---------------------------------------------------------------------------
// PutParamset — rxMode = Unset → NO 4th argument on the wire
// ---------------------------------------------------------------------------

func TestPutParamsetWithRxModeUnsetSendsNoArgument(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	values := map[string]any{"SET_TEMPERATURE": 22.0}
	if err := b.PutParamset(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParamsetKeyMaster,
		values,
		hmenum.CommandPriorityLow,
		hmenum.CommandRxModeUnset,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	method, args, ok := captureLastArgs(x)
	if !ok || method != "putParamset" {
		t.Fatalf("method=%s", method)
	}
	// Wire must carry ONLY address, paramset key, values — no rx_mode.
	if len(args) != 3 {
		t.Fatalf("arg count=%d, want 3 (no rx_mode on wire); args=%v", len(args), args)
	}
}

// ---------------------------------------------------------------------------
// PutParamset — rxMode = LAZY_CONFIG appended as 4th wire argument
// ---------------------------------------------------------------------------

func TestPutParamsetWithRxModeLazyConfig(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: nil}
	b := NewCcuBackend(x, nil, nil)

	values := map[string]any{"MIN_TEMPERATURE": 10.0}
	if err := b.PutParamset(
		context.Background(),
		"0001ABCD:1",
		hmenum.ParamsetKeyMaster,
		values,
		hmenum.CommandPriorityLow,
		hmenum.CommandRxModeLazyConfig,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	_, args, ok := captureLastArgs(x)
	if !ok {
		t.Fatal("no call recorded")
	}
	if len(args) != 4 || args[3] != "LAZY_CONFIG" {
		t.Fatalf("args=%v, want 4 args with args[3]=LAZY_CONFIG", args)
	}
}

// ---------------------------------------------------------------------------
// CUxD backend — rxMode is silently ignored (BIN-RPC has no rx_mode)
// ---------------------------------------------------------------------------

func TestCuxdSetValueIgnoresRxMode(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: nil}
	b := NewCuxdBackend(bin, nil)

	if err := b.SetValue(
		context.Background(),
		"CUX0001:1",
		hmenum.ParameterState,
		true,
		hmenum.CommandPriorityHigh,
		hmenum.CommandRxModeBurst, // should be silently ignored
	); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	method, args, ok := captureLastArgs(bin)
	if !ok || method != "setValue" {
		t.Fatalf("method=%s", method)
	}
	// BIN-RPC wire must carry ONLY address, parameter, value — NO rx_mode.
	if len(args) != 3 {
		t.Fatalf("CUxD must not put rxMode on BIN-RPC wire; arg count=%d args=%v", len(args), args)
	}
}

func TestCuxdPutParamsetIgnoresRxMode(t *testing.T) {
	t.Parallel()
	bin := &fakeCaller{reply: nil}
	b := NewCuxdBackend(bin, nil)

	values := map[string]any{"STATE": false}
	if err := b.PutParamset(
		context.Background(),
		"CUX0001:1",
		hmenum.ParamsetKeyValues,
		values,
		hmenum.CommandPriorityLow,
		hmenum.CommandRxModeBurst, // should be silently ignored
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	method, args, ok := captureLastArgs(bin)
	if !ok || method != "putParamset" {
		t.Fatalf("method=%s", method)
	}
	// BIN-RPC wire must carry ONLY address, paramset key, values — NO rx_mode.
	if len(args) != 3 {
		t.Fatalf("CUxD must not put rxMode on BIN-RPC wire; arg count=%d args=%v", len(args), args)
	}
}
