// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty_test

// Extra tests to push hmproperty coverage by exercising NormalizeValue,
// Descriptor.Key, descriptorsFor, and GetPropertyByKind paths not covered
// by the existing tests.

import (
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmproperty"
)

// stringer is a test type that implements fmt.Stringer.
type stringer struct{ s string }

func (s stringer) String() string { return s.s }

// ── NormalizeValue ────────────────────────────────────────────────────────────

// TestNormalizeValue_Nil verifies that nil returns nil.
func TestNormalizeValue_Nil(t *testing.T) {
	t.Parallel()
	if got := hmproperty.NormalizeValue(nil); got != nil {
		t.Fatalf("NormalizeValue(nil) = %v, want nil", got)
	}
}

// TestNormalizeValue_Time verifies that time.Time is converted to float64
// Unix timestamp.
func TestNormalizeValue_Time(t *testing.T) {
	t.Parallel()
	ts := time.Unix(1_700_000_000, 0)
	got := hmproperty.NormalizeValue(ts)
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("NormalizeValue(time.Time): type=%T, want float64", got)
	}
	if f != float64(ts.Unix()) {
		t.Fatalf("NormalizeValue(time.Time) = %v, want %v", f, float64(ts.Unix()))
	}
}

// TestNormalizeValue_Stringer verifies that fmt.Stringer values are converted
// to their .String() representation.
func TestNormalizeValue_Stringer(t *testing.T) {
	t.Parallel()
	s := stringer{"hello"}
	got := hmproperty.NormalizeValue(s)
	if got != "hello" {
		t.Fatalf("NormalizeValue(stringer) = %v, want hello", got)
	}
}

// TestNormalizeValue_Slice verifies that a []int is recursively normalised to
// []any.
func TestNormalizeValue_Slice(t *testing.T) {
	t.Parallel()
	in := []int{1, 2, 3}
	got := hmproperty.NormalizeValue(in)
	out, ok := got.([]any)
	if !ok {
		t.Fatalf("NormalizeValue([]int): type=%T, want []any", got)
	}
	if len(out) != 3 {
		t.Fatalf("len=%d, want 3", len(out))
	}
	if out[0] != 1 {
		t.Fatalf("out[0]=%v, want 1", out[0])
	}
}

// TestNormalizeValue_Array verifies that a [2]string array is normalised to
// []any.
func TestNormalizeValue_Array(t *testing.T) {
	t.Parallel()
	in := [2]string{"a", "b"}
	got := hmproperty.NormalizeValue(in)
	out, ok := got.([]any)
	if !ok {
		t.Fatalf("NormalizeValue([2]string): type=%T, want []any", got)
	}
	if len(out) != 2 {
		t.Fatalf("len=%d, want 2", len(out))
	}
	if out[1] != "b" {
		t.Fatalf("out[1]=%v, want b", out[1])
	}
}

// TestNormalizeValue_Map verifies that a map[string]int is normalised to
// map[string]any with stringified keys.
func TestNormalizeValue_Map(t *testing.T) {
	t.Parallel()
	in := map[string]int{"x": 42}
	got := hmproperty.NormalizeValue(in)
	out, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("NormalizeValue(map): type=%T, want map[string]any", got)
	}
	if out["x"] != 42 {
		t.Fatalf("out[x]=%v, want 42", out["x"])
	}
}

// TestNormalizeValue_Scalar verifies that a plain int is returned unchanged.
func TestNormalizeValue_Scalar(t *testing.T) {
	t.Parallel()
	got := hmproperty.NormalizeValue(99)
	if got != 99 {
		t.Fatalf("NormalizeValue(99) = %v, want 99", got)
	}
}

// ── Descriptor.Key ────────────────────────────────────────────────────────────

// TestDescriptor_Key_WithAltName verifies that Key() returns AltName when set.
func TestDescriptor_Key_WithAltName(t *testing.T) {
	t.Parallel()
	d := hmproperty.Descriptor{FieldName: "Foo", AltName: "bar"}
	if got := d.Key(); got != "bar" {
		t.Fatalf("Key() = %q, want bar", got)
	}
}

// TestDescriptor_Key_WithoutAltName verifies that Key() falls back to FieldName.
func TestDescriptor_Key_WithoutAltName(t *testing.T) {
	t.Parallel()
	d := hmproperty.Descriptor{FieldName: "Foo"}
	if got := d.Key(); got != "Foo" {
		t.Fatalf("Key() = %q, want Foo", got)
	}
}

// ── descriptorsFor / GetPropertyByKind paths ─────────────────────────────────

// structWithPayloadTags is a test struct with various `payload` tags.
type structWithPayloadTags struct {
	Name     string  `payload:"state,alt=device_name,log_context"`
	Temp     float64 `payload:"state"`
	Hidden   string  // no payload tag — skipped
	skipped  string  `payload:"state"` //nolint:unused // intentionally unexported to test IsExported filtering
	AltField int     `payload:"config,alt=alt_field"`
	Unknown  bool    `payload:"unknown_kind"` // forward-compat unknown token
}

// TestGetPropertyByKind_Basic verifies that state fields are collected and
// alt= names are applied.
func TestGetPropertyByKind_Basic(t *testing.T) {
	t.Parallel()
	obj := structWithPayloadTags{Name: "MyDevice", Temp: 22.5}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, false)
	if len(got) < 2 {
		t.Fatalf("expected ≥2 state fields, got %v", got)
	}
	if got["device_name"] != "MyDevice" {
		t.Errorf("device_name=%v, want MyDevice", got["device_name"])
	}
	if got["Temp"] != 22.5 {
		t.Errorf("Temp=%v, want 22.5", got["Temp"])
	}
}

// TestGetPropertyByKind_LogContextOnlyAlt verifies that only log_context fields
// are returned when the flag is set (using a struct with alt= name).
func TestGetPropertyByKind_LogContextOnlyAlt(t *testing.T) {
	t.Parallel()
	obj := structWithPayloadTags{Name: "X", Temp: 1.0}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, true)
	if _, ok := got["device_name"]; !ok {
		t.Error("log_context field device_name should be in result")
	}
	if _, ok := got["Temp"]; ok {
		t.Error("non-log_context field Temp should NOT be in result")
	}
}

// TestGetPropertyByKind_Nil verifies that nil input returns nil.
func TestGetPropertyByKind_Nil(t *testing.T) {
	t.Parallel()
	got := hmproperty.GetPropertyByKind(nil, hmproperty.KindState, false)
	if got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}

// TestGetPropertyByKind_NonStruct verifies that a non-struct returns nil.
func TestGetPropertyByKind_NonStruct(t *testing.T) {
	t.Parallel()
	v := 42
	got := hmproperty.GetPropertyByKind(v, hmproperty.KindState, false)
	if got != nil {
		t.Fatalf("expected nil for non-struct input, got %v", got)
	}
}

// TestGetPropertyByKind_PointerToStruct verifies that *struct is dereferenced.
func TestGetPropertyByKind_PointerToStruct(t *testing.T) {
	t.Parallel()
	obj := &structWithPayloadTags{Name: "ptr"}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, false)
	if got["device_name"] != "ptr" {
		t.Errorf("device_name=%v, want ptr", got["device_name"])
	}
}

// TestGetPropertyByKind_ConfigKind verifies that config fields are separately
// collected and that alt= names are applied.
func TestGetPropertyByKind_ConfigKind(t *testing.T) {
	t.Parallel()
	obj := structWithPayloadTags{AltField: 77}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindConfig, false)
	if got["alt_field"] != 77 {
		t.Errorf("alt_field=%v, want 77", got["alt_field"])
	}
}

// TestDescriptorsForCaching verifies that calling GetPropertyByKind twice
// on the same type returns the cached result (no panic, same content).
func TestDescriptorsForCaching(t *testing.T) {
	t.Parallel()
	obj := structWithPayloadTags{Name: "A"}
	got1 := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, false)
	got2 := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, false)
	if fmt.Sprintf("%v", got1) != fmt.Sprintf("%v", got2) {
		t.Error("second call returned different result than first (cache broken)")
	}
}
