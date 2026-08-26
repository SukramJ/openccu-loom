// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package value_test

import (
	"math"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/value"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestToHomematicValue_Bool verifies bool → int conversion.
func TestToHomematicValue_Bool(t *testing.T) {
	tests := []struct {
		in   bool
		want int
	}{
		{true, 1},
		{false, 0},
	}
	for _, tt := range tests {
		got := value.ToHomematicValue(tt.in)
		if got != tt.want {
			t.Errorf("ToHomematicValue(%v) = %v, want %d", tt.in, got, tt.want)
		}
	}
}

// TestToHomematicValue_Float64 verifies float64 is rounded to 6
// decimal places.
func TestToHomematicValue_Float64(t *testing.T) {
	in := 3.14159265359
	got, ok := value.ToHomematicValue(in).(float64)
	if !ok {
		t.Fatalf("expected float64 result")
	}
	// 6 decimal places means the result should be 3.141593.
	want := 3.141593
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("ToHomematicValue(%v) = %v, want %v", in, got, want)
	}
}

// TestToHomematicValue_Duration verifies time.Duration → float64
// total seconds.
func TestToHomematicValue_Duration(t *testing.T) {
	d := 90 * time.Second
	got, ok := value.ToHomematicValue(d).(float64)
	if !ok {
		t.Fatalf("expected float64 result")
	}
	if got != 90.0 {
		t.Errorf("ToHomematicValue(90s) = %v, want 90.0", got)
	}
}

// TestToHomematicValue_PassThrough verifies that unknown types are
// returned unchanged.
func TestToHomematicValue_PassThrough(t *testing.T) {
	in := "hello"
	got := value.ToHomematicValue(in)
	if got != in {
		t.Errorf("ToHomematicValue(%q) = %v, want %q", in, got, in)
	}
}

// TestFromHomematicValue_IntToBool verifies int → bool coercion.
func TestFromHomematicValue_IntToBool(t *testing.T) {
	tests := []struct {
		in   any
		want bool
	}{
		{1, true},
		{0, false},
		{42, true},
	}
	for _, tt := range tests {
		got := value.FromHomematicValue(tt.in, "bool")
		if got != tt.want {
			t.Errorf("FromHomematicValue(%v, bool) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestFromHomematicValue_StringToTime verifies string → time.Time
// coercion via RFC3339 parsing.
func TestFromHomematicValue_StringToTime(t *testing.T) {
	s := "2026-01-15T10:30:00Z"
	got := value.FromHomematicValue(s, "time.Time")
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if ts.Year() != 2026 || ts.Month() != 1 || ts.Day() != 15 {
		t.Errorf("Parsed time = %v, unexpected", ts)
	}
}

// TestFromHomematicValue_PassThrough verifies that nil targetType
// returns value unchanged.
func TestFromHomematicValue_PassThrough(t *testing.T) {
	in := 42
	got := value.FromHomematicValue(in, "")
	if got != in {
		t.Errorf("FromHomematicValue(%v, \"\") = %v, want %v", in, got, in)
	}
}

// TestConvertHMLevelToCPV verifies the level → CPV hex round-trip.
func TestConvertHMLevelToCPV(t *testing.T) {
	tests := []struct {
		level float64
		want  string
	}{
		{0.0, "0x00"},
		{1.0, "0xc8"},
		{0.5, "0x64"},
	}
	for _, tt := range tests {
		got := value.ConvertHMLevelToCPV(tt.level)
		if got != tt.want {
			t.Errorf("ConvertHMLevelToCPV(%v) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// TestConvertCPVToHMLevel verifies the CPV hex → level round-trip.
func TestConvertCPVToHMLevel(t *testing.T) {
	tests := []struct {
		cpv  string
		want float64
		ok   bool
	}{
		{"0x00", 0.0, true},
		{"0xc8", 1.0, true},
		{"0x64", 0.5, true},
		{"not-a-hex", 0, false},
	}
	for _, tt := range tests {
		got, ok := value.ConvertCPVToHMLevel(tt.cpv)
		if tt.ok && !ok {
			t.Errorf("ConvertCPVToHMLevel(%q): expected ok=true, got false", tt.cpv)
			continue
		}
		if tt.ok && math.Abs(got-tt.want) > 1e-6 {
			t.Errorf("ConvertCPVToHMLevel(%q) = %v, want %v", tt.cpv, got, tt.want)
		}
	}
}

// TestConvertableParameters verifies the set covers the known
// convertable parameter names.
func TestConvertableParameters(t *testing.T) {
	want := map[hmenum.Parameter]struct{}{
		hmenum.ParameterCombinedParameter: {},
		hmenum.ParameterLevelCombined:     {},
	}
	for _, p := range value.ConvertableParameters {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected parameter in ConvertableParameters: %s", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("missing parameter from ConvertableParameters: %s", p)
	}
}

// TestIsConvertableParameter verifies the predicate matches the set.
func TestIsConvertableParameter(t *testing.T) {
	if !value.IsConvertableParameter(hmenum.ParameterCombinedParameter) {
		t.Error("COMBINED_PARAMETER should be convertable")
	}
	if !value.IsConvertableParameter(hmenum.ParameterLevelCombined) {
		t.Error("LEVEL_COMBINED should be convertable")
	}
	if value.IsConvertableParameter(hmenum.ParameterState) {
		t.Error("STATE should NOT be convertable")
	}
}
