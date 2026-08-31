// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// weekprofileSlotsGaplessDay builds a weekday whose periods tile 00:00 to
// 24:00 without a gap, so that one period maps to exactly one wire slot.
func weekprofileSlotsGaplessDay(periods int) schedule.ClimateWeekday {
	day := schedule.ClimateWeekday{BaseTemperature: 17}
	step := schedule.ClimateEndOfDayMinutes / periods
	for i := range periods {
		start := i * step
		end := start + step
		if i == periods-1 {
			end = schedule.ClimateEndOfDayMinutes
		}
		startStr, _ := schedule.FormatClimateTime(start)
		endStr, _ := schedule.FormatClimateTime(end)
		day.Periods = append(day.Periods, schedule.ClimatePeriod{
			StartTime:   startStr,
			EndTime:     endStr,
			Temperature: 18 + float64(i)/2,
		})
	}
	return day
}

// TestWeekprofileSlotsEncoderCarriesEverythingTheValidatorAdmits pins the
// per-weekday slot count as one fact across two packages that used to spell
// it separately.
//
// The assertion is the useful direction: whatever number of periods
// schedule's validator lets through, weekprofile's wire encoder must be
// able to carry. When those two numbers drift apart the failure is silent —
// the validator says yes, the encoder trims the excess and writes a
// schedule that is not the one the operator saved.
func TestWeekprofileSlotsEncoderCarriesEverythingTheValidatorAdmits(t *testing.T) {
	t.Parallel()

	full := weekprofileSlotsGaplessDay(schedule.MaxClimatePeriods)
	if err := full.Validate(); err != nil {
		t.Fatalf("a gapless day of %d periods must validate: %v", schedule.MaxClimatePeriods, err)
	}
	over := weekprofileSlotsGaplessDay(schedule.MaxClimatePeriods + 1)
	if err := over.Validate(); err == nil {
		t.Errorf("a day of %d periods must be rejected", schedule.MaxClimatePeriods+1)
	}

	prof := schedule.NewClimateProfile()
	for _, w := range schedule.Weekdays {
		prof.Days[w] = full
	}
	clim := schedule.NewClimate()
	if err := clim.Put("P1", prof); err != nil {
		t.Fatalf("Put: %v", err)
	}

	raw, err := weekprofile.ClimateToRaw(clim)
	if err != nil {
		t.Fatalf("weekprofile.ClimateToRaw: %v", err)
	}
	paramset, err := weekprofile.BuildClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("weekprofile.BuildClimateRawParamset: %v", err)
	}

	for _, w := range schedule.Weekdays {
		endPrefix := fmt.Sprintf("P1_ENDTIME_%s_", string(w))
		got := 0
		for key := range paramset {
			if strings.HasPrefix(key, endPrefix) {
				got++
			}
		}
		if got != schedule.MaxClimateSlots {
			t.Errorf("%s: encoder emitted %d ENDTIME slots, want %d — the validator admits %d periods",
				w, got, schedule.MaxClimateSlots, schedule.MaxClimatePeriods)
		}
		// Slot n must carry period n, not a padding entry. A count alone
		// cannot tell a carried period from a slot the encoder trimmed and
		// the padding loop refilled with the base temperature.
		for i, p := range full.Periods {
			wantEnd, err := schedule.ParseClimateTime(p.EndTime)
			if err != nil {
				t.Fatalf("period end %q: %v", p.EndTime, err)
			}
			gotEnd, ok := paramset[fmt.Sprintf("%s%d", endPrefix, i+1)]
			if !ok {
				t.Errorf("%s: slot %d has no ENDTIME", w, i+1)
				continue
			}
			if gotEnd != wantEnd {
				t.Errorf("%s slot %d: ENDTIME = %v, want %d", w, i+1, gotEnd, wantEnd)
			}
			gotTemp := paramset[fmt.Sprintf("P1_TEMPERATURE_%s_%d", string(w), i+1)]
			if gotTemp != p.Temperature {
				t.Errorf("%s slot %d: TEMPERATURE = %v, want %v — the period was trimmed and refilled",
					w, i+1, gotTemp, p.Temperature)
			}
		}
	}
}
