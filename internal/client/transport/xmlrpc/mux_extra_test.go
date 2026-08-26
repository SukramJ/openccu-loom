// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"context"
	"testing"
)

// TestRegisterSystemMethodsListMethods verifies that system.listMethods
// returns the list of registered handlers.
func TestRegisterSystemMethodsListMethods(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.Handle("getValue", func(context.Context, []Value) (Value, error) { return NilValue{}, nil })
	m.RegisterSystemMethods()

	v, err := m.Dispatch(context.Background(), "system.listMethods", nil)
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}
	arr, err := AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	// Should contain at least "getValue" and the system methods.
	if len(arr) == 0 {
		t.Fatal("system.listMethods returned empty list")
	}
}

// TestRegisterSystemMethodsMethodHelp verifies that system.methodHelp
// returns an empty string (we intentionally don't populate help text).
func TestRegisterSystemMethodsMethodHelp(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.RegisterSystemMethods()
	v, err := m.Dispatch(context.Background(), "system.methodHelp", nil)
	if err != nil {
		t.Fatalf("system.methodHelp: %v", err)
	}
	s, err := AsString(v)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if s != "" {
		t.Fatalf("system.methodHelp = %q, want empty", s)
	}
}

// TestRegisterSystemMethodsMethodSignature verifies that
// system.methodSignature returns ["undef"].
func TestRegisterSystemMethodsMethodSignature(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.RegisterSystemMethods()
	v, err := m.Dispatch(context.Background(), "system.methodSignature", nil)
	if err != nil {
		t.Fatalf("system.methodSignature: %v", err)
	}
	arr, err := AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("signature len=%d, want 1", len(arr))
	}
	s, err := AsString(arr[0])
	if err != nil || s != "undef" {
		t.Fatalf("signature[0]=%q err=%v, want undef", s, err)
	}
}

// TestRegisterSystemMethodsMulticallWrongParamCount exercises the
// "wrong param count" branch in system.multicall.
func TestRegisterSystemMethodsMulticallWrongParamCount(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.RegisterSystemMethods()
	// Pass 0 params instead of 1.
	if _, err := m.Dispatch(context.Background(), "system.multicall", nil); err == nil {
		t.Fatal("system.multicall with 0 params must return error")
	}
}

// TestRegisterSystemMethodsMulticallNonArray exercises the
// "param is not an array" error in system.multicall.
func TestRegisterSystemMethodsMulticallNonArray(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.RegisterSystemMethods()
	// Pass an IntValue (not an ArrayValue).
	if _, err := m.Dispatch(context.Background(), "system.multicall", []Value{IntValue(99)}); err == nil {
		t.Fatal("system.multicall with non-array param must return error")
	}
}

// TestRegisterSystemMethodsMulticallNonStruct exercises the path where
// an array element is not a struct.
func TestRegisterSystemMethodsMulticallNonStruct(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.RegisterSystemMethods()
	// Each call must be a struct; pass a string instead.
	calls := ArrayValue{StringValue("notastruct")}
	if _, err := m.Dispatch(context.Background(), "system.multicall", []Value{calls}); err == nil {
		t.Fatal("system.multicall with non-struct call must return error")
	}
}

// TestRegisterSystemMethodsMulticallHappyPath exercises the successful
// multicall path with one embedded method call.
func TestRegisterSystemMethodsMulticallHappyPath(t *testing.T) {
	t.Parallel()

	m := NewMux()
	m.Handle("echo", func(_ context.Context, params []Value) (Value, error) {
		return params[0], nil
	})
	m.RegisterSystemMethods()

	call := StructValue{Members: []Member{
		{Name: "methodName", Value: StringValue("echo")},
		{Name: "params", Value: ArrayValue{StringValue("hello")}},
	}}
	calls := ArrayValue{call}
	v, err := m.Dispatch(context.Background(), "system.multicall", []Value{calls})
	if err != nil {
		t.Fatalf("system.multicall: %v", err)
	}
	arr, err := AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("results len=%d, want 1", len(arr))
	}
}
