// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmtypes

import (
	"testing"
)

// TestValueKindString exercises all ValueKind.String() branches, including
// the default unknown branch.
func TestValueKindString(t *testing.T) {
	cases := []struct {
		k    ValueKind
		want string
	}{
		{ValueKindNone, "none"},
		{ValueKindBool, "bool"},
		{ValueKindInt, "int"},
		{ValueKindFloat, "float"},
		{ValueKindString, "string"},
		{ValueKindList, "list"},
		{ValueKind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("ValueKind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

// TestNewParamValueAllTypes verifies every supported input type.
func TestNewParamValueAllTypes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		kind ValueKind
	}{
		{"nil", nil, ValueKindNone},
		{"bool-true", true, ValueKindBool},
		{"bool-false", false, ValueKindBool},
		{"int", 42, ValueKindInt},
		{"int32", int32(7), ValueKindInt},
		{"int64", int64(-3), ValueKindInt},
		{"float32", float32(1.5), ValueKindFloat},
		{"float64-frac", 3.14, ValueKindFloat},
		{"float64-int", float64(5), ValueKindInt}, // integer-valued float → int
		{"string", "hello", ValueKindString},
		{"[]string", []string{"a", "b"}, ValueKindList},
		{"[]any-strings", []any{"x", "y"}, ValueKindList},
	}
	for _, tc := range cases {
		v, err := NewParamValue(tc.in)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if v.Kind != tc.kind {
			t.Errorf("%s: kind = %v, want %v", tc.name, v.Kind, tc.kind)
		}
	}
}

func TestNewParamValueErrorCases(t *testing.T) {
	// []any with a non-string element.
	_, err := NewParamValue([]any{"ok", 42})
	if err == nil {
		t.Error("[]any with int element should return error")
	}

	// Unsupported type.
	_, err = NewParamValue(struct{}{})
	if err == nil {
		t.Error("unsupported type should return error")
	}
}

// TestParamValueUnwrap exercises every Unwrap branch.
func TestParamValueUnwrap(t *testing.T) {
	if NoneValue().Unwrap() != nil {
		t.Error("NoneValue.Unwrap() should be nil")
	}
	if BoolValue(true).Unwrap() != true {
		t.Error("BoolValue(true).Unwrap() != true")
	}
	if IntValue(7).Unwrap() != 7 {
		t.Error("IntValue(7).Unwrap() != 7")
	}
	if FloatValue(1.5).Unwrap() != 1.5 {
		t.Error("FloatValue(1.5).Unwrap() != 1.5")
	}
	if StringValue("x").Unwrap() != "x" {
		t.Error("StringValue(x).Unwrap() != x")
	}
	list := []string{"a", "b"}
	v := ListValue(list)
	got := v.Unwrap().([]string)
	if len(got) != 2 || got[0] != "a" {
		t.Errorf("ListValue.Unwrap() = %v", got)
	}
	// Unknown kind.
	bad := ParamValue{Kind: ValueKind(99)}
	if bad.Unwrap() != nil {
		t.Error("unknown kind Unwrap should be nil")
	}
}

// TestParamValueIsNone checks the IsNone sentinel.
func TestParamValueIsNone(t *testing.T) {
	if !NoneValue().IsNone() {
		t.Error("NoneValue should be none")
	}
	if IntValue(0).IsNone() {
		t.Error("IntValue(0) should not be none")
	}
}

// TestDataPointKeyDeviceAddressNoColon exercises the branch where ChannelAddress
// has no colon, so DeviceAddress should return it unchanged.
func TestDataPointKeyDeviceAddressNoColon(t *testing.T) {
	k := DataPointKey{ChannelAddress: "STANDALONE"}
	if got := k.DeviceAddress(); got != "STANDALONE" {
		t.Errorf("DeviceAddress with no colon = %q, want STANDALONE", got)
	}
}

// TestHashSHA256UnmarshalFallback exercises the error branch in HashSHA256 by
// passing a value that contains a channel (json.Marshal fails on chan types).
func TestHashSHA256UnmarshalFallback(t *testing.T) {
	// json.Marshal on a chan will fail → makeValueHashable fallback is taken.
	ch := make(chan int)
	h := HashSHA256(ch)
	if h == "" {
		t.Error("HashSHA256 on chan should produce non-empty hash via fallback")
	}
}

// TestHashSHA256MapAndSliceFallback exercises the map and slice branches in
// makeValueHashable (reached via HashSHA256 when Marshal fails).
func TestHashSHA256MapFallback(t *testing.T) {
	// A map containing a chan will cause json.Marshal to fail.
	m := map[string]any{"key": make(chan int)}
	h := HashSHA256(m)
	if h == "" {
		t.Error("HashSHA256 on map-with-chan should produce non-empty hash")
	}
}

// TestParamValueAsStringUnknown exercises the default/unknown branch in AsString.
func TestParamValueAsStringUnknown(t *testing.T) {
	v := ParamValue{Kind: ValueKind(99)}
	got := v.AsString()
	if got != "<unknown:99>" {
		t.Errorf("AsString(unknown) = %q, want <unknown:99>", got)
	}
}

// TestParamValueEqualExtraEdgeCases extends the existing equal tests.
func TestParamValueEqualExtraEdgeCases(t *testing.T) {
	// None == None.
	if !NoneValue().Equal(NoneValue()) {
		t.Error("NoneValue != NoneValue")
	}
	// Bool false == false.
	if !BoolValue(false).Equal(BoolValue(false)) {
		t.Error("false != false")
	}
	// Float.
	if !FloatValue(1.5).Equal(FloatValue(1.5)) {
		t.Error("1.5 != 1.5")
	}
	// String.
	if !StringValue("x").Equal(StringValue("x")) {
		t.Error("x != x")
	}
	// List same length different element.
	if ListValue([]string{"a", "c"}).Equal(ListValue([]string{"a", "b"})) {
		t.Error("[a,c] == [a,b]")
	}
	// Unknown kind — not equal by default (even compared against an identical copy).
	a := ParamValue{Kind: ValueKind(99)}
	b := a // identical copy
	if a.Equal(b) {
		t.Error("unknown kind equal to itself should be false")
	}
}
