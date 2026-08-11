// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Weekday mirrors the CCU schedule weekday labels.
type Weekday string

// Weekday values.
const (
	WeekdayMonday    Weekday = "MONDAY"
	WeekdayTuesday   Weekday = "TUESDAY"
	WeekdayWednesday Weekday = "WEDNESDAY"
	WeekdayThursday  Weekday = "THURSDAY"
	WeekdayFriday    Weekday = "FRIDAY"
	WeekdaySaturday  Weekday = "SATURDAY"
	WeekdaySunday    Weekday = "SUNDAY"
)

// Weekdays lists all seven values in Monday-first order.
var Weekdays = []Weekday{
	WeekdayMonday, WeekdayTuesday, WeekdayWednesday,
	WeekdayThursday, WeekdayFriday, WeekdaySaturday, WeekdaySunday,
}

func isValidWeekday(w Weekday) bool {
	return slices.Contains(Weekdays, w)
}

// Condition is the trigger condition of a schedule entry.
type Condition string

// Condition values, in the order of the CCU's `<NN>_WP_CONDITION`
// integer (0..7).
//
// The names come from the option list the CCU's own editor renders —
// `arOptions` in `WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js` —
// so a condition means here what it means on the device. Six of them
// used to say something else: condition 2 was called "astro before
// fixed" where the device selects the *fixed* time if it falls before
// the astro one, and 6/7 were called "between" and "or" where the device
// picks the earlier or the later of the two. The REST schedules domain
// had them right, so the two halves of the daemon named the same rule
// differently.
const (
	ConditionFixedTime            Condition = "fixed_time"
	ConditionAstro                Condition = "astro"
	ConditionFixedIfBeforeAstro   Condition = "fixed_if_before_astro"
	ConditionAstroIfBeforeFixed   Condition = "astro_if_before_fixed"
	ConditionFixedIfAfterAstro    Condition = "fixed_if_after_astro"
	ConditionAstroIfAfterFixed    Condition = "astro_if_after_fixed"
	ConditionEarliestOfFixedAstro Condition = "earliest_of_fixed_and_astro"
	ConditionLatestOfFixedAstro   Condition = "latest_of_fixed_and_astro"
)

func (c Condition) isAstro() bool { return c != ConditionFixedTime && c != "" }

// Astro is the type of astronomical event used by astro conditions.
type Astro string

// Astro values.
const (
	AstroSunrise Astro = "sunrise"
	AstroSunset  Astro = "sunset"
)

// LockMode is the schedule lock entry mode.
type LockMode string

// LockMode values.
const (
	LockModeDoorLock       LockMode = "door_lock"
	LockModeUserPermission LockMode = "user_permission"
)

// LockAction is the schedule action when LockMode == door_lock.
type LockAction string

// LockAction values mirror the canonical adapter-layer label set so that
// round-tripping a door-lock schedule entry produces the same string the
// REST/MQTT surface expects.
const (
	LockActionAutoRelockStart LockAction = "lock_autorelock_start"
	LockActionAutoRelockEnd   LockAction = "lock_autorelock_end"
	LockActionUnlock          LockAction = "unlock_autorelock_end"
	LockActionOpen            LockAction = "autorelock_end"
)

// LockActionLock is an alias kept for backward compatibility within the
// schedule package. New code should use [LockActionAutoRelockStart].
const LockActionLock = LockActionAutoRelockStart

// LockPermission is the permission when LockMode == user_permission.
type LockPermission string

// LockPermission values. The string forms mirror the adapter labels
// used by HA / MQTT discovery — "granted" / "not_granted" — so the
// north-bound publisher and the schedule decoder agree on the wire
// string without a translation step.
const (
	LockPermissionAllowed LockPermission = "granted"
	LockPermissionDenied  LockPermission = "not_granted"
)

// SimpleEntry is one trigger in a non-climate schedule.
type SimpleEntry struct {
	Weekdays []Weekday

	Time string // "HH:MM"

	Condition          Condition
	AstroType          Astro
	AstroOffsetMinutes int // -720 .. +720

	TargetChannels []string

	Level  float64  // 0.0 .. 1.01
	Level2 *float64 // cover slats

	Duration string // "10s", "5min", "1h", empty = none
	RampTime string // "500ms", "2s"

	LockMode   LockMode
	LockAction LockAction
	Permission LockPermission

	// Universal-light per-switch-point colour/effect fields, carried as
	// opaque ints for a lossless round-trip (nil = absent, 0 = valid).
	// ColorType discriminates (0 hue/sat, 1 colour temperature, 2 effect);
	// ColorValue is the packed value; OutputBehaviour is the HmIP-BSL
	// signal-LED field. Excluded from the domain validation rules.
	ColorType       *int
	ColorValue      *int
	OutputBehaviour *int
}

// --- validation ---

// maxDurationFactor is the maximum allowed numeric factor in a duration string
// (e.g. "30s", "30min", "30h"). Values above 30 are rejected to match CCU
// firmware limits; larger values are silently clipped by the CCU.
const maxDurationFactor = 30

var (
	timePattern     = regexp.MustCompile(`^(?:[01]?\d|2[0-3]):[0-5]\d$`)
	channelPattern  = regexp.MustCompile(`^[1-8]_[123]$`)
	durationPattern = regexp.MustCompile(`^\d+(?:ms|s|min|h)$`)
	durationUnitRE  = regexp.MustCompile(`(?:ms|s|min|h)$`)
)

// validateDuration checks the format and that the numeric factor is within
// the CCU-accepted range [1, maxDurationFactor].
func validateDuration(d string) error {
	if !durationPattern.MatchString(d) {
		return fmt.Errorf("schedule: invalid duration %q", d)
	}
	unit := durationUnitRE.FindString(d)
	factorStr := strings.TrimSuffix(d, unit)
	factor, err := strconv.Atoi(factorStr)
	if err != nil || factor < 1 || factor > maxDurationFactor {
		return fmt.Errorf("schedule: duration factor %d out of range (1..%d)", factor, maxDurationFactor)
	}
	return nil
}

// Validate checks the entry's in-model invariants. Domain-specific
// rules are enforced via [ValidateFor].
//
// When the [Condition] field is empty it defaults to [ConditionFixedTime].
func (e *SimpleEntry) Validate() error {
	if e.Condition == "" {
		e.Condition = ConditionFixedTime
	}
	if len(e.Weekdays) == 0 {
		return errors.New("schedule: at least one weekday required")
	}
	for _, w := range e.Weekdays {
		if !isValidWeekday(w) {
			return fmt.Errorf("schedule: invalid weekday %q", w)
		}
	}
	if !timePattern.MatchString(e.Time) {
		return fmt.Errorf("schedule: invalid time %q", e.Time)
	}
	if e.AstroOffsetMinutes < -720 || e.AstroOffsetMinutes > 720 {
		return fmt.Errorf("schedule: astro offset out of range: %d", e.AstroOffsetMinutes)
	}
	if e.Level < 0 || e.Level > 1.01 {
		return fmt.Errorf("schedule: level out of range: %v", e.Level)
	}
	if e.Level2 != nil && (*e.Level2 < 0 || *e.Level2 > 1) {
		return fmt.Errorf("schedule: level_2 out of range: %v", *e.Level2)
	}
	for _, ch := range e.TargetChannels {
		if !channelPattern.MatchString(ch) {
			return fmt.Errorf("schedule: invalid channel %q", ch)
		}
	}
	if e.Duration != "" {
		if err := validateDuration(e.Duration); err != nil {
			return err
		}
	}
	if e.RampTime != "" {
		if !durationPattern.MatchString(e.RampTime) {
			return fmt.Errorf("schedule: invalid ramp_time %q", e.RampTime)
		}
	}
	if e.Condition.isAstro() && e.AstroType == "" {
		return fmt.Errorf("schedule: condition %s requires astro_type", e.Condition)
	}
	return nil
}

// ValidateFor enforces category-specific rules on top of [Validate].
func (e *SimpleEntry) ValidateFor(category hmenum.DataPointCategory) error {
	if err := e.Validate(); err != nil {
		return err
	}
	switch category { //nolint:exhaustive // only categories with schedule constraints are listed
	case hmenum.DataPointCategorySwitch:
		if e.Level != 0 && e.Level != 1 {
			return fmt.Errorf("schedule/switch: level must be 0 or 1, got %v", e.Level)
		}
		if e.Level2 != nil {
			return errors.New("schedule/switch: level_2 not supported")
		}
		if e.RampTime != "" {
			return errors.New("schedule/switch: ramp_time not supported")
		}
	case hmenum.DataPointCategoryLight:
		if e.Level2 != nil {
			return errors.New("schedule/light: level_2 not supported")
		}
	case hmenum.DataPointCategoryCover:
		if e.RampTime != "" {
			return errors.New("schedule/cover: ramp_time not supported")
		}
		if e.Duration != "" {
			return errors.New("schedule/cover: duration not supported")
		}
	case hmenum.DataPointCategoryValve:
		if e.Level2 != nil {
			return errors.New("schedule/valve: level_2 not supported")
		}
		if e.RampTime != "" {
			return errors.New("schedule/valve: ramp_time not supported")
		}
	case hmenum.DataPointCategoryLock:
		if e.LockMode == "" {
			return errors.New("schedule/lock: lock_mode required")
		}
		switch e.LockMode {
		case LockModeDoorLock:
			if e.LockAction == "" {
				return errors.New("schedule/lock: lock_action required in door_lock mode")
			}
			if e.Permission != "" {
				return errors.New("schedule/lock: permission not allowed in door_lock mode")
			}
		case LockModeUserPermission:
			if e.Permission == "" {
				return errors.New("schedule/lock: permission required in user_permission mode")
			}
			if e.LockAction != "" {
				return errors.New("schedule/lock: lock_action not allowed in user_permission mode")
			}
		default:
			return fmt.Errorf("schedule/lock: unknown mode %q", e.LockMode)
		}
		if e.Level2 != nil || e.RampTime != "" || e.Duration != "" {
			return errors.New("schedule/lock: level_2/ramp_time/duration not supported")
		}
	}
	return nil
}

// EmptySimpleEntry returns a minimal but valid [SimpleEntry] that can
// serve as the UI default when adding a new schedule slot. The returned
// entry has MONDAY selected, time "00:00", condition fixed_time, and
// category-appropriate defaults for lock_mode / lock_action.
//
// For [hmenum.DataPointCategoryLock] the entry includes door_lock mode
// and lock_autorelock_end action so the round-trip through
// ValidateFor passes without extra editing.
//
// Mirrors `WeekProfileDataPoint.empty_schedule_entry` in week_profile.py.
func EmptySimpleEntry(category hmenum.DataPointCategory) SimpleEntry {
	base := SimpleEntry{
		Weekdays:       []Weekday{WeekdayMonday},
		Time:           "00:00",
		Condition:      ConditionFixedTime,
		Level:          0.0,
		TargetChannels: []string{"1_1"},
	}
	if category == hmenum.DataPointCategoryLock {
		base.LockMode = LockModeDoorLock
		base.LockAction = LockActionAutoRelockEnd
	}
	return base
}

// SimpleMaxSlot is the highest slot a simple schedule can hold.
//
// It is the largest count the CCU declares for such a channel: 75 on a
// dimmer, universal-light, switch, blind or servo channel, 69 on the
// models its web UI special-cases (HmIP-MP3P, HmIPW-WRC6(-A),
// HmIP-WRC6-230, water switches, HmIP-BSL on firmware 2.x). See
// `_getMaxEntries` in the CCU's own
// `WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js`, which edits
// every one of them.
//
// A given device may declare fewer, and that is the real bound: what a
// channel's MASTER paramset does not contain cannot be written to it.
// This constant is the model's outer limit, not a promise that every
// device has this many slots.
const SimpleMaxSlot = 75

// Simple is a slot-indexed schedule for non-climate devices, holding up
// to [SimpleMaxSlot] entries.
type Simple struct {
	Entries map[int]SimpleEntry
}

// NewSimple constructs a Simple with an empty slot map.
func NewSimple() *Simple { return &Simple{Entries: make(map[int]SimpleEntry)} }

// Put inserts or replaces an entry under slot. Slot must be
// 1..[SimpleMaxSlot].
func (s *Simple) Put(slot int, e SimpleEntry) error {
	if slot < 1 || slot > SimpleMaxSlot {
		return fmt.Errorf("schedule: slot %d out of range (1..%d)", slot, SimpleMaxSlot)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	// Write back the (possibly defaulted) entry so Condition is persisted.
	s.Entries[slot] = e
	return nil
}

// Slots returns the used slots in ascending order.
func (s *Simple) Slots() []int {
	out := make([]int, 0, len(s.Entries))
	for k := range s.Entries {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// ValidateAll iterates every slot and applies [ValidateFor].
func (s *Simple) ValidateAll(category hmenum.DataPointCategory) error {
	for _, slot := range s.Slots() {
		e := s.Entries[slot]
		if err := e.ValidateFor(category); err != nil {
			return fmt.Errorf("slot %d: %w", slot, err)
		}
		// Write back: ValidateFor may normalise the Condition field via Validate.
		s.Entries[slot] = e
	}
	return nil
}
