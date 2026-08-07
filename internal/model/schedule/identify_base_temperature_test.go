// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package schedule identify_base_temperature_test.go covers
// IdentifyBaseTemperature.
package schedule

import "testing"

// TestIdentifyBaseTemperatureAllSlotsEqual verifies that when every
// period has the same temperature, that temperature is returned.
// Mirrors Python's behaviour for a uniform-temperature weekday.
func TestIdentifyBaseTemperatureAllSlotsEqual(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "08:00", Temperature: 18},
			{StartTime: "08:00", EndTime: "16:00", Temperature: 18},
			{StartTime: "16:00", EndTime: "24:00", Temperature: 18},
		},
	}
	got := IdentifyBaseTemperature(day)
	if got != 18 {
		t.Errorf("IdentifyBaseTemperature(all 18)=%g, want 18", got)
	}
}

// TestIdentifyBaseTemperatureMixed verifies that the temperature with
// the most total minutes is returned. In this case 18 °C occupies
// 08:00+08:00 = 16 h, while 22 °C only occupies 8 h.
func TestIdentifyBaseTemperatureMixed(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "06:00", Temperature: 18}, // 6 h
			{StartTime: "06:00", EndTime: "22:00", Temperature: 22}, // 16 h — but we set it up so 18 wins
			{StartTime: "22:00", EndTime: "24:00", Temperature: 18}, // 2 h
			// 18 °C total: 6+2 = 8 h, 22 °C total: 16 h → 22 wins
		},
	}
	got := IdentifyBaseTemperature(day)
	// 22 °C occupies 16 h; 18 °C only 8 h → base = 22
	if got != 22 {
		t.Errorf("IdentifyBaseTemperature(22 dominant)=%g, want 22", got)
	}
}

// TestIdentifyBaseTemperatureMixed18Dominates verifies the opposite:
// 18 °C is dominant when it covers more total minutes.
func TestIdentifyBaseTemperatureMixed18Dominates(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "16:00", Temperature: 18}, // 16 h
			{StartTime: "16:00", EndTime: "22:00", Temperature: 22}, // 6 h
			{StartTime: "22:00", EndTime: "24:00", Temperature: 18}, // 2 h
			// 18 °C total: 16+2 = 18 h, 22 °C: 6 h → 18 wins
		},
	}
	got := IdentifyBaseTemperature(day)
	if got != 18 {
		t.Errorf("IdentifyBaseTemperature(18 dominant)=%g, want 18", got)
	}
}

// TestIdentifyBaseTemperatureEmpty verifies that an empty weekday
// (no periods at all) returns the default fill temperature 18.0,
// exactly as Python's `if not weekday_data: return
// DEFAULT_CLIMATE_FILL_TEMPERATURE` (week_profile.py:1737).
func TestIdentifyBaseTemperatureEmpty(t *testing.T) {
	t.Parallel()
	got := IdentifyBaseTemperature(ClimateWeekday{})
	if got != DefaultBaseTemperature {
		t.Errorf("IdentifyBaseTemperature(empty)=%g, want %g", got, DefaultBaseTemperature)
	}
}

// TestIdentifyBaseTemperatureSinglePeriod verifies that a weekday
// with exactly one period returns that period's temperature.
func TestIdentifyBaseTemperatureSinglePeriod(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 21},
		},
	}
	got := IdentifyBaseTemperature(day)
	if got != 21 {
		t.Errorf("IdentifyBaseTemperature(single 21)=%g, want 21", got)
	}
}

// TestIdentifyBaseTemperatureUnsortedInput verifies that the function
// produces the correct result even when the input periods are not in
// chronological order.
func TestIdentifyBaseTemperatureUnsortedInput(t *testing.T) {
	t.Parallel()
	// Deliberately reversed order.
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "16:00", EndTime: "24:00", Temperature: 22}, // 8 h
			{StartTime: "08:00", EndTime: "16:00", Temperature: 18}, // 8 h
			{StartTime: "00:00", EndTime: "08:00", Temperature: 22}, // 8 h
			// 22 °C: 16 h, 18 °C: 8 h → 22 wins
		},
	}
	got := IdentifyBaseTemperature(day)
	if got != 22 {
		t.Errorf("IdentifyBaseTemperature(unsorted)=%g, want 22", got)
	}
}

// TestIdentifyBaseTemperatureDefaultValue verifies the sentinel constant
// Value matches 0.
func TestIdentifyBaseTemperatureDefaultValue(t *testing.T) {
	if DefaultBaseTemperature != 18.0 {
		t.Errorf("DefaultBaseTemperature=%g, want 18.0", DefaultBaseTemperature)
	}
}

// TestIdentifyBaseTemperatureTieBreaksByEarliestPeriod pins the
// deterministic tie-break: on equal total minutes the temperature whose
// first period starts earliest wins, regardless of the input order.
// The previous map-iteration winner selection flipped this result
// between runs.
func TestIdentifyBaseTemperatureTieBreaksByEarliestPeriod(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "12:00", EndTime: "24:00", Temperature: 22}, // 12 h
			{StartTime: "00:00", EndTime: "12:00", Temperature: 18}, // 12 h
		},
	}
	for range 50 {
		if got := IdentifyBaseTemperature(day); got != 18 {
			t.Fatalf("IdentifyBaseTemperature(tie)=%g, want 18 (earliest period)", got)
		}
	}
}

// TestIdentifyBaseTemperatureAllZeroDurationFallsBack verifies the
// fallback when periods exist but none has a positive duration.
func TestIdentifyBaseTemperatureAllZeroDurationFallsBack(t *testing.T) {
	t.Parallel()
	day := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "08:00", EndTime: "08:00", Temperature: 22},
			{StartTime: "12:00", EndTime: "10:00", Temperature: 25},
		},
	}
	if got := IdentifyBaseTemperature(day); got != DefaultBaseTemperature {
		t.Errorf("IdentifyBaseTemperature(zero durations)=%g, want %g", got, DefaultBaseTemperature)
	}
}
