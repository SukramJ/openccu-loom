// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

func ptrInt(v int) *int { return &v }

const (
	wpColorType  = "_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"
	wpColorValue = "_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"
)

// TestParseSimpleSchedule_ColorPreserved asserts the colour/effect fields
// are decoded (including a legitimate 0 value) onto the switch point.
func TestParseSimpleSchedule_ColorPreserved(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"03_WP_WEEKDAY":      2,
		"03_WP_FIXED_HOUR":   7,
		"03_WP_FIXED_MINUTE": 30,
		"03_WP_LEVEL":        1.0,
		"03" + wpColorType:   2,      // effect
		"03" + wpColorValue:  524288, // packed value
		// A second slot whose colour value is a legitimate 0.
		"04_WP_WEEKDAY":      2,
		"04_WP_FIXED_HOUR":   8,
		"04_WP_FIXED_MINUTE": 0,
		"04_WP_LEVEL":        0.5,
		"04" + wpColorType:   0,
		"04" + wpColorValue:  0,
	}
	entries := parseSimpleSchedule(raw, nil)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	e3 := entries[0]
	if e3.ColorType == nil || *e3.ColorType != 2 || e3.ColorValue == nil || *e3.ColorValue != 524288 {
		t.Errorf("slot 3 colour not decoded: type=%v value=%v", e3.ColorType, e3.ColorValue)
	}
	e4 := entries[1]
	if e4.ColorValue == nil || *e4.ColorValue != 0 {
		t.Errorf("slot 4 colour value 0 must be preserved, got %v", e4.ColorValue)
	}
}

// TestSerializeSimpleSchedule_ColorGluedToSlot asserts colour is emitted on
// the entry's CURRENT slot (so it survives reorder/insert/delete) and that a
// non-colour entry emits no colour key.
func TestSerializeSimpleSchedule_ColorGluedToSlot(t *testing.T) {
	t.Parallel()
	entries := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:     5, // moved from its original slot
			Weekdays:   []string{"MONDAY"},
			Time:       "07:30",
			Level:      1.0,
			ColorType:  ptrInt(2),
			ColorValue: ptrInt(524288),
		},
		{
			SlotNo:   6,
			Weekdays: []string{"MONDAY"},
			Time:     "08:00",
			Level:    0.5,
		},
	}
	raw, err := serializeSimpleSchedule(entries, schedule.SimpleMaxSlot, nil)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	// Colour emitted on slot 5 (the entry's current slot).
	if raw["05"+wpColorType] != 2 || raw["05"+wpColorValue] != 524288 {
		t.Errorf("colour not glued to slot 5: %v / %v", raw["05"+wpColorType], raw["05"+wpColorValue])
	}
	// The non-colour entry (slot 6) must emit no colour key.
	if _, ok := raw["06"+wpColorType]; ok {
		t.Error("non-colour entry must not emit a colour key")
	}
}

// TestSerializeSimpleSchedule_ColorZeroEmitted asserts a colour value of 0
// (legitimate) is written, not dropped.
func TestSerializeSimpleSchedule_ColorZeroEmitted(t *testing.T) {
	t.Parallel()
	raw, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{{
		SlotNo:     1,
		Weekdays:   []string{"MONDAY"},
		Time:       "06:00",
		Level:      1.0,
		ColorType:  ptrInt(0),
		ColorValue: ptrInt(0),
	}}, schedule.SimpleMaxSlot, nil)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	v, ok := raw["01"+wpColorValue]
	if !ok || v != 0 {
		t.Errorf("colour value 0 must be emitted, got ok=%v v=%v", ok, v)
	}
}

// TestSimpleSchedule_ColorRoundTripAcrossReorder is the core regression:
// parse a colour switch point, move it to another slot, serialize — the
// colour follows the entry, and its original slot no longer carries it.
func TestSimpleSchedule_ColorRoundTripAcrossReorder(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"03_WP_WEEKDAY":      2,
		"03_WP_FIXED_HOUR":   7,
		"03_WP_FIXED_MINUTE": 30,
		"03_WP_LEVEL":        1.0,
		"03" + wpColorType:   1,
		"03" + wpColorValue:  4200,
	}
	entries := parseSimpleSchedule(raw, nil)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Relocate the switch point to slot 7.
	entries[0].SlotNo = 7
	out, err := serializeSimpleSchedule(entries, schedule.SimpleMaxSlot, nil)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if out["07"+wpColorType] != 1 || out["07"+wpColorValue] != 4200 {
		t.Errorf("colour did not follow the entry to slot 7: %v / %v",
			out["07"+wpColorType], out["07"+wpColorValue])
	}
	// The old slot 3 is now deactivated (no colour re-emitted there).
	if _, ok := out["03"+wpColorType]; ok {
		t.Error("colour must not linger on the vacated slot 3")
	}
}

func TestHasColorScheduleParams(t *testing.T) {
	t.Parallel()
	if !hasColorScheduleParams(map[string]any{"03" + wpColorType: 0}) {
		t.Error("must detect a colour field")
	}
	if !hasColorScheduleParams(map[string]any{"01_WP_OUTPUT_BEHAVIOUR": 5}) {
		t.Error("must detect an OUTPUT_BEHAVIOUR field")
	}
	if hasColorScheduleParams(map[string]any{"01_WP_LEVEL": 1.0, "01_WP_WEEKDAY": 2}) {
		t.Error("must not flag a plain switch schedule as colour-capable")
	}
}
