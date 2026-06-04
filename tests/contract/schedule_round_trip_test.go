// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSimpleScheduleRoundTripContract pins the read-write invariant
// that the SPA's schedule editor depends on:
//
//	for any entry e that the read path emits for a SimpleSchedule on
//	a given category C, the same e MUST pass ValidateFor(C).
//
// The bug class this guards: a CCU MASTER paramset that advertises
// optional fields (RAMP_TIME_BASE/FACTOR, LEVEL_2, DURATION_*) on a
// channel whose category validator rejects those fields — e.g.
// HmIP-PSMCO surfaces RAMP_TIME on a SWITCH channel. Without the
// read-path stripping the SPA loads a schedule, the user edits a
// slot, hits Save, and the put_schedule then fails validation on a
// field neither the user nor the SPA touched.
//
// The validators in internal/model/schedule/simple.go::ValidateFor
// reject:
//
//	SWITCH → level_2, ramp_time
//	LIGHT  → level_2
//	COVER  → ramp_time, duration
//	VALVE  → level_2, ramp_time
//	LOCK   → level_2, ramp_time, duration
//
// The simpleScheduleUnsupportedFields table in
// internal/central/adapter/schedules.go mirrors this catalogue and
// the strip pass in parseSimpleScheduleWithDomain enforces it.
// This contract test pins that invariant.
func TestSimpleScheduleRoundTripContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		category  hmenum.DataPointCategory
		entry     schedule.SimpleEntry
		wantError bool
	}{
		// SWITCH must reject level_2 + ramp_time
		{
			name: "switch with level_2 rejects", category: hmenum.DataPointCategorySwitch,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 1.0, Level2: new(0.5)},
			wantError: true,
		},
		{
			name: "switch with ramp_time rejects", category: hmenum.DataPointCategorySwitch,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 1.0, RampTime: "2s"},
			wantError: true,
		},
		{
			name: "switch bare passes", category: hmenum.DataPointCategorySwitch,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 1.0},
			wantError: false,
		},
		// COVER must reject ramp_time + duration
		{
			name: "cover with ramp_time rejects", category: hmenum.DataPointCategoryCover,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 0.5, RampTime: "2s"},
			wantError: true,
		},
		{
			name: "cover with duration rejects", category: hmenum.DataPointCategoryCover,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 0.5, Duration: "10s"},
			wantError: true,
		},
		// VALVE must reject level_2 + ramp_time
		{
			name: "valve with level_2 rejects", category: hmenum.DataPointCategoryValve,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 1.0, Level2: new(0.5)},
			wantError: true,
		},
		{
			name: "valve with ramp_time rejects", category: hmenum.DataPointCategoryValve,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 1.0, RampTime: "2s"},
			wantError: true,
		},
		// LIGHT must reject level_2
		{
			name: "light with level_2 rejects", category: hmenum.DataPointCategoryLight,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 0.8, Level2: new(0.5)},
			wantError: true,
		},
		{
			name: "light with ramp_time passes", category: hmenum.DataPointCategoryLight,
			entry:     schedule.SimpleEntry{Weekdays: allDays, Time: "08:00", Level: 0.8, RampTime: "2s"},
			wantError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.entry.ValidateFor(tc.category)
			if tc.wantError && err == nil {
				t.Fatalf("expected ValidateFor(%s) to reject %#v, got nil", tc.category, tc.entry)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected ValidateFor(%s) to accept %#v, got %v", tc.category, tc.entry, err)
			}
		})
	}
}

var allDays = []schedule.Weekday{
	schedule.WeekdayMonday,
	schedule.WeekdayTuesday,
	schedule.WeekdayWednesday,
	schedule.WeekdayThursday,
	schedule.WeekdayFriday,
	schedule.WeekdaySaturday,
	schedule.WeekdaySunday,
}
