// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---- ToHomematicValue ----

func TestToHomematicValue_Bool(t *testing.T) {
	if got := parameter.ToHomematicValue(true); got != 1 {
		t.Errorf("ToHomematicValue(true) = %v, want 1", got)
	}
	if got := parameter.ToHomematicValue(false); got != 0 {
		t.Errorf("ToHomematicValue(false) = %v, want 0", got)
	}
}

func TestToHomematicValue_Float64Rounding(t *testing.T) {
	// Should round to 6 decimal places.
	got := parameter.ToHomematicValue(3.14159265359)
	want := 3.141593
	if fmt.Sprintf("%.6f", got) != fmt.Sprintf("%.6f", want) {
		t.Errorf("ToHomematicValue(3.14159265359) = %v, want %v", got, want)
	}
}

func TestToHomematicValue_Time(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	got := parameter.ToHomematicValue(ts)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("ToHomematicValue(time.Time) = %T, want string", got)
	}
	if s == "" {
		t.Error("ToHomematicValue(time.Time) returned empty string")
	}
}

func TestToHomematicValue_Duration(t *testing.T) {
	d := 90 * time.Second
	got := parameter.ToHomematicValue(d)
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("ToHomematicValue(duration) = %T, want float64", got)
	}
	if f != 90.0 {
		t.Errorf("ToHomematicValue(90s) = %v, want 90", f)
	}
}

func TestToHomematicValue_Stringer(t *testing.T) {
	got := parameter.ToHomematicValue(hmenum.InterfaceHmIPRF)
	if got != "HmIP-RF" {
		t.Errorf("ToHomematicValue(Interface) = %v, want HmIP-RF", got)
	}
}

func TestToHomematicValue_Nil(t *testing.T) {
	if got := parameter.ToHomematicValue(nil); got != nil {
		t.Errorf("ToHomematicValue(nil) = %v, want nil", got)
	}
}

func TestToHomematicValue_Slice(t *testing.T) {
	in := []any{true, false, 1.5}
	got, ok := parameter.ToHomematicValue(in).([]any)
	if !ok {
		t.Fatalf("ToHomematicValue([]any) = %T, want []any", parameter.ToHomematicValue(in))
	}
	if got[0] != 1 || got[1] != 0 {
		t.Errorf("slice bool conversion failed: %v", got)
	}
}

// ---- FromHomematicValue ----

func TestFromHomematicValue_IntToBool(t *testing.T) {
	got, err := parameter.FromHomematicValue(1, "bool")
	if err != nil || got != true {
		t.Errorf("FromHomematicValue(1, bool) = %v %v, want true nil", got, err)
	}
	got, err = parameter.FromHomematicValue(0, "bool")
	if err != nil || got != false {
		t.Errorf("FromHomematicValue(0, bool) = %v %v, want false nil", got, err)
	}
}

func TestFromHomematicValue_StringToTime(t *testing.T) {
	got, err := parameter.FromHomematicValue("2025-01-15T10:30:00Z", "time.Time")
	if err != nil {
		t.Fatalf("FromHomematicValue(rfc3339) error: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("FromHomematicValue(rfc3339) = %T, want time.Time", got)
	}
	if tm.Year() != 2025 {
		t.Errorf("parsed year = %d, want 2025", tm.Year())
	}
}

func TestFromHomematicValue_NoTargetType(t *testing.T) {
	got, err := parameter.FromHomematicValue(42, "")
	if err != nil || got != 42 {
		t.Errorf("FromHomematicValue(42, '') = %v %v, want 42 nil", got, err)
	}
}

// ---- ConvertHMLevelToCPV ----

func TestConvertHMLevelToCPV(t *testing.T) {
	cases := []struct {
		level float64
		want  string
	}{
		{0.0, "0x00"},
		{0.5, "0x64"}, // int(0.5*100*2) = 100 → 0x64
		{1.0, "0xc8"}, // int(1.0*100*2) = 200 → 0xc8
	}
	for _, tc := range cases {
		got := parameter.ConvertHMLevelToCPV(tc.level)
		if got != tc.want {
			t.Errorf("ConvertHMLevelToCPV(%v) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

// ---- ConvertableParameters ----

func TestConvertableParameters_Contains(t *testing.T) {
	if !parameter.IsConvertable(hmenum.ParameterCombinedParameter) {
		t.Error("IsConvertable(COMBINED_PARAMETER) should be true")
	}
	if !parameter.IsConvertable(hmenum.ParameterLevelCombined) {
		t.Error("IsConvertable(LEVEL_COMBINED) should be true")
	}
	if parameter.IsConvertable(hmenum.ParameterLevel) {
		t.Error("IsConvertable(LEVEL) should be false")
	}
}
