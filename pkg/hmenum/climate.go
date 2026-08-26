// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// ClimateMode is the daemon-normalised, client-facing thermostat mode. The
// daemon already maps the raw CCU CONTROL_MODE onto this closed vocabulary (the
// Custom-DP state normalisation); publishing the enum here — the canonical
// codegen surface exported into assets/schemas/enums.json — lets a wire client
// dispatch on a typed, bounded set instead of matching free strings. The
// per-device *available* subset still rides `config.hvac_modes`; this is the
// universe those values are drawn from.
type ClimateMode string

// ClimateMode values.
const (
	ClimateModeAuto ClimateMode = "auto"
	ClimateModeHeat ClimateMode = "heat"
	ClimateModeCool ClimateMode = "cool"
	ClimateModeOff  ClimateMode = "off"
)

// String returns the wire representation.
func (m ClimateMode) String() string { return string(m) }

// ClimateProfile is the daemon-normalised, client-facing thermostat profile
// slot (presets + the week-program selectors). Same contract as [ClimateMode]:
// the closed vocabulary the per-device `config.preset_modes` subset is drawn
// from, published for typed client dispatch.
type ClimateProfile string

// ClimateProfile values.
const (
	ClimateProfileNone         ClimateProfile = "none"
	ClimateProfileAway         ClimateProfile = "away"
	ClimateProfileBoost        ClimateProfile = "boost"
	ClimateProfileComfort      ClimateProfile = "comfort"
	ClimateProfileEco          ClimateProfile = "eco"
	ClimateProfileWeekProgram1 ClimateProfile = "week_program_1"
	ClimateProfileWeekProgram2 ClimateProfile = "week_program_2"
	ClimateProfileWeekProgram3 ClimateProfile = "week_program_3"
	ClimateProfileWeekProgram4 ClimateProfile = "week_program_4"
	ClimateProfileWeekProgram5 ClimateProfile = "week_program_5"
	ClimateProfileWeekProgram6 ClimateProfile = "week_program_6"
)

// String returns the wire representation.
func (p ClimateProfile) String() string { return string(p) }
