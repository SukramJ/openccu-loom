// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func TestMuxDispatchRegistered(t *testing.T) {
	m := NewMux()
	m.Handle("add", func(_ context.Context, params []Value) (Value, error) {
		a, _ := AsInt(params[0])
		b, _ := AsInt(params[1])
		return IntValue(int32(a + b)), nil //nolint:gosec // test
	})
	v, err := m.Dispatch(context.Background(), "add", []Value{IntValue(2), IntValue(3)})
	if err != nil {
		t.Fatal(err)
	}
	n, err := AsInt(v)
	if err != nil || n != 5 {
		t.Fatalf("got %v err=%v, want 5", v, err)
	}
}

func TestMuxUnknownMethodProducesMethodNotFoundFault(t *testing.T) {
	m := NewMux()
	_, err := m.Dispatch(context.Background(), "unknown", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault, got %T", err)
	}
	if fault.Code != -32601 {
		t.Fatalf("fault code=%d", fault.Code)
	}
}

func TestMuxFallback(t *testing.T) {
	m := NewMux()
	called := ""
	m.HandleFallback(func(_ context.Context, _ []Value) (Value, error) {
		called = "fallback"
		return NilValue{}, nil
	})
	if _, err := m.Dispatch(context.Background(), "whatever", nil); err != nil {
		t.Fatal(err)
	}
	if called != "fallback" {
		t.Fatal("fallback not invoked")
	}
}

func TestSystemListMethods(t *testing.T) {
	m := NewMux()
	m.Handle("a", func(context.Context, []Value) (Value, error) { return NilValue{}, nil })
	m.Handle("b", func(context.Context, []Value) (Value, error) { return NilValue{}, nil })
	m.RegisterSystemMethods()

	v, err := m.Dispatch(context.Background(), "system.listMethods", nil)
	if err != nil {
		t.Fatal(err)
	}
	arr, err := AsArray(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(arr) < 2 {
		t.Fatalf("listMethods returned %d entries", len(arr))
	}
}

func TestSystemMulticallWithFault(t *testing.T) {
	m := NewMux()
	m.Handle("ok", func(context.Context, []Value) (Value, error) {
		return IntValue(1), nil
	})
	m.Handle("bad", func(context.Context, []Value) (Value, error) {
		return nil, &hmerr.XMLRPCFault{Code: -2, Message: "nope"}
	})
	m.RegisterSystemMethods()

	calls := ArrayValue{
		StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue("ok")},
			{Name: "params", Value: ArrayValue{}},
		}},
		StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue("bad")},
			{Name: "params", Value: ArrayValue{}},
		}},
	}
	v, err := m.Dispatch(context.Background(), "system.multicall", []Value{calls})
	if err != nil {
		t.Fatal(err)
	}
	results, err := AsArray(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d", len(results))
	}
	// First result is a success: array of one element.
	if _, err := AsArray(results[0]); err != nil {
		t.Errorf("expected success wrapped in array, got %T", results[0])
	}
	// Second result is a fault struct.
	faultStruct, err := AsStruct(results[1])
	if err != nil {
		t.Fatalf("expected fault struct, got %T", results[1])
	}
	code, err := StructField[IntValue](faultStruct, "faultCode")
	if err != nil || code != -2 {
		t.Fatalf("fault code=%v err=%v", code, err)
	}
}

func TestMuxHandleRejectsEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty method name")
		}
	}()
	NewMux().Handle("", func(context.Context, []Value) (Value, error) { return NilValue{}, nil })
}

func TestMuxHandleRejectsNilHandler(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil handler")
		}
	}()
	NewMux().Handle("m", nil)
}
