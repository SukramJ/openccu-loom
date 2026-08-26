// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// weekProgramPresetPrefix is the domain prefix of the week-program
// presets a thermostat exposes (`week_program_1` … `week_program_6`).
const weekProgramPresetPrefix = "week_program_"

// localiseClimatePresets translates the preset list of a climate entity
// — but only the entries Home Assistant cannot translate itself.
//
// HA ships translations for the presets its climate component defines
// (`boost`, `eco`, `comfort`, `away`, …) and renders them in the
// language of the HA instance, which is not necessarily the daemon's.
// Those are left as slugs on purpose: replacing them with labels from
// this daemon's catalogue would take that translation away and freeze
// one language into a retained discovery payload.
//
// `week_program_1` … `week_program_6` are ours. HA has never heard of
// them, so it prints them verbatim and the operator gets a dropdown of
// `week_program_3` next to translated neighbours.
//
// A translated list needs both templates. Without the value template the
// entity reports a state that is not in its own option list and HA logs
// it as invalid; without the command template the label travels back to
// `set_profile`, which knows only the slug.
func (d *DefaultDiscoveryBuilder) localiseClimatePresets(body map[string]any) {
	raw, ok := presetModeSlugs(body["preset_modes"])
	if !ok || len(raw) == 0 {
		return
	}
	labels := make([]string, len(raw))
	translated := false
	for i, slug := range raw {
		label, did := d.presetLabel(slug)
		labels[i] = label
		translated = translated || did
	}
	if !translated {
		return
	}
	body["preset_modes"] = labels
	state, command := presetModeTemplates(raw, labels)
	body["preset_mode_value_template"] = state
	body["preset_mode_command_template"] = command
}

// presetLabel returns the display label for one preset slug and whether
// it was translated at all.
func (d *DefaultDiscoveryBuilder) presetLabel(slug string) (label string, translated bool) {
	n, ok := weekProgramNumber(slug)
	if !ok {
		return slug, false
	}
	return strings.Replace(d.tr("climate.preset.week_program"), "{n}", strconv.Itoa(n), 1), true
}

// weekProgramNumber extracts the 1-based slot of a week-program preset.
// A prefix match alone is not enough: the suffix has to be a number, or
// a future `week_program_custom` would render as "Week program {n}".
func weekProgramNumber(slug string) (int, bool) {
	rest, found := strings.CutPrefix(slug, weekProgramPresetPrefix)
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// presetModeSlugs reads the preset list back out of the body. The
// climate builder writes a []string, but the body travels through
// generic map handling, so the []any form is accepted too rather than
// silently skipping the translation.
func presetModeSlugs(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		return list, true
	case []hmenum.ClimateProfile:
		out := make([]string, len(list))
		for i, p := range list {
			out[i] = string(p)
		}
		return out, true
	case []any:
		out := make([]string, 0, len(list))
		for _, e := range list {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// presetModeTemplates renders the state and command templates that map
// between the slugs the domain speaks and the labels HA shows.
//
// Both fall back to the incoming value when the map misses, so a preset
// that appears after this payload was retained still passes through
// instead of resolving to nothing.
func presetModeTemplates(slugs, labels []string) (valueTemplate, commandTemplate string) {
	var state, command strings.Builder
	state.WriteString(`{% set m = {`)
	command.WriteString(`{% set m = {`)
	for i, slug := range slugs {
		if i > 0 {
			state.WriteString(", ")
			command.WriteString(", ")
		}
		state.WriteString(jinjaQuote(slug) + ": " + jinjaQuote(labels[i]))
		command.WriteString(jinjaQuote(labels[i]) + ": " + jinjaQuote(slug))
	}
	state.WriteString(`} %}{% if value_json is defined and value_json.preset_mode is not none %}` +
		`{{ m.get(value_json.preset_mode, value_json.preset_mode) }}{% endif %}`)
	command.WriteString(`} %}{{ m.get(value, value) }}`)
	return state.String(), command.String()
}

// applySelectionLabels replaces the discovery-body lists a custom data
// point declared as localisable with the labels the event carries.
//
// Length equality is the guard: the labels are index-aligned with the
// VALUE_LIST, and a body list of a different length is a list the
// builder filtered or reordered. Substituting there would silently
// rename entries, which is worse than leaving them as tokens.
func applySelectionLabels(body map[string]any, labels map[string][]string) {
	for key, localised := range labels {
		current, ok := presetModeSlugs(body[key])
		if !ok || len(current) != len(localised) {
			continue
		}
		body[key] = append([]string(nil), localised...)
	}
}
