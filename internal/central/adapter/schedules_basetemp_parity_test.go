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

// The two read paths for a climate MASTER paramset are independent code:
// parseClimateSchedule feeds the REST/WS DTO, ParseClimateRawParamset +
// RawToClimate feeds the week-profile domain model and from there the MQTT
// climate state payload. Both derive a "base temperature" and report every
// stretch that is not the base as an explicit period, so the same paramset
// must not yield two different answers — an operator reading the SPA
// schedule editor and the MQTT topic is reading one device.
//
// This test lives in package adapter because parseClimateSchedule is
// unexported; both halves are production entry points and neither
// re-implements the rule.

// climateBaseTempParityPeriod is the shape both read paths are compared on.
type climateBaseTempParityPeriod struct {
	start string
	end   string
	temp  float64
}

// climateBaseTempParitySlot is one wire cell of the constructed paramset.
type climateBaseTempParitySlot struct {
	endMin int
	temp   float64
}

// climateBaseTempParityParamset renders the 13 MONDAY cells of profile P1
// in the flat CCU MASTER form both readers accept.
func climateBaseTempParityParamset(slots []climateBaseTempParitySlot) map[string]any {
	raw := make(map[string]any, len(slots)*2)
	for i, s := range slots {
		raw[fmt.Sprintf("P1_ENDTIME_MONDAY_%d", i+1)] = s.endMin
		raw[fmt.Sprintf("P1_TEMPERATURE_MONDAY_%d", i+1)] = s.temp
	}
	return raw
}

// climateBaseTempParityFill pads a day out to the full 13 slots the CCU
// always reports, repeating the last slot at end-of-day.
func climateBaseTempParityFill(slots []climateBaseTempParitySlot, padTemp float64) []climateBaseTempParitySlot {
	for len(slots) < schedule.MaxClimateSlots {
		slots = append(slots, climateBaseTempParitySlot{
			endMin: schedule.ClimateEndOfDayMinutes,
			temp:   padTemp,
		})
	}
	return slots
}

// climateBaseTempParityZeroLength builds a full 13-slot day in which every
// cell ends at midnight, so no slot spans a single minute — the degenerate
// shape that forces both readers onto their fallback.
func climateBaseTempParityZeroLength(temp float64) []climateBaseTempParitySlot {
	slots := make([]climateBaseTempParitySlot, 0, schedule.MaxClimateSlots)
	for range schedule.MaxClimateSlots {
		slots = append(slots, climateBaseTempParitySlot{endMin: 0, temp: temp})
	}
	return slots
}

func TestClimateBaseTemperatureAgreesAcrossReadPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		slots       []climateBaseTempParitySlot
		wantBase    float64
		wantPeriods []climateBaseTempParityPeriod
	}{
		{
			// Equal halves, the WARMER one first: the two historical rules
			// disagree exactly here. A day whose earliest stretch is also
			// the coolest cannot tell them apart.
			name: "equal_halves_warmer_first",
			slots: climateBaseTempParityFill([]climateBaseTempParitySlot{
				{endMin: 720, temp: 21.0},
				{endMin: schedule.ClimateEndOfDayMinutes, temp: 17.0},
			}, 17.0),
			wantBase: 21.0,
			wantPeriods: []climateBaseTempParityPeriod{
				{start: "12:00", end: "24:00", temp: 17.0},
			},
		},
		{
			// Off the 0.5 °C grid: the base must be a temperature the day
			// actually carries, otherwise it matches no stretch and the
			// whole day is reported as one spurious period.
			name: "flat_day_off_grid",
			slots: climateBaseTempParityFill([]climateBaseTempParitySlot{
				{endMin: schedule.ClimateEndOfDayMinutes, temp: 20.7},
			}, 20.7),
			wantBase:    20.7,
			wantPeriods: nil,
		},
		{
			// No stretch holds a single minute. Both paths fall back, and
			// they fall back to the same value.
			name:        "no_usable_stretch",
			slots:       climateBaseTempParityZeroLength(schedule.DefaultBaseTemperature),
			wantBase:    schedule.DefaultBaseTemperature,
			wantPeriods: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := climateBaseTempParityParamset(tc.slots)

			// Path A: the REST / WebSocket read (schedules.go).
			dto, err := parseClimateSchedule(t.Context(), raw)
			if err != nil {
				t.Fatalf("parseClimateSchedule: %v", err)
			}
			restDay := dto.Profiles["P1"].Weekdays["MONDAY"]
			restPeriods := make([]climateBaseTempParityPeriod, 0, len(restDay.Periods))
			for _, p := range restDay.Periods {
				restPeriods = append(restPeriods, climateBaseTempParityPeriod{p.StartTime, p.EndTime, p.Temperature})
			}

			// Path B: the week-profile read feeding the MQTT climate state.
			rawSched, err := weekprofile.ParseClimateRawParamset(raw)
			if err != nil {
				t.Fatalf("ParseClimateRawParamset: %v", err)
			}
			climate, err := weekprofile.RawToClimate(rawSched)
			if err != nil {
				t.Fatalf("RawToClimate: %v", err)
			}
			wpDay := climate.Profiles["P1"].Days[schedule.Weekday("MONDAY")]
			wpPeriods := make([]climateBaseTempParityPeriod, 0, len(wpDay.Periods))
			for _, p := range wpDay.Periods {
				wpPeriods = append(wpPeriods, climateBaseTempParityPeriod{p.StartTime, p.EndTime, p.Temperature})
			}

			if math.Abs(restDay.BaseTemperature-wpDay.BaseTemperature) > 1e-9 {
				t.Errorf("base temperature diverges: REST %g, week-profile %g",
					restDay.BaseTemperature, wpDay.BaseTemperature)
			}
			if math.Abs(restDay.BaseTemperature-tc.wantBase) > 1e-9 {
				t.Errorf("REST base = %g, want %g", restDay.BaseTemperature, tc.wantBase)
			}
			if math.Abs(wpDay.BaseTemperature-tc.wantBase) > 1e-9 {
				t.Errorf("week-profile base = %g, want %g", wpDay.BaseTemperature, tc.wantBase)
			}

			want := tc.wantPeriods
			if want == nil {
				want = []climateBaseTempParityPeriod{}
			}
			if !climateBaseTempParityPeriodsEqual(restPeriods, wpPeriods) {
				t.Errorf("periods diverge: REST %+v, week-profile %+v", restPeriods, wpPeriods)
			}
			if !climateBaseTempParityPeriodsEqual(restPeriods, want) {
				t.Errorf("REST periods = %+v, want %+v", restPeriods, want)
			}
			if !climateBaseTempParityPeriodsEqual(wpPeriods, want) {
				t.Errorf("week-profile periods = %+v, want %+v", wpPeriods, want)
			}
		})
	}
}

// climateBaseTempParityPeriodsEqual compares two period lists field by field.
// The two paths return different named types carrying the same three values,
// so a reflect-based comparison is not available.
func climateBaseTempParityPeriodsEqual(a, b []climateBaseTempParityPeriod) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].start != b[i].start || a[i].end != b[i].end {
			return false
		}
		if math.Abs(a[i].temp-b[i].temp) > 1e-9 {
			return false
		}
	}
	return true
}
