// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// interface_client_session_hook_test.go — tests for the Item-2 parity gap:
// Config.SessionRecorderHook is called after successful SetValue / PutParamset
// so the CacheCoordinator session recorder can capture CCU-communication
// traces. A nil hook must be a no-op with no overhead.

package client_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newHookIC constructs a minimal InterfaceClient wired with hook as
// the SessionRecorderHook. The underlying Caller always succeeds.
func newHookIC(t *testing.T, hook func(rpcType, method string, params, response any)) *client.InterfaceClient {
	t.Helper()
	nop := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
		return nil, nil
	})
	ic, err := client.New(client.Config{
		CentralName:         "test",
		Interface:           hmenum.InterfaceHmIPRF,
		Caller:              nop,
		SessionRecorderHook: hook,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return ic
}

// TestSessionRecorderHookCalledOnSetValue verifies that the hook fires
// exactly once after a successful SetValue, carrying the correct method
// name and channel address in the params slice.
func TestSessionRecorderHookCalledOnSetValue(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var lastMethod string
	var lastParams any

	hook := func(rpcType, method string, params, response any) {
		calls.Add(1)
		lastMethod = method
		lastParams = params
	}

	ic := newHookIC(t, hook)
	b := &orchBackend{}

	if err := ic.SetValue(
		context.Background(), b,
		"MEQ0123456:1", hmenum.ParameterLevel, 0.5,
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false,
	); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("hook called %d times, want 1", n)
	}
	if lastMethod != "setValue" {
		t.Errorf("method=%q want setValue", lastMethod)
	}
	ps, ok := lastParams.([]any)
	if !ok || len(ps) < 1 {
		t.Fatalf("params shape unexpected: %T %v", lastParams, lastParams)
	}
	if ps[0] != "MEQ0123456:1" {
		t.Errorf("params[0]=%v want MEQ0123456:1", ps[0])
	}
}

// TestSessionRecorderHookCalledOnPutParamset verifies that the hook fires
// exactly once after a successful PutParamset with method="putParamset".
func TestSessionRecorderHookCalledOnPutParamset(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var lastMethod string

	hook := func(rpcType, method string, params, response any) {
		calls.Add(1)
		lastMethod = method
	}

	ic := newHookIC(t, hook)
	b := &orchBackend{}

	if err := ic.PutParamset(
		context.Background(), b,
		"MEQ0123456:1", "MASTER",
		map[string]any{"CYCLIC_INFO_MSG_DIS": false},
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("hook called %d times, want 1", n)
	}
	if lastMethod != "putParamset" {
		t.Errorf("method=%q want putParamset", lastMethod)
	}
}

// TestSessionRecorderHookNotCalledWhenNil verifies that a nil
// SessionRecorderHook is a no-op: SetValue and PutParamset complete
// without panic even when no hook is wired.
func TestSessionRecorderHookNotCalledWhenNil(t *testing.T) {
	t.Parallel()

	ic := newHookIC(t, nil)
	b := &orchBackend{}

	// Neither call should panic.
	if err := ic.SetValue(
		context.Background(), b,
		"MEQ0123456:1", hmenum.ParameterLevel, 1.0,
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false,
	); err != nil {
		t.Fatalf("SetValue (nil hook): %v", err)
	}
	if err := ic.PutParamset(
		context.Background(), b,
		"MEQ0123456:1", "MASTER",
		map[string]any{"K": "V"},
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false,
	); err != nil {
		t.Fatalf("PutParamset (nil hook): %v", err)
	}
}

// TestSessionRecorderHookRPCTypeIsXMLRPC verifies that the rpcType
// argument passed to the hook is "xml-rpc" for both SetValue and
// PutParamset (XML-RPC is the primary transport for these calls).
func TestSessionRecorderHookRPCTypeIsXMLRPC(t *testing.T) {
	t.Parallel()

	var seenTypes []string
	hook := func(rpcType, method string, params, response any) {
		seenTypes = append(seenTypes, rpcType)
	}

	ic := newHookIC(t, hook)
	b := &orchBackend{}

	_ = ic.SetValue(context.Background(), b, "A:1", hmenum.ParameterLevel, 1.0,
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false)
	_ = ic.PutParamset(context.Background(), b, "A:1", "MASTER",
		map[string]any{}, hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset, false)

	if len(seenTypes) != 2 {
		t.Fatalf("hook called %d times, want 2", len(seenTypes))
	}
	for i, rt := range seenTypes {
		if rt != "xml-rpc" {
			t.Errorf("seenTypes[%d]=%q want xml-rpc", i, rt)
		}
	}
}
