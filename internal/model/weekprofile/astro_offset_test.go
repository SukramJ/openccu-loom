// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

func astroSchedule(offset int) *schedule.Simple {
	return &schedule.Simple{
		Entries: map[int]schedule.SimpleEntry{
			1: {
				Weekdays:           []schedule.Weekday{schedule.WeekdayMonday},
				Time:               "00:00",
				Condition:          schedule.ConditionAstro,
				AstroType:          "sunset",
				AstroOffsetMinutes: offset,
				Level:              1,
			},
		},
	}
}

// TestAstroOffsetIsBoundedByTheDeclaredRange pins that the accepted range is
// the one the channel declares, not a constant of ours. Every model in the
// corpus declares ASTRO_OFFSET as INTEGER MIN -128 MAX 127; the CCU's own
// editor reads those two fields out of the paramset description and clamps its
// input to them rather than carrying a number.
func TestAstroOffsetIsBoundedByTheDeclaredRange(t *testing.T) {
	t.Parallel()

	declared := AstroOffsetLimits{Min: -128, Max: 127, Declared: true}

	for _, offset := range []int{-128, -1, 0, 60, 127} {
		if _, err := BuildSimpleRawParamset(astroSchedule(offset), 1, nil, declared); err != nil {
			t.Errorf("offset %d is inside the declared range but was rejected: %v", offset, err)
		}
	}
	for _, offset := range []int{128, 200, 719, -129, -720} {
		_, err := BuildSimpleRawParamset(astroSchedule(offset), 1, nil, declared)
		if err == nil {
			t.Errorf("offset %d is outside the declared range -128..127 but was written to the wire", offset)
			continue
		}
		if !strings.Contains(err.Error(), "-128") || !strings.Contains(err.Error(), "127") {
			t.Errorf("offset %d: error should name the declared bounds, got %v", offset, err)
		}
	}
}

// TestAstroOffsetFallsBackWhenNothingIsDeclared pins that a channel whose
// descriptor has not been loaded is no less protected than before the declared
// range was read: the previous ±12 h bound still applies.
func TestAstroOffsetFallsBackWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()

	if _, err := BuildSimpleRawParamset(astroSchedule(300), 1, nil, AstroOffsetLimits{}); err != nil {
		t.Errorf("undeclared range should accept 300: %v", err)
	}
	for _, offset := range []int{800, -800} {
		if _, err := BuildSimpleRawParamset(astroSchedule(offset), 1, nil, AstroOffsetLimits{}); err == nil {
			t.Errorf("undeclared range should still reject %d", offset)
		}
	}
}
