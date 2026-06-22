// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

func TestClimateModeValues(t *testing.T) {
	cases := map[ClimateMode]string{
		ClimateModeAuto: "auto",
		ClimateModeHeat: "heat",
		ClimateModeCool: "cool",
		ClimateModeOff:  "off",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("ClimateMode %q String() = %q, want %q", string(mode), got, want)
		}
	}
}

func TestClimateProfileValues(t *testing.T) {
	cases := map[ClimateProfile]string{
		ClimateProfileNone:         "none",
		ClimateProfileAway:         "away",
		ClimateProfileBoost:        "boost",
		ClimateProfileComfort:      "comfort",
		ClimateProfileEco:          "eco",
		ClimateProfileWeekProgram1: "week_program_1",
		ClimateProfileWeekProgram6: "week_program_6",
	}
	for profile, want := range cases {
		if got := profile.String(); got != want {
			t.Errorf("ClimateProfile %q String() = %q, want %q", string(profile), got, want)
		}
	}
}
