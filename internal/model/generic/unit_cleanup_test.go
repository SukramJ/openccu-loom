// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// TestCleanupUnitSubstringReplace pins the substring-replace
// fallback path (fixUnitReplace) for parameters not in the
// per-param override map.
func TestCleanupUnitSubstringReplace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rawUnit string
		want    string
	}{
		{`"`, ""},
		{`some "thing" else`, ""}, // contains `"` → replace
		{"100%", "%"},
		{"% rF", "%"},
		{"Lux", "lx"},
		{"m3", "m³"},
		{"kWh", "kWh"}, // no replacement → unchanged
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
