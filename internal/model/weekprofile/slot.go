// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"fmt"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// slotCount is the number of slots per weekday in a CCU climate schedule.
// The firmware fact itself lives with the domain type it bounds.
const slotCount = schedule.MaxClimateSlots

// minScheduleTime and maxScheduleTime are the CCU boundary markers for
// climate slot end-times.
const (
	minScheduleTime = "00:00"
	maxScheduleTime = schedule.ClimateEndOfDay
)

// ScheduleSlot represents one CCU climate time slot: the endpoint and
// the target temperature. An end-time of "24:00" marks the end of day.
// Slot keys are 1-based (1..13). This is the internal wire-level type;
// public callers work with [schedule.ClimatePeriod] instead.
type ScheduleSlot struct {
	EndTime     string  // "HH:MM" or "24:00"
	Temperature float64 // degrees Celsius
}

// weekdaySlots is the 13-slot map for one weekday, keyed by slot number 1..13.
type weekdaySlots = map[int]ScheduleSlot

// profileSlots maps each weekday name to its 13-slot map.
type profileSlots = map[string]weekdaySlots

// rawClimateSchedule is the full CCU internal schedule (P1..P6 → weekday → slots).
type rawClimateSchedule = map[string]profileSlots

// filterWeekdaySlots removes redundant trailing "24:00" slots from a
// weekday's 13-slot map. The first "24:00" slot is kept; everything after it
// is discarded. Remaining slots are re-numbered sequentially from 1.
//
// Mirrors `_filter_weekday_entries` `week_profile.py`.
func filterWeekdaySlots(ws weekdaySlots) weekdaySlots {
	if len(ws) == 0 {
		return ws
	}
	type numbered struct {
		no   int
		slot ScheduleSlot
	}
	ordered := make([]numbered, 0, len(ws))
	for no, s := range ws {
		ordered = append(ordered, numbered{no, s})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].no < ordered[j].no })

	kept := make([]ScheduleSlot, 0, len(ordered))
	for _, n := range ordered {
		kept = append(kept, n.slot)
		if n.slot.EndTime == maxScheduleTime {
			break
		}
	}
	out := make(weekdaySlots, len(kept))
	for i, s := range kept {
		out[i+1] = s
	}
	return out
}

// normalizeWeekdaySlots sorts slots by EndTime, renumbers them 1..N, then
// fills slots N+1..13 with "24:00" + the final temperature. Always returns
// exactly 13 slots.
//
// Mirrors `_normalize_weekday_data` `week_profile.py`.
func normalizeWeekdaySlots(ws weekdaySlots) weekdaySlots {
	if len(ws) == 0 {
		// Return 13 zero slots.
		return fillUpWeekdaySlots(0, make(weekdaySlots))
	}
	type numbered struct {
		no   int
		slot ScheduleSlot
	}
	ordered := make([]numbered, 0, len(ws))
	for no, s := range ws {
		ordered = append(ordered, numbered{no, s})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return toMinutes(ordered[i].slot.EndTime) < toMinutes(ordered[j].slot.EndTime)
	})

	base := make(weekdaySlots, len(ordered))
	for i, n := range ordered {
		base[i+1] = n.slot
	}
	var fillTemp float64
	if len(ordered) > 0 {
		fillTemp = ordered[len(ordered)-1].slot.Temperature
	}
	return fillUpWeekdaySlots(fillTemp, base)
}

// fillUpWeekdaySlots pads ws to exactly 13 slots by appending "24:00"
// entries with the given fill temperature. Any slots beyond 13 are trimmed —
// a last-resort bound, not a policy: the expansion path checks capacity first
// and returns an error, because a silent trim drops the end-of-day entry along
// with the excess.
//
// The padding is about filling the paramset, not about marking the end: the
// CCU terminates a weekday by VALUE at whatever ordinal it occurs — its
// reader breaks on the first slot whose ENDTIME is 1440
// (www/config/easymodes/etc/hmipChannelConfigDialogs.tcl:3009) — and slot 13
// is schema-identical to slots 5..12. A day whose slot 3 already ends at
// 1440 is complete; nothing makes the thirteenth special. So the trailing
// entries this appends are inert padding behind a terminator that has
// already fired, not a terminator themselves.
//
// Mirrors `_fillup_weekday_data` `week_profile.py`.
func fillUpWeekdaySlots(fillTemp float64, ws weekdaySlots) weekdaySlots {
	out := make(weekdaySlots, slotCount)
	for no, s := range ws {
		if no >= 1 && no <= slotCount {
			out[no] = s
		}
	}
	for no := 1; no <= slotCount; no++ {
		if _, ok := out[no]; !ok {
			out[no] = ScheduleSlot{EndTime: maxScheduleTime, Temperature: fillTemp}
		}
	}
	return out
}

// identifyBaseTemperature determines which temperature occupies the most
// minutes in a weekday's slot map and returns it as the "base temperature".
//
// It owns only the wire-form normalisation — slot order, the implicit start
// at the preceding slot's end, the two CCU quirks below — and hands the
// winner rule to [schedule.IdentifyBaseTemperatureFromSegments]. Slot order
// is the accumulation order the shared rule breaks ties on, so the earliest
// slot wins a tie: the base temperature must stay stable across reloads of
// the same unchanged paramset, and a map-iteration tie-break would flip which
// segment is reported as an explicit period between two loads of identical
// data.
//
// Two quirks stay here because they are facts about the CCU paramset, not
// about the rule: an unparsable end-time counts as end-of-day, and the first
// "24:00" slot terminates the day (the remaining cells are padding).
//
// Mirrors `identify_base_temperature` `week_profile.py`.
func identifyBaseTemperature(ws weekdaySlots) float64 {
	type numbered struct {
		no   int
		slot ScheduleSlot
	}
	ordered := make([]numbered, 0, len(ws))
	for no, s := range ws {
		ordered = append(ordered, numbered{no, s})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].no < ordered[j].no })

	segs := make([]schedule.TempSegment, 0, len(ordered))
	prevMinutes := 0
	for _, n := range ordered {
		endMins := toMinutes(n.slot.EndTime)
		if endMins < 0 {
			endMins = schedule.ClimateEndOfDayMinutes
		}
		segs = append(segs, schedule.TempSegment{
			StartMin:    prevMinutes,
			EndMin:      endMins,
			Temperature: n.slot.Temperature,
		})
		prevMinutes = endMins
		if n.slot.EndTime == maxScheduleTime {
			break
		}
	}
	return schedule.IdentifyBaseTemperatureFromSegments(segs)
}

// ParseSlotTime converts a CCU ENDTIME value to a canonical "HH:MM" string.
// The CCU returns integer minutes (e.g. 360 for "06:00"); this function
// handles both int and string inputs.
//
// Mirrors the ENDTIME→time-string logic.
func ParseSlotTime(v any) (string, error) {
	switch val := v.(type) {
	case int:
		return minutesToTimeStr(val)
	case float64:
		return minutesToTimeStr(int(val))
	case string:
		mins, err := schedule.ParseClimateTime(val)
		if err != nil {
			return "", fmt.Errorf("weekprofile: invalid time string %q", val)
		}
		// Re-emit rather than returning val: the grammar accepts a
		// one-digit hour, and ScheduleSlot.EndTime is compared by string
		// identity against maxScheduleTime all over this package.
		return schedule.FormatClimateTime(mins)
	default:
		return "", fmt.Errorf("weekprofile: unsupported endtime type %T", v)
	}
}

// minutesToTimeStr converts total minutes since midnight to "HH:MM".
// 1440 → "24:00".
func minutesToTimeStr(mins int) (string, error) {
	return MinutesToTimeStr(mins)
}

// MinutesToTimeStr is the public form of minutesToTimeStr. It converts total
// minutes since midnight (0..1440) to "HH:MM" / "24:00". Returns an error
// when mins is out of range.
func MinutesToTimeStr(mins int) (string, error) {
	out, err := schedule.FormatClimateTime(mins)
	if err != nil {
		return "", fmt.Errorf("weekprofile: minutes %d out of range (0..%d)",
			mins, schedule.ClimateEndOfDayMinutes)
	}
	return out, nil
}

// toMinutes is a package-local helper that converts "HH:MM" / "24:00" to
// total minutes. Returns -1 for invalid input.
func toMinutes(s string) int {
	return ToMinutes(s)
}

// ToMinutes is the public form of toMinutes. It converts an "HH:MM" or "24:00"
// string to total minutes since midnight. Returns -1 for invalid input,
// including an out-of-range hour: the grammar is [schedule.ParseClimateTime].
// Exposed so that callers can implement roundtrip tests against [MinutesToTimeStr].
func ToMinutes(s string) int {
	m, err := schedule.ParseClimateTime(s)
	if err != nil {
		return -1
	}
	return m
}
