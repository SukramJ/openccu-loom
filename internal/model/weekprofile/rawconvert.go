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
	"errors"
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

// SimpleMaxGroup is the highest simple-schedule group this package
// parses, and it tracks what [schedule.Simple] can hold.
//
// It sat at 24 for a long time, which silently truncated every schedule
// an operator built past that point on the CCU: the device declares 75
// groups on a dimmer, switch, blind or servo channel and edits all of
// them. See [schedule.SimpleMaxSlot] for the source and the
// device-specific caveat.
const SimpleMaxGroup = schedule.SimpleMaxSlot

// SimpleGroupNo extracts the group number from a simple week-profile
// key ("01_WP_LEVEL" → 1). ok is false when the key is not of that
// form. The number is returned unclamped, so callers can tell an
// out-of-range group ("25_WP_LEVEL") apart from a key that is not a
// week-profile cell at all.
func SimpleGroupNo(key string) (groupNo int, ok bool) {
	parts := strings.SplitN(key, "_", 3)
	if len(parts) != 3 || parts[1] != "WP" || parts[2] == "" {
		return 0, false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// IsParameterName reports whether a MASTER paramset key is one cell of a
// week profile — either the climate form ("P1_ENDTIME_MONDAY_1") or the
// simple form ("01_WP_LEVEL").
//
// Callers outside this package use it to keep week-profile cells out of
// per-parameter surfaces. A single climate device carries up to 6
// profiles × 7 weekdays × 13 slots × 2 fields, so listing the cells
// individually buries every other parameter — and the profile already
// has a first-class editor that presents them as a schedule.
//
// A cell is recognised regardless of its group number: "25_WP_LEVEL" is
// as much a week-profile cell as "01_WP_LEVEL". Capping the predicate at
// [SimpleMaxGroup] — this package's own storage limit — filed two thirds
// of a fleet's cells under whichever unrelated rule also matched them,
// which is a statement about our parser, not about the parameter.
//
// Both branches read the same grammar the parsers in this file use:
// [climateParamPattern] and the "<NN>_WP_<FIELD>" split in
// [ParseSimpleRawParamset]. Keeping the predicate here means a change to
// either format updates the parser and its consumers together.
func IsParameterName(key string) bool {
	if climateParamPattern.MatchString(key) {
		return true
	}
	_, ok := SimpleGroupNo(key)
	return ok
}

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
		return nil, errors.New("weekprofile: nil climate schedule")
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
		return nil, errors.New("weekprofile: nil climate schedule")
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

// weekdaysByBit lists the CCU's `<NN>_WP_WEEKDAY` bit positions with the
// weekday each one stands for, in presentation order.
//
// Sunday is bit 0, not bit 7: the mask runs Sunday=1, Monday=2, …
// Saturday=64, so all seven days are 127. Taken from the checkbox values
// the CCU's own editor emits (`_getWeekDay` in
// `WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js`) and confirmed
// against a real CCU, which stores and returns WEEKDAY=1 for a
// Sunday-only entry.
var weekdaysByBit = []struct {
	bit int
	day schedule.Weekday
}{
	{1, schedule.WeekdayMonday},
	{2, schedule.WeekdayTuesday},
	{3, schedule.WeekdayWednesday},
	{4, schedule.WeekdayThursday},
	{5, schedule.WeekdayFriday},
	{6, schedule.WeekdaySaturday},
	{0, schedule.WeekdaySunday},
}

// WeekdayBitmaskToList converts a CCU WEEKDAY bitmask to a list of weekday strings.
func WeekdayBitmaskToList(mask int) []schedule.Weekday {
	var out []schedule.Weekday
	for _, e := range weekdaysByBit {
		if mask&(1<<e.bit) != 0 {
			out = append(out, e.day)
		}
	}
	return out
}

// WeekdayListToBitmask converts a list of weekday strings to the CCU bitmask.
func WeekdayListToBitmask(days []schedule.Weekday) int {
	bitPos := make(map[schedule.Weekday]int, len(weekdaysByBit))
	for _, e := range weekdaysByBit {
		bitPos[e.day] = e.bit
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
func ParseSimpleRawParamset(raw map[string]any) (*schedule.Simple, error) { //nolint:gocyclo,funlen // flat per-field switch dispatch; length/complexity is field count, not control-flow depth
	type group struct {
		weekday         int
		fixedHour       int
		fixedMinute     int
		level           float64
		level2          *float64
		condition       schedule.Condition
		astroType       schedule.Astro
		astroOffset     int
		targetChannels  int
		durationBase    int
		durationFactor  int
		rampBase        int
		rampFactor      int
		colorType       *int
		colorValue      *int
		outputBehaviour *int
	}
	groups := make(map[int]*group)

	for key, val := range raw {
		// Expected format: "01_WP_FIELDNAME". Groups above
		// [SimpleMaxGroup] are skipped because [schedule.Simple] has no
		// slot for them — the device declares them, no editor reaches
		// them.
		groupNo, ok := SimpleGroupNo(key)
		if !ok || groupNo > SimpleMaxGroup {
			continue
		}
		parts := strings.SplitN(key, "_", 3)
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
		case "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE":
			v := toInt(val)
			g.colorType = &v
		case "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE":
			v := toInt(val)
			g.colorValue = &v
		case "OUTPUT_BEHAVIOUR":
			v := toInt(val)
			g.outputBehaviour = &v
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
		entry.ColorType = g.colorType
		entry.ColorValue = g.colorValue
		entry.OutputBehaviour = g.outputBehaviour
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
// deactivateUpTo is the highest group the target channel declares. It
// must come from the device rather than from [schedule.SimpleMaxSlot]:
// channels differ (69 or 75 in the field), and naming a group the
// channel does not have earns the same -5 fault. Pass 0 to skip the
// deactivation sweep entirely, which writes the active groups and
// leaves every other one untouched.
func BuildSimpleRawParamset(s *schedule.Simple, deactivateUpTo int) map[string]any {
	out := make(map[string]any, (deactivateUpTo+1)*4)
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

			// Universal-light colour / effect — opaque, emit only when
			// present (nil leaves the CCU's stored value via sparse merge).
			if entry.ColorType != nil {
				out[prefix+"HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"] = *entry.ColorType
			}
			if entry.ColorValue != nil {
				out[prefix+"HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"] = *entry.ColorValue
			}
			if entry.OutputBehaviour != nil {
				out[prefix+"OUTPUT_BEHAVIOUR"] = *entry.OutputBehaviour
			}
		}
	}
	// Deactivate unused groups: zero WEEKDAY and TARGET_CHANNELS only.
	// Writing all optional fields for inactive groups triggers fault -5 on devices
	// whose MASTER paramset description omits those keys.
	//
	// deactivateUpTo bounds the sweep because the same fault applies to
	// the group itself: a channel that declares 69 groups rejects a write
	// naming group 70. The caller derives the bound from the device, so a
	// deleted entry is cleared wherever the device can hold one.
	for no := 1; no <= deactivateUpTo; no++ {
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

// maxTimeBaseFactor is the largest DURATION_FACTOR / RAMP_TIME_FACTOR the CCU
// firmware accepts (factor=31 is reserved as a "permanent" sentinel for lock
// channels). The encoder promotes to a larger TimeBase rather than emit a
// factor above this cap. Mirrors `_MAX_DURATION_FACTOR` in schedule_models.py.
const maxTimeBaseFactor = 30

// timeBaseTable maps a CCU TimeBase integer to (unit string, multiplier in that
// unit, base expressed in 100ms units). `mult` converts a factor to a concrete
// duration for decoding; `in100ms` is the encoder's divisor. Ordered by
// ascending granularity. Mirrors `TimeBase` enum + `_TIME_BASE_IN_100MS` in
// schedule_models.py:208.
var timeBaseTable = []struct {
	id      int
	unit    string
	mult    int // factor × mult = duration in unit
	in100ms int // base expressed in 100ms units (encoder divisor)
}{
	{0, "ms", 100, 1},    // MS_100: factor × 100 ms
	{1, "s", 1, 10},      // SEC_1: 1s = 10 × 100ms
	{2, "s", 5, 50},      // SEC_5
	{3, "s", 10, 100},    // SEC_10
	{4, "min", 1, 600},   // MIN_1
	{5, "min", 5, 3000},  // MIN_5
	{6, "min", 10, 6000}, // MIN_10
	{7, "h", 1, 36000},   // HOUR_1
}

// naturalBaseIndex is the index into [timeBaseTable] where the encoder starts
// its search for a given input unit. Starting at the natural base (rather than
// the finest base) makes "2min" encode as (MIN_1, 2) instead of being needlessly
// promoted to a finer base like (SEC_5, 24) — both are 120s, but the CCU editor
// surfaces the emitted base/factor, so the coarser natural base matches what the
// reference writes. Mirrors `_NATURAL_BASE_INDEX` in schedule_models.py:223.
var naturalBaseIndex = map[string]int{
	"ms":  0, // MS_100
	"s":   1, // SEC_1
	"min": 4, // MIN_1
	"h":   7, // HOUR_1
}

// durationUnitIn100ms converts a duration unit to 100ms steps. "ms" is
// special-cased (the literal millisecond value is collapsed to 100ms steps by
// the caller). Mirrors `_DURATION_UNIT_IN_100MS` in schedule_models.py:231.
var durationUnitIn100ms = map[string]int{
	"ms":  1,
	"s":   10,
	"min": 600,
	"h":   36000,
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
// ("10s", "5min", "1h", "500ms") to the (base id, factor) pair written into the
// CCU paramset. It starts the search at the *natural* base for the input unit
// and promotes to a larger base only when the factor would exceed the CCU cap,
// so "2min" encodes as (MIN_1, 2) — not the finer (SEC_5, 24) a smallest-base
// search would pick. Returns (0, 0) for unparseable or non-representable
// strings. Mirrors `convert_duration_to_base_factor` in schedule_models.py:706.
func parseDurationToBaseFactorInts(d string) (base, factor int) {
	if d == "" {
		return 0, 0
	}
	// Parse trailing unit. Order matters: the three-letter "min" suffix must be
	// tested before the "ms"/"s" suffixes so it wins.
	var unit, numStr string
	for _, u := range []string{"min", "ms", "h", "s"} {
		if strings.HasSuffix(d, u) {
			unit, numStr = u, strings.TrimSuffix(d, u)
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

	total100ms := n * durationUnitIn100ms[unit]
	if unit == "ms" {
		// ms carries a literal millisecond value; collapse it to 100ms steps.
		// Sub-100ms granularity is not representable on the CCU.
		if total100ms%100 != 0 {
			return 0, 0
		}
		total100ms /= 100
	}

	// Start at the natural base for the input unit and promote to a larger base
	// only when the factor would overflow the CCU cap.
	for _, row := range timeBaseTable[naturalBaseIndex[unit]:] {
		if total100ms%row.in100ms != 0 {
			continue
		}
		if f := total100ms / row.in100ms; f >= 1 && f <= maxTimeBaseFactor {
			return row.id, f
		}
	}
	return 0, 0
}
