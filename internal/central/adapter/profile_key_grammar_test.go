// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// TestProfileKeyGrammarAgreesAcrossPlanes drives one table of profile keys
// through every plane that gates on the grammar and asserts they return the
// same verdict. Each plane is exercised through its own entry point, never
// through the shared helper, so a plane that grows a second spelling of the
// rule shows up as a disagreement rather than as silently divergent
// behaviour on the same key.
//
// The planes: the domain grammar, the adapter's device-cap gate, the raw
// week-profile decoder, and [schedule.Climate.Put].
func TestProfileKeyGrammarAgreesAcrossPlanes(t *testing.T) {
	t.Parallel()

	// The cap used for the adapter gate is the highest any device declares,
	// so the cap never decides a row — only the grammar does.
	const cap6 = weekprofile.MaxProfileIndex

	for _, tc := range []struct {
		key  string
		want bool
	}{
		{key: "P1", want: true},
		{key: "P6", want: true},
		{key: "P01", want: false},
		{key: "P0", want: false},
		{key: "P7", want: false},
		{key: "P", want: false},
		{key: "PX", want: false},
		{key: "", want: false},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			if got := schedule.IsValidProfileKey(tc.key); got != tc.want {
				t.Errorf("schedule.IsValidProfileKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
			if got := isProfileIDWithinCap(tc.key, cap6); got != tc.want {
				t.Errorf("isProfileIDWithinCap(%q, %d) = %v, want %v", tc.key, cap6, got, tc.want)
			}

			raw := map[string]map[string]map[int]weekprofile.ScheduleSlot{
				tc.key: {"MONDAY": {1: {EndTime: "24:00", Temperature: 21.0}}},
			}
			_, err := weekprofile.RawToClimate(raw)
			if gotDecoded := err == nil; gotDecoded != tc.want {
				t.Errorf("weekprofile.RawToClimate with key %q: accepted = %v (err=%v), want %v",
					tc.key, gotDecoded, err, tc.want)
			}

			c := schedule.NewClimate()
			putErr := c.Put(tc.key, schedule.NewClimateProfile())
			if gotPut := putErr == nil; gotPut != tc.want {
				t.Errorf("schedule.Climate.Put(%q) accepted = %v (err=%v), want %v",
					tc.key, gotPut, putErr, tc.want)
			}
		})
	}
}
