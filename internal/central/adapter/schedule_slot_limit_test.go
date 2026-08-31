// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"
	"math"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// slotLimitPeriod is the comparable shape of one non-base window, used to
// hold the two read paths against each other.
type slotLimitPeriod struct {
	start string
	end   string
	temp  float64
}

// slotLimitPeriodsEqual compares two period lists elementwise, with a
// tolerance on the temperature because both paths carry it as a float.
func slotLimitPeriodsEqual(a, b []slotLimitPeriod) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].start != b[i].start || a[i].end != b[i].end ||
			math.Abs(a[i].temp-b[i].temp) > 1e-9 {
			return false
		}
	}
	return true
}

// slotLimitParamset renders a MONDAY day of profile P1 in the flat CCU
// MASTER form, one (ENDTIME, TEMPERATURE) pair per named ordinal. The
// ordinals are given explicitly so a cell outside 1..MaxClimateSlots can be
// placed in the paramset the way a firmware would carry it.
func slotLimitParamset(cells map[int]struct {
	endMin int
	temp   float64
},
) map[string]any {
	raw := make(map[string]any, len(cells)*2)
	for no, c := range cells {
		raw[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", no)] = c.endMin
		raw[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", no)] = c.temp
	}
	return raw
}

// TestClimateSlotOrdinalBoundAgreesAcrossReadPaths pins the ordinal bound of
// a climate cell as one fact across the two independent readers of the same
// MASTER paramset.
//
// The REST / WebSocket read (parseClimateSchedule) and the week-profile read
// (ParseClimateRawParamset + RawToClimate, which feeds the MQTT climate
// payload) are separate implementations. The week-profile side has always
// discarded an ordinal past the CCU slot count; the adapter side matched a
// bare "[0-9]+" and kept it. The same channel then described two different
// schedules depending on which surface an operator was looking at, and the
// extra window could not be written back — there is no cell to write it to.
//
// The comparison is between the two paths, not between a path and a literal,
// so neither side can satisfy it by restating its own bound.
func TestClimateSlotOrdinalBoundAgreesAcrossReadPaths(t *testing.T) {
	t.Parallel()

	type cell = struct {
		endMin int
		temp   float64
	}
	const (
		base       = 17.0
		inRange    = 22.0
		pastTop    = 25.0
		beforeOne  = 5.0
		slotMins   = 100
		windowSlot = 5
	)

	cells := make(map[int]cell, schedule.MaxClimateSlots+2)
	for no := 1; no <= schedule.MaxClimateSlots; no++ {
		temp := base
		if no == windowSlot {
			temp = inRange
		}
		cells[no] = cell{endMin: no * slotMins, temp: temp}
	}
	// Two cells the CCU has nowhere to store: one below the first ordinal,
	// one past the last. Both carry a temperature that shows up as its own
	// window if a reader admits the cell.
	cells[0] = cell{endMin: 30, temp: beforeOne}
	cells[schedule.MaxClimateSlots+1] = cell{
		endMin: schedule.ClimateEndOfDayMinutes,
		temp:   pastTop,
	}
	raw := slotLimitParamset(cells)

	// Path A: the REST / WebSocket read.
	dto, err := parseClimateSchedule(t.Context(), raw)
	if err != nil {
		t.Fatalf("parseClimateSchedule: %v", err)
	}
	restDay := dto.Profiles["P1"].Weekdays["MONDAY"]
	restPeriods := make([]slotLimitPeriod, 0, len(restDay.Periods))
	for _, p := range restDay.Periods {
		restPeriods = append(restPeriods, slotLimitPeriod{p.StartTime, p.EndTime, p.Temperature})
	}

	// Path B: the week-profile read.
	rawSched, err := weekprofile.ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("ParseClimateRawParamset: %v", err)
	}
	climate, err := weekprofile.RawToClimate(rawSched)
	if err != nil {
		t.Fatalf("RawToClimate: %v", err)
	}
	wpDay := climate.Profiles["P1"].Days[schedule.Weekday("MONDAY")]
	wpPeriods := make([]slotLimitPeriod, 0, len(wpDay.Periods))
	for _, p := range wpDay.Periods {
		wpPeriods = append(wpPeriods, slotLimitPeriod{p.StartTime, p.EndTime, p.Temperature})
	}

	if !slotLimitPeriodsEqual(restPeriods, wpPeriods) {
		t.Errorf("the two read paths report different schedules for one paramset:\n  REST         %+v\n  week-profile %+v",
			restPeriods, wpPeriods)
	}
	if math.Abs(restDay.BaseTemperature-wpDay.BaseTemperature) > 1e-9 {
		t.Errorf("base temperature diverges: REST %g, week-profile %g",
			restDay.BaseTemperature, wpDay.BaseTemperature)
	}

	// The in-range window must survive on both sides — otherwise the two
	// paths could agree by dropping everything.
	wantStart, _ := schedule.FormatClimateTime((windowSlot - 1) * slotMins)
	wantEnd, _ := schedule.FormatClimateTime(windowSlot * slotMins)
	want := []slotLimitPeriod{{start: wantStart, end: wantEnd, temp: inRange}}
	if !slotLimitPeriodsEqual(restPeriods, want) {
		t.Errorf("REST periods = %+v, want %+v — a cell outside 1..%d reached the DTO",
			restPeriods, want, schedule.MaxClimateSlots)
	}
	if !slotLimitPeriodsEqual(wpPeriods, want) {
		t.Errorf("week-profile periods = %+v, want %+v", wpPeriods, want)
	}
}
