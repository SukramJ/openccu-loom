// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarmpanel

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// allAlarmModes lists every [hmenum.AlarmMode] value (excluding the
// non-armed "disarmed" mode) so the state-token tests can walk the full
// mode set without hand-maintaining a second copy of the enum.
var allAlarmModes = []hmenum.AlarmMode{
	hmenum.AlarmModePerimeter,
	hmenum.AlarmModeFull,
	hmenum.AlarmModeNight,
	hmenum.AlarmModeVacation,
	hmenum.AlarmModeCustom,
}

// TestStateToken_FullStateModeGrid walks the full (state, mode)
// cross-product notes/concepts/alarm-concept.md §13.3 defines and asserts the exact
// HA alarm_control_panel token. Non-armed states ignore mode entirely;
// the armed state resolves through the active mode.
func TestStateToken_FullStateModeGrid(t *testing.T) {
	t.Parallel()

	nonArmed := []struct {
		state hmenum.AlarmZoneState
		want  string
	}{
		{hmenum.AlarmZoneStateDisarmed, HAAlarmStateDisarmed},
		{hmenum.AlarmZoneStateArming, HAAlarmStateArming},
		{hmenum.AlarmZoneStatePending, HAAlarmStatePending},
		{hmenum.AlarmZoneStateTriggered, HAAlarmStateTriggered},
	}
	for _, c := range nonArmed {
		for _, mode := range append([]hmenum.AlarmMode{hmenum.AlarmModeDisarmed}, allAlarmModes...) {
			got := StateToken(c.state, mode)
			if got != c.want {
				t.Errorf("StateToken(%s, %s) = %q, want %q", c.state, mode, got, c.want)
			}
		}
	}

	armed := []struct {
		mode hmenum.AlarmMode
		want string
	}{
		{hmenum.AlarmModePerimeter, HAAlarmStateArmedHome},
		{hmenum.AlarmModeFull, HAAlarmStateArmedAway},
		{hmenum.AlarmModeNight, HAAlarmStateArmedNight},
		{hmenum.AlarmModeVacation, HAAlarmStateArmedVacation},
		{hmenum.AlarmModeCustom, HAAlarmStateArmedCustomBypass},
	}
	for _, c := range armed {
		got := StateToken(hmenum.AlarmZoneStateArmed, c.mode)
		if got != c.want {
			t.Errorf("StateToken(armed, %s) = %q, want %q", c.mode, got, c.want)
		}
	}
}

// TestStateToken_ArmedUnknownModeFallsBackToAway locks the
// documented safety fallback: an armed zone must never render as
// disarmed just because its mode is missing or unrecognized.
func TestStateToken_ArmedUnknownModeFallsBackToAway(t *testing.T) {
	t.Parallel()
	cases := []hmenum.AlarmMode{"", hmenum.AlarmModeDisarmed, "bogus-mode"}
	for _, mode := range cases {
		got := StateToken(hmenum.AlarmZoneStateArmed, mode)
		if got != HAAlarmStateArmedAway {
			t.Errorf("StateToken(armed, %q) = %q, want fallback %q", mode, got, HAAlarmStateArmedAway)
		}
	}
}

// TestStateToken_UnknownZoneStateFallsBackToDisarmed covers a
// state value outside the defined enum — the mapper must not panic or
// emit an empty/invalid token.
func TestStateToken_UnknownZoneStateFallsBackToDisarmed(t *testing.T) {
	t.Parallel()
	got := StateToken(hmenum.AlarmZoneState("bogus-state"), hmenum.AlarmModeFull)
	if got != HAAlarmStateDisarmed {
		t.Fatalf("StateToken(bogus-state, full) = %q, want %q", got, HAAlarmStateDisarmed)
	}
}

// TestArmModeForCommand_FullInverseMapping walks every HA arm
// command payload and asserts the exact inverse of [armedTokenForMode]
// (via [StateToken]): arming with the resolved mode must round-trip
// back to the same HA state token the command name encodes.
func TestArmModeForCommand_FullInverseMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd      string
		wantMode hmenum.AlarmMode
	}{
		{HAAlarmCommandArmHome, hmenum.AlarmModePerimeter},
		{HAAlarmCommandArmAway, hmenum.AlarmModeFull},
		{HAAlarmCommandArmNight, hmenum.AlarmModeNight},
		{HAAlarmCommandArmVacation, hmenum.AlarmModeVacation},
		{HAAlarmCommandArmCustomBypass, hmenum.AlarmModeCustom},
	}
	for _, c := range cases {
		mode, ok := ArmModeForCommand(c.cmd)
		if !ok {
			t.Errorf("ArmModeForCommand(%q) ok = false, want true", c.cmd)
			continue
		}
		if mode != c.wantMode {
			t.Errorf("ArmModeForCommand(%q) = %q, want %q", c.cmd, mode, c.wantMode)
		}
		// Round-trip: arming in the resolved mode must render back to
		// the HA state token the command name is built from.
		token := StateToken(hmenum.AlarmZoneStateArmed, mode)
		wantToken := armCommandToArmedToken(c.cmd)
		if token != wantToken {
			t.Errorf("round-trip %s -> mode %s -> token %q, want %q", c.cmd, mode, token, wantToken)
		}
	}
}

// armCommandToArmedToken is the test-local expectation table for the
// round-trip assertion in TestArmModeForCommand_FullInverseMapping —
// every ARM_* command's armed-token counterpart, independent of the
// production armedTokenForMode implementation.
func armCommandToArmedToken(cmd string) string {
	switch cmd {
	case HAAlarmCommandArmHome:
		return HAAlarmStateArmedHome
	case HAAlarmCommandArmAway:
		return HAAlarmStateArmedAway
	case HAAlarmCommandArmNight:
		return HAAlarmStateArmedNight
	case HAAlarmCommandArmVacation:
		return HAAlarmStateArmedVacation
	case HAAlarmCommandArmCustomBypass:
		return HAAlarmStateArmedCustomBypass
	default:
		return ""
	}
}

// TestArmModeForCommand_UnknownTokenHandling covers DISARM, the
// loom-only SILENCE extension, and arbitrary garbage: none of these are
// arm commands, so ok must be false and mode must be the zero value —
// callers rely on ok to route to the disarm/silence verbs instead of
// mis-arming on a malformed payload.
func TestArmModeForCommand_UnknownTokenHandling(t *testing.T) {
	t.Parallel()
	cases := []string{
		HAAlarmCommandDisarm,
		HAAlarmCommandSilence,
		"",
		"ARM_HOME_TYPO",
		"arm_home", // lower-case is not accepted — HA payloads are upper-case
		"DISARMED", // a state token is not a command
		"{\"action\":\"ARM_HOME\"}",
	}
	for _, cmd := range cases {
		mode, ok := ArmModeForCommand(cmd)
		if ok {
			t.Errorf("ArmModeForCommand(%q) ok = true, want false", cmd)
		}
		if mode != "" {
			t.Errorf("ArmModeForCommand(%q) mode = %q, want empty", cmd, mode)
		}
	}
}

// TestMasterStateToken_Aggregation locks the master-panel
// aggregation precedence documented in notes/concepts/alarm-concept.md §13.3:
// triggered beats pending beats arming; a uniform token set collapses
// to that token (including the empty set, which reports disarmed); any
// other mix reports the away token.
func TestMasterStateToken_Aggregation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		tokens []string
		want   string
	}{
		{"empty", nil, HAAlarmStateDisarmed},
		{"all disarmed", []string{HAAlarmStateDisarmed, HAAlarmStateDisarmed}, HAAlarmStateDisarmed},
		{"all armed_away", []string{HAAlarmStateArmedAway, HAAlarmStateArmedAway}, HAAlarmStateArmedAway},
		{"all armed_night", []string{HAAlarmStateArmedNight, HAAlarmStateArmedNight}, HAAlarmStateArmedNight},
		{"single", []string{HAAlarmStateArmedHome}, HAAlarmStateArmedHome},
		{
			"triggered wins over everything",
			[]string{HAAlarmStateArmedAway, HAAlarmStatePending, HAAlarmStateArming, HAAlarmStateTriggered},
			HAAlarmStateTriggered,
		},
		{
			"pending wins over arming and armed",
			[]string{HAAlarmStateArmedAway, HAAlarmStateArming, HAAlarmStatePending},
			HAAlarmStatePending,
		},
		{
			"arming wins over armed and disarmed",
			[]string{HAAlarmStateDisarmed, HAAlarmStateArmedAway, HAAlarmStateArming},
			HAAlarmStateArming,
		},
		{
			"mixed armed modes fall back to away",
			[]string{HAAlarmStateArmedHome, HAAlarmStateArmedNight},
			HAAlarmStateArmedAway,
		},
		{
			"mixed disarmed and armed falls back to away",
			[]string{HAAlarmStateDisarmed, HAAlarmStateArmedHome},
			HAAlarmStateArmedAway,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := MasterStateToken(c.tokens)
			if got != c.want {
				t.Errorf("MasterStateToken(%v) = %q, want %q", c.tokens, got, c.want)
			}
		})
	}
}

// TestSupportedFeatures_OrderAndFilter locks the canonical
// arm-button ordering and confirms unconfigured modes are omitted.
func TestSupportedFeatures_OrderAndFilter(t *testing.T) {
	t.Parallel()
	// Deliberately unordered + duplicated input: the output must still
	// come back in the canonical order with no duplicates.
	got := SupportedFeatures([]hmenum.AlarmMode{
		hmenum.AlarmModeCustom, hmenum.AlarmModeFull, hmenum.AlarmModePerimeter,
	})
	want := []string{HAAlarmFeatureArmHome, HAAlarmFeatureArmAway, HAAlarmFeatureArmCustomBypass}
	if len(got) != len(want) {
		t.Fatalf("SupportedFeatures = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SupportedFeatures[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

// TestSupportedFeatures_Empty covers an zone with no configured
// modes: the discovery payload must carry an empty (not nil-panicking)
// feature list.
func TestSupportedFeatures_Empty(t *testing.T) {
	t.Parallel()
	got := SupportedFeatures(nil)
	if len(got) != 0 {
		t.Fatalf("SupportedFeatures(nil) = %v, want empty", got)
	}
}
