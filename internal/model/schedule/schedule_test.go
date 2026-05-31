// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schedule

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSimpleEntryValidate(t *testing.T) {
	e := SimpleEntry{
		Weekdays:       []Weekday{WeekdayMonday, WeekdayFriday},
		Time:           "07:30",
		Condition:      ConditionFixedTime,
		Level:          1.0,
		TargetChannels: []string{"1_1", "2_2"},
		Duration:       "10s",
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("good entry: %v", err)
	}
}

func TestSimpleEntryRejectsBadTime(t *testing.T) {
	e := SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "25:00", Level: 1}
	if err := e.Validate(); err == nil {
		t.Fatal("must reject 25:00")
	}
}

func TestSimpleEntryRequiresWeekday(t *testing.T) {
	e := SimpleEntry{Time: "08:00", Level: 1}
	if err := e.Validate(); err == nil {
		t.Fatal("must require weekday")
	}
}

func TestSimpleEntryAstroWithoutType(t *testing.T) {
	e := SimpleEntry{
		Weekdays:  []Weekday{WeekdayMonday},
		Time:      "08:00",
		Condition: ConditionAstro,
		Level:     1,
	}
	if err := e.Validate(); err == nil {
		t.Fatal("astro without astro_type must fail")
	}
}

func TestSimpleEntrySwitchBinaryLevel(t *testing.T) {
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 0.5,
	}
	if err := e.ValidateFor(hmenum.DataPointCategorySwitch); err == nil {
		t.Fatal("switch must reject level 0.5")
	}
}

func TestSimpleEntryLockRequiresMode(t *testing.T) {
	e := SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 1.01}
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Fatal("lock must require lock_mode")
	}
	e.LockMode = LockModeDoorLock
	e.LockAction = LockActionLock
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err != nil {
		t.Fatalf("valid lock door: %v", err)
	}
	e.LockMode = LockModeUserPermission
	e.LockAction = ""
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Fatal("user_permission requires permission")
	}
}

func TestSimpleSlotRange(t *testing.T) {
	s := NewSimple()
	if err := s.Put(0, SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 1}); err == nil {
		t.Fatal("slot 0 must be rejected")
	}
	if err := s.Put(25, SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 1}); err == nil {
		t.Fatal("slot 25 must be rejected")
	}
}

func TestClimatePeriodValidateOrdering(t *testing.T) {
	p := ClimatePeriod{StartTime: "08:00", EndTime: "07:00", Temperature: 21}
	if err := p.Validate(); err == nil {
		t.Fatal("end before start must fail")
	}
	p = ClimatePeriod{StartTime: "06:00", EndTime: "08:00", Temperature: 21}
	if err := p.Validate(); err != nil {
		t.Fatalf("good period: %v", err)
	}
	p = ClimatePeriod{StartTime: "22:00", EndTime: "24:00", Temperature: 18}
	if err := p.Validate(); err != nil {
		t.Fatalf("end=24:00: %v", err)
	}
}

func TestClimateWeekdayNonOverlapping(t *testing.T) {
	d := ClimateWeekday{
		BaseTemperature: 18,
		Periods: []ClimatePeriod{
			{StartTime: "06:00", EndTime: "08:00", Temperature: 21},
			{StartTime: "07:30", EndTime: "09:00", Temperature: 20},
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("overlap must fail")
	}
}

func TestClimateProfileWeekdayKeys(t *testing.T) {
	p := NewClimateProfile()
	if err := p.Put(Weekday("FOO"), ClimateWeekday{}); err == nil {
		t.Fatal("bad weekday must fail")
	}
	if err := p.Put(WeekdayMonday, ClimateWeekday{BaseTemperature: 18}); err != nil {
		t.Fatalf("monday: %v", err)
	}
}

func TestClimateProfileKeys(t *testing.T) {
	c := NewClimate()
	if err := c.Put("X1", NewClimateProfile()); err == nil {
		t.Fatal("bad profile key must fail")
	}
	if err := c.Put("P1", NewClimateProfile()); err != nil {
		t.Fatalf("P1: %v", err)
	}
	if err := c.Put("P7", NewClimateProfile()); err == nil {
		t.Fatal("P7 out of range")
	}
}

func TestClimateWeekdaySlotLimit(t *testing.T) {
	periods := make([]ClimatePeriod, 0, 14)
	// 14 trivial periods of 1h each (00:00-01:00, 01:00-02:00, ...).
	// All sequenced and non-overlapping — only the count violates.
	for i := 0; i < 14; i++ {
		periods = append(periods, ClimatePeriod{
			StartTime:   formatHour(i),
			EndTime:     formatHour(i + 1),
			Temperature: 21,
		})
	}
	d := ClimateWeekday{BaseTemperature: 21, Periods: periods}
	if err := d.Validate(); err == nil {
		t.Fatalf("14 periods must fail (limit %d)", MaxClimatePeriods)
	}
	// Drop to 13 — must validate (still 24h coverage check applies).
	d.Periods = periods[:13]
	d.Periods[12].EndTime = "24:00" // span the remaining 11h to satisfy 24h coverage.
	if err := d.Validate(); err != nil {
		t.Fatalf("13 periods with 24h coverage must validate: %v", err)
	}
}

func TestClimateWeekday24hCoverage(t *testing.T) {
	// First period must start at 00:00.
	d1 := ClimateWeekday{Periods: []ClimatePeriod{
		{StartTime: "06:00", EndTime: "24:00", Temperature: 21},
	}}
	if err := d1.Validate(); err == nil {
		t.Fatal("first period not at 00:00 must fail")
	}

	// Last period must end at 24:00.
	d2 := ClimateWeekday{Periods: []ClimatePeriod{
		{StartTime: "00:00", EndTime: "20:00", Temperature: 21},
	}}
	if err := d2.Validate(); err == nil {
		t.Fatal("last period not at 24:00 must fail")
	}

	// Gap between consecutive periods must fail.
	d3 := ClimateWeekday{Periods: []ClimatePeriod{
		{StartTime: "00:00", EndTime: "06:00", Temperature: 18},
		{StartTime: "08:00", EndTime: "24:00", Temperature: 21},
	}}
	if err := d3.Validate(); err == nil {
		t.Fatal("gap between periods must fail")
	}

	// Full coverage with no gap → ok.
	d4 := ClimateWeekday{Periods: []ClimatePeriod{
		{StartTime: "00:00", EndTime: "06:00", Temperature: 18},
		{StartTime: "06:00", EndTime: "08:00", Temperature: 20},
		{StartTime: "08:00", EndTime: "24:00", Temperature: 21},
	}}
	if err := d4.Validate(); err != nil {
		t.Fatalf("contiguous full-day schedule: %v", err)
	}

	// Empty schedule remains allowed (BaseTemperature alone).
	d5 := ClimateWeekday{BaseTemperature: 21}
	if err := d5.Validate(); err != nil {
		t.Fatalf("empty schedule must remain valid: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ValidateWire tests
// ---------------------------------------------------------------------------

// TestValidateWireAllowsPartialCoverage verifies that a single-period
// weekday that does not start at 00:00 or end at 24:00 passes
// ValidateWire but fails Validate.
func TestValidateWireAllowsPartialCoverage(t *testing.T) {
	t.Parallel()
	d := ClimateWeekday{
		BaseTemperature: 20,
		Periods: []ClimatePeriod{
			{StartTime: "06:00", EndTime: "22:00", Temperature: 21},
		},
	}
	if err := d.ValidateWire(); err != nil {
		t.Fatalf("ValidateWire must accept partial coverage: %v", err)
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Validate must reject partial coverage (no 24h rule)")
	}
}

// TestValidateWireRejectsBrokenSlot verifies that a period where
// endtime <= starttime is rejected by both ValidateWire and Validate.
func TestValidateWireRejectsBrokenSlot(t *testing.T) {
	t.Parallel()
	d := ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "10:00", EndTime: "08:00", Temperature: 21},
		},
	}
	if err := d.ValidateWire(); err == nil {
		t.Fatal("ValidateWire must reject endtime < starttime")
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Validate must reject endtime < starttime")
	}
}

// TestValidateWireFullCoverageBothOK verifies that a weekday with
// complete 00:00→24:00 coverage passes both ValidateWire and Validate.
func TestValidateWireFullCoverageBothOK(t *testing.T) {
	t.Parallel()
	d := ClimateWeekday{
		BaseTemperature: 18,
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "06:00", Temperature: 18},
			{StartTime: "06:00", EndTime: "08:00", Temperature: 21},
			{StartTime: "08:00", EndTime: "24:00", Temperature: 18},
		},
	}
	if err := d.ValidateWire(); err != nil {
		t.Fatalf("ValidateWire must accept full-coverage: %v", err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate must accept full-coverage: %v", err)
	}
}

// TestValidateWireSingleSlotFullDay verifies that a single period
// 00:00→24:00 passes both ValidateWire and Validate.
func TestValidateWireSingleSlotFullDay(t *testing.T) {
	t.Parallel()
	d := ClimateWeekday{
		BaseTemperature: 19,
		Periods: []ClimatePeriod{
			{StartTime: "00:00", EndTime: "24:00", Temperature: 19},
		},
	}
	if err := d.ValidateWire(); err != nil {
		t.Fatalf("ValidateWire: %v", err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestValidateWireClimateProfilePartial verifies that
// ClimateProfile.ValidateWire accepts a partial-day profile while
// ClimateProfile.Validate rejects the same profile.
func TestValidateWireClimateProfilePartial(t *testing.T) {
	t.Parallel()
	p := NewClimateProfile()
	p.Days[WeekdayMonday] = ClimateWeekday{
		BaseTemperature: 20,
		Periods: []ClimatePeriod{
			{StartTime: "08:00", EndTime: "18:00", Temperature: 21},
		},
	}
	if err := p.ValidateWire(); err != nil {
		t.Fatalf("ClimateProfile.ValidateWire: %v", err)
	}
	if err := p.Validate(); err == nil {
		t.Fatal("ClimateProfile.Validate must reject partial coverage")
	}
}

// TestValidateWireClimatePartial verifies that Climate.ValidateWire
// accepts a partial profile while Climate.Validate rejects it.
func TestValidateWireClimatePartial(t *testing.T) {
	t.Parallel()
	c := NewClimate()
	p := NewClimateProfile()
	p.Days[WeekdayTuesday] = ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "07:00", EndTime: "23:00", Temperature: 22},
		},
	}
	c.Profiles["P2"] = p
	if err := c.ValidateWire(); err != nil {
		t.Fatalf("Climate.ValidateWire: %v", err)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("Climate.Validate must reject partial coverage")
	}
}

func formatHour(h int) string {
	if h < 10 {
		return "0" + string(rune('0'+h)) + ":00" //nolint:gosec // G115: h is 0..9; '0'+h is 48..57, well within valid rune range
	}
	if h == 24 {
		return "24:00"
	}
	return string(rune('0'+h/10)) + string(rune('0'+h%10)) + ":00" //nolint:gosec // G115: h is 10..23; each digit is 0..9 so '0'+digit is 48..57
}

// TestClimateKeys verifies Climate.Keys returns sorted profile keys.
func TestClimateKeys(t *testing.T) {
	t.Parallel()
	c := NewClimate()
	if err := c.Put("P3", NewClimateProfile()); err != nil {
		t.Fatalf("Put P3: %v", err)
	}
	if err := c.Put("P1", NewClimateProfile()); err != nil {
		t.Fatalf("Put P1: %v", err)
	}
	keys := c.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys() = %v, want len 2", keys)
	}
	if keys[0] != "P1" || keys[1] != "P3" {
		t.Errorf("Keys() = %v, want [P1 P3]", keys)
	}
}

// TestSimpleSlots verifies Simple.Slots returns used slots sorted.
func TestSimpleSlots(t *testing.T) {
	t.Parallel()
	s := NewSimple()
	good := SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 1}
	if err := s.Put(5, good); err != nil {
		t.Fatalf("Put(5): %v", err)
	}
	if err := s.Put(2, good); err != nil {
		t.Fatalf("Put(2): %v", err)
	}
	slots := s.Slots()
	if len(slots) != 2 || slots[0] != 2 || slots[1] != 5 {
		t.Errorf("Slots() = %v, want [2 5]", slots)
	}
}

// TestSimpleValidateAll verifies Simple.ValidateAll validates every slot.
func TestSimpleValidateAll(t *testing.T) {
	t.Parallel()
	s := NewSimple()
	good := SimpleEntry{Weekdays: []Weekday{WeekdayMonday}, Time: "08:00", Level: 1}
	if err := s.Put(1, good); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.ValidateAll(hmenum.DataPointCategorySwitch); err != nil {
		t.Errorf("ValidateAll on valid data: %v", err)
	}

	// Insert an entry that passes Validate but fails ValidateFor(Cover) — ramp_time not allowed.
	coverBad := SimpleEntry{
		Weekdays: []Weekday{WeekdayFriday},
		Time:     "10:00",
		Level:    0.5,
		RampTime: "2s", // forbidden for cover
	}
	s2 := NewSimple()
	_ = s2.Put(1, coverBad)
	if err := s2.ValidateAll(hmenum.DataPointCategoryCover); err == nil {
		t.Error("ValidateAll with ramp_time on cover should fail")
	}
}

// TestSimplePutInvalidEntry verifies Put rejects invalid entries.
func TestSimplePutInvalidEntry(t *testing.T) {
	t.Parallel()
	s := NewSimple()
	bad := SimpleEntry{} // no weekdays → Validate fails
	if err := s.Put(1, bad); err == nil {
		t.Error("Put with invalid entry must fail")
	}
}

// TestSimpleEntryValidateForCoverDuration verifies cover rejects duration.
func TestSimpleEntryValidateForCoverDuration(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    0.5,
		Duration: "10s", // forbidden for cover
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryCover); err == nil {
		t.Error("cover must reject duration")
	}
}

// TestSimpleEntryValidateForValveLevel2 verifies valve rejects level_2.
func TestSimpleEntryValidateForValveLevel2(t *testing.T) {
	t.Parallel()
	l2 := 0.5
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    0.5,
		Level2:   &l2, // forbidden for valve
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryValve); err == nil {
		t.Error("valve must reject level_2")
	}
}

// TestSimpleEntryValidateForValveRampTime verifies valve rejects ramp_time.
func TestSimpleEntryValidateForValveRampTime(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    0.5,
		RampTime: "1s", // forbidden for valve
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryValve); err == nil {
		t.Error("valve must reject ramp_time")
	}
}

// TestSimpleEntryValidateForLightLevel2 verifies light rejects level_2.
func TestSimpleEntryValidateForLightLevel2(t *testing.T) {
	t.Parallel()
	l2 := 0.5
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    0.5,
		Level2:   &l2, // forbidden for light
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryLight); err == nil {
		t.Error("light must reject level_2")
	}
}

// TestSimpleEntryValidateForSwitchLevel2 verifies switch rejects level_2.
func TestSimpleEntryValidateForSwitchLevel2(t *testing.T) {
	t.Parallel()
	l2 := 0.0
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    0.0, // valid binary
		Level2:   &l2, // forbidden for switch
	}
	if err := e.ValidateFor(hmenum.DataPointCategorySwitch); err == nil {
		t.Error("switch must reject level_2")
	}
}

// TestSimpleEntryValidateForSwitchRampTime verifies switch rejects ramp_time.
func TestSimpleEntryValidateForSwitchRampTime(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "09:00",
		Level:    1.0,
		RampTime: "500ms", // forbidden for switch
	}
	if err := e.ValidateFor(hmenum.DataPointCategorySwitch); err == nil {
		t.Error("switch must reject ramp_time")
	}
}

// TestSimpleEntryValidateForLockDoorLockWithPermission verifies door_lock
// rejects a permission field.
func TestSimpleEntryValidateForLockDoorLockWithPermission(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays:   []Weekday{WeekdayMonday},
		Time:       "08:00",
		Level:      1,
		LockMode:   LockModeDoorLock,
		LockAction: LockActionLock,
		Permission: LockPermissionAllowed, // not allowed in door_lock mode
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("door_lock with permission must fail")
	}
}

// TestSimpleEntryValidateForLockUserPermissionWithAction verifies
// user_permission rejects a lock_action.
func TestSimpleEntryValidateForLockUserPermissionWithAction(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays:   []Weekday{WeekdayMonday},
		Time:       "08:00",
		Level:      1,
		LockMode:   LockModeUserPermission,
		Permission: LockPermissionAllowed,
		LockAction: LockActionLock, // not allowed in user_permission mode
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("user_permission with lock_action must fail")
	}
}

// TestSimpleEntryValidateForLockUnknownMode verifies unknown lock mode fails.
func TestSimpleEntryValidateForLockUnknownMode(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "08:00",
		Level:    1,
		LockMode: LockMode("unknown"),
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("unknown lock mode must fail")
	}
}

// TestSimpleEntryValidateForLockExtrasRejected verifies lock rejects
// level_2, ramp_time, and duration.
func TestSimpleEntryValidateForLockExtrasRejected(t *testing.T) {
	t.Parallel()
	l2 := 0.5
	e := SimpleEntry{
		Weekdays:   []Weekday{WeekdayMonday},
		Time:       "08:00",
		Level:      1,
		LockMode:   LockModeDoorLock,
		LockAction: LockActionLock,
		Level2:     &l2,
	}
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("lock with level_2 must fail")
	}

	e.Level2 = nil
	e.RampTime = "1s"
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("lock with ramp_time must fail")
	}

	e.RampTime = ""
	e.Duration = "10s"
	if err := e.ValidateFor(hmenum.DataPointCategoryLock); err == nil {
		t.Error("lock with duration must fail")
	}
}

// TestSimpleEntryValidateInvalidWeekday verifies an invalid weekday is rejected.
func TestSimpleEntryValidateInvalidWeekday(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{"NOTADAY"},
		Time:     "08:00",
		Level:    1,
	}
	if err := e.Validate(); err == nil {
		t.Error("invalid weekday must fail")
	}
}

// TestSimpleEntryValidateAstroOffset verifies the offset range check.
func TestSimpleEntryValidateAstroOffset(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays:           []Weekday{WeekdayMonday},
		Time:               "08:00",
		Level:              1,
		AstroOffsetMinutes: 721, // > 720
	}
	if err := e.Validate(); err == nil {
		t.Error("offset > 720 must fail")
	}
	e.AstroOffsetMinutes = -721
	if err := e.Validate(); err == nil {
		t.Error("offset < -720 must fail")
	}
}

// TestSimpleEntryValidateLevel2Range verifies level_2 range check.
func TestSimpleEntryValidateLevel2Range(t *testing.T) {
	t.Parallel()
	bad := -0.1
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "08:00",
		Level:    1,
		Level2:   &bad,
	}
	if err := e.Validate(); err == nil {
		t.Error("level_2 < 0 must fail")
	}
	over := 1.01
	e.Level2 = &over
	if err := e.Validate(); err == nil {
		t.Error("level_2 > 1 must fail")
	}
}

// TestSimpleEntryValidateInvalidChannel verifies bad channel format rejected.
func TestSimpleEntryValidateInvalidChannel(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays:       []Weekday{WeekdayMonday},
		Time:           "08:00",
		Level:          1,
		TargetChannels: []string{"9_4"}, // not matching pattern
	}
	if err := e.Validate(); err == nil {
		t.Error("invalid channel pattern must fail")
	}
}

// TestSimpleEntryValidateInvalidRampTime verifies invalid ramp time rejected.
func TestSimpleEntryValidateInvalidRampTime(t *testing.T) {
	t.Parallel()
	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "08:00",
		Level:    1,
		RampTime: "5x", // invalid unit
	}
	if err := e.Validate(); err == nil {
		t.Error("invalid ramp_time must fail")
	}
}

// TestClimateValidateWireOnClimate verifies Climate.ValidateWire
// accepts profiles with broken periods and rejects them.
func TestClimateValidateWireOnClimate(t *testing.T) {
	t.Parallel()
	c := NewClimate()
	// A broken period: endtime before starttime.
	p := NewClimateProfile()
	p.Days[WeekdayMonday] = ClimateWeekday{
		Periods: []ClimatePeriod{
			{StartTime: "10:00", EndTime: "08:00", Temperature: 21},
		},
	}
	c.Profiles["P1"] = p
	if err := c.ValidateWire(); err == nil {
		t.Error("Climate.ValidateWire must reject endtime < starttime")
	}
}

// TestClimateValidateRejectsBadKey verifies Climate.Validate rejects a
// bad profile key that was inserted directly.
func TestClimateValidateRejectsBadKey(t *testing.T) {
	t.Parallel()
	c := NewClimate()
	c.Profiles["X1"] = NewClimateProfile()
	if err := c.Validate(); err == nil {
		t.Error("Validate must reject bad profile key X1")
	}
}

// TestClimateValidateWireRejectsBadKey verifies Climate.ValidateWire
// also rejects bad profile keys.
func TestClimateValidateWireRejectsBadKey(t *testing.T) {
	t.Parallel()
	c := NewClimate()
	c.Profiles["X1"] = NewClimateProfile()
	if err := c.ValidateWire(); err == nil {
		t.Error("ValidateWire must reject bad profile key X1")
	}
}

// TestToMinutesEdgeCases exercises the validateClimateTime and toMinutes
// helpers indirectly via ClimatePeriod.Validate.
func TestToMinutesEdgeCases(t *testing.T) {
	t.Parallel()
	// "24:00" should be valid as end-of-day marker.
	p := ClimatePeriod{StartTime: "23:00", EndTime: "24:00", Temperature: 20}
	if err := p.Validate(); err != nil {
		t.Errorf("23:00→24:00: %v", err)
	}
	// Invalid time format should fail.
	p2 := ClimatePeriod{StartTime: "abc", EndTime: "24:00", Temperature: 20}
	if err := p2.Validate(); err == nil {
		t.Error("invalid starttime must fail")
	}
}

// TestSimpleEntryConditionDefaultsToFixedTime verifies that an entry with an
// empty Condition field is treated as ConditionFixedTime during Validate().
// Mirrors schedule_models.py:396 `condition: Field(default="fixed_time")`.
func TestSimpleEntryConditionDefaultsToFixedTime(t *testing.T) {
	t.Parallel()

	e := SimpleEntry{
		Weekdays: []Weekday{WeekdayMonday},
		Time:     "08:00",
		Level:    0.5,
		// Condition deliberately left empty
	}
	// Validate must succeed — empty Condition implies ConditionFixedTime.
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate() with empty Condition failed: %v", err)
	}
}

// TestSimpleEntryConditionAstroRequiresType verifies that an astro condition
// without AstroType still fails validation.
func TestSimpleEntryConditionAstroRequiresType(t *testing.T) {
	t.Parallel()

	e := SimpleEntry{
		Weekdays:  []Weekday{WeekdayMonday},
		Time:      "08:00",
		Level:     0.5,
		Condition: ConditionAstro,
		// AstroType deliberately missing
	}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate() must fail when Condition is astro and AstroType is empty")
	}
}
