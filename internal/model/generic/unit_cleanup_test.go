// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestCleanupUnitParameterOverrides pins the per-parameter overrides
// (fixUnitByParam) — these always win over the raw CCU unit.
func TestCleanupUnitParameterOverrides(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param   hmenum.Parameter
		rawUnit string
		want    string
	}{
		{hmenum.ParameterActualTemperature, "degree", "°C"},
		{hmenum.ParameterHumidity, "% rF", "%"},
		{hmenum.ParameterLevel, "100%", "%"},
		{hmenum.ParameterOperatingVoltage, "V", "V"},
		{hmenum.ParameterRSSIDevice, "", "dBm"},
		{hmenum.ParameterRSSIPeer, "anything", "dBm"},
	}
	for _, tc := range cases {
		got := generic.CleanupUnit(tc.param, tc.rawUnit)
		if got != tc.want {
			t.Errorf("CleanupUnit(%q, %q) = %q, want %q", tc.param, tc.rawUnit, got, tc.want)
		}
	}
}

// TestCleanupUnitWholeStringReplace pins the whole-string replace
// fallback path (fixUnitReplace) for parameters not in the
// per-param override map, plus the quote strip that runs before it.
func TestCleanupUnitWholeStringReplace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rawUnit string
		want    string
	}{
		{`"`, ""},
		{`""`, ""},                               // the HmIP legacy config declares this literally
		{`some "thing" else`, "some thing else"}, // quotes stripped, unit kept
		{"100%", "%"},
		{"% rF", "%"},
		{"Lux", "lx"},
		{"m3", "m³"},
		{"m3/Imp.", "m3/Imp."}, // compound unit keeps its suffix
		{"kWh", "kWh"},         // no replacement → unchanged
	}
	for _, tc := range cases {
		// Use an arbitrary parameter that has no override entry.
		got := generic.CleanupUnit("KWH_COUNTER", tc.rawUnit)
		if got != tc.want {
			t.Errorf("CleanupUnit(%q) = %q, want %q", tc.rawUnit, got, tc.want)
		}
	}
}

// TestCleanupUnitEmptyReturnsEmpty pins the empty-input path: an
// unknown parameter with empty raw unit yields an empty string,
func TestCleanupUnitEmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := generic.CleanupUnit("KWH_COUNTER", ""); got != "" {
		t.Errorf("CleanupUnit on unknown param with empty raw = %q, want empty", got)
	}
}

// TestMultiplierForUnit pins the `_MULTIPLIER_UNIT` lookup.
func TestMultiplierForUnit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want float64
	}{
		{"100%", 100.0},
		{"%", generic.DefaultMultiplier},
		{"V", generic.DefaultMultiplier},
		{"", generic.DefaultMultiplier},
	}
	for _, tc := range cases {
		got := generic.MultiplierForUnit(tc.raw)
		if got != tc.want {
			t.Errorf("MultiplierForUnit(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestMultiplierForParam_TimeOfOperation verifies the HmIP-SWSD
// TIME_OF_OPERATION per-parameter override: CCU reports seconds;
// HA entity expects days; multiplier = 1/86400.
func TestMultiplierForParam_TimeOfOperation(t *testing.T) {
	t.Parallel()
	const want = 1.0 / 86400.0
	got := generic.MultiplierForParam(hmenum.ParameterTimeOfOperation)
	if got != want {
		t.Errorf("MultiplierForParam(TIME_OF_OPERATION) = %v, want %v", got, want)
	}
}

// TestMultiplierForParam_GenericReturnsDefault verifies that params
// without a specific override return DefaultMultiplier.
func TestMultiplierForParam_GenericReturnsDefault(t *testing.T) {
	t.Parallel()
	got := generic.MultiplierForParam(hmenum.ParameterActualTemperature)
	if got != generic.DefaultMultiplier {
		t.Errorf("MultiplierForParam(ACTUAL_TEMPERATURE) = %v, want %v", got, generic.DefaultMultiplier)
	}
}

// TestDataPointMultiplier_TimeOfOperation verifies that a DataPoint
// for TIME_OF_OPERATION reports the seconds→days multiplier
// (1/86400) regardless of the CCU-reported unit ("s" or "").
// A raw value of 86400 seconds after multiplication should equal 1.0 day.
func TestDataPointMultiplier_TimeOfOperation(t *testing.T) {
	t.Parallel()
	const wantMultiplier = 1.0 / 86400.0
	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			Parameter: string(hmenum.ParameterTimeOfOperation),
		},
		Descriptor: hmproto.ParameterData{Unit: "s"},
	})
	got := dp.Multiplier()
	if got != wantMultiplier {
		t.Errorf("DataPoint[TIME_OF_OPERATION].Multiplier() = %v, want %v", got, wantMultiplier)
	}
	// Semantic check: 86400 raw seconds × multiplier = 1.0 day.
	rawSeconds := 86400.0
	days := rawSeconds * got
	if days != 1.0 {
		t.Errorf("86400 * multiplier = %v, want 1.0 (1 day)", days)
	}
}

// TestDisplayValue_TrivialMultiplierAbsent pins the "nothing to
// project" gate: both the identity multiplier and a zero multiplier
// (an un-set field, not a real 0×) leave the result absent so a
// caller does not re-publish the wire value under a second name.
func TestDisplayValue_TrivialMultiplierAbsent(t *testing.T) {
	t.Parallel()
	for _, mult := range []float64{generic.DefaultMultiplier, 0} {
		if _, ok := generic.DisplayValue(0.42, mult); ok {
			t.Errorf("DisplayValue(0.42, %v) reported ok=true, want false (trivial multiplier)", mult)
		}
	}
}

// TestDisplayValue_NonNumericAbsent pins the type gate: a raw value
// that is not one of the numeric kinds a wire value ever takes
// (string ENUM tokens, bool, nil) leaves the projection absent rather
// than panicking or silently coercing.
func TestDisplayValue_NonNumericAbsent(t *testing.T) {
	t.Parallel()
	for _, raw := range []any{"BLACK", true, nil, []byte("x")} {
		if _, ok := generic.DisplayValue(raw, 100.0); ok {
			t.Errorf("DisplayValue(%#v, 100) reported ok=true, want false (non-numeric)", raw)
		}
	}
}

// TestDisplayValue_ProjectsAndReportsFloat pins the projection itself
// and its output type: every accepted numeric kind — including the
// integer kinds TIME_OF_OPERATION arrives as — projects to a float64,
// because the display value describes a quantity (e.g. 1.5 days), not
// a wire encoding.
func TestDisplayValue_ProjectsAndReportsFloat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  any
		mult float64
		want float64
	}{
		{"float64 LEVEL", float64(0.42), 100.0, 42.0},
		{"float32", float32(0.5), 100.0, 50.0},
		{"int", int(2), 100.0, 200.0},
		{"int32", int32(3), 100.0, 300.0},
		{"int64 TIME_OF_OPERATION seconds", int64(86400), 1.0 / 86400.0, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := generic.DisplayValue(tc.raw, tc.mult)
			if !ok {
				t.Fatalf("DisplayValue(%#v, %v) reported ok=false, want true", tc.raw, tc.mult)
			}
			f, isFloat := got.(float64)
			if !isFloat {
				t.Fatalf("DisplayValue(%#v, %v) = %T, want float64", tc.raw, tc.mult, got)
			}
			if f != tc.want {
				t.Errorf("DisplayValue(%#v, %v) = %v, want %v", tc.raw, tc.mult, f, tc.want)
			}
		})
	}
}
