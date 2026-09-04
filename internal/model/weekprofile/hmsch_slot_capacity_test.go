// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// hmSchGappedWeekday builds a weekday of n periods separated by gaps, so the
// expansion has to insert a base-temperature slot before each one. n periods
// therefore cost 2n slots, plus one for the trailing gap to 24:00.
//
// The periods are one hour long and start on even hours, which keeps them
// inside the day for the counts this test uses.
func hmSchGappedWeekday(n int) schedule.ClimateWeekday {
	periods := make([]schedule.ClimatePeriod, 0, n)
	for i := range n {
		start := 2*i + 1
		periods = append(periods, schedule.ClimatePeriod{
			StartTime:   hmSchHour(start),
			EndTime:     hmSchHour(start + 1),
			Temperature: 21.0,
		})
	}
	return schedule.ClimateWeekday{BaseTemperature: 17.0, Periods: periods}
}

// hmSchHour renders a whole hour as "HH:00".
func hmSchHour(h int) string {
	return string(rune('0'+h/10)) + string(rune('0'+h%10)) + ":00"
}

// TestHmSchWeekdayExpansionRefusesToOverfillTheDay pins that a weekday which
// expands past the CCU's 13 slots is refused, not trimmed.
//
// The wire-side validator deliberately drops the gapless-coverage rule, so a
// day of seven gapped periods is valid input: it expands to 7 periods + 7 gap
// fills + 1 trailing entry = 15 slots. Trimming to 13 discards the last two —
// including the entry that ends the day at 24:00 — and writes a schedule whose
// last slot ends in the afternoon, with nothing said about it.
func TestHmSchWeekdayExpansionRefusesToOverfillTheDay(t *testing.T) {
	t.Parallel()

	// Six gapped periods still fit: 6 + 6 + 1 = 13.
	fits := hmSchGappedWeekday(6)
	slots, err := climateWeekdayToSlotsExpand(fits)
	if err != nil {
		t.Fatalf("a weekday expanding to exactly %d slots was refused: %v", slotCount, err)
	}
	if len(slots) != slotCount {
		t.Fatalf("expansion produced %d slots, want %d", len(slots), slotCount)
	}
	if last := slots[slotCount]; last.EndTime != maxScheduleTime {
		t.Errorf("slot %d ends at %q, want the end-of-day marker %q",
			slotCount, last.EndTime, maxScheduleTime)
	}

	// Seven gapped periods do not: 7 + 7 + 1 = 15.
	overfull := hmSchGappedWeekday(7)
	if _, err := climateWeekdayToSlotsExpand(overfull); err == nil {
		t.Fatalf("a weekday expanding to 15 slots was accepted; the CCU stores %d, "+
			"so the excess — the end-of-day entry among it — would be dropped silently",
			slotCount)
	} else if !strings.Contains(err.Error(), "slots") {
		t.Errorf("over-capacity error does not name the slot count: %v", err)
	}
}

// TestHmSchTrailingGapUsesTheDomainEndOfDay pins that the trailing fill runs
// to the domain's own end-of-day marker rather than to a re-spelled 1440.
func TestHmSchTrailingGapUsesTheDomainEndOfDay(t *testing.T) {
	t.Parallel()

	day := schedule.ClimateWeekday{
		BaseTemperature: 17.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "06:00", Temperature: 21.0},
		},
	}
	slots, err := climateWeekdayToSlotsExpand(day)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if got := slots[2].EndTime; got != schedule.ClimateEndOfDay {
		t.Errorf("the trailing gap ends at %q, want %q", got, schedule.ClimateEndOfDay)
	}
	if schedule.ClimateEndOfDayMinutes != toMinutes(schedule.ClimateEndOfDay) {
		t.Errorf("the domain's end-of-day marker %q is %d minutes, but its minute form "+
			"says %d", schedule.ClimateEndOfDay, toMinutes(schedule.ClimateEndOfDay),
			schedule.ClimateEndOfDayMinutes)
	}
}
