// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestIsClimateSlotNameAcceptsExactlyWhatTheParserConsumes ties the
// predicate to the parser rather than to a second copy of the grammar.
//
// Consumers outside this package use the predicate to keep cells off
// per-parameter surfaces, and the CCU adapter drops them from hydration
// outright. A key the predicate calls a cell but the parser cannot read
// would therefore reach no surface at all, so the two must answer alike
// for every key.
func TestIsClimateSlotNameAcceptsExactlyWhatTheParserConsumes(t *testing.T) {
	t.Parallel()
	keys := []string{
		"P1_ENDTIME_MONDAY_1",
		"P1_TEMPERATURE_MONDAY_1",
		"P6_TEMPERATURE_FRIDAY_13",
		"P1_ENDTIME_MONDAY_13",
		"P1_ENDTIME_MONDAY_14",
		"P1_TEMPERATURE_MONDAY_0",
		"P1_X",
		"P1_LEVEL_MONDAY_1",
		"P7_ENDTIME_MONDAY_1",
		"P0_ENDTIME_MONDAY_1",
		"ENDTIME_MONDAY_1",
		"TEMPERATURE_FRIDAY_13",
		"TEMPERATURE_OFFSET",
		"WEEK_PROGRAM_POINTER",
		"01_WP_LEVEL",
		"",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			raw, err := ParseClimateRawParamset(map[string]any{key: 360})
			if err != nil {
				t.Fatalf("ParseClimateRawParamset(%q): %v", key, err)
			}
			parsed := len(raw) > 0
			if got := IsClimateSlotName(key); got != parsed {
				t.Errorf("IsClimateSlotName(%q) = %v but the parser %s it",
					key, got, map[bool]string{true: "consumes", false: "discards"}[parsed])
			}
		})
	}
}
