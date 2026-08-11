// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// ---------------------------------------------------------------------------
// filterWeekdaySlots
// ---------------------------------------------------------------------------

func TestFilterWeekdaySlotsFull13WithRedundant2400(t *testing.T) {
	// Slots 1-7 have real end-times; slots 8-13 are all "24:00".
	// Result: slots 1-7 plus the first 24:00 (slot 8) = 8 entries.
	t.Parallel()
	ws := make(weekdaySlots)
	for i := 1; i <= 7; i++ {
		ts, _ := minutesToTimeStr(i * 3 * 60)
		ws[i] = ScheduleSlot{EndTime: ts, Temperature: 20.0}
	}
	for i := 8; i <= 13; i++ {
		ws[i] = ScheduleSlot{EndTime: "24:00", Temperature: 18.0}
	}
	got := filterWeekdaySlots(ws)
	if len(got) != 8 {
		t.Errorf("expected 8 slots, got %d", len(got))
	}
}

func TestFilterWeekdaySlotsMultiple2400(t *testing.T) {
	// Input has slots keyed 1,3,5,7; slots 5 and 7 end at 24:00.
	// Filter: keep slots sorted by key; stop after first 24:00; renumber from 1.
	t.Parallel()
	ws := weekdaySlots{
		7: {EndTime: "24:00", Temperature: 18.0},
		3: {EndTime: "18:00", Temperature: 21.0},
		5: {EndTime: "24:00", Temperature: 19.0},
		1: {EndTime: "06:00", Temperature: 20.0},
	}
	got := filterWeekdaySlots(ws)
	// Sorted order: 1(06:00), 3(18:00), 5(24:00) — stop after first 24:00.
	if len(got) != 3 {
		t.Errorf("expected 3 slots, got %d", len(got))
	}
	if got[1].EndTime != "06:00" {
		t.Errorf("slot 1 EndTime = %q, want 06:00", got[1].EndTime)
	}
	if got[2].EndTime != "18:00" {
		t.Errorf("slot 2 EndTime = %q, want 18:00", got[2].EndTime)
	}
	if got[3].EndTime != "24:00" {
		t.Errorf("slot 3 EndTime = %q, want 24:00", got[3].EndTime)
	}
	if got[3].Temperature != 19.0 {
		t.Errorf("slot 3 Temperature = %v, want 19.0 (from slot-5)", got[3].Temperature)
	}
}

func TestFilterWeekdaySlotsNo2400(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18.0},
		2: {EndTime: "08:00", Temperature: 21.0},
		3: {EndTime: "18:00", Temperature: 18.0},
	}
	got := filterWeekdaySlots(ws)
	if len(got) != 3 {
		t.Errorf("expected 3 slots, got %d", len(got))
	}
	want := map[int]string{1: "06:00", 2: "08:00", 3: "18:00"}
	for k, e := range want {
		if got[k].EndTime != e {
			t.Errorf("slot %d EndTime = %q, want %q", k, got[k].EndTime, e)
		}
	}
}

func TestFilterWeekdaySlotsSingle2400(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18.0},
		2: {EndTime: "24:00", Temperature: 18.0},
	}
	got := filterWeekdaySlots(ws)
	if len(got) != 2 {
		t.Errorf("expected 2 slots, got %d", len(got))
	}
	if got[2].EndTime != "24:00" {
		t.Errorf("slot 2 EndTime = %q, want 24:00", got[2].EndTime)
	}
}

// ---------------------------------------------------------------------------
// normalizeWeekdaySlots
// ---------------------------------------------------------------------------

func TestNormalizeWeekdaySlotsEmpty(t *testing.T) {
	// Empty input normalizes to 13 zero-temperature slots, not empty.
	t.Parallel()
	got := normalizeWeekdaySlots(weekdaySlots{})
	if len(got) != slotCount {
		t.Errorf("expected %d slots, got %d", slotCount, len(got))
	}
}

func TestNormalizeWeekdaySlotsFullExample(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		5: {EndTime: "18:00", Temperature: 20.0},
		1: {EndTime: "06:00", Temperature: 18.0},
		3: {EndTime: "12:00", Temperature: 22.0},
	}
	got := normalizeWeekdaySlots(ws)
	if len(got) != slotCount {
		t.Errorf("expected %d slots, got %d", slotCount, len(got))
	}
	if got[1].EndTime != "06:00" {
		t.Errorf("slot 1 = %q", got[1].EndTime)
	}
	if got[2].EndTime != "12:00" {
		t.Errorf("slot 2 = %q", got[2].EndTime)
	}
	if got[3].EndTime != "18:00" {
		t.Errorf("slot 3 = %q", got[3].EndTime)
	}
	for i := 4; i <= slotCount; i++ {
		if got[i].EndTime != "24:00" {
			t.Errorf("slot %d EndTime = %q, want 24:00", i, got[i].EndTime)
		}
		if got[i].Temperature != 20.0 {
			t.Errorf("slot %d Temperature = %v, want 20.0", i, got[i].Temperature)
		}
	}
}

// ---------------------------------------------------------------------------
// minutesToTimeStr / toMinutes
// ---------------------------------------------------------------------------

func TestMinutesToTimeStrValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mins int
		want string
	}{
		{0, "00:00"},
		{360, "06:00"},
		{750, "12:30"},
		{1440, "24:00"},
	}
	for _, tc := range cases {
		got, err := minutesToTimeStr(tc.mins)
		if err != nil {
			t.Errorf("minutesToTimeStr(%d): %v", tc.mins, err)
			continue
		}
		if got != tc.want {
			t.Errorf("minutesToTimeStr(%d) = %q, want %q", tc.mins, got, tc.want)
		}
	}
}

func TestMinutesToTimeStrInvalidType(t *testing.T) {
	// Invalid inputs return errors via ParseSlotTime.
	t.Parallel()
	if _, err := ParseSlotTime("invalid"); err == nil {
		t.Error("expected error for string 'invalid'")
	}
	if _, err := ParseSlotTime(nil); err == nil {
		t.Error("expected error for nil input")
	}
}

func TestToMinutesValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		want int
	}{
		{"00:00", 0},
		{"06:00", 360},
		{"12:30", 750},
		{"24:00", 1440},
	}
	for _, tc := range cases {
		got := toMinutes(tc.s)
		if got != tc.want {
			t.Errorf("toMinutes(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestParseSlotTimeRejectsOutOfRangeHour(t *testing.T) {
	t.Parallel()
	if _, err := ParseSlotTime("25:00"); err == nil {
		t.Error("ParseSlotTime(25:00): expected error for hour > 24")
	}
}

// ---------------------------------------------------------------------------
// identifyBaseTemperature
// ---------------------------------------------------------------------------

func TestIdentifyBaseTemperatureTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ws   weekdaySlots
		want float64
	}{
		{
			name: "base_temp_dominates",
			ws: weekdaySlots{
				1: {EndTime: "06:00", Temperature: 18.0}, // 360 min
				2: {EndTime: "08:00", Temperature: 21.0}, // 120 min
				3: {EndTime: "17:00", Temperature: 18.0}, // 540 min
				4: {EndTime: "22:00", Temperature: 21.0}, // 300 min
				5: {EndTime: "24:00", Temperature: 18.0}, // 120 min
				// 18.0: 360+540+120 = 1020; 21.0: 120+300 = 420
			},
			want: 18.0,
		},
		{
			name: "complex_schedule",
			ws: weekdaySlots{
				1: {EndTime: "05:00", Temperature: 17.0}, // 300 min
				2: {EndTime: "08:00", Temperature: 20.0}, // 180 min
				3: {EndTime: "17:00", Temperature: 18.0}, // 540 min
				4: {EndTime: "22:00", Temperature: 22.0}, // 300 min
				5: {EndTime: "24:00", Temperature: 18.0}, // 120 min
				// 18.0: 540+120 = 660 (most)
			},
			want: 18.0,
		},
		{
			name: "empty_data",
			ws:   weekdaySlots{},
			want: 0, // Go returns 0 for empty input
		},
		{
			name: "multiple_temperatures",
			ws: weekdaySlots{
				1: {EndTime: "06:00", Temperature: 15.0}, // 360 min
				2: {EndTime: "08:00", Temperature: 18.0}, // 120 min
				3: {EndTime: "24:00", Temperature: 21.0}, // 960 min (most)
			},
			want: 21.0,
		},
		{
			name: "single_temperature",
			ws: weekdaySlots{
				1: {EndTime: "24:00", Temperature: 18.0},
			},
			want: 18.0,
		},
		{
			name: "unsorted_slots",
			ws: weekdaySlots{
				5: {EndTime: "24:00", Temperature: 18.0},
				1: {EndTime: "06:00", Temperature: 18.0},
				3: {EndTime: "17:00", Temperature: 18.0},
				2: {EndTime: "08:00", Temperature: 21.0},
				4: {EndTime: "22:00", Temperature: 21.0},
			},
			want: 18.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := identifyBaseTemperature(tc.ws)
			if got != tc.want {
				t.Errorf("identifyBaseTemperature(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIdentifyBaseTemperatureEqualTime(t *testing.T) {
	// Python asserts result is one of [18.0, 21.0]; Go returns one deterministically.
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "12:00", Temperature: 18.0},
		2: {EndTime: "24:00", Temperature: 21.0},
	}
	got := identifyBaseTemperature(ws)
	if got != 18.0 && got != 21.0 {
		t.Errorf("identifyBaseTemperature = %v, want 18.0 or 21.0", got)
	}
}

// ---------------------------------------------------------------------------
// fillUpWeekdaySlots
// ---------------------------------------------------------------------------

func TestFillUpWeekdaySlots(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18.0},
	}
	got := fillUpWeekdaySlots(20.0, ws)
	if len(got) != slotCount {
		t.Errorf("expected %d slots, got %d", slotCount, len(got))
	}
	for i := 2; i <= slotCount; i++ {
		if got[i].EndTime != "24:00" {
			t.Errorf("slot %d EndTime = %q, want 24:00", i, got[i].EndTime)
		}
		if got[i].Temperature != 20.0 {
			t.Errorf("slot %d Temperature = %v, want 20.0", i, got[i].Temperature)
		}
	}
}

// ---------------------------------------------------------------------------
// WeekdayBitmaskToList / WeekdayListToBitmask
// ---------------------------------------------------------------------------

func TestWeekdayBitmaskHelpers(t *testing.T) {
	t.Parallel()

	// Sunday is bit 0, so all seven days are 127 — the value the CCU's
	// own editor produces when every checkbox is ticked. Reading Sunday
	// as bit 7 made this 254, a mask whose top bit the device ignores.
	allDays := schedule.Weekdays // 7 days
	bitmask := WeekdayListToBitmask(allDays)
	if bitmask != 127 { // bits 0-6: 1+2+4+8+16+32+64 = 127
		t.Errorf("bitmask(allDays) = %d, want 127 (bits 0-6)", bitmask)
	}

	back := WeekdayBitmaskToList(bitmask)
	if len(back) != 7 {
		t.Errorf("WeekdayBitmaskToList(allBits) len = %d, want 7", len(back))
	}

	// Sunday on its own is 1, and it must survive the round-trip: the
	// defect dropped it silently in both directions.
	sunday := []schedule.Weekday{schedule.WeekdaySunday}
	if bm := WeekdayListToBitmask(sunday); bm != 1 {
		t.Errorf("bitmask(SUNDAY) = %d, want 1 (bit 0)", bm)
	}
	backSun := WeekdayBitmaskToList(1)
	if len(backSun) != 1 || backSun[0] != schedule.WeekdaySunday {
		t.Errorf("WeekdayBitmaskToList(1) = %v, want [SUNDAY]", backSun)
	}

	// Empty list → 0.
	if WeekdayListToBitmask(nil) != 0 {
		t.Error("WeekdayListToBitmask(nil) != 0")
	}
	if len(WeekdayBitmaskToList(0)) != 0 {
		t.Error("WeekdayBitmaskToList(0) not empty")
	}

	// Single day: MONDAY is bit 1 → value 2 (1<<1).
	monday := []schedule.Weekday{schedule.WeekdayMonday}
	bm := WeekdayListToBitmask(monday)
	if bm != 2 {
		t.Errorf("bitmask(MONDAY) = %d, want 2 (bit 1)", bm)
	}
	back1 := WeekdayBitmaskToList(bm)
	if len(back1) != 1 {
		t.Errorf("WeekdayBitmaskToList(2) len = %d, want 1", len(back1))
	}
	if back1[0] != schedule.WeekdayMonday {
		t.Errorf("WeekdayBitmaskToList(2)[0] = %q, want MONDAY", back1[0])
	}
}

// ---------------------------------------------------------------------------
// ParseClimateRawParamset / BuildClimateRawParamset round-trips
// ---------------------------------------------------------------------------

func TestClimateConvertRawToDictSchedule(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_TEMPERATURE_MONDAY_1":  20.0,
		"P1_ENDTIME_MONDAY_1":      360, // 6 hours = "06:00"
		"P1_TEMPERATURE_TUESDAY_1": 21.0,
		"P1_ENDTIME_TUESDAY_1":     420, // 7 hours = "07:00"
		"INVALID_FORMAT":           42,  // skipped
		"P1_TEMP_MON":              42,  // skipped (wrong pattern)
	}
	got, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("ParseClimateRawParamset: %v", err)
	}
	p1, ok := got["P1"]
	if !ok {
		t.Fatal("missing profile P1")
	}
	if _, ok := p1["MONDAY"]; !ok {
		t.Error("missing weekday MONDAY")
	}
	if _, ok := p1["TUESDAY"]; !ok {
		t.Error("missing weekday TUESDAY")
	}
	if p1["MONDAY"][1].Temperature != 20.0 {
		t.Errorf("MONDAY slot 1 temperature = %v, want 20.0", p1["MONDAY"][1].Temperature)
	}
	if p1["MONDAY"][1].EndTime != "06:00" {
		t.Errorf("MONDAY slot 1 endtime = %q, want 06:00", p1["MONDAY"][1].EndTime)
	}
	if p1["TUESDAY"][1].Temperature != 21.0 {
		t.Errorf("TUESDAY slot 1 temperature = %v, want 21.0", p1["TUESDAY"][1].Temperature)
	}
	if p1["TUESDAY"][1].EndTime != "07:00" {
		t.Errorf("TUESDAY slot 1 endtime = %q, want 07:00", p1["TUESDAY"][1].EndTime)
	}
}

func TestClimateConvertRawToDictScheduleInvalidEntries(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"INVALID_PROFILE_TEMPERATURE_MONDAY_1": 20.0,
		"P1_INVALID_TYPE_MONDAY_1":             20.0,
		"P1_TEMPERATURE_INVALID_DAY_1":         20.0,
		"P1_TEMPERATURE_MONDAY_INVALID":        20.0,
	}
	got, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestClimateConvertDictToRawSchedule(t *testing.T) {
	t.Parallel()
	raw := rawClimateSchedule{
		"P1": profileSlots{
			"MONDAY": weekdaySlots{
				1: {EndTime: "06:00", Temperature: 20.0},
			},
		},
	}
	got, err := BuildClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("BuildClimateRawParamset: %v", err)
	}
	if got["P1_TEMPERATURE_MONDAY_1"] != 20.0 {
		t.Errorf("P1_TEMPERATURE_MONDAY_1 = %v, want 20.0", got["P1_TEMPERATURE_MONDAY_1"])
	}
	if got["P1_ENDTIME_MONDAY_1"] != 360 {
		t.Errorf("P1_ENDTIME_MONDAY_1 = %v, want 360", got["P1_ENDTIME_MONDAY_1"])
	}
}

func TestClimateConvertRawWithIntEndtime(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_ENDTIME_MONDAY_1": 360,
	}
	got, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("ParseClimateRawParamset: %v", err)
	}
	if got["P1"]["MONDAY"][1].EndTime != "06:00" {
		t.Errorf("endtime = %q, want 06:00", got["P1"]["MONDAY"][1].EndTime)
	}
}

func TestClimateConvertRawWithInvalidSlotNumberStr(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_TEMPERATURE_MONDAY_ABC": 20.0,
	}
	got, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Simple paramset round-trip
// ---------------------------------------------------------------------------

func TestSimpleParamsetConvertRawToDictSchedule(t *testing.T) {
	// Note: TARGET_CHANNELS/CONDITION/ASTRO_* are not yet in the Go parser;
	// only the core WEEKDAY/LEVEL/FIXED_HOUR/FIXED_MINUTE fields are tested.
	t.Parallel()
	raw := map[string]any{
		"01_WP_WEEKDAY":      6, // MONDAY(2) + TUESDAY(4) = bitmask 6
		"01_WP_LEVEL":        1.0,
		"01_WP_FIXED_HOUR":   10,
		"01_WP_FIXED_MINUTE": 30,
		"INVALID_FORMAT":     42, // skipped
		"01_INVALID":         42, // skipped
	}
	got, err := ParseSimpleRawParamset(raw)
	if err != nil {
		t.Fatalf("ParseSimpleRawParamset: %v", err)
	}
	entry, ok := got.Entries[1]
	if !ok {
		t.Fatal("missing entry slot 1")
	}
	hasMonday, hasTuesday := false, false
	for _, d := range entry.Weekdays {
		if d == schedule.WeekdayMonday {
			hasMonday = true
		}
		if d == schedule.WeekdayTuesday {
			hasTuesday = true
		}
	}
	if !hasMonday || !hasTuesday {
		t.Errorf("weekdays = %v, want MONDAY and TUESDAY", entry.Weekdays)
	}
	if entry.Time != "10:30" {
		t.Errorf("time = %q, want 10:30", entry.Time)
	}
	if entry.Level != 1.0 {
		t.Errorf("level = %v, want 1.0", entry.Level)
	}
}

func TestSimpleParamsetFiltersInactive(t *testing.T) {
	// Groups with WEEKDAY == 0 are inactive and must be excluded.
	t.Parallel()
	raw := map[string]any{
		"01_WP_LEVEL":        1.0,
		"01_WP_FIXED_HOUR":   10,
		"01_WP_FIXED_MINUTE": 0,
		// no WEEKDAY → defaults to 0 → inactive
	}
	got, err := ParseSimpleRawParamset(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got.Entries[1]; ok {
		t.Error("inactive entry (WEEKDAY=0) must not appear in result")
	}
}

func TestSimpleParamsetBuildDictToRaw(t *testing.T) {
	t.Parallel()
	entry := schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday, schedule.WeekdayTuesday},
		Time:     "10:30",
		Level:    1.0,
	}
	ss := schedule.NewSimple()
	if err := ss.Put(1, entry); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got := BuildSimpleRawParamset(ss, schedule.SimpleMaxSlot)
	if _, ok := got["01_WP_WEEKDAY"]; !ok {
		t.Error("missing 01_WP_WEEKDAY")
	}
	if _, ok := got["01_WP_LEVEL"]; !ok {
		t.Error("missing 01_WP_LEVEL")
	}
	if _, ok := got["01_WP_FIXED_HOUR"]; !ok {
		t.Error("missing 01_WP_FIXED_HOUR")
	}
	if _, ok := got["01_WP_FIXED_MINUTE"]; !ok {
		t.Error("missing 01_WP_FIXED_MINUTE")
	}
	if got["01_WP_LEVEL"] != 1.0 {
		t.Errorf("01_WP_LEVEL = %v, want 1.0", got["01_WP_LEVEL"])
	}
	if got["01_WP_FIXED_HOUR"] != 10 {
		t.Errorf("01_WP_FIXED_HOUR = %v, want 10", got["01_WP_FIXED_HOUR"])
	}
	if got["01_WP_FIXED_MINUTE"] != 30 {
		t.Errorf("01_WP_FIXED_MINUTE = %v, want 30", got["01_WP_FIXED_MINUTE"])
	}
	// MONDAY=bit1→2, TUESDAY=bit2→4; total bitmask = 6
	if got["01_WP_WEEKDAY"] != 6 {
		t.Errorf("01_WP_WEEKDAY = %v, want 6 (MONDAY+TUESDAY)", got["01_WP_WEEKDAY"])
	}
}

// TestSimpleRawParamsetRoundTripsGroupsAboveTwentyFour pins the group
// range against the hardware.
//
// The parser capped at 24, a number that came from this project's own
// storage rather than from any device: a switch, dimmer, blind or servo
// channel declares 75 groups and the CCU's own editor edits all of them
// (`_getMaxEntries` in HmIPWeeklyProgram.js). A real CCU stores and
// returns a group-25 entry unchanged. Everything past 24 was therefore
// dropped on read and never written back.
func TestSimpleRawParamsetRoundTripsGroupsAboveTwentyFour(t *testing.T) {
	t.Parallel()

	for _, group := range []int{1, 24, 25, 69, schedule.SimpleMaxSlot} {
		t.Run(fmt.Sprintf("group_%d", group), func(t *testing.T) {
			t.Parallel()

			raw := map[string]any{
				fmt.Sprintf("%02d_WP_WEEKDAY", group):         2, // Monday
				fmt.Sprintf("%02d_WP_FIXED_HOUR", group):      7,
				fmt.Sprintf("%02d_WP_FIXED_MINUTE", group):    30,
				fmt.Sprintf("%02d_WP_LEVEL", group):           1.0,
				fmt.Sprintf("%02d_WP_TARGET_CHANNELS", group): 1,
			}
			s, err := ParseSimpleRawParamset(raw)
			if err != nil {
				t.Fatalf("ParseSimpleRawParamset: %v", err)
			}
			entry, ok := s.Entries[group]
			if !ok {
				t.Fatalf("group %d missing after parse; parsed %d entries", group, len(s.Entries))
			}
			if entry.Time != "07:30" {
				t.Errorf("group %d time = %q, want 07:30", group, entry.Time)
			}

			// And back out again: a schedule that survives the read must
			// survive the write, or an operator opening it loses it.
			out := BuildSimpleRawParamset(s, schedule.SimpleMaxSlot)
			if got := out[fmt.Sprintf("%02d_WP_WEEKDAY", group)]; got != 2 {
				t.Errorf("group %d WEEKDAY after build = %v, want 2", group, got)
			}
		})
	}
}

// TestBuildSimpleRawParamsetHonoursTheDeactivationBound pins that the
// sweep stays inside what the device declares. Naming a group a channel
// does not have fails the entire paramset with fault -5, so the bound is
// a correctness requirement, not a tidiness one.
func TestBuildSimpleRawParamsetHonoursTheDeactivationBound(t *testing.T) {
	t.Parallel()

	s := schedule.NewSimple()
	if err := s.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "07:30",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	out := BuildSimpleRawParamset(s, 69)
	if _, ok := out["69_WP_WEEKDAY"]; !ok {
		t.Error("group 69 not deactivated although the device declares it")
	}
	if _, ok := out["70_WP_WEEKDAY"]; ok {
		t.Error("group 70 written although the device declares only 69")
	}

	// Bound 0 means "device unknown": write the active groups, touch
	// nothing else.
	none := BuildSimpleRawParamset(s, 0)
	if _, ok := none["02_WP_WEEKDAY"]; ok {
		t.Error("deactivation swept with bound 0")
	}
	if _, ok := none["01_WP_WEEKDAY"]; !ok {
		t.Error("active group 1 missing with bound 0")
	}
}
