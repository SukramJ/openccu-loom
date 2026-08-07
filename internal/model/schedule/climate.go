// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// MaxClimatePeriods is the per-weekday slot limit imposed by the CCU.
// Schedules with more than 13 periods are rejected — the CCU silently drops
// the excess otherwise.
const MaxClimatePeriods = 13

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
		if toMinutes(last.EndTime) != 24*60 {
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
// [IdentifyBaseTemperature] when the weekday has no periods.
const DefaultBaseTemperature = 18.0

// IdentifyBaseTemperature identifies the base temperature of a climate
// weekday schedule by finding the temperature that occupies the most
// Total minutes across all periods. This mirrors
// `identify_base_temperature` helper (model/week_profile.py:1731–1765)
// which converts 13-slot wire format to the simplified
// (BaseTemperature + Periods) representation.
//
// Algorithm: iterate the periods in time order (sorted by StartTime),
// accumulate total minutes per unique temperature value, and return the
// temperature with the highest total. Ties break deterministically in
// favour of the temperature whose first period starts earliest — the
// reference helper's max() keeps the first key in accumulation order,
// and a map-iteration tie-break would flip the result between runs.
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

	// Accumulate total minutes per temperature, remembering first-seen
	// order so the winner selection stays deterministic.
	tempMinutes := make(map[float64]int, len(sorted))
	order := make([]float64, 0, len(sorted))
	for _, p := range sorted {
		dur := toMinutes(p.EndTime) - toMinutes(p.StartTime)
		if dur <= 0 {
			continue
		}
		if _, seen := tempMinutes[p.Temperature]; !seen {
			order = append(order, p.Temperature)
		}
		tempMinutes[p.Temperature] += dur
	}
	if len(order) == 0 {
		return DefaultBaseTemperature
	}

	best := order[0]
	for _, temp := range order[1:] {
		if tempMinutes[temp] > tempMinutes[best] {
			best = temp
		}
	}
	return best
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

// validateClimateTime accepts HH:MM between 00:00 and 24:00
// (inclusive — climate schedules use 24:00 as end-of-day marker).
func validateClimateTime(s string) error {
	if s == "24:00" {
		return nil
	}
	if !timePattern.MatchString(s) {
		return fmt.Errorf("invalid time %q", s)
	}
	return nil
}

// toMinutes converts "HH:MM" to total minutes since midnight. "24:00"
// returns 1440. Invalid input returns -1 (callers should validate
// first).
func toMinutes(s string) int {
	if s == "24:00" {
		return 24 * 60
	}
	before, after, ok := strings.Cut(s, ":")
	if !ok {
		return -1
	}
	h, errH := parseDigit2(before)
	m, errM := parseDigit2(after)
	if errH != nil || errM != nil {
		return -1
	}
	return h*60 + m
}

func parseDigit2(s string) (int, error) {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, err
	}
	return v, nil
}
