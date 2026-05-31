// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproperty"
)

// --------------------------------------------------------------------------
// ConstResolver
// --------------------------------------------------------------------------

func TestConstResolver_ReturnsValue(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"available": true}
	v, ok := r.Resolve("available")
	if !ok {
		t.Fatal("Resolve(\"available\") ok=false, want true")
	}
	if v != true {
		t.Errorf("Resolve(\"available\") = %v, want true", v)
	}
}

func TestConstResolver_MissingKey(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{}
	_, ok := r.Resolve("missing")
	if ok {
		t.Error("Resolve(\"missing\") ok=true, want false")
	}
}

// --------------------------------------------------------------------------
// DelegatedProperty[T].Value
// --------------------------------------------------------------------------

func TestDelegatedProperty_Value_Bool(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"available": true}
	dp := hmproperty.Delegated[bool](r, "available")
	if !dp.Value() {
		t.Error("Value() = false, want true")
	}
}

func TestDelegatedProperty_Value_ZeroOnMissing(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{}
	dp := hmproperty.Delegated[int](r, "count")
	if dp.Value() != 0 {
		t.Errorf("Value() on missing = %d, want 0", dp.Value())
	}
}

func TestDelegatedProperty_Value_ZeroOnTypeMismatch(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"n": "not-an-int"}
	dp := hmproperty.Delegated[int](r, "n")
	if dp.Value() != 0 {
		t.Errorf("Value() on type mismatch = %d, want 0", dp.Value())
	}
}

func TestDelegatedProperty_Value_NilResolver(t *testing.T) {
	t.Parallel()
	dp := hmproperty.Delegated[string](nil, "x")
	if dp.Value() != "" {
		t.Errorf("Value() on nil resolver = %q, want \"\"", dp.Value())
	}
}

func TestDelegatedProperty_Value_Float64(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"level": float64(0.75)}
	dp := hmproperty.Delegated[float64](r, "level")
	if dp.Value() != 0.75 {
		t.Errorf("Value() = %v, want 0.75", dp.Value())
	}
}

// --------------------------------------------------------------------------
// DelegatedProperty.IsSet
// --------------------------------------------------------------------------

func TestDelegatedProperty_IsSet_True(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"x": 1}
	dp := hmproperty.Delegated[int](r, "x")
	if !dp.IsSet() {
		t.Error("IsSet() = false, want true")
	}
}

func TestDelegatedProperty_IsSet_False(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{}
	dp := hmproperty.Delegated[int](r, "x")
	if dp.IsSet() {
		t.Error("IsSet() = true, want false")
	}
}

func TestDelegatedProperty_IsSet_NilResolver(t *testing.T) {
	t.Parallel()
	dp := hmproperty.Delegated[int](nil, "x")
	if dp.IsSet() {
		t.Error("IsSet() on nil resolver = true, want false")
	}
}

// --------------------------------------------------------------------------
// DelegatedProperty.String
// --------------------------------------------------------------------------

func TestDelegatedProperty_String_ContainsPath(t *testing.T) {
	t.Parallel()
	r := hmproperty.ConstResolver{"foo": "bar"}
	dp := hmproperty.Delegated[string](r, "foo")
	s := dp.String()
	if s == "" {
		t.Error("String() returned empty")
	}
}
