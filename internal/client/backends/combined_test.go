// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsCombinedParameter(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"COMBINED_PARAMETER", true},
		{"LEVEL_COMBINED", true},
		{"LEVEL", false},
		{"STATE", false},
		{"", false},
		{"combined_parameter", false}, // case-sensitive — wire is upper
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCombinedParameter(c.name); got != c.want {
				t.Fatalf("IsCombinedParameter(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestParseCombinedParameter_Combined(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  map[string]any
		ok    bool
	}{
		{
			name:  "single L",
			value: "L=50",
			want:  map[string]any{"LEVEL": 0.5},
			ok:    true,
		},
		{
			name:  "L and L2",
			value: "L=100,L2=50",
			want:  map[string]any{"LEVEL": 1.0, "LEVEL_2": 0.5},
			ok:    true,
		},
		{
			name:  "L and L2 reversed",
			value: "L2=20,L=80",
			want:  map[string]any{"LEVEL": 0.8, "LEVEL_2": 0.2},
			ok:    true,
		},
		{
			name:  "zero",
			value: "L=0",
			want:  map[string]any{"LEVEL": 0.0},
			ok:    true,
		},
		{
			name:  "non-numeric value aborts whole parse",
			value: "L=not_a_number",
			want:  nil,
			ok:    false,
		},
		{
			name:  "missing equals aborts",
			value: "invalid_no_equals",
			want:  nil,
			ok:    false,
		},
		{
			name:  "unknown shorthand silently dropped",
			value: "X=99,L=50",
			want:  map[string]any{"LEVEL": 0.5},
			ok:    true,
		},
		{
			name:  "all shortcuts unknown → empty result",
			value: "X=1,Y=2",
			want:  nil,
			ok:    false,
		},
		{
			name:  "empty value",
			value: "",
			want:  nil,
			ok:    false,
		},
		{
			name:  "trailing comma yields empty pair → abort",
			value: "L=50,",
			want:  nil,
			ok:    false,
		},
		{
			name:  "whitespace trimmed in numeric",
			value: "L= 75 ",
			want:  map[string]any{"LEVEL": 0.75},
			ok:    true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseCombinedParameter("COMBINED_PARAMETER", c.value)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (got %v)", ok, c.ok, got)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestParseCombinedParameter_LevelCombined(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  map[string]any
		ok    bool
	}{
		{
			name:  "two hex values",
			value: "0x64,0x32",
			want:  map[string]any{"LEVEL": 0.5, "LEVEL_SLATS": 0.25},
			ok:    true,
		},
		{
			name:  "max + min",
			value: "0xc8,0x00",
			want:  map[string]any{"LEVEL": 1.0, "LEVEL_SLATS": 0.0},
			ok:    true,
		},
		{
			name:  "uppercase hex",
			value: "0X64,0X32",
			want:  map[string]any{"LEVEL": 0.5, "LEVEL_SLATS": 0.25},
			ok:    true,
		},
		{
			name:  "no comma → empty result (mirrors python)",
			value: "0x64",
			want:  nil,
			ok:    false,
		},
		{
			name:  "non-hex strings preserved as raw (mirrors python branch)",
			value: "100,50",
			want:  map[string]any{"LEVEL": "100", "LEVEL_SLATS": "50"},
			ok:    true,
		},
		{
			name:  "mixed hex/non-hex",
			value: "0x64,raw",
			want:  map[string]any{"LEVEL": 0.5, "LEVEL_SLATS": "raw"},
			ok:    true,
		},
		{
			name:  "malformed hex falls back to raw string",
			value: "0xZZ,0x32",
			want:  map[string]any{"LEVEL": "0xZZ", "LEVEL_SLATS": 0.25},
			ok:    true,
		},
		{
			name:  "empty string",
			value: "",
			want:  nil,
			ok:    false,
		},
		{
			name:  "more than two parts aborts (mirrors python's ValueError)",
			value: "0x64,0x32,0x10",
			want:  nil,
			ok:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseCombinedParameter("LEVEL_COMBINED", c.value)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (got %v)", ok, c.ok, got)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestParseCombinedParameter_Unknown(t *testing.T) {
	got, ok := ParseCombinedParameter("LEVEL", "0.5")
	if ok || got != nil {
		t.Fatalf("ParseCombinedParameter on non-combined name must return (nil, false), got (%v, %v)", got, ok)
	}
}

func TestEncodeHMLevel(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0.0, "0x00"},
		{"half", 0.5, "0x64"},     // 100 dec
		{"quarter", 0.25, "0x32"}, // 50 dec
		{"max", 1.0, "0xc8"},      // 200 dec
		{"clamps below zero", -0.5, "0x00"},
		{"clamps above one", 1.5, "0xc8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EncodeHMLevel(c.in); got != c.want {
				t.Fatalf("EncodeHMLevel(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestEncodeHMLevel_RoundTrip verifies that encoding then decoding a
// 0..1 level yields the same value (within HM's 1-step quantum,
// i.e. value*200 is integer). This is the property python's
// converter pair guarantees.
func TestEncodeHMLevel_RoundTrip(t *testing.T) {
	values := []float64{0.0, 0.005, 0.25, 0.5, 0.75, 0.995, 1.0}
	for _, v := range values {
		hex := EncodeHMLevel(v)
		// LEVEL_COMBINED expects the hex pair shape, prepend a partner
		// so parseLevelCombined accepts it.
		decoded, ok := ParseCombinedParameter("LEVEL_COMBINED", hex+","+hex)
		if !ok {
			t.Fatalf("roundtrip ok=false for v=%v hex=%s", v, hex)
		}
		got, _ := decoded["LEVEL"].(float64)
		// HM level grid is 0.005 (200 quanta over 0..1). Compare with
		// generous tolerance.
		if abs(got-v) > 0.0051 {
			t.Fatalf("roundtrip v=%v hex=%s decoded=%v", v, hex, got)
		}
	}
}

// TestParseCombinedParameter_PythonParity replays the exact inputs
// Covered by py so we have a
// contract anchor against the Python reference. Each row mirrors
// one test_* function name from the python suite.
func TestParseCombinedParameter_PythonParity(t *testing.T) {
	type row struct {
		pyTest string
		name   string
		value  string
		want   map[string]any
		ok     bool
	}
	rows := []row{
		{
			pyTest: "test_convert_combined_parameter_basic",
			name:   "COMBINED_PARAMETER", value: "L=50",
			want: map[string]any{"LEVEL": 0.5}, ok: true,
		},
		{
			pyTest: "test_convert_combined_parameter_multiple_params",
			name:   "COMBINED_PARAMETER", value: "L=100,L2=50",
			want: map[string]any{"LEVEL": 1.0, "LEVEL_2": 0.5}, ok: true,
		},
		{
			pyTest: "test_convert_combined_parameter_with_string_value",
			name:   "COMBINED_PARAMETER", value: "L=not_a_number",
			want: nil, ok: false,
		},
		{
			pyTest: "test_convert_invalid_format_exception",
			name:   "COMBINED_PARAMETER", value: "invalid_no_equals",
			want: nil, ok: false,
		},
		{
			pyTest: "test_convert_level_combined_with_comma",
			name:   "LEVEL_COMBINED", value: "0x64,0x32",
			want: map[string]any{"LEVEL": 0.5, "LEVEL_SLATS": 0.25}, ok: true,
		},
		{
			pyTest: "test_convert_level_combined_without_comma",
			name:   "LEVEL_COMBINED", value: "0x64",
			want: nil, ok: false,
		},
		{
			pyTest: "test_convert_cpv_to_hm_level_non_hex",
			name:   "LEVEL_COMBINED", value: "100,50",
			want: map[string]any{"LEVEL": "100", "LEVEL_SLATS": "50"}, ok: true,
		},
	}
	for _, r := range rows {
		t.Run(r.pyTest, func(t *testing.T) {
			got, ok := ParseCombinedParameter(r.name, r.value)
			if ok != r.ok {
				t.Fatalf("ok = %v, want %v (got %v)", ok, r.ok, got)
			}
			if !reflect.DeepEqual(got, r.want) {
				t.Fatalf("got %#v, want %#v", got, r.want)
			}
		})
	}
}

// Sanity helpers.

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TestConvertCpvLevelHm exercises the hex-parser directly so the
// silent-fallback branch is documented.
func TestConvertCpvLevelHm(t *testing.T) {
	if got := convertCpvLevelHm("0x00"); !approx(got, 0.0) {
		t.Fatalf("0x00 → %v", got)
	}
	if got := convertCpvLevelHm("0xc8"); !approx(got, 1.0) {
		t.Fatalf("0xc8 → %v", got)
	}
	if got := convertCpvLevelHm("not_hex"); got != "not_hex" {
		t.Fatalf("non-hex must pass through, got %#v", got)
	}
	// Hex prefix but unparseable → silent fallback to raw value.
	if got := convertCpvLevelHm("0xZZ"); got != "0xZZ" {
		t.Fatalf("malformed hex must pass through, got %#v", got)
	}
	// Whitespace tolerance.
	if got := convertCpvLevelHm("  0x64  "); !approx(got, 0.5) {
		t.Fatalf("whitespace not tolerated, got %#v", got)
	}
}

func approx(v any, want float64) bool {
	f, ok := v.(float64)
	if !ok {
		return false
	}
	return abs(f-want) < 1e-9
}

// TestConvertCpvLevelHmip exercises the HmIP decimal-parser directly.
// The non-numeric path propagates failure up through parseCombined to (nil, false).
func TestConvertCpvLevelHmip(t *testing.T) {
	if got, ok := convertCpvLevelHmip("0"); !ok || got.(float64) != 0 {
		t.Fatalf("0 → (%v, %v)", got, ok)
	}
	if got, ok := convertCpvLevelHmip("100"); !ok || got.(float64) != 1 {
		t.Fatalf("100 → (%v, %v)", got, ok)
	}
	if _, ok := convertCpvLevelHmip("xyz"); ok {
		t.Fatalf("non-numeric must return ok=false")
	}
}

// TestEncodeHMLevel_HexFormat asserts the wire format is always
// lowercase, prefixed and zero-padded to two digits — matching
// python's `format(int(...), "#04x")`.
func TestEncodeHMLevel_HexFormat(t *testing.T) {
	hex := EncodeHMLevel(0.5)
	if !strings.HasPrefix(hex, "0x") {
		t.Fatalf("missing 0x prefix: %q", hex)
	}
	if hex != strings.ToLower(hex) {
		t.Fatalf("must be lowercase: %q", hex)
	}
	if len(hex) < 4 {
		t.Fatalf("must be zero-padded to at least 4 chars: %q", hex)
	}
}
