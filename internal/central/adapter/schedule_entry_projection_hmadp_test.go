// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// TestHmAdpMQTTScheduleEntryCarriesTheColourFields couples the two projections
// of one [schedule.SimpleEntry]: the REST/WS DTO and the MQTT
// `schedule_data.entries` map published on the Zeitplan sensor's
// json_attributes topic.
//
// Both are fed from the same wire read through the same parser, so a field one
// carries and the other drops is a plain loss on the published plane. The three
// universal-light colour fields (HmIP-BSL, HmIP-RGBW: `<NN>_WP_COLOR` and
// friends) were present over REST and absent over MQTT, which makes a coloured
// switch point unrenderable from the retained attribute.
func TestHmAdpMQTTScheduleEntryCarriesTheColourFields(t *testing.T) {
	t.Parallel()

	colorType, colorValue, outputBehaviour := 1, 4200, 3
	e := schedule.SimpleEntry{
		Weekdays:        []schedule.Weekday{schedule.WeekdayMonday},
		Time:            "07:30",
		Level:           1,
		ColorType:       &colorType,
		ColorValue:      &colorValue,
		OutputBehaviour: &outputBehaviour,
	}

	got := simpleEntryJSON(e)
	for key, want := range map[string]any{
		"color_type":       colorType,
		"color_value":      colorValue,
		"output_behaviour": outputBehaviour,
	} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("MQTT entry has no %q key — the REST DTO carries it from the same domain struct", key)
		}
		if v != want {
			t.Fatalf("MQTT entry %q = %v, want %v", key, v, want)
		}
	}
}

// TestHmAdpMQTTScheduleEntryNullsAbsentColourFields pins the absent case in the
// payload's own idiom: an optional field that is not set is published as JSON
// null, as every other optional field in this map is.
func TestHmAdpMQTTScheduleEntryNullsAbsentColourFields(t *testing.T) {
	t.Parallel()

	got := simpleEntryJSON(schedule.SimpleEntry{Time: "06:00"})
	for _, key := range []string{"color_type", "color_value", "output_behaviour"} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("MQTT entry has no %q key", key)
		}
		if v != nil {
			t.Fatalf("MQTT entry %q = %v, want nil for an unset field", key, v)
		}
	}
}

// TestHmAdpRESTAndMQTTProjectionsAgreeOnTheColourFields is the coupling half:
// whatever the REST DTO reports for the colour fields, the MQTT map reports
// the same. It starts from one raw `<NN>_WP_*` MASTER paramset — the shape the
// CCU actually returns — and runs each plane's own production projection over
// it: [parseSimpleSchedule] for REST/WS (schedules.go), and
// [weekprofile.ParseSimpleRawParamset] + [simpleEntryJSON] for the MQTT
// json_attributes payload (eventbridge.go), which is the pair
// [simpleScheduleEntriesJSON] runs. Neither side is hand-built here, so
// dropping a field on either one turns this red.
func TestHmAdpRESTAndMQTTProjectionsAgreeOnTheColourFields(t *testing.T) {
	t.Parallel()

	// Weekday bit 2 = Tuesday; FIXED_HOUR/MINUTE 18:00; LEVEL 0.5.
	// The colour triple is what an HmIP-RGBW switch point carries.
	raw := map[string]any{
		"01_WP_WEEKDAY":      2,
		"01_WP_FIXED_HOUR":   18,
		"01_WP_FIXED_MINUTE": 0,
		"01_WP_LEVEL":        0.5,
		"01_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":  1,
		"01_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE": 4200,
		"01_WP_OUTPUT_BEHAVIOUR":                              3,
	}
	bits := weekprofile.TargetChannelBits{"1_1": 0}

	restEntries := parseSimpleSchedule(raw, bits)
	if len(restEntries) != 1 {
		t.Fatalf("REST projection produced %d entries, want 1 — the fixture no longer parses", len(restEntries))
	}
	rest := restEntries[0]

	domain, err := weekprofile.ParseSimpleRawParamset(raw, bits)
	if err != nil {
		t.Fatalf("ParseSimpleRawParamset: %v", err)
	}
	entry, ok := domain.Entries[rest.SlotNo]
	if !ok {
		t.Fatalf("MQTT side has no slot %d", rest.SlotNo)
	}
	mqtt := simpleEntryJSON(entry)

	// Both planes must carry the value; a plane that drops the field
	// publishes nil here, which is the divergence this test exists for.
	for key, restVal := range map[string]*int{
		"color_type":       rest.ColorType,
		"color_value":      rest.ColorValue,
		"output_behaviour": rest.OutputBehaviour,
	} {
		if restVal == nil {
			t.Fatalf("REST projection dropped %s — the paramset carries it", key)
		}
		if mqtt[key] != *restVal {
			t.Fatalf("REST reports %s=%d, MQTT reports %v — the two projections of one paramset disagree",
				key, *restVal, mqtt[key])
		}
	}
}
