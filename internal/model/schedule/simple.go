// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
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
	AstroOffsetMinutes int // bounded by the channel's declared ASTRO_OFFSET range

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

// AstroOffsetFallbackLimit bounds an astro offset, in minutes, when nothing
// better is known about the channel.
//
// The CCU holds no constant for this: its weekly-program editor reads
// ASTRO_OFFSET_MIN / ASTRO_OFFSET_MAX out of the paramset description and
// clamps its input to them, so the accepted range is whatever the channel
// declares — every model in the descriptor corpus declares INTEGER MIN -128
// MAX 127. This ±12 h bound describes nothing about a device; it is this
// project's long-standing conservative limit, kept so a channel whose
// descriptor has not been loaded is no less protected than it used to be.
//
// It is declared here rather than beside the encoder because both the domain
// validator and the raw converter's undeclared-range fallback state it, and
// the two must move together.
const AstroOffsetFallbackLimit = 720

var (
	timePattern     = regexp.MustCompile(`^(?:[01]?\d|2[0-3]):[0-5]\d$`)
	channelPattern  = regexp.MustCompile(`^[1-8]_[123]$`)
	durationPattern = regexp.MustCompile(`^\d+(?:ms|s|min|h)$`)
)

// validateDuration checks that d is spelled like a duration this schedule
// domain can carry: a whole number with one of the four units, or one of the
// two reserved words ([PermanentDuration], [ZeroDuration]).
//
// It deliberately does NOT bound the numeral. The digits are the duration in
// the unit shown, not the CCU's DURATION_FACTOR: the wire pair is (base,
// factor), the reader multiplies the factor out in the base's own unit, and a
// coarse base therefore emits "50min" (base MIN_10, factor 5) or "500ms"
// (base MS_100, factor 5). Reading the digits as the factor and capping them
// at 30 rejected values the daemon's own read path produces — every slot on a
// coarse base, "0ms" for a lock's auto-relock start, and the "permanent"
// sentinel the CCU holds for a standing user permission.
//
// What decides whether a duration is representable is the encoder that has to
// produce the pair: weekprofile.ParseTimeBaseFactor picks the coarsest base
// that divides the value into a factor of 1..30 and fails the write when none
// does. This function is the domain's spelling check, not that search.
func validateDuration(d string) error {
	switch d {
	case PermanentDuration, ZeroDuration:
		return nil
	}
	if !durationPattern.MatchString(d) {
		return fmt.Errorf("schedule: invalid duration %q", d)
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
	// A device-independent sanity bound only. The range that decides is the
	// one the channel declares for ASTRO_OFFSET, enforced on the way to the
	// wire in weekprofile.BuildSimpleRawParamset — the CCU's own editor reads
	// ASTRO_OFFSET_MIN / MAX out of the paramset description rather than
	// holding a number, and every model in the corpus declares ±128.
	if e.AstroOffsetMinutes < -AstroOffsetFallbackLimit || e.AstroOffsetMinutes > AstroOffsetFallbackLimit {
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
		// The same grammar and the same reserved words: the CCU builds the
		// duration and ramp-time editors from one helper, so the encoding is
		// shared.
		if err := validateDuration(e.RampTime); err != nil {
			return fmt.Errorf("schedule: invalid ramp_time %q", e.RampTime)
		}
	}
	if e.Condition.isAstro() && e.AstroType == "" {
		return fmt.Errorf("schedule: condition %s requires astro_type", e.Condition)
	}
	return nil
}

// UnsupportedFieldsFor reports which of the three optional schedule fields a
// category rejects: level_2, ramp_time and duration.
//
// It is the single catalogue of that fact. [SimpleEntry.ValidateFor] enforces
// it on the write path, and the schedule adapter strips the same set off a
// parsed entry on the read path — the CCU's COMBINED_PARAMETER carries fields
// a given channel type never uses, and an entry that keeps them fails the
// validator on the way back in. Two spellings of this table drift into a read
// that emits a field the write then rejects.
//
// A category with no schedule constraints supports all three.
func UnsupportedFieldsFor(category hmenum.DataPointCategory) (level2, rampTime, duration bool) {
	switch category { //nolint:exhaustive // only categories with schedule constraints are listed
	case hmenum.DataPointCategorySwitch:
		return true, true, false
	case hmenum.DataPointCategoryLight:
		return true, false, false
	case hmenum.DataPointCategoryCover:
		return false, true, true
	case hmenum.DataPointCategoryValve:
		return true, true, false
	case hmenum.DataPointCategoryLock:
		return true, true, true
	default:
		return false, false, false
	}
}

// rejectUnsupportedFields reports the fields the entry carries that
// [UnsupportedFieldsFor] says the category does not accept.
func (e *SimpleEntry) rejectUnsupportedFields(category hmenum.DataPointCategory) error {
	noLevel2, noRampTime, noDuration := UnsupportedFieldsFor(category)
	var offending []string
	if noLevel2 && e.Level2 != nil {
		offending = append(offending, "level_2")
	}
	if noRampTime && e.RampTime != "" {
		offending = append(offending, "ramp_time")
	}
	if noDuration && e.Duration != "" {
		offending = append(offending, "duration")
	}
	if len(offending) == 0 {
		return nil
	}
	return fmt.Errorf("schedule/%s: %s not supported", category, strings.Join(offending, "/"))
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
		return e.rejectUnsupportedFields(category)
	case hmenum.DataPointCategoryLight, hmenum.DataPointCategoryCover, hmenum.DataPointCategoryValve:
		return e.rejectUnsupportedFields(category)
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
		return e.rejectUnsupportedFields(category)
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
