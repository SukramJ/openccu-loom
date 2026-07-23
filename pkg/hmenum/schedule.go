// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// ScheduleType discriminates the kinds of schedules a CCU exposes.
type ScheduleType string

// ScheduleType values.
const (
	ScheduleTypeClimate ScheduleType = "climate"
	ScheduleTypeDefault ScheduleType = "default"
)

// String returns the wire representation.
func (t ScheduleType) String() string { return string(t) }

// ScheduleProfile names one of the six profile slots a CCU climate
// device supports.
type ScheduleProfile string

// ScheduleProfile values.
const (
	ScheduleProfileP1 ScheduleProfile = "P1"
	ScheduleProfileP2 ScheduleProfile = "P2"
	ScheduleProfileP3 ScheduleProfile = "P3"
	ScheduleProfileP4 ScheduleProfile = "P4"
	ScheduleProfileP5 ScheduleProfile = "P5"
	ScheduleProfileP6 ScheduleProfile = "P6"
)

// String returns the wire representation.
func (p ScheduleProfile) String() string { return string(p) }

// WeekdayStr names the weekday as the string token the CCU uses.
type WeekdayStr string

// WeekdayStr values.
const (
	WeekdayStrMonday    WeekdayStr = "MONDAY"
	WeekdayStrTuesday   WeekdayStr = "TUESDAY"
	WeekdayStrWednesday WeekdayStr = "WEDNESDAY"
	WeekdayStrThursday  WeekdayStr = "THURSDAY"
	WeekdayStrFriday    WeekdayStr = "FRIDAY"
	WeekdayStrSaturday  WeekdayStr = "SATURDAY"
	WeekdayStrSunday    WeekdayStr = "SUNDAY"
)

// String returns the wire representation.
func (d WeekdayStr) String() string { return string(d) }

// WeekdayInt is the bitmask-friendly numeric weekday representation.
type WeekdayInt int

// WeekdayInt values.
const (
	WeekdayIntSunday    WeekdayInt = 1
	WeekdayIntMonday    WeekdayInt = 2
	WeekdayIntTuesday   WeekdayInt = 4
	WeekdayIntWednesday WeekdayInt = 8
	WeekdayIntThursday  WeekdayInt = 16
	WeekdayIntFriday    WeekdayInt = 32
	WeekdayIntSaturday  WeekdayInt = 64
)

// ScheduleField names one of the per-group paramset keys that a device
// exposes in its MASTER paramset (pattern `NN_WP_<FIELDNAME>`).
//
// The set of fields a device supports is discovered at runtime from its MASTER
// paramset description. Mirrors the Python ScheduleField enum from const.py.
type ScheduleField string

// ScheduleField values.
const (
	ScheduleFieldAstroOffset    ScheduleField = "ASTRO_OFFSET"
	ScheduleFieldAstroType      ScheduleField = "ASTRO_TYPE"
	ScheduleFieldCondition      ScheduleField = "CONDITION"
	ScheduleFieldDurationBase   ScheduleField = "DURATION_BASE"
	ScheduleFieldDurationFactor ScheduleField = "DURATION_FACTOR"
	ScheduleFieldFixedHour      ScheduleField = "FIXED_HOUR"
	ScheduleFieldFixedMinute    ScheduleField = "FIXED_MINUTE"
	ScheduleFieldLevel          ScheduleField = "LEVEL"
	ScheduleFieldLevel2         ScheduleField = "LEVEL_2"
	ScheduleFieldRampTimeBase   ScheduleField = "RAMP_TIME_BASE"
	ScheduleFieldRampTimeFactor ScheduleField = "RAMP_TIME_FACTOR"
	ScheduleFieldTargetChannels ScheduleField = "TARGET_CHANNELS"
	ScheduleFieldWeekday        ScheduleField = "WEEKDAY"
	// Universal-light per-switch-point colour/effect fields. ColorType is
	// the discriminator (0 = hue/saturation, 1 = colour temperature,
	// 2 = effect); ColorValue is the packed value; both are carried as
	// opaque ints for a lossless round-trip. OutputBehaviour is the
	// HmIP-BSL signal-LED field.
	ScheduleFieldColorType       ScheduleField = "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE"
	ScheduleFieldColorValue      ScheduleField = "HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_VALUE"
	ScheduleFieldOutputBehaviour ScheduleField = "OUTPUT_BEHAVIOUR"
)

// String returns the wire representation.
func (f ScheduleField) String() string { return string(f) }

// TimeBase is the time-unit selector some CCU duration parameters use.
type TimeBase int

// TimeBase values.
const (
	TimeBaseMs100 TimeBase = 0
	TimeBaseSec1  TimeBase = 1
	TimeBaseSec5  TimeBase = 2
	TimeBaseSec10 TimeBase = 3
	TimeBaseMin1  TimeBase = 4
	TimeBaseMin5  TimeBase = 5
	TimeBaseMin10 TimeBase = 6
	TimeBaseHour1 TimeBase = 7
)
