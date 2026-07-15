// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarmpanel

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// HA alarm_control_panel state tokens. These are the plain strings the
// retained `<base>/alarm/<area>/state` topic carries and that Home
// Assistant's alarm_control_panel entity renders 1:1. Wire-stable.
const (
	HAAlarmStateDisarmed          = "disarmed"
	HAAlarmStateArming            = "arming"
	HAAlarmStatePending           = "pending"
	HAAlarmStateTriggered         = "triggered"
	HAAlarmStateArmedHome         = "armed_home"
	HAAlarmStateArmedAway         = "armed_away"
	HAAlarmStateArmedNight        = "armed_night"
	HAAlarmStateArmedVacation     = "armed_vacation"
	HAAlarmStateArmedCustomBypass = "armed_custom_bypass"
)

// HA alarm_control_panel command payloads. Home Assistant publishes one
// of these bare strings (or the JSON `{"action":"…"}` form) to the
// `<base>/alarm/<area>/set` command topic.
//
//nolint:gosec // HA command vocabulary tokens, not credentials
const (
	HAAlarmCommandArmHome         = "ARM_HOME"
	HAAlarmCommandArmAway         = "ARM_AWAY"
	HAAlarmCommandArmNight        = "ARM_NIGHT"
	HAAlarmCommandArmVacation     = "ARM_VACATION"
	HAAlarmCommandArmCustomBypass = "ARM_CUSTOM_BYPASS"
	HAAlarmCommandDisarm          = "DISARM"
	// HAAlarmCommandSilence is a deliberate OpenCCU-Loom extension of the
	// HA vocabulary (docs/alarm-concept.md §13.3): the raw command plane
	// accepts SILENCE to stop sounding outputs without a state change.
	HAAlarmCommandSilence = "SILENCE"
)

// StateToken maps an engine (area-state, mode) pair onto the HA
// alarm_control_panel state token. The armed state resolves through the
// active mode; every other state maps directly. An armed area whose mode
// is unexpected falls back to the away token — an armed panel must never
// render as disarmed.
func StateToken(state hmenum.AlarmAreaState, mode hmenum.AlarmMode) string {
	switch state {
	case hmenum.AlarmAreaStateDisarmed:
		return HAAlarmStateDisarmed
	case hmenum.AlarmAreaStateArming:
		return HAAlarmStateArming
	case hmenum.AlarmAreaStatePending:
		return HAAlarmStatePending
	case hmenum.AlarmAreaStateTriggered:
		return HAAlarmStateTriggered
	case hmenum.AlarmAreaStateArmed:
		return armedTokenForMode(mode)
	default:
		return HAAlarmStateDisarmed
	}
}

// armedTokenForMode resolves the armed HA token for a protection mode.
func armedTokenForMode(mode hmenum.AlarmMode) string {
	switch mode {
	case hmenum.AlarmModePerimeter:
		return HAAlarmStateArmedHome
	case hmenum.AlarmModeFull:
		return HAAlarmStateArmedAway
	case hmenum.AlarmModeNight:
		return HAAlarmStateArmedNight
	case hmenum.AlarmModeVacation:
		return HAAlarmStateArmedVacation
	case hmenum.AlarmModeCustom:
		return HAAlarmStateArmedCustomBypass
	default:
		return HAAlarmStateArmedAway
	}
}

// ArmModeForCommand maps an HA arm command payload onto the engine
// protection mode. ok is false for DISARM, SILENCE, and any unknown
// payload — the caller routes those through the disarm/silence verbs or
// drops them.
func ArmModeForCommand(cmd string) (mode hmenum.AlarmMode, ok bool) {
	switch cmd {
	case HAAlarmCommandArmHome:
		return hmenum.AlarmModePerimeter, true
	case HAAlarmCommandArmAway:
		return hmenum.AlarmModeFull, true
	case HAAlarmCommandArmNight:
		return hmenum.AlarmModeNight, true
	case HAAlarmCommandArmVacation:
		return hmenum.AlarmModeVacation, true
	case HAAlarmCommandArmCustomBypass:
		return hmenum.AlarmModeCustom, true
	default:
		return "", false
	}
}

// MasterStateToken aggregates the per-area HA state tokens onto the
// single master-panel token (docs/alarm-concept.md §13.3 decision): any
// triggered wins, then pending, then arming; a set that is entirely one
// token collapses to that token (all disarmed → disarmed, all armed in
// the same mode → that armed token); every other mix reports away.
func MasterStateToken(tokens []string) string {
	if len(tokens) == 0 {
		return HAAlarmStateDisarmed
	}
	has := func(want string) bool {
		for _, t := range tokens {
			if t == want {
				return true
			}
		}
		return false
	}
	switch {
	case has(HAAlarmStateTriggered):
		return HAAlarmStateTriggered
	case has(HAAlarmStatePending):
		return HAAlarmStatePending
	case has(HAAlarmStateArming):
		return HAAlarmStateArming
	}
	first := tokens[0]
	for _, t := range tokens[1:] {
		if t != first {
			return HAAlarmStateArmedAway
		}
	}
	return first
}

// SupportedFeatures maps the configured protection modes onto the HA
// alarm_control_panel supported-feature tokens, emitted in the canonical
// arm-button order so the panel layout stays stable across republishes.
func SupportedFeatures(modes []hmenum.AlarmMode) []string {
	present := make(map[hmenum.AlarmMode]bool, len(modes))
	for _, m := range modes {
		present[m] = true
	}
	features := make([]string, 0, len(modes))
	for _, m := range []hmenum.AlarmMode{
		hmenum.AlarmModePerimeter,
		hmenum.AlarmModeFull,
		hmenum.AlarmModeNight,
		hmenum.AlarmModeVacation,
		hmenum.AlarmModeCustom,
	} {
		if !present[m] {
			continue
		}
		switch m {
		case hmenum.AlarmModePerimeter:
			features = append(features, HAAlarmFeatureArmHome)
		case hmenum.AlarmModeFull:
			features = append(features, HAAlarmFeatureArmAway)
		case hmenum.AlarmModeNight:
			features = append(features, HAAlarmFeatureArmNight)
		case hmenum.AlarmModeVacation:
			features = append(features, HAAlarmFeatureArmVacation)
		case hmenum.AlarmModeCustom:
			features = append(features, HAAlarmFeatureArmCustomBypass)
		default:
			// disarmed carries no arm feature.
		}
	}
	return features
}

// HA supported_features tokens of the alarm_control_panel discovery
// payload. Wire-stable.
//
//nolint:gosec // HA vocabulary tokens, not credentials
const (
	HAAlarmFeatureArmHome         = "arm_home"
	HAAlarmFeatureArmAway         = "arm_away"
	HAAlarmFeatureArmNight        = "arm_night"
	HAAlarmFeatureArmVacation     = "arm_vacation"
	HAAlarmFeatureArmCustomBypass = "arm_custom_bypass"
)
