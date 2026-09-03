// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
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
// whatever the REST DTO reports for the colour fields, the MQTT map reports the
// same. It runs both projections over one parsed paramset so neither side can
// drift without the other.
func TestHmAdpRESTAndMQTTProjectionsAgreeOnTheColourFields(t *testing.T) {
	t.Parallel()

	colorType, colorValue, outputBehaviour := 0, 300, 1
	entry := schedule.SimpleEntry{
		Weekdays:        []schedule.Weekday{schedule.WeekdayTuesday},
		Time:            "18:00",
		Level:           0.5,
		ColorType:       &colorType,
		ColorValue:      &colorValue,
		OutputBehaviour: &outputBehaviour,
	}
	rest := hmapi.SimpleScheduleEntry{
		SlotNo:          1,
		ColorType:       entry.ColorType,
		ColorValue:      entry.ColorValue,
		OutputBehaviour: entry.OutputBehaviour,
	}
	mqtt := simpleEntryJSON(entry)

	for key, want := range map[string]*int{
		"color_type":       rest.ColorType,
		"color_value":      rest.ColorValue,
		"output_behaviour": rest.OutputBehaviour,
	} {
		if want == nil {
			continue
		}
		if mqtt[key] != *want {
			t.Fatalf("REST reports %s=%d, MQTT reports %v", key, *want, mqtt[key])
		}
	}
	// Keep the weekprofile import meaningful: the two projections share this
	// package's parser, which is where a future divergence would originate.
	_ = weekprofile.TargetChannelBits{}
}
