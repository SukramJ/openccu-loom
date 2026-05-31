// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// TestSetWeekdayWritesProfileSlice verifies that SetWeekday loads the
// existing schedule, replaces a single weekday in a single profile, and
// writes the modified schedule back.
func TestSetWeekdayWritesProfileSlice(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	day := schedule.ClimateWeekday{
		BaseTemperature: 21.5,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 21.5},
		},
	}
	if err := domain.SetWeekday(
		t.Context(), "0001ABCD", 1, "P1", schedule.WeekdayTuesday, day,
	); err != nil {
		t.Fatalf("SetWeekday: %v", err)
	}
	if got := backend.putCallCount(); got != 1 {
		t.Fatalf("backend Put calls: got %d, want 1", got)
	}
	written := backend.lastPut("0001ABCD:1")
	if written == nil {
		t.Fatalf("no Put recorded for 0001ABCD:1; got=%v", backend.putValues)
	}
	// The TUESDAY slot 1 must reflect the new value.
	if got, ok := written["P1_TEMPERATURE_TUESDAY_1"]; !ok || got != 21.5 {
		t.Errorf("P1_TEMPERATURE_TUESDAY_1: got %v, want 21.5", got)
	}
}

// TestSetWeekdayInvalidWeekdayReturnsError verifies that SetWeekday
// rejects malformed weekday data before any backend write.
func TestSetWeekdayInvalidWeekdayReturnsError(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	// Invalid: end-time decreasing across periods.
	bad := schedule.ClimateWeekday{
		BaseTemperature: 18.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "13:20", Temperature: 21.0},
			{StartTime: "20:00", EndTime: "10:00", Temperature: 19.0}, // start>=end and overlaps
		},
	}
	err := domain.SetWeekday(
		t.Context(), "0001ABCD", 1, "P1", schedule.WeekdayWednesday, bad,
	)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if backend.putCallCount() != 0 {
		t.Errorf("backend Put calls: got %d, want 0 (validation should short-circuit)", backend.putCallCount())
	}
}

// TestSetWeekdayInvalidProfileKeyReturnsError verifies that SetWeekday
// rejects a profile-id that exceeds the device's MaxProfiles cap before
// any backend write.
func TestSetWeekdayInvalidProfileKeyReturnsError(t *testing.T) {
	t.Parallel()
	domain, backend := buildScheduleIOFixture(t, fixtureClimateRawP1Monday())

	day := schedule.ClimateWeekday{
		BaseTemperature: 18.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 18.0},
		},
	}
	// HmIP-eTRV supports P1..P3; P99 is out of range.
	err := domain.SetWeekday(
		t.Context(), "0001ABCD", 1, "P99", schedule.WeekdayMonday, day,
	)
	if err == nil {
		t.Fatal("expected validation error for out-of-range profile key, got nil")
	}
	if backend.putCallCount() != 0 {
		t.Errorf("backend Put calls: got %d, want 0 (validation should short-circuit)", backend.putCallCount())
	}
}
