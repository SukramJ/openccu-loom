// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"
)

// presetBody is the shape the climate custom data point emits: slugs,
// and the state template that reads the aggregate topic.
func presetBody(presets ...string) map[string]any {
	return map[string]any{
		"preset_modes":               presets,
		"preset_mode_state_topic":    "gh/ccu/HmIP-RF/0001ABCD/1/custom",
		"preset_mode_value_template": "{{ value_json.preset_mode }}",
		"preset_mode_command_topic":  "gh/ccu/HmIP-RF/0001ABCD/1/set_profile",
	}
}

// TestClimateWeekProgramPresetsAreTranslatedAndStandardOnesAreNot pins
// both halves of the rule, because each is wrong on its own.
//
// A thermostat's preset dropdown mixes two kinds of entry. Home
// Assistant defines `boost`, `eco`, `comfort` and `away` and renders
// them in the language of the HA instance. `week_program_1` … are ours;
// HA has never heard of them and prints the slug, so the operator sees
// `week_program_3` sitting next to translated neighbours.
//
// Translating everything here would fix the visible half and break the
// invisible one: the daemon's language would be frozen into a retained
// discovery payload, and an HA running in another language would lose
// the translation it already had.
func TestClimateWeekProgramPresetsAreTranslatedAndStandardOnesAreNot(t *testing.T) {
	t.Parallel()

	d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	d.Locale = "de"
	body := presetBody("boost", "week_program_1", "week_program_2")
	d.localiseClimatePresets(body)

	got, ok := body["preset_modes"].([]string)
	if !ok {
		t.Fatalf("preset_modes is %T, want []string", body["preset_modes"])
	}
	if len(got) != 3 {
		t.Fatalf("got %d presets, want 3: %v", len(got), got)
	}
	if got[0] != "boost" {
		t.Errorf("standard preset = %q, want the slug %q left for HA to translate in its own "+
			"language — replacing it freezes this daemon's locale into a retained payload",
			got[0], "boost")
	}
	for i, want := range []string{"Wochenprogramm 1", "Wochenprogramm 2"} {
		if got[i+1] != want {
			t.Errorf("week-program preset %d = %q, want %q", i+1, got[i+1], want)
		}
	}
}

// TestTranslatedClimatePresetsCarryBothTemplates guards the half that
// is invisible until an operator actually uses the dropdown.
//
// The state template has to map the slug the daemon publishes onto the
// label now in preset_modes, or HA reports a state that is not in the
// entity's own option list and logs it as invalid. The command template
// has to map back, or the label travels to `set_profile`, which knows
// only the slug and rejects it.
func TestTranslatedClimatePresetsCarryBothTemplates(t *testing.T) {
	t.Parallel()

	d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	d.Locale = "de"
	body := presetBody("boost", "week_program_1")
	d.localiseClimatePresets(body)

	state, _ := body["preset_mode_value_template"].(string)
	command, _ := body["preset_mode_command_template"].(string)

	for _, c := range []struct{ name, tpl, want string }{
		{"state maps the slug to the label", state, "'week_program_1': 'Wochenprogramm 1'"},
		{"state reads the aggregate field", state, "value_json.preset_mode"},
		{"command maps the label back", command, "'Wochenprogramm 1': 'week_program_1'"},
	} {
		if !strings.Contains(c.tpl, c.want) {
			t.Errorf("%s: %q not found in %q", c.name, c.want, c.tpl)
		}
	}
	// The standard preset must survive the round trip untouched, in both
	// directions — it is in the option list as its slug.
	if !strings.Contains(state, "'boost': 'boost'") {
		t.Errorf("the state template drops the untranslated preset: %q", state)
	}
}

// TestClimatePresetsWithoutAWeekProgramAreLeftAlone keeps the change
// from touching entities it has no business touching: a thermostat whose
// presets HA already translates keeps the payload it had, templates
// included.
func TestClimatePresetsWithoutAWeekProgramAreLeftAlone(t *testing.T) {
	t.Parallel()

	d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	d.Locale = "de"
	body := presetBody("boost", "eco")
	d.localiseClimatePresets(body)

	if got, _ := body["preset_modes"].([]string); len(got) != 2 || got[0] != "boost" || got[1] != "eco" {
		t.Errorf("preset_modes = %v, want the slugs unchanged", body["preset_modes"])
	}
	if tpl, _ := body["preset_mode_value_template"].(string); tpl != "{{ value_json.preset_mode }}" {
		t.Errorf("value template = %q, want the original; nothing needed mapping", tpl)
	}
	if _, present := body["preset_mode_command_template"]; present {
		t.Error("a command template was added although no preset was translated")
	}
}

// TestAWeekProgramSuffixThatIsNotANumberIsLeftAlone pins the guard on
// the prefix match. A future `week_program_custom` would otherwise
// render as the literal "Wochenprogramm {n}".
func TestAWeekProgramSuffixThatIsNotANumberIsLeftAlone(t *testing.T) {
	t.Parallel()

	if n, ok := weekProgramNumber("week_program_custom"); ok {
		t.Errorf("week_program_custom parsed as slot %d", n)
	}
	if n, ok := weekProgramNumber("week_program_0"); ok {
		t.Errorf("week_program_0 parsed as slot %d; the slots are 1-based", n)
	}
	if n, ok := weekProgramNumber("week_program_3"); !ok || n != 3 {
		t.Errorf("week_program_3 → (%d, %v), want (3, true)", n, ok)
	}
}

// TestBuildTranslatesWeekProgramPresets pins the wiring, not the helper.
//
// The three tests above call localiseClimatePresets directly, which
// proves it works and says nothing about whether the discovery path
// reaches it. This one goes through Build — the method the publisher
// calls — and reads the emitted payload, so removing the call site
// fails here rather than passing quietly.
func TestBuildTranslatesWeekProgramPresets(t *testing.T) {
	t.Parallel()

	src := &stubBuilder{
		component: "climate",
		body: map[string]any{
			"min_temp":                   5.0,
			"max_temp":                   30.5,
			"preset_modes":               []string{"boost", "week_program_1"},
			"preset_mode_state_topic":    "gh/ccu-01/HmIP-RF/0001ABCD/1/custom",
			"preset_mode_value_template": "{{ value_json.preset_mode }}",
			"preset_mode_command_topic":  "gh/ccu-01/HmIP-RF/0001ABCD/1/set_profile",
		},
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	db.Locale = "de"
	ev := Event{
		Source:         src,
		Central:        "ccu-01",
		Interface:      "HmIP-RF",
		DeviceAddress:  "0001ABCD",
		ChannelNo:      1,
		ChannelAddress: "0001ABCD:1",
		Model:          "HmIP-BWTH",
		ChannelType:    "CLIMATECONTROL_RT_TRANSCEIVER",
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	presets, _ := body["preset_modes"].([]any)
	if len(presets) != 2 {
		t.Fatalf("preset_modes = %v, want two entries", body["preset_modes"])
	}
	if presets[0] != "boost" {
		t.Errorf("standard preset = %v, want the slug left alone", presets[0])
	}
	if presets[1] != "Wochenprogramm 1" {
		t.Errorf("week-program preset = %v, want it translated on the way out — the operator sees "+
			"whatever this payload says", presets[1])
	}
	if _, present := body["preset_mode_command_template"]; !present {
		t.Error("no command template in the emitted payload; the label would travel to set_profile")
	}
}
