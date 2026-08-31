// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// rawconvert.go — round-trip helpers between the CCU flat paramset format
// and the in-memory [schedule.Climate] / [schedule.Simple] models.
//
// CLIMATE PARAMSET FORMAT (CCU):
//   "P1_TEMPERATURE_MONDAY_1" : 18.0
//   "P1_ENDTIME_MONDAY_1"     : 360      ← integer minutes since midnight
//
// SIMPLE (NON-CLIMATE) PARAMSET FORMAT (CCU):
//   "01_WP_WEEKDAY"    : 127   ← bitwise weekday mask (Sun=1, Mon=2, …, Sat=64)
//   "01_WP_FIXED_HOUR" : 7
//   "01_WP_FIXED_MINUTE": 30
//   "01_WP_LEVEL"      : 1.0
//
// The week wraps at the bottom: Sunday is bit 0, not bit 7. See
// [weekdaysByBit] for the source and what reading it the other way cost.
//
// This file holds the daemon's only translation of either format. The
// REST/WS schedules domain used to carry a second implementation of the
// simple one; the two were written in parallel, never reconciled, and
// every defect in the format had to be found and fixed twice.

package weekprofile

import (
	"errors"
	"fmt"
	"math"
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
// [IsClimateSlotName] and the "<NN>_WP_<FIELD>" split in
// [ParseSimpleRawParamset]. Keeping the predicate here means a change to
// either format updates the parser and its consumers together.
func IsParameterName(key string) bool {
	if IsClimateSlotName(key) {
		return true
	}
	_, ok := SimpleGroupNo(key)
	return ok
}

// IsClimateSlotName reports whether a MASTER paramset key is one cell of
// a profile-prefixed climate week profile ("P1_ENDTIME_MONDAY_1").
//
// It accepts exactly the keys [ParseClimateRawParamset] consumes, and it
// is the only predicate the daemon uses for that question — the hydration
// filter in the CCU adapter delegates to it. The equivalence is the point:
// a caller that hides or drops a cell the parser cannot read takes a
// parameter off every surface without putting it anywhere else. A key that
// merely shares the "P<N>_" prefix ("P1_X"), or one whose ordinal is past
// the CCU slot count ([schedule.MaxClimateSlots], via [slotCount]), is
// therefore an ordinary parameter here, not a cell.
//
// Narrowing the prefix rule this way exposes no parameter on the fleet the
// in-process CCU simulator models: across its paramset descriptions every
// MASTER key of the "P<1-6>_" shape is a TEMPERATURE/ENDTIME cell, and the
// largest slot ordinal that occurs is [slotCount]. That is a statement
// about that corpus, not about all firmware; a device carrying a higher
// ordinal surfaces the key as a parameter rather than losing it, and
// raising the bound is one constant.
func IsClimateSlotName(key string) bool {
	_, _, _, _, ok := parseClimateSlotKey(key)
	return ok
}

// parseClimateSlotKey decomposes a profile-prefixed climate slot key into
// its parts. ok is false for every key the climate schedule cannot hold,
// including an ordinal outside 1..[slotCount].
//
// It is this package's single reading of that grammar: the parser below
// and [IsClimateSlotName] share it, so the two can never drift into
// disagreeing about what a slot cell is.
func parseClimateSlotKey(key string) (profile, fieldType, weekday string, slotNo int, ok bool) {
	m := climateParamPattern.FindStringSubmatch(key)
	if m == nil {
		return "", "", "", 0, false
	}
	no, err := strconv.Atoi(m[4])
	if err != nil || no < 1 || no > slotCount {
		return "", "", "", 0, false
	}
	return m[1], m[2], m[3], no, true
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
		profile, fieldType, weekday, slotNo, ok := parseClimateSlotKey(key)
		if !ok {
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
	2: schedule.ConditionFixedIfBeforeAstro,
	3: schedule.ConditionAstroIfBeforeFixed,
	4: schedule.ConditionFixedIfAfterAstro,
	5: schedule.ConditionAstroIfAfterFixed,
	6: schedule.ConditionEarliestOfFixedAstro,
	7: schedule.ConditionLatestOfFixedAstro,
}

// ConditionForWire maps a CCU `<NN>_WP_CONDITION` integer to its
// [schedule.Condition]. Unknown values yield the empty condition.
//
// Exported so the parity guard can compare this vocabulary against the
// REST schedules domain, which translates the same field independently.
func ConditionForWire(id int) schedule.Condition { return conditionFromInt[id] }

// WireForCondition is the inverse of [ConditionForWire]. Unknown
// conditions yield 0 (fixed time).
func WireForCondition(c schedule.Condition) int { return conditionToInt[c] }

// ConditionIsKnown reports whether c is one of the eight conditions the
// CCU's `<NN>_WP_CONDITION` field carries. Callers that accept the
// condition as a free string — the REST surface does — use it to reject
// a typo by name instead of silently writing "fixed time".
func ConditionIsKnown(c schedule.Condition) bool {
	_, ok := conditionToInt[c]
	return ok
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
// This is the one translation of the `<NN>_WP_<FIELD>` read direction:
// the REST/WS schedules domain projects its DTOs off this result rather
// than decoding the paramset a second time.
//
// What the CCU holds is authoritative, so entries are stored as read
// rather than passed through [schedule.Simple.Put] — the same rule
// [RawToClimate] follows and for the same reason. Validating here
// discarded real data: [schedule.SimpleEntry.Validate] caps a duration's
// numeral at 30, which every slot on a coarse time base exceeds, so a
// switching programme with a 12-minute duration and every lock slot the
// CCU encodes with the "permanent" sentinel vanished from the surface
// instead of being displayed.
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
		astroTypeWire   int
		astroTypeSeen   bool
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
			g.astroTypeSeen = true
			g.astroTypeWire = toInt(val)
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
		if groupNo < 1 || groupNo > SimpleMaxGroup {
			continue
		}
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
			AstroOffsetMinutes: g.astroOffset,
			TargetChannels:     TargetChannelsBitmaskToList(g.targetChannels),
		}
		// ASTRO_TYPE is only meaningful once the condition consults an
		// astro event. The CCU carries the field on every group, so
		// reading it unconditionally labelled plain fixed-time entries
		// "sunrise" and offered the operator a sun picker the device
		// ignores.
		if g.astroTypeSeen && entry.Condition != schedule.ConditionFixedTime {
			if g.astroTypeWire == 1 {
				entry.AstroType = schedule.AstroSunset
			} else {
				entry.AstroType = schedule.AstroSunrise
			}
		}
		entry.Duration = decodeWireDuration(g.durationBase, g.durationFactor)
		entry.RampTime = decodeWireDuration(g.rampBase, g.rampFactor)
		entry.ColorType = g.colorType
		entry.ColorValue = g.colorValue
		entry.OutputBehaviour = g.outputBehaviour
		s.Entries[groupNo] = entry
	}
	return s, nil
}

// decodeWireDuration renders a DURATION / RAMP_TIME (base, factor) pair
// as a duration string, or "" when the pair carries no duration.
//
// Factors above [MaxTimeBaseFactor] are read as "unset". The CCU parks
// factor 31 on slots that have no duration — it is the firmware default
// and the lock domain's "permanent" sentinel — but rejects a write of
// any factor past 30 with fault -5. Surfacing such a value would offer
// the operator a duration the device then refuses to take back.
func decodeWireDuration(base, factor int) string {
	if factor <= 0 || factor > MaxTimeBaseFactor {
		return ""
	}
	return FormatTimeBaseFactor(base, factor)
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
//
// This is the one translation of the `<NN>_WP_<FIELD>` write direction:
// the REST/WS schedules domain maps its DTOs onto [schedule.Simple] and
// calls this rather than encoding the paramset a second time. Errors
// name the offending group so a REST caller gets a usable 4xx.
func BuildSimpleRawParamset(s *schedule.Simple, deactivateUpTo int) (map[string]any, error) { //nolint:gocyclo,gocognit,funlen // flat per-field emit; length/complexity is field count, not control-flow depth
	out := make(map[string]any, (deactivateUpTo+1)*4)
	if s != nil {
		for _, groupNo := range s.Slots() {
			entry := s.Entries[groupNo]
			if groupNo < 1 || groupNo > schedule.SimpleMaxSlot {
				return nil, fmt.Errorf("slot_no out of range: %d (1..%d)", groupNo, schedule.SimpleMaxSlot)
			}
			mask := WeekdayListToBitmask(entry.Weekdays)
			if mask == 0 {
				return nil, fmt.Errorf("slot %d: no weekday selected", groupNo)
			}
			hour, minute, err := splitHHMM(entry.Time)
			if err != nil {
				return nil, fmt.Errorf("slot %d: %w", groupNo, err)
			}
			prefix := fmt.Sprintf("%02d_WP_", groupNo)

			// Mandatory fields.
			out[prefix+"WEEKDAY"] = mask
			out[prefix+"FIXED_HOUR"] = hour
			out[prefix+"FIXED_MINUTE"] = minute
			out[prefix+"LEVEL"] = entry.Level

			// CONDITION — integer code; 0 = fixed_time (default).
			condID := 0
			if entry.Condition != "" {
				c, ok := conditionToInt[entry.Condition]
				if !ok {
					return nil, fmt.Errorf("slot %d: unknown condition %q", groupNo, entry.Condition)
				}
				condID = c
			}
			out[prefix+"CONDITION"] = condID

			// ASTRO_TYPE — 0 = sunrise, 1 = sunset. Written even for
			// fixed-time entries so the CCU does not retain stale data.
			astroID := 0
			switch entry.AstroType {
			case "", schedule.AstroSunrise:
				// astroID stays 0 — sunrise is the wire default.
			case schedule.AstroSunset:
				astroID = 1
			default:
				return nil, fmt.Errorf("slot %d: unknown astro_type %q", groupNo, entry.AstroType)
			}
			out[prefix+"ASTRO_TYPE"] = astroID
			if entry.AstroOffsetMinutes < -720 || entry.AstroOffsetMinutes > 720 {
				return nil, fmt.Errorf("slot %d: astro_offset_minutes out of range", groupNo)
			}
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
				b, f, ok := ParseTimeBaseFactor(entry.Duration)
				if !ok {
					return nil, fmt.Errorf("slot %d: invalid duration %q", groupNo, entry.Duration)
				}
				out[prefix+"DURATION_BASE"] = b
				out[prefix+"DURATION_FACTOR"] = f
			}

			// RAMP_TIME — same conditional emit policy as DURATION.
			if entry.RampTime != "" {
				b, f, ok := ParseTimeBaseFactor(entry.RampTime)
				if !ok {
					return nil, fmt.Errorf("slot %d: invalid ramp_time %q", groupNo, entry.RampTime)
				}
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
	return out, nil
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

// MaxTimeBaseFactor is the largest DURATION_FACTOR / RAMP_TIME_FACTOR the CCU
// firmware accepts. The encoder promotes to a larger TimeBase rather than emit
// a factor above this cap, and the reader treats anything above it as "no
// duration". Mirrors `_MAX_DURATION_FACTOR` in schedule_models.py.
const MaxTimeBaseFactor = 30

// permanentBase and permanentFactor are the pair the CCU firmware parks
// on a slot that carries no duration, and the encoding the lock domain
// uses for "until further notice" — an auto-relock end, an unlock, or a
// standing user permission.
//
// It sits one past [MaxTimeBaseFactor] on purpose: the device stores it
// but rejects a write of any *other* factor above the cap. The encoder
// therefore passes this one pair through verbatim so a lock schedule
// read from the CCU can be saved again.
const (
	permanentBase   = 7 // HOUR_1
	permanentFactor = 31
)

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

// durationUnitIn100ms converts a duration unit to 100ms steps. "ms" is
// special-cased (the literal millisecond value is collapsed to 100ms steps by
// the caller). Mirrors `_DURATION_UNIT_IN_100MS` in schedule_models.py:231.
var durationUnitIn100ms = map[string]float64{
	"ms":  0.01,
	"s":   10,
	"m":   600,
	"min": 600,
	"h":   36000,
}

// ZeroDuration is the string form of the (base 0, factor 0) pair — a
// duration of zero, which the CCU stores as a value like any other.
//
// It exists because "" already means "leave the device's duration
// alone": [BuildSimpleRawParamset] writes a sparse paramset, so an
// entry with no duration string emits no DURATION_* keys at all. A door
// lock's `lock_autorelock_start` encodes as exactly (0, 0), and without
// a string for it the write kept whatever the slot held — the firmware
// default (7, 31), which reads back as `lock_autorelock_end`, the
// opposite intent.
const ZeroDuration = "0ms"

// FormatTimeBaseFactor converts a (base, factor) pair from the CCU
// paramset into a human-readable duration string used by [schedule.SimpleEntry].
// Returns "" when the factor is negative or the base id is unknown, and
// [ZeroDuration] for a zero factor in any base — zero is the same
// duration however it is spelled, and it re-parses to the canonical
// (0, 0).
//
// The rendering is exact: the factor is multiplied out in the base's own
// unit, so (SEC_5, 13) reads "65s". Choosing the unit by magnitude
// instead — rendering the same pair as "1min" — rounds the value away,
// and the string is what gets encoded on the next save, so the device
// ends up with a duration the operator never asked for. Mirrors
// `convert_base_factor_to_duration` in schedule_models.py.
func FormatTimeBaseFactor(base, factor int) string {
	if factor < 0 {
		return ""
	}
	if factor == 0 {
		return ZeroDuration
	}
	for _, row := range timeBaseTable {
		if row.id == base {
			return fmt.Sprintf("%d%s", factor*row.mult, row.unit)
		}
	}
	return ""
}

// ParseTimeBaseFactor converts a human-readable duration string ("10s",
// "5min", "1h", "500ms") to the (base id, factor) pair written into the
// CCU paramset. ok is false for unparseable or non-representable input.
//
// It picks the *coarsest* base that divides the value evenly into a
// factor of 1..[MaxTimeBaseFactor]. Coarsest-first matters because the
// factor is capped: 45s has no representation in SEC_1 (factor 45) and
// lands on SEC_5 (factor 9), and every value that fits a coarse base
// also fits several finer ones, so without a rule the two ends of the
// daemon picked different pairs for the same duration.
//
// The search is exact — it runs in whole 100ms steps rather than
// floating-point seconds — so a value the CCU can hold is never rejected
// for a rounding artefact, and one it cannot hold is never silently
// snapped to a neighbour.
//
// Input is taken leniently because it reaches here straight from a REST
// payload: a bare number counts as seconds, "m" as minutes, and a
// fractional value is accepted when it lands on a whole 100ms step.
func ParseTimeBaseFactor(d string) (base, factor int, ok bool) {
	total100ms, ok := durationIn100ms(d)
	if !ok {
		return 0, 0, false
	}
	// Zero is a duration the CCU holds ([ZeroDuration]), not an absent
	// one, and it is the same value in every base — so it resolves to the
	// canonical (MS_100, 0) rather than walking the table below, whose
	// search starts at factor 1.
	if total100ms == 0 {
		return 0, 0, true
	}
	// Coarsest base first: timeBaseTable is ordered by ascending
	// granularity, so walk it backwards.
	for i := len(timeBaseTable) - 1; i >= 0; i-- {
		row := timeBaseTable[i]
		if total100ms%row.in100ms != 0 {
			continue
		}
		if f := total100ms / row.in100ms; f >= 1 && f <= MaxTimeBaseFactor {
			return row.id, f, true
		}
	}
	// The "permanent" pair is one past the cap and therefore unreachable
	// above, but the device holds it and a lock schedule read from the
	// CCU carries it straight back into a save.
	for _, row := range timeBaseTable {
		if row.id == permanentBase && total100ms == permanentFactor*row.in100ms {
			return permanentBase, permanentFactor, true
		}
	}
	return 0, 0, false
}

// durationIn100ms converts a duration string to whole 100ms steps.
// ok is false for an unparseable string, a negative value, or one
// with sub-100ms granularity, which the CCU cannot represent. Zero is
// accepted — see [ZeroDuration].
func durationIn100ms(d string) (total100ms int, ok bool) {
	d = strings.TrimSpace(strings.ToLower(d))
	if d == "" {
		return 0, false
	}
	// Order matters: "min" must be tested before "ms"/"m"/"s" so the
	// three-letter suffix wins, and "ms" before "m"/"s".
	unit, numStr := "s", d
	for _, u := range []string{"min", "ms", "h", "m", "s"} {
		if strings.HasSuffix(d, u) {
			unit, numStr = u, strings.TrimSuffix(d, u)
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil || n < 0 {
		return 0, false
	}
	// Sub-100ms granularity is not representable on the CCU; require the
	// value to land on a whole step rather than rounding to one.
	steps := n * durationUnitIn100ms[unit]
	rounded := math.Round(steps)
	if math.Abs(steps-rounded) > 1e-6 {
		return 0, false
	}
	return int(rounded), true
}

// splitHHMM parses a "HH:MM" switching time into its two components.
//
// The length bound rejects a stray "1:2" or "123:45" before the field
// parse can read them as a plausible time — the REST surface hands this
// string straight through from the request body.
func splitHHMM(hhmm string) (hour, minute int, err error) {
	if len(hhmm) < 4 || len(hhmm) > 5 {
		return 0, 0, fmt.Errorf("invalid time %q", hhmm)
	}
	before, after, found := strings.Cut(hhmm, ":")
	if !found {
		return 0, 0, fmt.Errorf("invalid time %q", hhmm)
	}
	h, err := strconv.Atoi(before)
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", hhmm)
	}
	m, err := strconv.Atoi(after)
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", hhmm)
	}
	return h, m, nil
}
