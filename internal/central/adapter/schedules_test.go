// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	schedulemodel "github.com/SukramJ/openccu-loom/internal/model/schedule"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Minimal CCU Paramset fixture that mirrors the
// example: P1 for Monday has a 18°C base with a 06:00–22:00 heated
// stretch at 21°C. The remaining slots (5..13) pad to 24:00 with the
// base temperature. Field shape matches the wire (ENDTIME in minutes,
// TEMPERATURE as float).
func fixtureSimpleMondayP1() map[string]any {
	return map[string]any{
		"P1_ENDTIME_MONDAY_1":     360, // 06:00
		"P1_TEMPERATURE_MONDAY_1": 18.0,
		"P1_ENDTIME_MONDAY_2":     1320, // 22:00
		"P1_TEMPERATURE_MONDAY_2": 21.0,
		"P1_ENDTIME_MONDAY_3":     1440, // 24:00
		"P1_TEMPERATURE_MONDAY_3": 18.0,
		// Padding slots mimic what
		"P1_ENDTIME_MONDAY_4":     1440,
		"P1_TEMPERATURE_MONDAY_4": 18.0,
		"P1_ENDTIME_MONDAY_5":     1440,
		"P1_TEMPERATURE_MONDAY_5": 18.0,
		"P1_ENDTIME_MONDAY_6":     1440,
		"P1_TEMPERATURE_MONDAY_6": 18.0,
		// Other unrelated MASTER keys must not break parsing.
		"TEMPERATUREFALL_MODUS": 2,
		"GLOBAL_BUTTON_LOCK":    false,
	}
}

func TestParseClimateScheduleSimple(t *testing.T) {
	t.Parallel()
	got, err := parseClimateSchedule(fixtureSimpleMondayP1())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p1, ok := got.Profiles["P1"]
	if !ok {
		t.Fatalf("P1 missing in %+v", got.Profiles)
	}
	monday, ok := p1.Weekdays["MONDAY"]
	if !ok {
		t.Fatalf("MONDAY missing")
	}
	// The fixture has 16h at 21°C vs. 8h at 18°C, so 21°C wins and the two 18°C
	// bookends become explicit periods.
	if math.Abs(monday.BaseTemperature-21.0) > 1e-6 {
		t.Errorf("base temp: got %v, want 21.0", monday.BaseTemperature)
	}
	if len(monday.Periods) != 2 {
		t.Fatalf("expected 2 periods, got %d: %+v", len(monday.Periods), monday.Periods)
	}
	want := []hmapi.ClimatePeriod{
		{StartTime: "00:00", EndTime: "06:00", Temperature: 18.0},
		{StartTime: "22:00", EndTime: "24:00", Temperature: 18.0},
	}
	if !reflect.DeepEqual(monday.Periods, want) {
		t.Errorf("periods mismatch:\n  got  %+v\n  want %+v", monday.Periods, want)
	}
}

func TestParseClimateScheduleMissingRaisesSentinel(t *testing.T) {
	t.Parallel()
	_, err := parseClimateSchedule(map[string]any{"UNRELATED": 1})
	if !errors.Is(err, ErrNoSchedule) {
		t.Errorf("got %v, want ErrNoSchedule", err)
	}
}

func TestSerializeClimateScheduleRoundTrip(t *testing.T) {
	t.Parallel()
	// 18°C base with two heated stretches (morning + evening).
	schedule := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {
						BaseTemperature: 18.0,
						Periods: []hmapi.ClimatePeriod{
							{StartTime: "06:00", EndTime: "08:00", Temperature: 21.0},
							{StartTime: "17:00", EndTime: "22:00", Temperature: 21.0},
						},
					},
				},
			},
		},
	}
	raw, err := serializeClimateSchedule(schedule)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	// Re-parse must recover the simple form exactly.
	back, err := parseClimateSchedule(raw)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	gotDay := back.Profiles["P1"].Weekdays["MONDAY"]
	wantDay := schedule.Profiles["P1"].Weekdays["MONDAY"]
	if !reflect.DeepEqual(gotDay, wantDay) {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", gotDay, wantDay)
	}
}

func TestSerializeClimateScheduleEmitsThirteenSlots(t *testing.T) {
	t.Parallel()
	// A weekday with no periods still yields 13 slots (all ending at
	// 24:00 with the base temperature) so the CCU's fixed paramset
	// shape is satisfied.
	schedule := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {BaseTemperature: 19.0, Periods: nil},
				},
			},
		},
	}
	raw, err := serializeClimateSchedule(schedule)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	for i := 1; i <= 13; i++ {
		if _, ok := raw[key(i, "ENDTIME", "MONDAY")]; !ok {
			t.Errorf("missing endtime slot %d", i)
		}
		if _, ok := raw[key(i, "TEMPERATURE", "MONDAY")]; !ok {
			t.Errorf("missing temperature slot %d", i)
		}
	}
	if raw["P1_ENDTIME_MONDAY_13"] != 1440 {
		t.Errorf("last slot endtime: got %v, want 1440", raw["P1_ENDTIME_MONDAY_13"])
	}
}

func TestSerializeClimateScheduleRejectsOverlap(t *testing.T) {
	t.Parallel()
	schedule := &hmapi.ClimateSchedule{
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {
						BaseTemperature: 18.0,
						Periods: []hmapi.ClimatePeriod{
							{StartTime: "06:00", EndTime: "10:00", Temperature: 21.0},
							{StartTime: "09:00", EndTime: "11:00", Temperature: 22.0},
						},
					},
				},
			},
		},
	}
	if _, err := serializeClimateSchedule(schedule); err == nil {
		t.Errorf("expected overlap error, got nil")
	}
}

// fixtureSimpleScheduleHmIPPSM mirrors the WEEK_PROFILE paramset of
// a HmIP-PSM with two active slots: weekdays 06:30 → ON, weekdays
// 22:00 → OFF. Other slots are zeroed (deactivated).
func fixtureSimpleScheduleHmIPPSM() map[string]any {
	weekdays := (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 5) // Mo–Fr
	return map[string]any{
		"01_WP_WEEKDAY":      weekdays,
		"01_WP_FIXED_HOUR":   6,
		"01_WP_FIXED_MINUTE": 30,
		"01_WP_LEVEL":        1.0,
		"02_WP_WEEKDAY":      weekdays,
		"02_WP_FIXED_HOUR":   22,
		"02_WP_FIXED_MINUTE": 0,
		"02_WP_LEVEL":        0.0,
		// inactive slot — must be filtered out
		"03_WP_WEEKDAY":      0,
		"03_WP_FIXED_HOUR":   0,
		"03_WP_FIXED_MINUTE": 0,
		"03_WP_LEVEL":        0.0,
		// unrelated MASTER keys must not break parsing
		"GLOBAL_BUTTON_LOCK": false,
	}
}

func TestParseSimpleScheduleActiveSlots(t *testing.T) {
	t.Parallel()
	entries := parseSimpleSchedule(fixtureSimpleScheduleHmIPPSM())
	if len(entries) != 2 {
		t.Fatalf("expected 2 active slots, got %d: %+v", len(entries), entries)
	}
	if entries[0].SlotNo != 1 || entries[0].Time != "06:30" || entries[0].Level != 1.0 {
		t.Errorf("slot 1 mismatch: %+v", entries[0])
	}
	wantDays := []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY"}
	if !reflect.DeepEqual(entries[0].Weekdays, wantDays) {
		t.Errorf("slot 1 weekdays: got %v, want %v", entries[0].Weekdays, wantDays)
	}
	if entries[1].SlotNo != 2 || entries[1].Time != "22:00" || entries[1].Level != 0.0 {
		t.Errorf("slot 2 mismatch: %+v", entries[1])
	}
}

func TestSerializeSimpleScheduleZeroesUnusedSlots(t *testing.T) {
	t.Parallel()
	raw, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{
		{
			SlotNo:   1,
			Weekdays: []string{"MONDAY", "WEDNESDAY"},
			Time:     "07:30",
			Level:    1,
		},
	}, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	want := (1 << 1) | (1 << 3) // Mo + We
	if raw["01_WP_WEEKDAY"] != want {
		t.Errorf("slot 1 weekday bits: got %v, want %d", raw["01_WP_WEEKDAY"], want)
	}
	if raw["01_WP_FIXED_HOUR"] != 7 || raw["01_WP_FIXED_MINUTE"] != 30 {
		t.Errorf("slot 1 time: got %v:%v", raw["01_WP_FIXED_HOUR"], raw["01_WP_FIXED_MINUTE"])
	}
	// Slots 2..24 must be deactivated.
	for i := 2; i <= 24; i++ {
		key := fmt.Sprintf("%02d_WP_WEEKDAY", i)
		if raw[key] != 0 {
			t.Errorf("slot %d should be zeroed; got %v", i, raw[key])
		}
	}
}

func TestSerializeSimpleScheduleRoundTrip(t *testing.T) {
	t.Parallel()
	in := []hmapi.SimpleScheduleEntry{
		{SlotNo: 1, Weekdays: []string{"MONDAY", "TUESDAY"}, Time: "06:30", Level: 1, Condition: "fixed_time"},
		{SlotNo: 2, Weekdays: []string{"SATURDAY", "SUNDAY"}, Time: "08:00", Level: 0.5, Condition: "fixed_time"},
	}
	raw, err := serializeSimpleSchedule(in, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	out := parseSimpleSchedule(raw)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", out, in)
	}
}

func TestSerializeSimpleScheduleAstroAndDuration(t *testing.T) {
	t.Parallel()
	in := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:             1,
			Weekdays:           []string{"MONDAY"},
			Time:               "20:00",
			Condition:          "astro_if_after_fixed",
			AstroType:          "sunset",
			AstroOffsetMinutes: -15,
			TargetChannels:     []string{"1_1", "2_1"},
			Level:              1,
			Duration:           "10s",
			RampTime:           "500ms",
		},
	}
	raw, err := serializeSimpleSchedule(in, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if raw["01_WP_CONDITION"] != 5 {
		t.Errorf("CONDITION wire id: got %v, want 5 (astro_if_after_fixed)", raw["01_WP_CONDITION"])
	}
	if raw["01_WP_ASTRO_TYPE"] != 1 {
		t.Errorf("ASTRO_TYPE: got %v, want 1 (sunset)", raw["01_WP_ASTRO_TYPE"])
	}
	if raw["01_WP_ASTRO_OFFSET"] != -15 {
		t.Errorf("ASTRO_OFFSET: got %v, want -15", raw["01_WP_ASTRO_OFFSET"])
	}
	// Target channels: 1_1 = bit 0 (=1), 2_1 = bit 3 (=8) → 9
	if raw["01_WP_TARGET_CHANNELS"] != 9 {
		t.Errorf("TARGET_CHANNELS: got %v, want 9", raw["01_WP_TARGET_CHANNELS"])
	}
	// Duration "10s" → base SEC_10 (3) × factor 1 OR base SEC_1 (1) × factor 10. Heuristic picks largest.
	dBase := raw["01_WP_DURATION_BASE"].(int)
	dFactor := raw["01_WP_DURATION_FACTOR"].(int)
	gotDuration := weekprofile.FormatTimeBaseFactor(dBase, dFactor)
	if gotDuration != "10s" {
		t.Errorf("DURATION_BASE/FACTOR encodes %q, want %q", gotDuration, "10s")
	}
	// Ramp 500ms → base MS_100 (0) × factor 5
	if raw["01_WP_RAMP_TIME_BASE"] != 0 || raw["01_WP_RAMP_TIME_FACTOR"] != 5 {
		t.Errorf("RAMP_TIME: got base=%v, factor=%v; want 0/5",
			raw["01_WP_RAMP_TIME_BASE"], raw["01_WP_RAMP_TIME_FACTOR"])
	}
	// Re-parse should recover everything.
	out := parseSimpleSchedule(raw)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	got := out[0]
	if got.Condition != "astro_if_after_fixed" || got.AstroType != "sunset" || got.AstroOffsetMinutes != -15 {
		t.Errorf("trigger fields lost: %+v", got)
	}
	if !reflect.DeepEqual(got.TargetChannels, []string{"1_1", "2_1"}) {
		t.Errorf("target channels lost: %v", got.TargetChannels)
	}
	if got.Duration == "" {
		t.Errorf("Duration lost")
	}
	if got.RampTime != "500ms" {
		t.Errorf("RampTime: got %q, want 500ms", got.RampTime)
	}
}

func TestIsWeekProfileChannelMatchesHmIPSuffix(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"WEEK_PROFILE":         true,
		"SWITCH_WEEK_PROFILE":  true,
		"HEATING_WEEK_PROFILE": true,
		"COVER_WEEK_PROFILE":   true,
		"WEEK_PROFILE_2":       false,
		"WEEK_PROGRAM_CHANNEL": false,
		"SWITCH_TRANSCEIVER":   false,
	}
	for typ, want := range cases {
		if got := isWeekProfileChannel(typ); got != want {
			t.Errorf("isWeekProfileChannel(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestSimplifyWeekdayTieBreakPrefersLowerBase(t *testing.T) {
	t.Parallel()
	// Constructed equal-weight day: 12h at 18°C, 12h at 21°C.
	slots := map[int]*slotVals{
		1: {endtime: 720, temperature: 18.0, hasEnd: true, hasTemp: true},
		2: {endtime: 1440, temperature: 21.0, hasEnd: true, hasTemp: true},
	}
	wd := simplifyWeekday(slots)
	if math.Abs(wd.BaseTemperature-18.0) > 1e-6 {
		t.Errorf("base: got %v, want 18 (lower-temp tie-break)", wd.BaseTemperature)
	}
	if len(wd.Periods) != 1 || wd.Periods[0].Temperature != 21 {
		t.Errorf("expected one 21° period, got %+v", wd.Periods)
	}
}

func key(slot int, field, weekday string) string {
	return "P1_" + field + "_" + weekday + "_" + itoa(slot)
}

func itoa(n int) string {
	// Tiny helper to avoid importing strconv for a single call in the
	// tests; matches the production helper's semantics.
	if n < 10 {
		return string(rune('0' + n)) //nolint:gosec // G115: n is 0..9; '0'+n is 48..57, well within valid rune range
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10)) //nolint:gosec // G115: n is 10..99; each digit is 0..9 so '0'+digit is 48..57
}

// ============================================================
// detectScheduleDomain — device-found paths
// ============================================================

func addDeviceWithChannelType(reg *central.Registry, devAddr, chAddr, chType string) {
	c, err := central.New(central.Config{Name: "ccu-sched"})
	if err != nil {
		panic("central.New: " + err.Error())
	}
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: devAddr, InterfaceID: "HmIP-RF", Model: "TestModel"})
	dev.AddChannel(chAddr, 1, chType, hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)
}

func TestDetectScheduleDomainWithSwitchWeekProfile(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	addDeviceWithChannelType(reg, "DEV001", "DEV001:1", "SWITCH_WEEK_PROFILE")
	s := NewSchedulesDomain(reg, nil)
	got := s.detectScheduleDomain("DEV001", 1)
	if got != "switch" {
		t.Errorf("detectScheduleDomain SWITCH_WEEK_PROFILE = %q, want switch", got)
	}
}

func TestDetectScheduleDomainFallbackActorType(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	// Use a non-week-profile type → falls through to actor type scan.
	addDeviceWithChannelType(reg, "DEV002", "DEV002:1", "SWITCH_VIRTUAL_RECEIVER")
	s := NewSchedulesDomain(reg, nil)
	got := s.detectScheduleDomain("DEV002", 99) // scheduleChannelNo=99 → no channel at that number
	// The actor scan will find SWITCH_VIRTUAL_RECEIVER which starts with "SWITCH"
	if got != "switch" {
		t.Errorf("detectScheduleDomain actor fallback = %q, want switch", got)
	}
}

func TestDetectScheduleDomainNoMatchingChannel(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	// Use a type that matches neither week profile nor actor.
	addDeviceWithChannelType(reg, "DEV003", "DEV003:1", "UNKNOWN_TYPE_XYZ")
	s := NewSchedulesDomain(reg, nil)
	got := s.detectScheduleDomain("DEV003", 99)
	if got != "" {
		t.Errorf("detectScheduleDomain no match = %q, want empty", got)
	}
}

// ============================================================
// serializeSimpleSchedule — error branches
// ============================================================

// baseEntry creates a minimal valid SimpleScheduleEntry for slot 1.
func baseEntry(slot int) hmapi.SimpleScheduleEntry {
	return hmapi.SimpleScheduleEntry{
		SlotNo:   slot,
		Weekdays: []string{"MONDAY"},
		Time:     "08:00",
		Level:    0.5,
	}
}

func TestSerializeSimpleScheduleSlotOutOfRange(t *testing.T) {
	t.Parallel()
	e := baseEntry(0) // slot 0 is out of range
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("slot 0 must error")
	}
}

func TestSerializeSimpleScheduleSlotTooHigh(t *testing.T) {
	t.Parallel()
	// Slot 25 used to be the first rejected one, which made every
	// schedule the CCU holds past that point readable but unsavable —
	// the device declares up to schedulemodel.SimpleMaxSlot groups and edits
	// all of them. Only past the model's limit is out of range.
	if _, err := serializeSimpleSchedule(
		[]hmapi.SimpleScheduleEntry{baseEntry(25)}, schedulemodel.SimpleMaxSlot,
	); err != nil {
		t.Errorf("slot 25 must serialize: %v", err)
	}
	if _, err := serializeSimpleSchedule(
		[]hmapi.SimpleScheduleEntry{baseEntry(schedulemodel.SimpleMaxSlot + 1)}, schedulemodel.SimpleMaxSlot,
	); err == nil {
		t.Errorf("slot %d must error", schedulemodel.SimpleMaxSlot+1)
	}
}

func TestSerializeSimpleScheduleDuplicateSlot(t *testing.T) {
	t.Parallel()
	e1 := baseEntry(1)
	e2 := baseEntry(1)
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e1, e2}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("duplicate slot must error")
	}
}

func TestSerializeSimpleScheduleNoWeekday(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.Weekdays = nil // empty weekday list
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("no weekday must error")
	}
}

func TestSerializeSimpleScheduleInvalidTime(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.Time = "not-a-time"
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("invalid time must error")
	}
}

func TestSerializeSimpleScheduleUnknownCondition(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.Condition = "NOT_A_REAL_CONDITION_X99"
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("unknown condition must error")
	}
}

func TestSerializeSimpleScheduleUnknownAstroType(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.AstroType = "moonrise" // not sunrise or sunset
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("unknown astro_type must error")
	}
}

func TestSerializeSimpleScheduleAstroOffsetOutOfRange(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.AstroOffsetMinutes = 800 // > 720
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("astro offset > 720 must error")
	}
}

func TestSerializeSimpleScheduleAstroOffsetNegativeOutOfRange(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.AstroOffsetMinutes = -800 // < -720
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("astro offset < -720 must error")
	}
}

func TestSerializeSimpleScheduleSunsetAstroType(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.AstroType = "sunset"
	out, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("sunset astro_type: %v", err)
	}
	if out["01_WP_ASTRO_TYPE"] != 1 {
		t.Errorf("01_WP_ASTRO_TYPE = %v, want 1", out["01_WP_ASTRO_TYPE"])
	}
}

func TestSerializeSimpleScheduleLevel2NonNil(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	level2 := 0.75
	e.Level2 = &level2
	out, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("Level2 non-nil: %v", err)
	}
	if out["01_WP_LEVEL_2"] != level2 {
		t.Errorf("01_WP_LEVEL_2 = %v, want %v", out["01_WP_LEVEL_2"], level2)
	}
}

func TestSerializeSimpleScheduleInvalidDuration(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.Duration = "not-a-duration"
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("invalid duration must error")
	}
}

func TestSerializeSimpleScheduleInvalidRampTime(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.RampTime = "not-a-ramp"
	_, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err == nil {
		t.Error("invalid ramp_time must error")
	}
}

func TestSerializeSimpleScheduleValidRampTime(t *testing.T) {
	t.Parallel()
	e := baseEntry(1)
	e.RampTime = "10s"
	out, err := serializeSimpleSchedule([]hmapi.SimpleScheduleEntry{e}, schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("valid ramp_time: %v", err)
	}
	// The RAMP_TIME_FACTOR must be non-zero.
	if out["01_WP_RAMP_TIME_FACTOR"] == 0 {
		t.Error("01_WP_RAMP_TIME_FACTOR should be non-zero for 10s")
	}
}

// ============================================================
// weekprofile.ParseTimeBaseFactor — bare-number path and "m" suffix
// ============================================================

func TestParseTimeBaseFactorBareNumber(t *testing.T) {
	t.Parallel()
	// "60" (bare seconds) → ok
	_, _, ok := weekprofile.ParseTimeBaseFactor("60")
	if !ok {
		t.Error("ParseTimeBaseFactor bare number 60 must succeed")
	}
}

func TestParseTimeBaseFactorMinuteSuffix(t *testing.T) {
	t.Parallel()
	// "5m" → 5 × 60 = 300 seconds
	_, _, ok := weekprofile.ParseTimeBaseFactor("5m")
	if !ok {
		t.Error("ParseTimeBaseFactor 5m must succeed")
	}
}

func TestParseTimeBaseFactorZeroSeconds(t *testing.T) {
	t.Parallel()
	// A zero duration is a pair the CCU holds — (MS_100, 0), the door
	// lock's `lock_autorelock_start` encoding — not an absent one, so it
	// resolves rather than being rejected. "" remains the only way to
	// say "leave the device's duration alone".
	base, factor, ok := weekprofile.ParseTimeBaseFactor("0s")
	if !ok || base != 0 || factor != 0 {
		t.Errorf("ParseTimeBaseFactor(0s) = (%d, %d, %v), want (0, 0, true)", base, factor, ok)
	}
	if _, _, ok := weekprofile.ParseTimeBaseFactor(""); ok {
		t.Error("ParseTimeBaseFactor(\"\") must stay unset, not a zero duration")
	}
}

func TestParseTimeBaseFactorNegativeNumber(t *testing.T) {
	t.Parallel()
	_, _, ok := weekprofile.ParseTimeBaseFactor("-5")
	if ok {
		t.Error("ParseTimeBaseFactor -5 must fail (negative duration)")
	}
}

// TestParseTimeBaseFactorPermanentSentinel locks the (HOUR_1, 31)
// sentinel round-trip. FormatTimeBaseFactor renders that pair as "31h";
// without the pass-through, ParseTimeBaseFactor would reject it because
// factor=31 exceeds the regular MaxTimeBaseFactor=30 cap. The sentinel
// appears in the wild on lock auto-relock actions and on switch slots
// where the CCU firmware default writes 31 into DURATION_FACTOR.
func TestParseTimeBaseFactorPermanentSentinel(t *testing.T) {
	t.Parallel()
	b, f, ok := weekprofile.ParseTimeBaseFactor("31h")
	if !ok {
		t.Fatal("ParseTimeBaseFactor 31h must succeed (permanent sentinel)")
	}
	if b != 7 || f != 31 {
		t.Errorf("ParseTimeBaseFactor 31h = (%d, %d), want (7, 31)", b, f)
	}
}

// TestTimeBaseFactorRoundTripPermanent guards the formatter→parser cycle
// for every (base, factor) the formatter can emit at the sentinel value.
// Regression for the "Speichern fehlgeschlagen ... invalid duration 31h"
// error: reading a schedule from the CCU yielded "31h" which then refused
// to re-encode on save.
func TestTimeBaseFactorRoundTripPermanent(t *testing.T) {
	t.Parallel()
	s := weekprofile.FormatTimeBaseFactor(7, 31)
	if s != "31h" {
		t.Fatalf("FormatTimeBaseFactor(7,31) = %q, want %q", s, "31h")
	}
	b, f, ok := weekprofile.ParseTimeBaseFactor(s)
	if !ok || b != 7 || f != 31 {
		t.Fatalf("round-trip %q = (%d, %d, %v), want (7, 31, true)", s, b, f, ok)
	}
}

// TestSerializeSimpleScheduleLockAutorelockEnd exercises the lock save
// path with an action whose encoding uses the (HOUR_1, 31) sentinel pair.
// Before the parser fix, applyLockEncoding produced "31h" and the
// downstream serializeSimpleSchedule failed with "invalid duration".
func TestSerializeSimpleScheduleLockAutorelockEnd(t *testing.T) {
	t.Parallel()
	entries := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:     1,
			Weekdays:   []string{"MONDAY"},
			Time:       "07:30",
			LockMode:   "door_lock",
			LockAction: "lock_autorelock_end",
		},
	}
	m, err := serializeSimpleScheduleWithDomain(entries, "lock", schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serializeSimpleScheduleWithDomain lock autorelock_end: %v", err)
	}
	if got := m["01_WP_DURATION_BASE"]; got != 7 {
		t.Errorf("01_WP_DURATION_BASE = %v, want 7 (HOUR_1)", got)
	}
	if got := m["01_WP_DURATION_FACTOR"]; got != 31 {
		t.Errorf("01_WP_DURATION_FACTOR = %v, want 31 (permanent sentinel)", got)
	}
}

// TestParseSimpleScheduleHidesPermanentSentinel mirrors the user-facing
// scenario: an outdoor outlet whose CCU-stored schedule has the firmware
// default DURATION_FACTOR=31 in slot 2. parseSimpleSchedule must NOT
// surface that as Duration="31h" because put_paramset rejects factor>30
// (xml-rpc fault -5), so the SPA round-trip would explode on save.
// Skipping the duration emission means the encoder later writes (0, 0)
// — the canonical "no duration" wire encoding that the CCU accepts.
func TestParseSimpleScheduleHidesPermanentSentinel(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"02_WP_WEEKDAY":          int(1 << 2), // TUESDAY
		"02_WP_FIXED_HOUR":       18,
		"02_WP_FIXED_MINUTE":     0,
		"02_WP_LEVEL":            1.0,
		"02_WP_DURATION_BASE":    7,  // HOUR_1
		"02_WP_DURATION_FACTOR":  31, // sentinel — CCU firmware default
		"02_WP_RAMP_TIME_BASE":   7,
		"02_WP_RAMP_TIME_FACTOR": 31,
	}
	entries := parseSimpleSchedule(raw)
	if len(entries) != 1 {
		t.Fatalf("parseSimpleSchedule returned %d entries, want 1", len(entries))
	}
	if got := entries[0].Duration; got != "" {
		t.Errorf("Duration = %q, want empty (sentinel must be hidden)", got)
	}
	if got := entries[0].RampTime; got != "" {
		t.Errorf("RampTime = %q, want empty (sentinel must be hidden)", got)
	}
}

// TestParseSimpleScheduleEmitsValidDuration is the positive counterpart:
// values within the writable cap must still be surfaced as a string.
func TestParseSimpleScheduleEmitsValidDuration(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"03_WP_WEEKDAY":         int(1 << 3), // WEDNESDAY
		"03_WP_FIXED_HOUR":      8,
		"03_WP_FIXED_MINUTE":    30,
		"03_WP_LEVEL":           1.0,
		"03_WP_DURATION_BASE":   4,  // MIN_1
		"03_WP_DURATION_FACTOR": 10, // -> 10min
	}
	entries := parseSimpleSchedule(raw)
	if len(entries) != 1 {
		t.Fatalf("parseSimpleSchedule returned %d entries, want 1", len(entries))
	}
	if got := entries[0].Duration; got != "10min" {
		t.Errorf("Duration = %q, want %q", got, "10min")
	}
}

// TestSerializeSimpleScheduleSwitchEmptyDuration locks the post-fix
// re-save behaviour for the user's reported scenario: after the
// read-side filter hides the sentinel, the SPA round-trips Duration=""
// for that slot. The encoder must then OMIT the DURATION_BASE/FACTOR
// and RAMP_TIME_BASE/FACTOR fields entirely so put_paramset doesn't
// fault with -5 ("Invalid parameter or value") on a switch channel
// that rejects DURATION_FACTOR=0.
func TestSerializeSimpleScheduleSwitchEmptyDuration(t *testing.T) {
	t.Parallel()
	entries := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:   2,
			Weekdays: []string{"TUESDAY"},
			Time:     "18:00",
			Level:    1.0,
		},
	}
	m, err := serializeSimpleScheduleWithDomain(entries, "switch", schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serializeSimpleScheduleWithDomain switch: %v", err)
	}
	for _, key := range []string{
		"02_WP_DURATION_BASE",
		"02_WP_DURATION_FACTOR",
		"02_WP_RAMP_TIME_BASE",
		"02_WP_RAMP_TIME_FACTOR",
	} {
		if _, present := m[key]; present {
			t.Errorf("%s present in paramset; expected omission (CCU rejects 0 with fault -5)", key)
		}
	}
	// The active-slot fields must still be present.
	if got := m["02_WP_LEVEL"]; got != 1.0 {
		t.Errorf("02_WP_LEVEL = %v, want 1.0", got)
	}
	if got := m["02_WP_WEEKDAY"]; got != (1 << 2) {
		t.Errorf("02_WP_WEEKDAY = %v, want %v (TUESDAY)", got, 1<<2)
	}
}

// TestSerializeSimpleScheduleSwitchWithDuration is the inverse: when
// the caller DID set a duration, DURATION_BASE/FACTOR must be emitted.
func TestSerializeSimpleScheduleSwitchWithDuration(t *testing.T) {
	t.Parallel()
	entries := []hmapi.SimpleScheduleEntry{
		{
			SlotNo:   1,
			Weekdays: []string{"MONDAY"},
			Time:     "07:30",
			Level:    1.0,
			Duration: "5min",
		},
	}
	m, err := serializeSimpleScheduleWithDomain(entries, "switch", schedulemodel.SimpleMaxSlot)
	if err != nil {
		t.Fatalf("serializeSimpleScheduleWithDomain switch 5min: %v", err)
	}
	// Encoder picks the largest base: 5min = MIN_5 × 1.
	if got := m["01_WP_DURATION_BASE"]; got != 5 {
		t.Errorf("01_WP_DURATION_BASE = %v, want 5 (MIN_5)", got)
	}
	if got := m["01_WP_DURATION_FACTOR"]; got != 1 {
		t.Errorf("01_WP_DURATION_FACTOR = %v, want 1", got)
	}
}

// TestFindScheduleChannelWeekProfileChannelType exercises path-1:
// the device has a channel whose Type ends in "WEEK_PROFILE" →
// FindScheduleChannel returns that channel's number immediately
// (lines 141-143 in schedules.go).
func TestFindScheduleChannelWeekProfileChannelType(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-fsch"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := device.New(device.Config{
		Address:     "FSCHDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-BSL",
	})
	// Channel :1 has a week-profile type → path-1 short-circuit.
	ch := d.AddChannel("FSCHDEV001:1", 1, "BLIND_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
	_ = ch
	c.ModelRegistry.Put(d)

	domain := NewSchedulesDomain(reg, nil)
	no, err := domain.FindScheduleChannel(context.Background(), "FSCHDEV001")
	if err != nil {
		t.Fatalf("FindScheduleChannel: %v", err)
	}
	if no != 1 {
		t.Errorf("expected channel 1, got %d", no)
	}
}

// TestFindScheduleChannelDeviceNotFound covers the ErrDescriptionNotFound
// path when no central has the requested device (line 169).
func TestFindScheduleChannelDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	c, err := central.New(central.Config{Name: "ccu-fsch2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	_ = reg.Register(c)

	domain := NewSchedulesDomain(reg, nil)
	_, err = domain.FindScheduleChannel(context.Background(), "NODEV999")
	if err == nil {
		t.Error("FindScheduleChannel with unknown device must return error")
	}
}

// TestListScheduleDevicesIsTypeDerived pins the two paths and, more
// importantly, that the overview costs no CCU traffic: the domain is
// built without a writer, so any MASTER probe would panic or error.
func TestListScheduleDevicesIsTypeDerived(t *testing.T) {
	t.Parallel()

	reg, unit := scheduleTestRegistry(t)

	// A switch with a dedicated week-profile channel.
	sw := scheduleTestDevice(unit, "ABC001", "HmIP-BSM", "Kitchen light")
	sw.AddChannel("ABC001:1", 1, "SWITCH_TRANSCEIVER", hmenum.ParamsetKeyValues)
	sw.AddChannel("ABC001:3", 3, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyMaster)

	// A thermostat carrying the profile in MASTER.
	trv := scheduleTestDevice(unit, "ABC002", "HmIP-eTRV-2", "Bath radiator")
	trv.AddChannel("ABC002:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyMaster)

	// A device with neither.
	plug := scheduleTestDevice(unit, "ABC003", "HmIP-PS", "Bookshelf")
	plug.AddChannel("ABC003:3", 3, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)

	// No writer: a MASTER probe would have nowhere to go.
	got, err := NewSchedulesDomain(reg, nil).ListScheduleDevices(context.Background())
	if err != nil {
		t.Fatalf("ListScheduleDevices: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d devices, want the switch and the thermostat: %+v", len(got), got)
	}
	if got[0].Address != "ABC001" || got[0].Kind != "week_profile" || got[0].Channel.Number != 3 {
		t.Errorf("first entry = %+v, want ABC001 week_profile on channel 3", got[0])
	}
	if got[1].Address != "ABC002" || got[1].Kind != "climate" || got[1].Channel.Number != 1 {
		t.Errorf("second entry = %+v, want ABC002 climate on channel 1", got[1])
	}
	if got[0].Central != "ccu1" || got[0].Name != "Kitchen light" || got[0].Model != "HmIP-BSM" {
		t.Errorf("entry lacks its identity: %+v", got[0])
	}
}

// TestListScheduleDevicesPrefersTheDedicatedChannel pins the ordering of
// the two paths: a thermostat that also has a week-profile channel is
// reported through that channel, mirroring FindScheduleChannel.
func TestListScheduleDevicesPrefersTheDedicatedChannel(t *testing.T) {
	t.Parallel()

	reg, unit := scheduleTestRegistry(t)
	dev := scheduleTestDevice(unit, "ABC010", "HmIP-Hybrid", "Hybrid")
	dev.AddChannel("ABC010:1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyMaster)
	dev.AddChannel("ABC010:5", 5, "HEATING_WEEK_PROFILE", hmenum.ParamsetKeyMaster)

	got, err := NewSchedulesDomain(reg, nil).ListScheduleDevices(context.Background())
	if err != nil {
		t.Fatalf("ListScheduleDevices: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "week_profile" || got[0].Channel.Number != 5 {
		t.Fatalf("got %+v, want the dedicated week-profile channel", got)
	}
}

// scheduleTestRegistry builds a one-central registry for the overview tests.
func scheduleTestRegistry(t *testing.T) (*central.Registry, *central.Unit) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu1"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	return reg, c
}

// scheduleTestDevice registers a device with the given identity.
func scheduleTestDevice(c *central.Unit, address, model, name string) *device.Device {
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: address, Model: model, Name: name,
	})
	c.ModelRegistry.Put(d)
	return d
}

// TestWeekdayBitsMatchTheCCUEditor pins the wire layout of
// `<NN>_WP_WEEKDAY` against the CCU's own editor, which emits Sunday=1,
// Monday=2 … Saturday=64 (`_getWeekDay` in HmIPWeeklyProgram.js).
//
// Sunday sat on bit 7 here, a bit the device does not evaluate. A
// Sunday schedule made on the CCU came back with an empty weekday list,
// and one saved from here never fired. Both directions are asserted
// because the two tables that encode this are separate.
func TestWeekdayBitsMatchTheCCUEditor(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		"SUNDAY": 1, "MONDAY": 2, "TUESDAY": 4, "WEDNESDAY": 8,
		"THURSDAY": 16, "FRIDAY": 32, "SATURDAY": 64,
	}
	for day, bit := range want {
		days := []schedulemodel.Weekday{schedulemodel.Weekday(strings.ToUpper(day))}
		if got := weekprofile.WeekdayListToBitmask(days); got != bit {
			t.Errorf("WeekdayListToBitmask(%s) = %d, want %d", day, got, bit)
		}
		names := weekprofile.WeekdayBitmaskToList(bit)
		if len(names) != 1 || string(names[0]) != day {
			t.Errorf("WeekdayBitmaskToList(%d) = %v, want [%s]", bit, names, day)
		}
	}

	// Every day at once is 127, not 254.
	all := make([]schedulemodel.Weekday, 0, len(want))
	for day := range want {
		all = append(all, schedulemodel.Weekday(strings.ToUpper(day)))
	}
	if got := weekprofile.WeekdayListToBitmask(all); got != 127 {
		t.Errorf("WeekdayListToBitmask(all seven) = %d, want 127", got)
	}
	if got := weekprofile.WeekdayBitmaskToList(127); len(got) != 7 {
		t.Errorf("WeekdayBitmaskToList(127) = %v, want all seven days", got)
	}
}
