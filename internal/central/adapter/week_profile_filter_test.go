// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import "testing"

// TestIsWeekProfileSlotParameter pins the filter contract that decides
// which MASTER paramset names are dropped during hydration. False
// positives here would silently hide unrelated config parameters from
// the UI / MQTT; false negatives would re-introduce the ~84 ghost
// topics per thermostat the filter is meant to suppress.
//
// The Schema-A verdicts are the model's
// ([weekprofile.IsClimateSlotName]); the cases below record what the
// hydration path does with them. The effect that matters is asserted in
// TestWeekProfileSlotHydrationDropsOnlyDecodableCells.
func TestIsWeekProfileSlotParameter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want bool
	}{
		// Slot parameters — must be filtered.
		{"P1 endtime monday", "P1_ENDTIME_MONDAY_1", true},
		{"P2 temperature tuesday last slot", "P2_TEMPERATURE_TUESDAY_13", true},
		{"P3 endtime sunday", "P3_ENDTIME_SUNDAY_7", true},
		{"P4 temperature wednesday", "P4_TEMPERATURE_WEDNESDAY_3", true},
		{"P5 endtime saturday", "P5_ENDTIME_SATURDAY_2", true},
		{"P6 temperature friday", "P6_TEMPERATURE_FRIDAY_4", true},

		// Top-level scheduling parameters — must NOT be filtered.
		{"WEEK_PROGRAM_POINTER", "WEEK_PROGRAM_POINTER", false},
		{"ACTIVE_PROFILE", "ACTIVE_PROFILE", false},
		{"WEEK_PROGRAM_CHANNEL_LOCKS", "WEEK_PROGRAM_CHANNEL_LOCKS", false},

		// Other parameters that share a prefix letter — must NOT be filtered.
		{"PRESS short", "PRESS_SHORT", false},
		{"PARTY mode", "PARTY_MODE", false},
		{"P_NUMBER (no digit)", "P_NUMBER", false},
		{"P0 (zero — no profile 0)", "P0_FOO", false},
		{"P7 (no profile 7)", "P7_FOO", false},

		// Edge cases.
		{"empty", "", false},
		{"too short", "P1", false},
		{"missing underscore", "P1FOO", false},
		{"lowercase suffix (not CCU style)", "P1_temperature", false},
		{"P1_ alone (no body)", "P1_", false}, // prefix with empty body — invalid CCU shape, reject

		// Schema-A near-misses: the profile prefix alone is not a cell.
		// Suppression here is a drop, so a key the week-profile parser
		// cannot decode must stay a normal parameter.
		{"P1_X (prefix, no cell body)", "P1_X", false},
		{"P1_LEVEL_MONDAY_1 (unknown field)", "P1_LEVEL_MONDAY_1", false},
		{"P1 slot ordinal past the CCU limit", "P1_ENDTIME_MONDAY_14", false},
		{"P1 slot ordinal zero", "P1_TEMPERATURE_MONDAY_0", false},
		{"P1 last valid slot ordinal", "P1_ENDTIME_MONDAY_13", true},

		// Schema B (classic HM, single profile) — bare names without P-prefix.
		{"bare ENDTIME monday slot 1", "ENDTIME_MONDAY_1", true},
		{"bare TEMPERATURE friday slot 13", "TEMPERATURE_FRIDAY_13", true},
		{"bare ENDTIME sunday slot 7", "ENDTIME_SUNDAY_7", true},
		// Schema-B near-misses — must NOT trigger.
		{"TEMPERATURE_MINIMUM (bare bound)", "TEMPERATURE_MINIMUM", false},
		{"TEMPERATURE_MAXIMUM (bare bound)", "TEMPERATURE_MAXIMUM", false},
		{"TEMPERATURE_OFFSET (bare offset)", "TEMPERATURE_OFFSET", false},
		{"TEMPERATURE_MONDAY_ (no slot)", "TEMPERATURE_MONDAY_", false},
		{"TEMPERATURE_MONDAYX_1 (bad day)", "TEMPERATURE_MONDAYX_1", false},
		{"TEMPERATURE_FRIDAYS_1 (extra char in day)", "TEMPERATURE_FRIDAYS_1", false},
		{"WEATHER_MONDAY_1 (different prefix)", "WEATHER_MONDAY_1", false},
		{"endtime_monday_1 (lowercase)", "endtime_monday_1", false},
		{"TEMPERATURE_MONDAY_X (slot not numeric)", "TEMPERATURE_MONDAY_X", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isWeekProfileSlotParameter(tc.in); got != tc.want {
				t.Errorf("isWeekProfileSlotParameter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
