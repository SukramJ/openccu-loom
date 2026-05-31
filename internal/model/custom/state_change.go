// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

// StateChangeArg enumerates the argument keys accepted by every
// is_state_change implementation across custom data point types.
// Values are lower-case string labels matching the Python StrEnum literals
// exactly so that north-bound adapters can do string-based dispatch when
// needed (e.g. MQTT payload → Go function call).
type StateChangeArg string

// StateChangeArg values. Grouped by domain for readability; all are part
// of the single flat enum on the Python side.
const (
	// On/Off state — shared by Switch, Valve, Light, Siren.
	StateChangeArgOn  StateChangeArg = "on"
	StateChangeArgOff StateChangeArg = "off"

	// Light-specific.
	StateChangeArgBrightness      StateChangeArg = "brightness"
	StateChangeArgHsColor         StateChangeArg = "hs_color"
	StateChangeArgColorTempKelvin StateChangeArg = "color_temp_kelvin"
	StateChangeArgEffect          StateChangeArg = "effect"
	StateChangeArgOnTime          StateChangeArg = "on_time"
	StateChangeArgRampTime        StateChangeArg = "ramp_time"

	// Climate-specific.
	StateChangeArgTargetTemperature StateChangeArg = "target_temperature"
	StateChangeArgMode              StateChangeArg = "mode"
	StateChangeArgProfile           StateChangeArg = "profile"

	// Cover-specific.
	StateChangeArgClose        StateChangeArg = "close"
	StateChangeArgOpen         StateChangeArg = "open"
	StateChangeArgPosition     StateChangeArg = "position"
	StateChangeArgTiltClose    StateChangeArg = "tilt_close"
	StateChangeArgTiltOpen     StateChangeArg = "tilt_open"
	StateChangeArgTiltPosition StateChangeArg = "tilt_position"
	StateChangeArgVent         StateChangeArg = "vent"
)

// StateChangeArgs is a map of [StateChangeArg] keys to arbitrary values.
// It mirrors.py:38) as
// a Go map — the type system is weaker than TypedDict but the map can be
// passed around uniformly without requiring per-type override signatures.
//
// Usage:
//
//	if cover.IsStateChange(StateChangeArgs{
//	    StateChangeArgPosition: 75,
//	}) { /* command is a net state change */ }
//
// Most callers that already have Go-typed call sites should prefer the
// strongly-typed per-method signatures (Cover.IsStateChange(float64), etc.)
// and reserve this type for generic dispatch paths (MQTT → domain).
type StateChangeArgs map[StateChangeArg]any
