// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// weekprofileSlotsWeekdayAnchor is an independently declared spelling of the
// CCU weekday tokens. It is deliberately NOT derived from
// [schedule.Weekdays]: an anchor that reads its subject cannot disagree
// with it. pkg/hmenum is the repo's declared home for wire enumerations, so
// the two declarations are supposed to name the same seven words in the
// same order for as long as both exist.
var weekprofileSlotsWeekdayAnchor = []hmenum.WeekdayStr{
	hmenum.WeekdayStrMonday,
	hmenum.WeekdayStrTuesday,
	hmenum.WeekdayStrWednesday,
	hmenum.WeekdayStrThursday,
	hmenum.WeekdayStrFriday,
	hmenum.WeekdayStrSaturday,
	hmenum.WeekdayStrSunday,
}

// TestWeekprofileSlotsWeekdaySetIsOneFact pins the weekday set that the
// schedule adapter, the week-profile filter and the paramset key grammar all
// gate on.
//
// Order is part of the fact, not decoration: the set is consumed positionally
// on the read path, so a reordering is as much a drift as a deletion, and
// neither produces an error anywhere — the day simply stops matching and its
// slots vanish from the schedule.
func TestWeekprofileSlotsWeekdaySetIsOneFact(t *testing.T) {
	t.Parallel()

	if len(schedule.Weekdays) != len(weekprofileSlotsWeekdayAnchor) {
		t.Fatalf("schedule.Weekdays has %d entries, the hmenum declaration has %d",
			len(schedule.Weekdays), len(weekprofileSlotsWeekdayAnchor))
	}
	for i, want := range weekprofileSlotsWeekdayAnchor {
		if string(schedule.Weekdays[i]) != string(want) {
			t.Errorf("weekday[%d] = %q, the hmenum declaration says %q",
				i, schedule.Weekdays[i], want)
		}
	}

	// The effect: every day in the set must survive the CCU paramset key
	// grammar and come back out of the parser as that same day, and a word
	// outside the set must not. TEMPERATURE_OFFSET is the concrete
	// near-miss — a real device-master key that a wildcard weekday group
	// would swallow as a schedule cell.
	for _, w := range schedule.Weekdays {
		key := fmt.Sprintf("P1_ENDTIME_%s_1", string(w))
		raw, err := weekprofile.ParseClimateRawParamset(map[string]any{
			key: 360,
			fmt.Sprintf("P1_TEMPERATURE_%s_1", string(w)): 21.0,
		})
		if err != nil {
			t.Fatalf("ParseClimateRawParamset(%q): %v", key, err)
		}
		clim, err := weekprofile.RawToClimate(raw)
		if err != nil {
			t.Fatalf("RawToClimate for %q: %v", key, err)
		}
		prof := clim.Profiles["P1"]
		if prof == nil {
			t.Errorf("%q produced no P1 profile", key)
			continue
		}
		if _, ok := prof.Days[w]; !ok {
			t.Errorf("%q did not land on weekday %q", key, w)
		}
	}

	for _, notADay := range []string{"OFFSET", "FUNDAY", "MONDAYS", ""} {
		raw, err := weekprofile.ParseClimateRawParamset(map[string]any{
			"P1_ENDTIME_" + notADay + "_1":     360,
			"P1_TEMPERATURE_" + notADay + "_1": 21.0,
		})
		if err != nil {
			t.Fatalf("ParseClimateRawParamset(%q): %v", notADay, err)
		}
		if _, err := weekprofile.RawToClimate(raw); err == nil && len(raw) > 0 {
			t.Errorf("%q was accepted as a weekday", notADay)
		}
	}
}
