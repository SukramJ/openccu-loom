// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// ============================================================
// goToXMLRPCValue tests
// ============================================================

// TestGoToXMLRPCValueNil verifies nil → NilValue.
func TestGoToXMLRPCValueNil(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(nil)
	if err != nil {
		t.Fatalf("nil: %v", err)
	}
	if _, ok := v.(xmlrpc.NilValue); !ok {
		t.Errorf("nil → %T, want NilValue", v)
	}
}

// TestGoToXMLRPCValueString verifies string → StringValue.
func TestGoToXMLRPCValueString(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue("hello")
	if err != nil {
		t.Fatalf("string: %v", err)
	}
	if s, ok := v.(xmlrpc.StringValue); !ok || string(s) != "hello" {
		t.Errorf("string → %T(%v), want StringValue(hello)", v, v)
	}
}

// TestGoToXMLRPCValueInt verifies int → IntValue.
func TestGoToXMLRPCValueInt(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(42)
	if err != nil {
		t.Fatalf("int: %v", err)
	}
	if n, ok := v.(xmlrpc.IntValue); !ok || int(n) != 42 {
		t.Errorf("int → %T(%v), want IntValue(42)", v, v)
	}
}

// TestGoToXMLRPCValueInt32 verifies int32 → IntValue.
func TestGoToXMLRPCValueInt32(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(int32(7))
	if err != nil {
		t.Fatalf("int32: %v", err)
	}
	if n, ok := v.(xmlrpc.IntValue); !ok || int(n) != 7 {
		t.Errorf("int32 → %T(%v), want IntValue(7)", v, v)
	}
}

// TestGoToXMLRPCValueInt64 verifies int64 → IntValue.
func TestGoToXMLRPCValueInt64(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(int64(100))
	if err != nil {
		t.Fatalf("int64: %v", err)
	}
	if n, ok := v.(xmlrpc.IntValue); !ok || int(n) != 100 {
		t.Errorf("int64 → %T(%v), want IntValue(100)", v, v)
	}
}

// TestGoToXMLRPCValueBool verifies bool → BoolValue.
func TestGoToXMLRPCValueBool(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(true)
	if err != nil {
		t.Fatalf("bool: %v", err)
	}
	if b, ok := v.(xmlrpc.BoolValue); !ok || !bool(b) {
		t.Errorf("bool → %T(%v), want BoolValue(true)", v, v)
	}
}

// TestGoToXMLRPCValueFloat64 verifies float64 → DoubleValue.
func TestGoToXMLRPCValueFloat64(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(float64(3.14))
	if err != nil {
		t.Fatalf("float64: %v", err)
	}
	if f, ok := v.(xmlrpc.DoubleValue); !ok || float64(f) != 3.14 {
		t.Errorf("float64 → %T(%v), want DoubleValue(3.14)", v, v)
	}
}

// TestGoToXMLRPCValueFloat32 verifies float32 → DoubleValue.
func TestGoToXMLRPCValueFloat32(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(float32(1.5))
	if err != nil {
		t.Fatalf("float32: %v", err)
	}
	if _, ok := v.(xmlrpc.DoubleValue); !ok {
		t.Errorf("float32 → %T, want DoubleValue", v)
	}
}

// TestGoToXMLRPCValueStringSlice verifies []string → ArrayValue.
func TestGoToXMLRPCValueStringSlice(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue([]string{"a", "b"})
	if err != nil {
		t.Fatalf("[]string: %v", err)
	}
	arr, ok := v.(xmlrpc.ArrayValue)
	if !ok {
		t.Fatalf("[]string → %T, want ArrayValue", v)
	}
	if len(arr) != 2 {
		t.Errorf("len = %d, want 2", len(arr))
	}
}

// TestGoToXMLRPCValueAnySlice verifies []any → ArrayValue (recursive).
func TestGoToXMLRPCValueAnySlice(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue([]any{1, "two", true})
	if err != nil {
		t.Fatalf("[]any: %v", err)
	}
	arr, ok := v.(xmlrpc.ArrayValue)
	if !ok {
		t.Fatalf("[]any → %T, want ArrayValue", v)
	}
	if len(arr) != 3 {
		t.Errorf("len = %d, want 3", len(arr))
	}
}

// TestGoToXMLRPCValueMapStringAny verifies map[string]any → StructValue.
func TestGoToXMLRPCValueMapStringAny(t *testing.T) {
	t.Parallel()
	v, err := goToXMLRPCValue(map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	sv, ok := v.(xmlrpc.StructValue)
	if !ok {
		t.Fatalf("map → %T, want StructValue", v)
	}
	if len(sv.Members) != 1 || sv.Members[0].Name != "key" {
		t.Errorf("StructValue members = %+v", sv.Members)
	}
}

// TestGoToXMLRPCValueUnsupportedType verifies that an unsupported type
// returns a non-nil error.
func TestGoToXMLRPCValueUnsupportedType(t *testing.T) {
	t.Parallel()
	_, err := goToXMLRPCValue(struct{ X int }{X: 1})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// ============================================================
// xmlRPCValueToGo tests — covers additional branches not yet hit
// ============================================================

// TestXMLRPCValueToGoNilValue verifies NilValue → nil.
func TestXMLRPCValueToGoNilValue(t *testing.T) {
	t.Parallel()
	if got := xmlRPCValueToGo(xmlrpc.NilValue{}); got != nil {
		t.Errorf("NilValue → %v, want nil", got)
	}
}

// TestXMLRPCValueToGoStringValue verifies StringValue → string.
func TestXMLRPCValueToGoStringValue(t *testing.T) {
	t.Parallel()
	if got := xmlRPCValueToGo(xmlrpc.StringValue("hello")); got != "hello" {
		t.Errorf("StringValue → %v, want hello", got)
	}
}

// TestXMLRPCValueToGoIntValue verifies IntValue → int.
func TestXMLRPCValueToGoIntValue(t *testing.T) {
	t.Parallel()
	if got := xmlRPCValueToGo(xmlrpc.IntValue(7)); got != 7 {
		t.Errorf("IntValue → %v, want 7", got)
	}
}

// TestXMLRPCValueToGoBoolValue verifies BoolValue → bool.
func TestXMLRPCValueToGoBoolValue(t *testing.T) {
	t.Parallel()
	if got := xmlRPCValueToGo(xmlrpc.BoolValue(true)); got != true {
		t.Errorf("BoolValue → %v, want true", got)
	}
}

// TestXMLRPCValueToGoDoubleValue verifies DoubleValue → float64.
func TestXMLRPCValueToGoDoubleValue(t *testing.T) {
	t.Parallel()
	if got := xmlRPCValueToGo(xmlrpc.DoubleValue(1.5)); got != 1.5 {
		t.Errorf("DoubleValue → %v, want 1.5", got)
	}
}

// TestXMLRPCValueToGoArrayValue verifies ArrayValue → []any (recursive).
func TestXMLRPCValueToGoArrayValue(t *testing.T) {
	t.Parallel()
	arr := xmlrpc.ArrayValue{xmlrpc.IntValue(1), xmlrpc.StringValue("two")}
	got, ok := xmlRPCValueToGo(arr).([]any)
	if !ok {
		t.Fatalf("ArrayValue → %T, want []any", xmlRPCValueToGo(arr))
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// TestXMLRPCValueToGoStructValue verifies StructValue → map[string]any.
func TestXMLRPCValueToGoStructValue(t *testing.T) {
	t.Parallel()
	sv := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "k", Value: xmlrpc.IntValue(99)},
	}}
	got, ok := xmlRPCValueToGo(sv).(map[string]any)
	if !ok {
		t.Fatalf("StructValue → %T, want map[string]any", xmlRPCValueToGo(sv))
	}
	if got["k"] != 99 {
		t.Errorf("k = %v, want 99", got["k"])
	}
}

// TestXMLRPCValueToGoEmptyArray verifies empty ArrayValue → empty []any.
func TestXMLRPCValueToGoEmptyArray(t *testing.T) {
	t.Parallel()
	got := xmlRPCValueToGo(xmlrpc.ArrayValue{})
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("empty ArrayValue → %T, want []any", got)
	}
	if len(arr) != 0 {
		t.Errorf("len = %d, want 0", len(arr))
	}
}
