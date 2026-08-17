// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"
)

// ---------------------------------------------------------------------------
// coerce.go — toBool, toInt64, toFloat64, toString, coerceWire
// ---------------------------------------------------------------------------

func TestToBoolAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want bool
		ok   bool
	}{
		{true, true, true},
		{false, false, true},
		{"true", true, true},
		{"1", true, true},
		{"on", true, true},
		{"yes", true, true},
		{"false", false, true},
		{"0", false, true},
		{"off", false, true},
		{"no", false, true},
		{"", false, true},
		{int(1), true, true},
		{int(0), false, true},
		{int32(1), true, true},
		{int64(0), false, true},
		{float32(1), true, true},
		{float64(0), false, true},
		{"maybe", false, false},
		{[]int{}, false, false},
	}
	for _, c := range cases {
		got, ok := toBool(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("toBool(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestToInt64AllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int(5), 5, true},
		{int32(3), 3, true},
		{int64(9), 9, true},
		{float32(2), 2, true},
		{float64(7), 7, true},
		{true, 1, true},
		{false, 0, true},
		{"42", 42, true},
		{"3.5", 3, true},
		{"bad", 0, false},
		{[]int{}, 0, false},
	}
	for _, c := range cases {
		got, ok := toInt64(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("toInt64(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestToFloat64AllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(1.5), 1.5, true},
		{float32(2), 2, true},
		{int(3), 3, true},
		{int32(4), 4, true},
		{int64(5), 5, true},
		{true, 1, true},
		{false, 0, true},
		{"3.14", 3.14, true},
		{"bad", 0, false},
		{[]int{}, 0, false},
		// strconv.ParseFloat accepts "nan"/"inf" as valid float text, but
		// a non-finite value has no JSON representation and would corrupt
		// every north-bound response encoding the resulting data point
		// alongside healthy ones — coerceWire must reject it the same way
		// an unparsable string is rejected.
		{"nan", 0, false},
		{"NaN", 0, false},
		{"inf", 0, false},
		{"+Inf", 0, false},
		{"-Inf", 0, false},
	}
	for _, c := range cases {
		got, ok := toFloat64(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("toFloat64(%v) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestToStringAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{int(42), "42"},
		{int32(7), "7"},
		{int64(99), "99"},
		{float32(1.5), "1.5"},
		{float64(3.14), "3.14"},
		{[]int{}, ""}, // unknown type
	}
	for _, c := range cases {
		got := toString(c.in)
		if got != c.want {
			t.Errorf("toString(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestCoerceWireIntAndInt64(t *testing.T) {
	t.Parallel()
	// int target
	v, ok := coerceWire[int](int32(5))
	if !ok || v != 5 {
		t.Fatalf("coerceWire[int](int32(5)) = %v %v", v, ok)
	}
	// int64 target
	v64, ok := coerceWire[int64](float64(3))
	if !ok || v64 != 3 {
		t.Fatalf("coerceWire[int64](float64(3)) = %v %v", v64, ok)
	}
	// string target
	vs, ok := coerceWire[string](42)
	if !ok || vs == "" {
		t.Fatalf("coerceWire[string](42) = %q %v", vs, ok)
	}
	// bool target
	vb, ok := coerceWire[bool]("true")
	if !ok || !vb {
		t.Fatalf("coerceWire[bool](\"true\") = %v %v", vb, ok)
	}
	// unknown target → (zero, false)
	_, ok2 := coerceWire[struct{}](42)
	if ok2 {
		t.Fatal("coerceWire[struct{}] should return ok=false")
	}
}
