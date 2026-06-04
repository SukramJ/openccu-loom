// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// rawconvert.go — round-trip helpers between the CCU flat paramset format
// and the in-memory [schedule.Climate] / [schedule.Simple] models.
//
// CLIMATE PARAMSET FORMAT (CCU):
//   "P1_TEMPERATURE_MONDAY_1" : 18.0
//   "P1_ENDTIME_MONDAY_1"     : 360      ← integer minutes since midnight
//
// SIMPLE (NON-CLIMATE) PARAMSET FORMAT (CCU):
//   "01_WP_WEEKDAY"    : 127   ← bitwise weekday mask (Mon=2, …, Sun=64)
//   "01_WP_FIXED_HOUR" : 7
//   "01_WP_FIXED_MINUTE": 30
//   "01_WP_LEVEL"      : 1.0
//
// These helpers are the Go port of the corresponding static methods on
// `ClimateWeekProfile` and `DefaultWeekProfile` in

package weekprofile

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// ---------------------------------------------------------------------------
// Climate paramset patterns
// ---------------------------------------------------------------------------

// climateParamPattern matches "P1_TEMPERATURE_MONDAY_1" (case-sensitive).
// Group 1 = profile, 2 = field-type (TEMPERATURE|ENDTIME), 3 = weekday, 4 = slot.
var climateParamPattern = regexp.MustCompile(
	`^(P[1-6])_(TEMPERATURE|ENDTIME)_([A-Z]+)_(\d+)$`,
)

// ---------------------------------------------------------------------------
// Climate: raw paramset → [rawClimateSchedule]
// ---------------------------------------------------------------------------

// ParseClimateRawParamset converts the flat CCU MASTER paramset dictionary
// for a climate device into the internal (profile → weekday → slot) map.
// Slots with ENDTIME are converted from CCU integer minutes to "HH:MM".
// Unrecognised keys are silently ignored.
//
// Mirrors `ClimateWeekProfile.convert_raw_to_dict_schedule` in
func ParseClimateRawParamset(raw map[string]any) (rawClimateSchedule, error) { //nolint:revive // rawClimateSchedule is a type alias for a concrete map type; callers see the underlying type.
	out := make(rawClimateSchedule)
	for key, val := range raw {
		m := climateParamPattern.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		profile, fieldType, weekday, slotStr := m[1], m[2], m[3], m[4]
		slotNo, err := strconv.Atoi(slotStr)
		if err != nil || slotNo < 1 || slotNo > slotCount {
			continue
		}
		if _, ok := out[profile]; !ok {
			out[profile] = make(profileSlots)
		}
		if _, ok := out[profile][weekday]; !ok {
			out[profile][weekday] = make(weekdaySlots)
		}
		slot := out[profile][weekday][slotNo]
		switch fieldType {
		case "ENDTIME":
			ts, err := ParseSlotTime(val)
			if err != nil {
				continue
			}
			slot.EndTime = ts
		case "TEMPERATURE":
			switch v := val.(type) {
			case float64:
				slot.Temperature = v
			case int:
				slot.Temperature = float64(v)
			default:
				continue
			}
		}
		out[profile][weekday][slotNo] = slot
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Climate: [rawClimateSchedule] → flat paramset
// ---------------------------------------------------------------------------

// BuildClimateRawParamset converts the internal (profile → weekday → slot) map
// back to the flat CCU paramset dictionary expected by put_paramset / MASTER.
// ENDTIME values are converted to integer minutes.
//
// Mirrors `ClimateWeekProfile.convert_dict_to_raw_schedule` in
func BuildClimateRawParamset(sched rawClimateSchedule) (map[string]any, error) {
	out := make(map[string]any)
	for profile, ps := range sched {
		for weekday, ws := range ps {
			for slotNo, slot := range ws {
				key := fmt.Sprintf("%s_TEMPERATURE_%s_%d", profile, weekday, slotNo)
				out[key] = slot.Temperature

				endMins := toMinutes(slot.EndTime)
				if endMins < 0 {
					return nil, fmt.Errorf("weekprofile: invalid endtime %q in %s/%s slot %d",
						slot.EndTime, profile, weekday, slotNo)
				}
				out[fmt.Sprintf("%s_ENDTIME_%s_%d", profile, weekday, slotNo)] = endMins
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Climate: [schedule.Climate] ↔ [rawClimateSchedule]
// ---------------------------------------------------------------------------

// ClimateToRaw converts the domain model ([schedule.Climate]) to the CCU
// internal 13-slot raw schedule. For each ClimateWeekday, the periods list
// is expanded into full-day 13-slot coverage (base temperature fills gaps).
//
// Mirrors `_validate_and_convert_simple_to_weekday` and the path through
// `set_schedule` / `set_profile` / `set_weekday` in
func ClimateToRaw(c *schedule.Climate) (rawClimateSchedule, error) { //nolint:revive // rawClimateSchedule is a type alias for a concrete map type; callers see the underlying type.
	if c == nil {
		return nil, fmt.Errorf("weekprofile: nil climate schedule")
	}
	out := make(rawClimateSchedule)
	for _, profileKey := range c.Keys() {
		prof := c.Profiles[profileKey]
		if prof == nil {
			continue
		}
		ps := make(profileSlots)
		for day, sched := range prof.Days {
			slots, err := climateWeekdayToSlots(sched)
			if err != nil {
				return nil, fmt.Errorf("weekprofile: %s/%s: %w", profileKey, day, err)
			}
			ps[string(day)] = slots
		}
		out[profileKey] = ps
	}
	return out, nil
}

// ClimateToRawWire is the wire-form variant of [ClimateToRaw]. It uses
// [ClimateWeekday.ValidateWire] (structural checks only, no 24-hour
// coverage rule) so partial-day period sets produced by a single-profile
// copy from the CCU pass validation. Gaps are still expanded to base
// temperature, producing a well-formed 13-slot CCU payload.
//
// Use [ClimateToRaw] for the full round-trip write path (e.g.
// [SetSchedule]); use ClimateToRawWire for [CopyProfileTo] where the
// source is authoritative CCU data that may not span the whole day.
func ClimateToRawWire(c *schedule.Climate) (rawClimateSchedule, error) { //nolint:revive // rawClimateSchedule is a type alias; callers see the underlying type.
	if c == nil {
		return nil, fmt.Errorf("weekprofile: nil climate schedule")
	}
	out := make(rawClimateSchedule)
	for _, profileKey := range c.Keys() {
		prof := c.Profiles[profileKey]
		if prof == nil {
			continue
		}
		ps := make(profileSlots)
		for day, sched := range prof.Days {
			slots, err := climateWeekdayToSlotsWire(sched)
			if err != nil {
				return nil, fmt.Errorf("weekprofile: %s/%s: %w", profileKey, day, err)
			}
			ps[string(day)] = slots
		}
		out[profileKey] = ps
	}
	return out, nil
}

// RawToClimate converts the CCU 13-slot internal structure into the
// domain model ([schedule.Climate]). Each weekday is converted via slot
// filtering and the identify-base-temperature logic so the result contains
// non-redundant [schedule.ClimatePeriod] values only.
//
// Note: the resulting [schedule.ClimateWeekday] values hold only the non-base
// temperature periods (gaps are implicit). They are stored directly into the
// profile's Days map without running [ClimateWeekday.Validate], because the
// CCU raw data is authoritative and may represent a constant-temperature day
// with a single non-base window that does not satisfy the full-coverage rule
// required by Validate.
//
// Mirrors `_validate_and_convert_weekday_to_simple` (and upwards) in
func RawToClimate(raw rawClimateSchedule) (*schedule.Climate, error) {
	c := schedule.NewClimate()
	for profileKey, ps := range raw {
		if !isValidProfileKey(profileKey) {
			return nil, fmt.Errorf("weekprofile: invalid profile key %q", profileKey)
		}
		prof := schedule.NewClimateProfile()
		for weekday, ws := range ps {
			cwd := slotsToClimateWeekday(ws)
			wd := schedule.Weekday(weekday)
			if !isValidWeekday(wd) {
				return nil, fmt.Errorf("weekprofile: %s: invalid weekday %q", profileKey, weekday)
			}
			// Store directly — bypasses full-coverage Validate because the CCU
			// data is authoritative and the "periods" here are non-base windows only.
			prof.Days[wd] = cwd
		}
		c.Profiles[profileKey] = prof
	}
	return c, nil
}

// isValidProfileKey mirrors the schedule package helper but is package-local
// to avoid importing it.
func isValidProfileKey(k string) bool {
	if len(k) < 2 || k[0] != 'P' {
		return false
	}
	var n int
	if _, err := fmt.Sscanf(k, "P%d", &n); err != nil {
		return false
	}
	return n >= 1 && n <= 6
}

// isValidWeekday checks whether a string is a known CCU weekday.
func isValidWeekday(w schedule.Weekday) bool {
	return slices.Contains(schedule.Weekdays, w)
}

// ---------------------------------------------------------------------------
// Climate helper: [schedule.ClimateWeekday] ↔ [weekdaySlots]
// ---------------------------------------------------------------------------

// climateWeekdayToSlots expands a [schedule.ClimateWeekday] (base temperature
// + non-overlapping periods) into the CCU 13-slot representation. Gaps between
// periods are filled with the base temperature; the result always has exactly 13
// slots ordered by end-time.
func climateWeekdayToSlots(cwd schedule.ClimateWeekday) (weekdaySlots, error) {
	if err := cwd.Validate(); err != nil {
		return nil, err
	}
	return climateWeekdayToSlotsExpand(cwd)
}

// climateWeekdayToSlotsWire is the wire-form variant of
// [climateWeekdayToSlots]. It applies [ClimateWeekday.ValidateWire]
// (structural checks, no 24-hour coverage rule) and then expands gaps
// with the base temperature exactly as [climateWeekdayToSlots] does.
// Use this for the single-profile copy path where the source data is
// read from the CCU and may not cover the full day.
func climateWeekdayToSlotsWire(cwd schedule.ClimateWeekday) (weekdaySlots, error) {
	if err := cwd.ValidateWire(); err != nil {
		return nil, err
	}
	return climateWeekdayToSlotsExpand(cwd)
}

// climateWeekdayToSlotsExpand performs the shared slot-expansion logic
// for [climateWeekdayToSlots] and [climateWeekdayToSlotsWire]. The
// caller is responsible for validation.
func climateWeekdayToSlotsExpand(cwd schedule.ClimateWeekday) (weekdaySlots, error) {
	type period struct {
		startMins int
		endMins   int
		temp      float64
	}
	sorted := make([]period, 0, len(cwd.Periods))
	for _, p := range cwd.Periods {
		sorted = append(sorted, period{
			startMins: toMinutes(p.StartTime),
			endMins:   toMinutes(p.EndTime),
			temp:      p.Temperature,
		})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].startMins < sorted[j].startMins })

	ws := make(weekdaySlots)
	slotNo := 1
	prevEnd := 0

	for _, p := range sorted {
		if p.startMins > prevEnd {
			// Gap before this period — fill with base temperature.
			endStr, _ := minutesToTimeStr(p.startMins)
			ws[slotNo] = ScheduleSlot{EndTime: endStr, Temperature: cwd.BaseTemperature}
			slotNo++
		}
		endStr, _ := minutesToTimeStr(p.endMins)
		ws[slotNo] = ScheduleSlot{EndTime: endStr, Temperature: p.temp}
		slotNo++
		prevEnd = p.endMins
	}

	if prevEnd < 24*60 {
		// Trailing gap — fill to 24:00 with base temperature.
		ws[slotNo] = ScheduleSlot{EndTime: maxScheduleTime, Temperature: cwd.BaseTemperature}
	}

	return fillUpWeekdaySlots(cwd.BaseTemperature, ws), nil
}

// slotsToClimateWeekday converts a 13-slot CCU map back to a
// [schedule.ClimateWeekday]. Consecutive slots with the same temperature are
// merged; base temperature segments are excluded from the periods list.
//
// Mirrors `_validate_and_convert_weekday_to_simple`.
func slotsToClimateWeekday(ws weekdaySlots) schedule.ClimateWeekday {
	if len(ws) == 0 {
		return schedule.ClimateWeekday{}
	}
	baseTemp := identifyBaseTemperature(ws)
	filtered := filterWeekdaySlots(ws)

	type numbered struct {
		no   int
		slot ScheduleSlot
	}
	ordered := make([]numbered, 0, len(filtered))
	for no, s := range filtered {
		ordered = append(ordered, numbered{no, s})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].no < ordered[j].no })

	var periods []schedule.ClimatePeriod
	prevEnd := minScheduleTime

	type openRange struct {
		start string
		end   string
		temp  float64
	}
	var current *openRange

	for _, n := range ordered {
		endStr := n.slot.EndTime
		temp := n.slot.Temperature

		if temp != baseTemp {
			if current == nil {
				current = &openRange{start: prevEnd, end: endStr, temp: temp}
			} else if temp == current.temp {
				current.end = endStr
			} else {
				periods = append(periods, schedule.ClimatePeriod{
					StartTime:   current.start,
					EndTime:     current.end,
					Temperature: current.temp,
				})
				current = &openRange{start: prevEnd, end: endStr, temp: temp}
			}
		} else if current != nil {
			periods = append(periods, schedule.ClimatePeriod{
				StartTime:   current.start,
				EndTime:     current.end,
				Temperature: current.temp,
			})
			current = nil
		}
		prevEnd = endStr
		if endStr == maxScheduleTime {
			break
		}
	}
	if current != nil {
		periods = append(periods, schedule.ClimatePeriod{
			StartTime:   current.start,
			EndTime:     current.end,
			Temperature: current.temp,
		})
	}

	return schedule.ClimateWeekday{BaseTemperature: baseTemp, Periods: periods}
}

// ---------------------------------------------------------------------------
// Simple / non-climate: raw paramset ↔ [schedule.Simple]
// ---------------------------------------------------------------------------

// weekdayBitMap maps CCU bit positions to weekday strings.
// Bit 1 = Monday, 2 = Tuesday, …, 7 = Sunday (1-indexed bits).
var weekdayBitMap = []schedule.Weekday{
	1: schedule.WeekdayMonday,
	2: schedule.WeekdayTuesday,
	3: schedule.WeekdayWednesday,
	4: schedule.WeekdayThursday,
	5: schedule.WeekdayFriday,
	6: schedule.WeekdaySaturday,
	7: schedule.WeekdaySunday,
}

// WeekdayBitmaskToList converts a CCU WEEKDAY bitmask to a list of weekday strings.
// Mirrors `_bitwise_to_list(value, WeekdayInt)`.
func WeekdayBitmaskToList(mask int) []schedule.Weekday {
	var out []schedule.Weekday
	for bit := 1; bit <= 7; bit++ {
		if mask&(1<<bit) != 0 {
			out = append(out, weekdayBitMap[bit])
		}
	}
	return out
}

// WeekdayListToBitmask converts a list of weekday strings to the CCU bitmask.
// Mirrors `_list_to_bitwise`.
func WeekdayListToBitmask(days []schedule.Weekday) int {
	bitPos := map[schedule.Weekday]int{
		schedule.WeekdayMonday:    1,
		schedule.WeekdayTuesday:   2,
		schedule.WeekdayWednesday: 3,
		schedule.WeekdayThursday:  4,
		schedule.WeekdayFriday:    5,
		schedule.WeekdaySaturday:  6,
		schedule.WeekdaySunday:    7,
	}
	mask := 0
	for _, day := range days {
		if bit, ok := bitPos[day]; ok {
			mask |= 1 << bit
		}
	}
	return mask
}

// conditionStringFromInt maps the CCU integer CONDITION value to a
// [schedule.Condition] string. Mirrors the Python
// `_CONDITION_STR_TO_ENUM` reverse mapping in schedule_models.py.
var conditionFromInt = map[int]schedule.Condition{
	0: schedule.ConditionFixedTime,
	1: schedule.ConditionAstro,
	2: schedule.ConditionAstroBeforeFixed,
	3: schedule.ConditionAstroAfterFixed,
	4: schedule.ConditionFixedBetweenAstro,
	5: schedule.ConditionAstroBetweenFixed,
	6: schedule.ConditionAstroBetweenAstro,
	7: schedule.ConditionFixedAstroThreshold,
}

// conditionToInt maps a [schedule.Condition] to the CCU integer value.
var conditionToInt = func() map[schedule.Condition]int {
	m := make(map[schedule.Condition]int, len(conditionFromInt))
	for k, v := range conditionFromInt {
		m[v] = k
	}
	return m
}()

// ParseSimpleRawParamset converts the flat CCU MASTER paramset for a
// non-climate device into a [schedule.Simple]. Keys follow the pattern
// "NN_WP_FIELD". Groups with WEEKDAY == 0 are treated as inactive and
// are not included in the result.
//
// All 13 optional schedule fields (CONDITION, ASTRO_TYPE, ASTRO_OFFSET,
// TARGET_CHANNELS, LEVEL_2, DURATION_BASE, DURATION_FACTOR,
// RAMP_TIME_BASE, RAMP_TIME_FACTOR) are decoded when present so that a
// round-trip through ParseSimpleRawParamset → BuildSimpleRawParamset
// produces a semantically equivalent paramset.
//
// Mirrors `DefaultWeekProfile.convert_raw_to_dict_schedule`.
func ParseSimpleRawParamset(raw map[string]any) (*schedule.Simple, error) {
	type group struct {
		weekday        int
		fixedHour      int
		fixedMinute    int
		level          float64
		level2         *float64
		condition      schedule.Condition
		astroType      schedule.Astro
		astroOffset    int
		targetChannels int
		durationBase   int
		durationFactor int
		rampBase       int
		rampFactor     int
	}
	groups := make(map[int]*group)

	for key, val := range raw {
		// Expected format: "01_WP_FIELDNAME"
		parts := strings.SplitN(key, "_", 3)
		if len(parts) != 3 || parts[1] != "WP" {
			continue
		}
		groupNo, err := strconv.Atoi(parts[0])
		if err != nil || groupNo < 1 || groupNo > 24 {
			continue
		}
		if _, ok := groups[groupNo]; !ok {
			groups[groupNo] = &group{condition: schedule.ConditionFixedTime}
		}
		g := groups[groupNo]
		switch parts[2] {
		case "WEEKDAY":
			g.weekday = toInt(val)
		case "FIXED_HOUR":
			g.fixedHour = toInt(val)
		case "FIXED_MINUTE":
			g.fixedMinute = toInt(val)
		case "LEVEL":
			g.level = toFloat(val)
		case "LEVEL_2":
			v := toFloat(val)
			g.level2 = &v
		case "CONDITION":
			if c, ok := conditionFromInt[toInt(val)]; ok {
				g.condition = c
			}
		case "ASTRO_TYPE":
			if toInt(val) == 1 {
				g.astroType = schedule.AstroSunset
			} else {
				g.astroType = schedule.AstroSunrise
			}
		case "ASTRO_OFFSET":
			g.astroOffset = toInt(val)
		case "TARGET_CHANNELS":
			g.targetChannels = toInt(val)
		case "DURATION_BASE":
			g.durationBase = toInt(val)
		case "DURATION_FACTOR":
			g.durationFactor = toInt(val)
		case "RAMP_TIME_BASE":
			g.rampBase = toInt(val)
		case "RAMP_TIME_FACTOR":
			g.rampFactor = toInt(val)
		}
	}

	s := schedule.NewSimple()
	for groupNo, g := range groups {
		if g.weekday == 0 {
			continue // inactive group
		}
		days := WeekdayBitmaskToList(g.weekday)
		if len(days) == 0 {
			continue
		}
		timeStr := fmt.Sprintf("%02d:%02d", g.fixedHour, g.fixedMinute)
		entry := schedule.SimpleEntry{
			Weekdays:           days,
			Time:               timeStr,
			Level:              g.level,
			Level2:             g.level2,
			Condition:          g.condition,
			AstroType:          g.astroType,
			AstroOffsetMinutes: g.astroOffset,
			TargetChannels:     TargetChannelsBitmaskToList(g.targetChannels),
		}
		if g.durationFactor > 0 {
			entry.Duration = FormatTimeBaseFactor(g.durationBase, g.durationFactor)
		}
		if g.rampFactor > 0 {
			entry.RampTime = FormatTimeBaseFactor(g.rampBase, g.rampFactor)
		}
		if err := s.Put(groupNo, entry); err != nil {
			// Skip entries that fail basic validation rather than aborting.
			continue
		}
	}
	return s, nil
}

// BuildSimpleRawParamset converts a [schedule.Simple] to the CCU flat
// paramset dictionary. All 13 schedule fields are written for active
// groups. Groups not present in the schedule are zeroed on WEEKDAY and
// TARGET_CHANNELS so the CCU deactivates them; the other optional fields
// (CONDITION, ASTRO_*, DURATION_*, RAMP_TIME_*, LEVEL_2) are not written
// for inactive groups to avoid spurious -5 faults on devices whose
// MASTER paramset description omits those fields.
//
// Mirrors `DefaultWeekProfile.convert_dict_to_raw_schedule`.
func BuildSimpleRawParamset(s *schedule.Simple) map[string]any {
	out := make(map[string]any, 24*4)
	if s != nil {
		for groupNo, entry := range s.Entries { //nolint:gocritic // rangeValCopy: map values cannot be addressed; copy is unavoidable
			mask := WeekdayListToBitmask(entry.Weekdays)
			hour, minute := parseHHMM(entry.Time)
			prefix := fmt.Sprintf("%02d_WP_", groupNo)

			// Mandatory fields.
			out[prefix+"WEEKDAY"] = mask
			out[prefix+"FIXED_HOUR"] = hour
			out[prefix+"FIXED_MINUTE"] = minute
			out[prefix+"LEVEL"] = entry.Level

			// CONDITION — integer code; 0 = fixed_time (default).
			condID := 0
			if c, ok := conditionToInt[entry.Condition]; ok {
				condID = c
			}
			out[prefix+"CONDITION"] = condID

			// ASTRO_TYPE — 0 = sunrise, 1 = sunset.
			astroID := 0
			if entry.AstroType == schedule.AstroSunset {
				astroID = 1
			}
			out[prefix+"ASTRO_TYPE"] = astroID
			out[prefix+"ASTRO_OFFSET"] = entry.AstroOffsetMinutes

			// TARGET_CHANNELS bitmask.
			out[prefix+"TARGET_CHANNELS"] = TargetChannelsListToBitmask(entry.TargetChannels)

			// LEVEL_2 — only when explicitly set.
			if entry.Level2 != nil {
				out[prefix+"LEVEL_2"] = *entry.Level2
			}

			// DURATION — emit only when set; (0,0) is rejected by some CCU
			// firmware when the MASTER description omits DURATION_BASE.
			if entry.Duration != "" {
				b, f := parseDurationToBaseFactorInts(entry.Duration)
				out[prefix+"DURATION_BASE"] = b
				out[prefix+"DURATION_FACTOR"] = f
			}

			// RAMP_TIME — same conditional emit policy as DURATION.
			if entry.RampTime != "" {
				b, f := parseDurationToBaseFactorInts(entry.RampTime)
				out[prefix+"RAMP_TIME_BASE"] = b
				out[prefix+"RAMP_TIME_FACTOR"] = f
			}
		}
	}
	// Deactivate unused groups (1..24): zero WEEKDAY and TARGET_CHANNELS only.
	// Writing all optional fields for inactive groups triggers fault -5 on devices
	// whose MASTER paramset description omits those keys.
	for no := 1; no <= 24; no++ {
		key := fmt.Sprintf("%02d_WP_WEEKDAY", no)
		if _, ok := out[key]; !ok {
			out[key] = 0
			out[fmt.Sprintf("%02d_WP_TARGET_CHANNELS", no)] = 0
		}
	}
	return out
}

// IsSimpleGroupActive reports whether a raw schedule group is active,
// i.e. has at least one weekday bit set. Mirrors `is_schedule_active`.
func IsSimpleGroupActive(weekdayMask int) bool {
	return weekdayMask != 0
}

// ---------------------------------------------------------------------------
// Local helpers
// ---------------------------------------------------------------------------

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

func toFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	return 0
}

func parseHHMM(s string) (hour, minute int) {
	fmt.Sscanf(s, "%d:%d", &hour, &minute) //nolint:errcheck // best-effort: hour/minute stay 0 on bad input
	return hour, minute
}

// ---------------------------------------------------------------------------
// Target-channel bitmask helpers
// ---------------------------------------------------------------------------

// targetChannelBitPos maps the "actor_sub" channel key to its bitmask
// bit position (0-indexed). Mirrors the Python channel ordering used by
// `_list_to_bitwise` for TARGET_CHANNELS.
var targetChannelBitPos = map[string]uint{
	"1_1": 0, "1_2": 1, "1_3": 2,
	"2_1": 3, "2_2": 4, "2_3": 5,
	"3_1": 6, "3_2": 7, "3_3": 8,
	"4_1": 9, "4_2": 10, "4_3": 11,
	"5_1": 12, "5_2": 13, "5_3": 14,
	"6_1": 15, "6_2": 16, "6_3": 17,
	"7_1": 18, "7_2": 19, "7_3": 20,
	"8_1": 21, "8_2": 22, "8_3": 23,
}

// TargetChannelsListToBitmask encodes a slice of "actor_sub" channel
// strings into the CCU TARGET_CHANNELS integer bitmask.
// Mirrors Python `_list_to_bitwise` for ScheduleField.TARGET_CHANNELS.
func TargetChannelsListToBitmask(channels []string) int {
	mask := 0
	for _, ch := range channels {
		if bit, ok := targetChannelBitPos[ch]; ok {
			mask |= 1 << bit
		}
	}
	return mask
}

// TargetChannelsBitmaskToList expands the CCU TARGET_CHANNELS integer
// bitmask into a sorted "actor_sub" channel string slice.
// Mirrors Python `_bitwise_to_list` for ScheduleField.TARGET_CHANNELS.
func TargetChannelsBitmaskToList(mask int) []string {
	if mask == 0 {
		return nil
	}
	// Ordered iteration: actors 1..8 × subs 1..3.
	var out []string
	for actor := 1; actor <= 8; actor++ {
		for sub := 1; sub <= 3; sub++ {
			key := fmt.Sprintf("%d_%d", actor, sub)
			bit, ok := targetChannelBitPos[key]
			if !ok {
				continue
			}
			if mask&(1<<bit) != 0 {
				out = append(out, key)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Duration / ramp-time encoding helpers
// ---------------------------------------------------------------------------

// timeBaseTable maps a CCU TimeBase integer to (unit string, multiplier in
// that unit). The multiplier converts the factor to the concrete duration.
// Mirrors Python `TimeBase` enum + `_TIME_BASE_IN_100MS` mapping.
var timeBaseTable = []struct {
	id   int
	unit string
	mult int // factor × mult = duration in unit
}{
	{0, "ms", 100}, // MS_100: factor × 100 ms
	{1, "s", 1},    // SEC_1
	{2, "s", 5},    // SEC_5
	{3, "s", 10},   // SEC_10
	{4, "min", 1},  // MIN_1
	{5, "min", 5},  // MIN_5
	{6, "min", 10}, // MIN_10
	{7, "h", 1},    // HOUR_1
}

// FormatTimeBaseFactor converts a (base, factor) pair from the CCU
// paramset into a human-readable duration string used by [schedule.SimpleEntry].
// Returns "" when factor == 0 or the base id is unknown.
// Mirrors `convert_base_factor_to_duration` in schedule_models.py.
func FormatTimeBaseFactor(base, factor int) string {
	if factor == 0 {
		return ""
	}
	for _, row := range timeBaseTable {
		if row.id == base {
			return fmt.Sprintf("%d%s", factor*row.mult, row.unit)
		}
	}
	return ""
}

// parseDurationToBaseFactorInts converts a human-readable duration string
// ("10s", "5min", "1h", "500ms") to the (base id, factor) pair written
// into the CCU paramset. Picks the smallest base whose factor is ≤ 30.
// Returns (0, 0) for unparseable strings.
// Mirrors `convert_duration_to_base_factor` in schedule_models.py.
func parseDurationToBaseFactorInts(d string) (base, factor int) {
	if d == "" {
		return 0, 0
	}
	// Parse trailing unit.
	var unit string
	var numStr string
	for _, u := range []string{"min", "ms", "h", "s"} {
		if strings.HasSuffix(d, u) {
			unit = u
			numStr = strings.TrimSuffix(d, u)
			break
		}
	}
	if unit == "" {
		return 0, 0
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, 0
	}
	// Convert n in `unit` to milliseconds, then pick smallest viable base.
	var ms int
	switch unit {
	case "ms":
		ms = n
	case "s":
		ms = n * 1000
	case "min":
		ms = n * 60 * 1000
	case "h":
		ms = n * 3600 * 1000
	}
	for _, row := range timeBaseTable {
		baseMS := row.mult * 100
		if unit == "ms" {
			baseMS = row.mult * 100
		} else {
			// Convert row.mult to ms
			switch row.unit {
			case "ms":
				baseMS = row.mult
			case "s":
				baseMS = row.mult * 1000
			case "min":
				baseMS = row.mult * 60 * 1000
			case "h":
				baseMS = row.mult * 3600 * 1000
			}
		}
		if baseMS == 0 || ms%baseMS != 0 {
			continue
		}
		f := ms / baseMS
		if f >= 1 && f <= 30 {
			return row.id, f
		}
	}
	return 0, 0
}
