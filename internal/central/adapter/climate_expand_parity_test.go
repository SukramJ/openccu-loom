// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// TestClimateDayTerminatorIsOneValue pins that this package does not carry its
// own spelling of the day terminator.
//
// A climate weekday's last slot must end at 24:00, and the CCU's own reader
// stops at that value. The number was spelled four times here as a bare 1440
// while internal/model/schedule owns it, so a change to the domain constant
// would have left this expander behind — and the two expanders (this one and
// weekprofile's) would then disagree about where a day ends.
func TestClimateDayTerminatorIsOneValue(t *testing.T) {
	t.Parallel()

	if schedule.ClimateEndOfDayMinutes != 1440 {
		t.Fatalf("ClimateEndOfDayMinutes = %d, want 1440", schedule.ClimateEndOfDayMinutes)
	}
	// The expander must fill an unterminated day up to exactly that value,
	// whatever it is — read from the domain, not restated here.
	wd := hmapi.ClimateWeekday{Periods: []hmapi.ClimatePeriod{
		{StartTime: "06:00", EndTime: "08:00", Temperature: 21},
	}}
	slots, err := expandWeekday(wd)
	if err != nil {
		t.Fatalf("expandWeekday: %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("expander produced no slots")
	}
	// Two distinct producers of the terminator, checked separately because a
	// test that only reads the final slot measures the padding branch and
	// leaves the fill-up branch unguarded.
	//
	// 06:00-08:00 yields [{06:00, base}, {08:00, 21}, {24:00, base}] and then
	// pads the remaining slots with the same terminator.
	fillUp := slots[2]
	if fillUp.endMin != schedule.ClimateEndOfDayMinutes {
		t.Errorf("fill-up slot ends at %d, want %d", fillUp.endMin, schedule.ClimateEndOfDayMinutes)
	}
	padded := slots[len(slots)-1]
	if padded.endMin != schedule.ClimateEndOfDayMinutes {
		t.Errorf("padded slot ends at %d, want %d", padded.endMin, schedule.ClimateEndOfDayMinutes)
	}
}
