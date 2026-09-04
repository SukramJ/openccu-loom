// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ParseSlotTime
// ---------------------------------------------------------------------------

func TestParseSlotTimeInteger(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input any
		want  string
	}{
		{0, "00:00"},
		{360, "06:00"},
		{1440, "24:00"},
		{480, "08:00"},
		{float64(1320), "22:00"},
	}
	for _, tc := range cases {
		got, err := ParseSlotTime(tc.input)
		if err != nil {
			t.Errorf("ParseSlotTime(%v): %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSlotTime(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseSlotTimeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		ok    bool
	}{
		{"06:00", true},
		{"24:00", true},
		{"00:00", true},
		{"23:59", true},
		{"25:00", false},
		{"bad", false},
	}
	for _, tc := range cases {
		_, err := ParseSlotTime(tc.input)
		if tc.ok && err != nil {
			t.Errorf("ParseSlotTime(%q) unexpected error: %v", tc.input, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseSlotTime(%q) expected error", tc.input)
		}
	}
}

// ---------------------------------------------------------------------------
// minutesToTimeStr
// ---------------------------------------------------------------------------

func TestMinutesToTimeStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mins int
		want string
		ok   bool
	}{
		{0, "00:00", true},
		{90, "01:30", true},
		{1440, "24:00", true},
		{1441, "", false},
		{-1, "", false},
	}
	for _, tc := range cases {
		got, err := minutesToTimeStr(tc.mins)
		if tc.ok {
			if err != nil {
				t.Errorf("minutesToTimeStr(%d): %v", tc.mins, err)
				continue
			}
			if got != tc.want {
				t.Errorf("minutesToTimeStr(%d) = %q, want %q", tc.mins, got, tc.want)
			}
		} else if err == nil {
			t.Errorf("minutesToTimeStr(%d) expected error", tc.mins)
		}
	}
}

// ---------------------------------------------------------------------------
// toMinutes
// ---------------------------------------------------------------------------

func TestToMinutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		want int
	}{
		{"00:00", 0},
		{"06:00", 360},
		{"24:00", 1440},
		{"08:30", 510},
		{"bad", -1},
	}
	for _, tc := range cases {
		if got := toMinutes(tc.s); got != tc.want {
			t.Errorf("toMinutes(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// filterWeekdaySlots
// ---------------------------------------------------------------------------

func TestFilterWeekdaySlotsRemovesTrailing2400(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18},
		2: {EndTime: "22:00", Temperature: 21},
		3: {EndTime: "24:00", Temperature: 18},
		4: {EndTime: "24:00", Temperature: 18}, // redundant
		5: {EndTime: "24:00", Temperature: 18}, // redundant
	}
	got := filterWeekdaySlots(ws)
	if len(got) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(got))
	}
	for _, no := range []int{1, 2, 3} {
		if _, ok := got[no]; !ok {
			t.Errorf("slot %d missing after filter", no)
		}
	}
}

func TestFilterWeekdaySlotsEmpty(t *testing.T) {
	t.Parallel()
	got := filterWeekdaySlots(weekdaySlots{})
	if len(got) != 0 {
		t.Errorf("expected empty, got %d slots", len(got))
	}
}

// ---------------------------------------------------------------------------
// normalizeWeekdaySlots
// ---------------------------------------------------------------------------

func TestNormalizeWeekdaySlotsFillsTo13(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18},
		2: {EndTime: "24:00", Temperature: 21},
	}
	got := normalizeWeekdaySlots(ws)
	if len(got) != 13 {
		t.Fatalf("expected 13 slots, got %d", len(got))
	}
	for no := 1; no <= 13; no++ {
		if _, ok := got[no]; !ok {
			t.Errorf("slot %d missing after normalize", no)
		}
	}
	// Slots 3..13 must be 24:00 fill.
	for no := 3; no <= 13; no++ {
		if got[no].EndTime != "24:00" {
			t.Errorf("slot %d: expected 24:00 fill, got %s", no, got[no].EndTime)
		}
	}
}

func TestNormalizeWeekdaySlotsSortsByEndTime(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "22:00", Temperature: 21}, // should be slot 2 after sort
		2: {EndTime: "08:00", Temperature: 18}, // should be slot 1 after sort
	}
	got := normalizeWeekdaySlots(ws)
	if got[1].EndTime != "08:00" {
		t.Errorf("slot 1 after sort: got %s, want 08:00", got[1].EndTime)
	}
	if got[2].EndTime != "22:00" {
		t.Errorf("slot 2 after sort: got %s, want 22:00", got[2].EndTime)
	}
}

// ---------------------------------------------------------------------------
// identifyBaseTemperature
// ---------------------------------------------------------------------------

func TestIdentifyBaseTemperature(t *testing.T) {
	t.Parallel()
	// 18°C for 6+2=8 h, 21°C for 14 h → base = 21°C
	ws := weekdaySlots{
		1: {EndTime: "06:00", Temperature: 18},
		2: {EndTime: "22:00", Temperature: 21},
		3: {EndTime: "24:00", Temperature: 18},
	}
	got := identifyBaseTemperature(ws)
	// 18°C: 0→6h (360 min) + 22→24h (120 min) = 480 min
	// 21°C: 6→22h = 960 min → 21°C occupies the most minutes.
	if got != 21 {
		t.Errorf("identifyBaseTemperature = %.1f, want 21.0 (most minutes)", got)
	}
}

func TestIdentifyBaseTemperatureConstantDay(t *testing.T) {
	t.Parallel()
	ws := weekdaySlots{
		1: {EndTime: "24:00", Temperature: 20},
	}
	if got := identifyBaseTemperature(ws); got != 20 {
		t.Errorf("constant day: got %.1f, want 20", got)
	}
}

func TestIdentifyBaseTemperatureEmpty(t *testing.T) {
	t.Parallel()
	// No usable slot means no temperature held any minutes; the shared
	// rule answers with the default fill temperature rather than 0 °C.
	if got := identifyBaseTemperature(weekdaySlots{}); got != schedule.DefaultBaseTemperature {
		t.Errorf("empty: got %.1f, want %.1f", got, schedule.DefaultBaseTemperature)
	}
}

// ---------------------------------------------------------------------------
// ParseClimateRawParamset / BuildClimateRawParamset round-trip
// ---------------------------------------------------------------------------

func TestClimateParamsetRoundTrip(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_TEMPERATURE_MONDAY_1": 18.0,
		"P1_ENDTIME_MONDAY_1":     360, // 06:00
		"P1_TEMPERATURE_MONDAY_2": 21.0,
		"P1_ENDTIME_MONDAY_2":     1440, // 24:00
	}
	internal, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(internal) == 0 {
		t.Fatal("expected at least one profile")
	}
	p1, ok := internal["P1"]
	if !ok {
		t.Fatal("P1 missing")
	}
	monday, ok := p1["MONDAY"]
	if !ok {
		t.Fatal("MONDAY missing")
	}
	if monday[1].EndTime != "06:00" {
		t.Errorf("slot 1 endtime = %s, want 06:00", monday[1].EndTime)
	}
	if monday[2].Temperature != 21 {
		t.Errorf("slot 2 temp = %v, want 21", monday[2].Temperature)
	}

	rebuilt, err := BuildClimateRawParamset(internal)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rebuilt["P1_ENDTIME_MONDAY_1"] != 360 {
		t.Errorf("endtime_monday_1 = %v, want 360", rebuilt["P1_ENDTIME_MONDAY_1"])
	}
}

func TestParseClimateRawParamsetIgnoresBadKeys(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"GARBAGE":                 99.0,
		"P7_TEMPERATURE_MONDAY_1": 20.0, // P7 is invalid
		"P1_ENDTIME_MONDAY_14":    360,  // slot 14 out of range
		"P1_TEMPERATURE_MONDAY_1": 18.0,
		"P1_ENDTIME_MONDAY_1":     360,
	}
	internal, err := ParseClimateRawParamset(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := internal["P7"]; ok {
		t.Error("P7 must be ignored")
	}
}

// ---------------------------------------------------------------------------
// ClimateToRaw / RawToClimate round-trip
// ---------------------------------------------------------------------------

func makeSimpleClimate(t *testing.T) *schedule.Climate {
	t.Helper()
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	// Full 24h coverage: 00:00-07:00 @ 18°C, 07:00-22:00 @ 21°C, 22:00-24:00 @ 18°C.
	if err := prof.Put(schedule.WeekdayMonday, schedule.ClimateWeekday{
		BaseTemperature: 18,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "07:00", Temperature: 18},
			{StartTime: "07:00", EndTime: "22:00", Temperature: 21},
			{StartTime: "22:00", EndTime: "24:00", Temperature: 18},
		},
	}); err != nil {
		t.Fatalf("prof.Put(MONDAY): %v", err)
	}
	if err := c.Put("P1", prof); err != nil {
		t.Fatalf("c.Put: %v", err)
	}
	return c
}

func TestClimateToRawRoundTrip(t *testing.T) {
	t.Parallel()
	orig := makeSimpleClimate(t)

	raw, err := ClimateToRaw(orig)
	if err != nil {
		t.Fatalf("ClimateToRaw: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty raw")
	}

	// Each weekday in raw must have exactly 13 slots.
	for _, ps := range raw {
		for day, ws := range ps {
			if len(ws) != 13 {
				t.Errorf("day %s: expected 13 slots, got %d", day, len(ws))
			}
		}
	}

	rebuilt, err := RawToClimate(raw)
	if err != nil {
		t.Fatalf("RawToClimate: %v", err)
	}

	// P1/Monday round-trip must preserve the non-base period.
	prof := rebuilt.Profiles["P1"]
	if prof == nil {
		t.Fatal("P1 missing after round-trip")
	}
	mon, ok := prof.Days[schedule.WeekdayMonday]
	if !ok {
		t.Fatal("MONDAY missing after round-trip")
	}
	// BaseTemperature after round-trip is inferred by identify_base_temperature:
	// the temperature occupying the most minutes (21°C = 900 min > 18°C = 540 min).
	if mon.BaseTemperature != 21 {
		t.Errorf("inferred base temp = %.1f, want 21 (most minutes)", mon.BaseTemperature)
	}
	// The 18°C windows must appear as non-base periods in the round-tripped value.
	if len(mon.Periods) == 0 {
		t.Fatal("expected at least one period after round-trip")
	}
}

func TestClimateToRawNilReturnsError(t *testing.T) {
	t.Parallel()
	if _, err := ClimateToRaw(nil); err == nil {
		t.Fatal("expected error for nil climate")
	}
}

func TestRawToClimateEmptyReturnsEmptyClimate(t *testing.T) {
	t.Parallel()
	got, err := RawToClimate(rawClimateSchedule{})
	if err != nil {
		t.Fatalf("RawToClimate(empty): %v", err)
	}
	if len(got.Profiles) != 0 {
		t.Errorf("expected empty profiles, got %d", len(got.Profiles))
	}
}

// ---------------------------------------------------------------------------
// climateWeekdayToSlots — gap filling and base-temp fill
// ---------------------------------------------------------------------------

func TestClimateWeekdayToSlotsFillsGapBefore(t *testing.T) {
	t.Parallel()
	cwd := schedule.ClimateWeekday{
		BaseTemperature: 18,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "07:00", Temperature: 18},
			{StartTime: "07:00", EndTime: "22:00", Temperature: 21},
			{StartTime: "22:00", EndTime: "24:00", Temperature: 18},
		},
	}
	slots, err := climateWeekdayToSlots(cwd)
	if err != nil {
		t.Fatalf("climateWeekdayToSlots: %v", err)
	}
	if len(slots) != 13 {
		t.Fatalf("expected 13 slots, got %d", len(slots))
	}
}

func TestClimateWeekdayToSlotsEmptyPeriods(t *testing.T) {
	t.Parallel()
	// No periods: entire day at base temperature.
	cwd := schedule.ClimateWeekday{BaseTemperature: 20}
	slots, err := climateWeekdayToSlots(cwd)
	if err != nil {
		t.Fatalf("climateWeekdayToSlots(empty): %v", err)
	}
	if len(slots) != 13 {
		t.Fatalf("expected 13 slots, got %d", len(slots))
	}
	// All slots must end at 24:00 (constant day).
	for no, s := range slots {
		if s.EndTime != "24:00" {
			t.Errorf("slot %d: EndTime = %s, want 24:00", no, s.EndTime)
		}
	}
}

// ---------------------------------------------------------------------------
// WeekdayBitmaskToList / WeekdayListToBitmask
// ---------------------------------------------------------------------------

func TestWeekdayBitmaskRoundTrip(t *testing.T) {
	t.Parallel()
	days := []schedule.Weekday{schedule.WeekdayMonday, schedule.WeekdayFriday}
	mask := WeekdayListToBitmask(days)
	if mask == 0 {
		t.Fatal("expected non-zero mask")
	}
	got := WeekdayBitmaskToList(mask)
	if len(got) != 2 {
		t.Fatalf("expected 2 days, got %v", got)
	}
	found := map[schedule.Weekday]bool{}
	for _, d := range got {
		found[d] = true
	}
	for _, want := range days {
		if !found[want] {
			t.Errorf("day %s missing from round-trip", want)
		}
	}
}

func TestWeekdayBitmaskAllDays(t *testing.T) {
	t.Parallel()
	mask := WeekdayListToBitmask(schedule.Weekdays)
	got := WeekdayBitmaskToList(mask)
	if len(got) != 7 {
		t.Errorf("all 7 days: got %d, want 7", len(got))
	}
}

func TestWeekdayBitmaskZero(t *testing.T) {
	t.Parallel()
	if got := WeekdayBitmaskToList(0); len(got) != 0 {
		t.Errorf("mask=0: expected empty, got %v", got)
	}
	if mask := WeekdayListToBitmask(nil); mask != 0 {
		t.Errorf("nil list: expected 0, got %d", mask)
	}
}

// ---------------------------------------------------------------------------
// ParseSimpleRawParamset / BuildSimpleRawParamset round-trip
// ---------------------------------------------------------------------------

func TestSimpleParamsetRoundTrip(t *testing.T) {
	t.Parallel()
	s := schedule.NewSimple()
	_ = s.Put(1, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday, schedule.WeekdayFriday},
		Time:     "07:30",
		Level:    1,
	})
	_ = s.Put(2, schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdaySaturday},
		Time:     "09:00",
		Level:    0.5,
	})

	raw, err := BuildSimpleRawParamset(s, schedule.SimpleMaxSlot, nil, AstroOffsetLimits{})
	if err != nil {
		t.Fatalf("BuildSimpleRawParamset: %v", err)
	}

	// Groups 1 and 2 must be non-zero; rest must be zeroed.
	if raw["01_WP_WEEKDAY"] == 0 {
		t.Error("group 1 weekday must be non-zero")
	}
	if raw["02_WP_WEEKDAY"] == 0 {
		t.Error("group 2 weekday must be non-zero")
	}
	if raw["03_WP_WEEKDAY"] != 0 {
		t.Error("group 3 weekday must be zero")
	}

	got, err := ParseSimpleRawParamset(raw, nil)
	if err != nil {
		t.Fatalf("ParseSimpleRawParamset: %v", err)
	}
	slots := got.Slots()
	if len(slots) != 2 {
		t.Fatalf("expected 2 active slots, got %v", slots)
	}
}

// TestSimpleParamsetColorRoundTrip asserts the universal-light colour
// fields round-trip losslessly through the model path (parse → build),
// including a legitimate 0 value, and that a colour-less group emits no
// colour key.
func TestSimpleParamsetColorRoundTrip(t *testing.T) {
	t.Parallel()
	ct, cv := 2, 524288
	raw := map[string]any{
		"03_WP_WEEKDAY":      2,
		"03_WP_FIXED_HOUR":   7,
		"03_WP_FIXED_MINUTE": 30,
		"03_WP_LEVEL":        1.0,
		"03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":  ct,
		"03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE": cv,
	}
	s, err := ParseSimpleRawParamset(raw, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry, ok := s.Entries[3]
	if !ok {
		t.Fatal("group 3 missing")
	}
	if entry.ColorType == nil || *entry.ColorType != ct || entry.ColorValue == nil || *entry.ColorValue != cv {
		t.Fatalf("colour not parsed: %+v", entry)
	}

	out, err := BuildSimpleRawParamset(s, schedule.SimpleMaxSlot, nil, AstroOffsetLimits{})
	if err != nil {
		t.Fatalf("BuildSimpleRawParamset: %v", err)
	}
	if out["03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"] != ct ||
		out["03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"] != cv {
		t.Errorf("colour not re-emitted: %v / %v",
			out["03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"],
			out["03_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"])
	}
	// A colour-less active group emits no colour key.
	if _, present := out["01_WP_HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"]; present {
		t.Error("colour-less group must not carry a colour key")
	}
}

func TestParseSimpleRawParamsetSkipsInactiveGroups(t *testing.T) {
	t.Parallel()
	// Group 5 WEEKDAY = 0 → inactive.
	raw := map[string]any{
		"05_WP_WEEKDAY":      0,
		"05_WP_FIXED_HOUR":   8,
		"05_WP_FIXED_MINUTE": 0,
		"05_WP_LEVEL":        1.0,
	}
	got, err := ParseSimpleRawParamset(raw, nil)
	if err != nil {
		t.Fatalf("ParseSimpleRawParamset: %v", err)
	}
	if len(got.Slots()) != 0 {
		t.Errorf("expected 0 active slots, got %v", got.Slots())
	}
}

func TestBuildSimpleRawParamsetNilIsAllZeros(t *testing.T) {
	t.Parallel()
	raw, err := BuildSimpleRawParamset(nil, schedule.SimpleMaxSlot, nil, AstroOffsetLimits{})
	if err != nil {
		t.Fatalf("BuildSimpleRawParamset: %v", err)
	}
	for no := 1; no <= 24; no++ {
		key := fmt.Sprintf("%02d_WP_WEEKDAY", no)
		if raw[key] != 0 {
			t.Errorf("group %d weekday must be 0 for nil schedule", no)
		}
	}
}

// ---------------------------------------------------------------------------
// IsSimpleGroupActive
// ---------------------------------------------------------------------------

func TestIsSimpleGroupActive(t *testing.T) {
	t.Parallel()
	if IsSimpleGroupActive(0) {
		t.Error("mask 0 must be inactive")
	}
	if !IsSimpleGroupActive(2) {
		t.Error("mask 2 must be active")
	}
}

// ---------------------------------------------------------------------------
// ClimateToRawWire
// ---------------------------------------------------------------------------

func TestClimateToRawWireRoundtrip(t *testing.T) {
	t.Parallel()
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: 18.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "06:00", EndTime: "22:00", Temperature: 21.0},
		},
	}
	c.Profiles["P1"] = prof

	raw, err := ClimateToRawWire(c)
	if err != nil {
		t.Fatalf("ClimateToRawWire: %v", err)
	}
	if raw == nil {
		t.Fatal("raw must not be nil")
	}
	if _, ok := raw["P1"]; !ok {
		t.Fatal("raw must contain P1 key")
	}
}

func TestClimateToRawWireNilReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ClimateToRawWire(nil)
	if err == nil {
		t.Fatal("nil climate must return error")
	}
}

// ---------------------------------------------------------------------------
// climateWeekdayToSlotsWire — valid and too-many-periods paths
// ---------------------------------------------------------------------------

func TestClimateWeekdayToSlotsWireValid(t *testing.T) {
	t.Parallel()
	// ValidateWire allows partial-day coverage (unlike Validate which requires 24h).
	cwd := schedule.ClimateWeekday{
		BaseTemperature: 17.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "08:00", EndTime: "20:00", Temperature: 22.0},
		},
	}
	slots, err := climateWeekdayToSlotsWire(cwd)
	if err != nil {
		t.Fatalf("climateWeekdayToSlotsWire: %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("must produce at least one slot")
	}
}

func TestClimateWeekdayToSlotsWireInvalid(t *testing.T) {
	t.Parallel()
	// Too many periods violates structural check.
	periods := make([]schedule.ClimatePeriod, schedule.MaxClimatePeriods+1)
	for i := range periods {
		periods[i] = schedule.ClimatePeriod{
			StartTime:   "00:00",
			EndTime:     "01:00",
			Temperature: 18,
		}
	}
	cwd := schedule.ClimateWeekday{BaseTemperature: 18.0, Periods: periods}
	_, err := climateWeekdayToSlotsWire(cwd)
	if err == nil {
		t.Fatal("too many periods must produce error")
	}
}

// ---------------------------------------------------------------------------
// toInt / toFloat — string branch
// ---------------------------------------------------------------------------

func TestToIntStringBranch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want int
	}{
		{"42", 42},
		{int(7), 7},
		{float64(3.9), 3},
		{"bad", 0}, // non-numeric string
	}
	for _, c := range cases {
		if got := toInt(c.in); got != c.want {
			t.Fatalf("toInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestToFloatStringBranch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want float64
	}{
		{"3.14", 3.14},
		{float64(2.5), 2.5},
		{int(4), 4.0},
		{"bad", 0.0},
	}
	for _, c := range cases {
		if got := toFloat(c.in); got != c.want {
			t.Fatalf("toFloat(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// mapToProfileKey — all type branches
// ---------------------------------------------------------------------------

// TestMapToProfileKeyAllBranches walks every wire type the pointer can arrive
// as. The base comes from the parameter, not from the type: ACTIVE_PROFILE is
// declared 1-based and WEEK_PROGRAM_POINTER 0-based, so the same int32(0)
// means "no such profile" on one and "the first week program" on the other.
func TestMapToProfileKeyAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param hmenum.Parameter
		in    any
		want  string
	}{
		{hmenum.ParameterActiveProfile, nil, ""},
		{hmenum.ParameterActiveProfile, int(1), "P1"},
		{hmenum.ParameterActiveProfile, int(0), ""}, // below range
		{hmenum.ParameterActiveProfile, int(7), ""}, // above range
		{hmenum.ParameterActiveProfile, int32(3), "P3"},
		{hmenum.ParameterActiveProfile, int32(0), ""},
		{hmenum.ParameterActiveProfile, int64(6), "P6"},
		{hmenum.ParameterActiveProfile, int64(7), ""},
		{hmenum.ParameterActiveProfile, float64(2), "P2"},
		{hmenum.ParameterActiveProfile, float64(0), ""},
		{hmenum.ParameterActiveProfile, "bad", ""},
		{hmenum.ParameterWeekProgramPointer, int32(0), "P1"}, // 0-based → P1
		{hmenum.ParameterWeekProgramPointer, int32(2), "P3"},
		{hmenum.ParameterWeekProgramPointer, "0", "P1"}, // 0-based string → P1
		{hmenum.ParameterWeekProgramPointer, "5", "P6"}, // 0-based string → P6
		{hmenum.ParameterWeekProgramPointer, "6", ""},   // 0-based out of range
		{hmenum.ParameterWeekProgramPointer, "bad", ""},
		{hmenum.ParameterLevel, int32(1), ""}, // not a profile pointer
	}
	for _, c := range cases {
		got := mapToProfileKey(c.param, c.in)
		if got != c.want {
			t.Errorf("mapToProfileKey(%s, %#v) = %q, want %q", c.param, c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// profile-key grammar — invalid formats
// ---------------------------------------------------------------------------

func TestProfileKeyGrammarInvalid(t *testing.T) {
	t.Parallel()
	invalid := []string{"", "P", "P0", "P7", "X1", "1"}
	for _, k := range invalid {
		if schedule.IsValidProfileKey(k) {
			t.Errorf("schedule.IsValidProfileKey(%q) = true, want false", k)
		}
	}
	valid := []string{"P1", "P2", "P6"}
	for _, k := range valid {
		if !schedule.IsValidProfileKey(k) {
			t.Errorf("schedule.IsValidProfileKey(%q) = false, want true", k)
		}
	}
}

// ---------------------------------------------------------------------------
// isValidWeekday — valid + invalid
// ---------------------------------------------------------------------------

func TestIsValidWeekday(t *testing.T) {
	t.Parallel()
	for _, wd := range schedule.Weekdays {
		if !isValidWeekday(wd) {
			t.Errorf("isValidWeekday(%q) = false, want true", wd)
		}
	}
	if isValidWeekday(schedule.Weekday("UNKNOWN")) {
		t.Fatal("unknown weekday must return false")
	}
}

// ---------------------------------------------------------------------------
// climateWeekdayToSlots — validation failure path
// ---------------------------------------------------------------------------

func TestClimateWeekdayToSlotsInvalid(t *testing.T) {
	t.Parallel()
	// Overlap between periods causes Validate to fail.
	cwd := schedule.ClimateWeekday{
		BaseTemperature: 18.0,
		Periods: []schedule.ClimatePeriod{
			{StartTime: "06:00", EndTime: "12:00", Temperature: 21.0},
			{StartTime: "10:00", EndTime: "22:00", Temperature: 20.0}, // overlaps
		},
	}
	_, err := climateWeekdayToSlots(cwd)
	if err == nil {
		t.Fatal("overlapping periods must produce error")
	}
}

// ---------------------------------------------------------------------------
// RawToClimate — invalid profile key and invalid weekday
// ---------------------------------------------------------------------------

func TestRawToClimateInvalidProfileKey(t *testing.T) {
	t.Parallel()
	raw := rawClimateSchedule{
		"INVALID": profileSlots{},
	}
	_, err := RawToClimate(raw)
	if err == nil {
		t.Fatal("invalid profile key must return error")
	}
}

func TestRawToClimateInvalidWeekday(t *testing.T) {
	t.Parallel()
	raw := rawClimateSchedule{
		"P1": profileSlots{
			"BADDAY": nil,
		},
	}
	_, err := RawToClimate(raw)
	if err == nil {
		t.Fatal("invalid weekday must return error")
	}
}

// ---------------------------------------------------------------------------
// Door-lock action encoding
// ---------------------------------------------------------------------------

// TestBuildSimpleRawParamsetWritesEveryLockActionDuration walks the door-lock
// actions through the two hops the write path uses — the wire triplet is
// rendered as a duration string and the encoder turns it back into
// DURATION_BASE / DURATION_FACTOR — and asserts each action reaches the
// paramset as the pair its table entry declares.
//
// putParamset is a sparse merge, so an omitted key leaves whatever the
// device holds. `lock_autorelock_start` is the one action whose encoding is
// (0, 0), and dropping those keys left the slot on the firmware default
// (7, 31) — the encoding of `lock_autorelock_end`, i.e. auto-relock
// switched off rather than on, with the read-back flipping the operator's
// choice straight back.
func TestBuildSimpleRawParamsetWritesEveryLockActionDuration(t *testing.T) {
	t.Parallel()

	for action, want := range schedule.LockActionTable {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			// Filled the way the write path fills it: the DTO is mapped
			// onto the entry map directly. The model validator caps a
			// duration factor at 30 and would reject the lock domain's
			// own "permanent" sentinel (31), so going through Put would
			// test a path no lock save takes.
			s := schedule.NewSimple()
			s.Entries[1] = schedule.SimpleEntry{
				Weekdays:       []schedule.Weekday{schedule.WeekdayMonday},
				Time:           "07:30",
				Level:          want.Level(),
				Duration:       FormatTimeBaseFactor(want.DurBase(), want.DurFactor()),
				TargetChannels: []string{"1_1"},
			}

			raw, err := BuildSimpleRawParamset(s, schedule.SimpleMaxSlot, nil, AstroOffsetLimits{})
			if err != nil {
				t.Fatalf("BuildSimpleRawParamset: %v", err)
			}
			base, baseSet := raw["01_WP_DURATION_BASE"]
			factor, factorSet := raw["01_WP_DURATION_FACTOR"]
			if !baseSet || !factorSet {
				t.Fatalf("DURATION_BASE/FACTOR not written — the CCU keeps its stale pair, which reads back as a different action")
			}
			if base != want.DurBase() || factor != want.DurFactor() {
				t.Errorf("DURATION = (%v, %v), want (%d, %d)", base, factor, want.DurBase(), want.DurFactor())
			}
			// The read side has to agree, or the operator's choice
			// silently flips back on the next load.
			if got := schedule.DetectLockAction(want.Level(), want.DurBase(), want.DurFactor()); got != action {
				t.Errorf("DetectLockAction(%v, %d, %d) = %v, want %v", want.Level(), want.DurBase(), want.DurFactor(), got, action)
			}
		})
	}
}
