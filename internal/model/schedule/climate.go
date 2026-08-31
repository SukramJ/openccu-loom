// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule

import (
	"fmt"
	"sort"
	"strings"
)

// ClimatePeriod is one temperature period inside a climate weekday
// schedule. Times use HH:MM (00:00 to 24:00); starttime < endtime.
type ClimatePeriod struct {
	StartTime   string
	EndTime     string
	Temperature float64
}

// Validate checks format and ordering invariants.
func (p ClimatePeriod) Validate() error {
	if err := validateClimateTime(p.StartTime); err != nil {
		return fmt.Errorf("starttime: %w", err)
	}
	if err := validateClimateTime(p.EndTime); err != nil {
		return fmt.Errorf("endtime: %w", err)
	}
	if toMinutes(p.StartTime) >= toMinutes(p.EndTime) {
		return fmt.Errorf("schedule: starttime %s must be before endtime %s", p.StartTime, p.EndTime)
	}
	return nil
}

// MaxClimateSlots is the number of climate slots the CCU stores per
// weekday and per profile. It is a firmware fact, not a policy: the
// paramset declares exactly this many ENDTIME/TEMPERATURE cells, and a
// slot beyond it has nowhere to be written.
const MaxClimateSlots = 13

// MaxClimatePeriods is the per-weekday period limit. Schedules with more
// periods are rejected — the CCU silently drops the excess otherwise.
//
// It equals [MaxClimateSlots] because a gapless day maps one period to one
// slot. It is an UPPER bound on a smaller quantity: a period preceded by a
// gap expands to two slots, so a day validated by [ClimateWeekday.ValidateWire]
// (which does not enforce gapless coverage) can still exceed the slot count.
const MaxClimatePeriods = MaxClimateSlots

// ClimateWeekday is a single weekday's base temperature plus a list
// of non-overlapping periods.
type ClimateWeekday struct {
	BaseTemperature float64
	Periods         []ClimatePeriod
}

// validateWeekdayStructure performs the structural checks that are
// common to both [ClimateWeekday.Validate] and
// [ClimateWeekday.ValidateWire]: slot-count limit, individual period
// validity, and overlap detection. It does NOT enforce the 24-hour
// coverage rule — that is the caller's responsibility.
func (d ClimateWeekday) validateWeekdayStructure() (sorted []ClimatePeriod, err error) {
	if len(d.Periods) > MaxClimatePeriods {
		return nil, fmt.Errorf("schedule: %d periods exceeds CCU limit of %d slots/day",
			len(d.Periods), MaxClimatePeriods)
	}
	sorted = append([]ClimatePeriod(nil), d.Periods...)
	sort.Slice(sorted, func(i, j int) bool {
		return toMinutes(sorted[i].StartTime) < toMinutes(sorted[j].StartTime)
	})
	for i, p := range sorted {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("period[%d]: %w", i, err)
		}
	}
	for i := range len(sorted) - 1 {
		curEnd := toMinutes(sorted[i].EndTime)
		nextStart := toMinutes(sorted[i+1].StartTime)
		if curEnd > nextStart {
			return nil, fmt.Errorf("schedule: period overlap at %s / %s",
				sorted[i].EndTime, sorted[i+1].StartTime)
		}
	}
	return sorted, nil
}

// Validate checks the periods are individually valid, do not
// exceed [MaxClimatePeriods], do not overlap, and (when at least one
// period is given) collectively cover the full day from 00:00 to
// 24:00 with no gaps. Empty Periods is allowed — the BaseTemperature
// alone then represents a constant-temperature day.
func (d ClimateWeekday) Validate() error {
	sorted, err := d.validateWeekdayStructure()
	if err != nil {
		return err
	}
	if len(sorted) > 0 {
		// 24-hour coverage rule: when any period is configured, the
		// schedule must span 00:00 to 24:00 without gaps. The CCU
		// rejects partial-day schedules ("ENDTIME N missing").
		if toMinutes(sorted[0].StartTime) != 0 {
			return fmt.Errorf("schedule: first period must start at 00:00 (got %s)",
				sorted[0].StartTime)
		}
		last := sorted[len(sorted)-1]
		if toMinutes(last.EndTime) != ClimateEndOfDayMinutes {
			return fmt.Errorf("schedule: last period must end at 24:00 (got %s)",
				last.EndTime)
		}
		for i := range len(sorted) - 1 {
			if toMinutes(sorted[i].EndTime) != toMinutes(sorted[i+1].StartTime) {
				return fmt.Errorf("schedule: gap between %s and %s",
					sorted[i].EndTime, sorted[i+1].StartTime)
			}
		}
	}
	return nil
}

// ValidateWire performs all structural checks (slot count, individual
// period validity, overlap) but does NOT enforce the 24-hour coverage
// rule. This mirrors the wire-form semantics used when reading partial
// profiles from the CCU: individual slots are valid even when they do
// not collectively span 00:00→24:00. Use [Validate] for the strict
// round-trip check before writing back to the CCU.
func (d ClimateWeekday) ValidateWire() error {
	_, err := d.validateWeekdayStructure()
	return err
}

// ClimateProfile maps Weekday → [ClimateWeekday]. The CCU supports
// up to six such profiles per device (P1..P6).
type ClimateProfile struct {
	Days map[Weekday]ClimateWeekday
}

// NewClimateProfile constructs an empty profile.
func NewClimateProfile() *ClimateProfile {
	return &ClimateProfile{Days: make(map[Weekday]ClimateWeekday)}
}

// Put stores a weekday's schedule after validating it.
func (p *ClimateProfile) Put(day Weekday, sched ClimateWeekday) error {
	if !isValidWeekday(day) {
		return fmt.Errorf("schedule: invalid weekday %q", day)
	}
	if err := sched.Validate(); err != nil {
		return fmt.Errorf("%s: %w", day, err)
	}
	p.Days[day] = sched
	return nil
}

// Validate runs [ClimateWeekday.Validate] for every entry.
func (p *ClimateProfile) Validate() error {
	for day, sched := range p.Days {
		if !isValidWeekday(day) {
			return fmt.Errorf("schedule: invalid weekday %q", day)
		}
		if err := sched.Validate(); err != nil {
			return fmt.Errorf("%s: %w", day, err)
		}
	}
	return nil
}

// ValidateWire runs [ClimateWeekday.ValidateWire] for every entry.
// It accepts partial-day period sets produced by the CCU wire form
// (no 24-hour coverage enforced). Use [Validate] for write-path checks.
func (p *ClimateProfile) ValidateWire() error {
	for day, sched := range p.Days {
		if !isValidWeekday(day) {
			return fmt.Errorf("schedule: invalid weekday %q", day)
		}
		if err := sched.ValidateWire(); err != nil {
			return fmt.Errorf("%s: %w", day, err)
		}
	}
	return nil
}

// Climate is the full P1..Pn schedule set.
type Climate struct {
	Profiles map[string]*ClimateProfile
}

// NewClimate constructs an empty Climate schedule.
func NewClimate() *Climate {
	return &Climate{Profiles: make(map[string]*ClimateProfile)}
}

// Put registers a profile under key ("P1".."P6").
func (c *Climate) Put(key string, p *ClimateProfile) error {
	if !isValidProfileKey(key) {
		return fmt.Errorf("schedule: invalid profile key %q", key)
	}
	if p == nil {
		return fmt.Errorf("schedule: nil profile for %q", key)
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	c.Profiles[key] = p
	return nil
}

// Keys returns the registered profile keys in sorted order.
func (c *Climate) Keys() []string {
	out := make([]string, 0, len(c.Profiles))
	for k := range c.Profiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate runs [ClimateProfile.Validate] on every profile.
func (c *Climate) Validate() error {
	for k, p := range c.Profiles {
		if !isValidProfileKey(k) {
			return fmt.Errorf("schedule: invalid profile key %q", k)
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// ValidateWire runs [ClimateProfile.ValidateWire] on every profile.
// It accepts partial-day period sets (no 24-hour coverage enforced),
// mirroring the permissive form used when reading from the CCU wire.
// Use [Validate] for the strict write-path check.
func (c *Climate) ValidateWire() error {
	for k, p := range c.Profiles {
		if !isValidProfileKey(k) {
			return fmt.Errorf("schedule: invalid profile key %q", k)
		}
		if err := p.ValidateWire(); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// DefaultBaseTemperature is the fallback returned by
// [IdentifyBaseTemperature] and [IdentifyBaseTemperatureFromSegments]
// when the weekday has no usable stretch. 18.0 is the reference
// fill temperature; 0.0 is not a plausible thermostat base and renders
// as a 0 °C setpoint wherever the value is published.
const DefaultBaseTemperature = 18.0

// TempSegment is one temperature stretch of a weekday, expressed in
// minutes since midnight. It is the input shape of
// [IdentifyBaseTemperatureFromSegments].
//
// It exists because the same winner rule is applied to three different
// wire forms — domain periods, the CCU 13-slot map, and the flat
// paramset cells — whose normalisations genuinely differ and must stay
// with their own reader. Only the winner rule is shared.
type TempSegment struct {
	StartMin    int
	EndMin      int
	Temperature float64
}

// IdentifyBaseTemperatureFromSegments returns the temperature that
// occupies the most minutes across the given segments — the weekday's
// "base temperature", the value every other stretch is reported
// relative to.
//
// Input order IS accumulation order: the caller sorts. Ties are broken
// in favour of the temperature seen FIRST in the given order, so the
// caller's normalisation decides which of two equally long temperatures
// wins. Callers that want the time-ordered tie-break must hand the
// segments over in time order (see [IdentifyBaseTemperature], which
// sorts). The helper deliberately does not sort: each of its callers
// derives StartMin from the preceding slot's end, so a re-sort here
// would silently re-order malformed wire data behind the caller's back.
//
// Segments with a non-positive duration are ignored entirely — they do
// not even establish first-seen order. When no segment survives that
// filter the result is [DefaultBaseTemperature].
//
// No rounding is applied: the base is always a temperature that
// literally occurs in the input. Snapping it to a 0.5 °C grid would
// report — and, on the write path, persist into the device's unused
// slots — a value no slot of the day carries.
func IdentifyBaseTemperatureFromSegments(segs []TempSegment) float64 {
	// First-seen order is tracked explicitly: a map-iteration tie-break
	// would flip the reported base between two reads of identical data.
	tempMinutes := make(map[float64]int, len(segs))
	order := make([]float64, 0, len(segs))
	for _, s := range segs {
		dur := s.EndMin - s.StartMin
		if dur <= 0 {
			continue
		}
		if _, seen := tempMinutes[s.Temperature]; !seen {
			order = append(order, s.Temperature)
		}
		tempMinutes[s.Temperature] += dur
	}
	if len(order) == 0 {
		return DefaultBaseTemperature
	}

	// Strict ">" keeps the first-seen temperature on a tie.
	best := order[0]
	for _, temp := range order[1:] {
		if tempMinutes[temp] > tempMinutes[best] {
			best = temp
		}
	}
	return best
}

// IdentifyBaseTemperature identifies the base temperature of a climate
// weekday schedule by finding the temperature that occupies the most
// Total minutes across all periods. This mirrors
// `identify_base_temperature` helper (model/week_profile.py:1731–1765)
// which converts 13-slot wire format to the simplified
// (BaseTemperature + Periods) representation.
//
// It sorts the periods by StartTime and then defers to
// [IdentifyBaseTemperatureFromSegments], which carries the single
// winner rule shared with the wire-form readers. The sort is what makes
// the tie-break "earliest period wins" — the reference helper's max()
// likewise keeps the first key in accumulation order.
// Falls back to [DefaultBaseTemperature] when the weekday has no
// periods, or when every period has a non-positive duration.
func IdentifyBaseTemperature(day ClimateWeekday) float64 {
	if len(day.Periods) == 0 {
		return DefaultBaseTemperature
	}

	// Time order is load-bearing: the accumulation order below fixes
	// which temperature wins on equal totals.
	sorted := make([]ClimatePeriod, len(day.Periods))
	copy(sorted, day.Periods)
	sort.SliceStable(sorted, func(i, j int) bool {
		return toMinutes(sorted[i].StartTime) < toMinutes(sorted[j].StartTime)
	})

	segs := make([]TempSegment, 0, len(sorted))
	for _, p := range sorted {
		segs = append(segs, TempSegment{
			StartMin:    toMinutes(p.StartTime),
			EndMin:      toMinutes(p.EndTime),
			Temperature: p.Temperature,
		})
	}
	return IdentifyBaseTemperatureFromSegments(segs)
}

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

// ClimateEndOfDay is the CCU end-of-day marker used as the last slot's
// end time. It is deliberately outside the wall-clock grammar: a climate
// schedule has to be able to say "until midnight" without wrapping to
// 00:00, which would sort before every other slot.
const ClimateEndOfDay = "24:00"

// ClimateEndOfDayMinutes is [ClimateEndOfDay] expressed as minutes since
// midnight.
const ClimateEndOfDayMinutes = 24 * 60

// ParseClimateTime is the single acceptance set for climate schedule
// times and converts one to minutes since midnight.
//
// It exists because the same grammar used to be spelled once per layer,
// and the three spellings disagreed: one accepted "24:30" and wrote 1470
// minutes to the device, one rejected it, and the read paths then
// disagreed about what the device held — one clamping to 24:00, the other
// dropping the slot. A time the operator can save must be a time every
// layer can read back.
//
// Accepted: a one- or two-digit hour 0..23, a colon, a two-digit minute
// 0..59; plus the literal [ClimateEndOfDay]. Nothing else — in particular
// no hour above 23 other than the end-of-day marker itself.
func ParseClimateTime(s string) (int, error) {
	if s == ClimateEndOfDay {
		return ClimateEndOfDayMinutes, nil
	}
	before, after, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	h, hOK := parseClockField(before, 1, 2, 23)
	m, mOK := parseClockField(after, 2, 2, 59)
	if !hOK || !mOK {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return h*60 + m, nil
}

// parseClockField reads a zero-padded decimal field of minLen..maxLen
// digits and bounds it at maxVal. It rejects signs and whitespace, which
// strconv.Atoi would accept.
func parseClockField(s string, minLen, maxLen, maxVal int) (int, bool) {
	if len(s) < minLen || len(s) > maxLen {
		return 0, false
	}
	v := 0
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	if v > maxVal {
		return 0, false
	}
	return v, true
}

// FormatClimateTime is the inverse of [ParseClimateTime]. It renders
// minutes since midnight as canonical zero-padded "HH:MM", and
// [ClimateEndOfDayMinutes] as [ClimateEndOfDay]. Anything outside
// 0..[ClimateEndOfDayMinutes] is an error rather than a clamp: clamping is
// what let an out-of-range value look like a legitimate time on one plane
// while another plane discarded it.
func FormatClimateTime(minutes int) (string, error) {
	if minutes == ClimateEndOfDayMinutes {
		return ClimateEndOfDay, nil
	}
	if minutes < 0 || minutes > ClimateEndOfDayMinutes {
		return "", fmt.Errorf("schedule: minutes %d out of range (0..%d)", minutes, ClimateEndOfDayMinutes)
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60), nil
}

// validateClimateTime accepts HH:MM between 00:00 and 24:00
// (inclusive — climate schedules use 24:00 as end-of-day marker).
func validateClimateTime(s string) error {
	_, err := ParseClimateTime(s)
	return err
}

// toMinutes converts "HH:MM" to total minutes since midnight. "24:00"
// returns 1440. Invalid input returns -1.
//
// The -1 sentinel is load-bearing: several callers here are sort
// comparators that run before validation, and a comparator has to stay
// total or sort.Slice sees an inconsistent ordering.
func toMinutes(s string) int {
	m, err := ParseClimateTime(s)
	if err != nil {
		return -1
	}
	return m
}
