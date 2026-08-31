// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// TimerUnit is the ordinal of the unit parameter that accompanies every
// CCU timer value — DURATION_UNIT, ON_TIME_UNIT, RAMP_TIME_UNIT. The wire
// carries the ordinal, never the label, and a timer is a (value, unit)
// pair: the same ordinal therefore has to mean the same thing to every
// encoder, or one requested duration reaches the device as another.
//
// The ordinals are positions in the parameter's own VALUE_LIST. The
// captured HmIP-MP3P ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER paramset
// description in
// internal/model/custom/siren/testdata/hmip_mp3p_sound_receiver_values.json
// declares DURATION_UNIT as ENUM with VALUE_LIST ["S", "M", "H", "10MS"],
// which is where the three constants below get their numbers.
type TimerUnit int

// TimerUnit values, in DURATION_UNIT VALUE_LIST order.
//
// The list's fourth entry, "10MS" at ordinal 3, deliberately has no
// constant. No encoder in this tree selects it, and every timer encoder
// promotes upwards from seconds only, so a constant for it would offer a
// unit that nothing here knows how to produce or read back.
const (
	// TimerUnitSeconds is VALUE_LIST entry "S" and the parameter's DEFAULT.
	TimerUnitSeconds TimerUnit = 0
	// TimerUnitMinutes is VALUE_LIST entry "M".
	TimerUnitMinutes TimerUnit = 1
	// TimerUnitHours is VALUE_LIST entry "H". It also carries the
	// disabled-timer sentinel, which is written in the hours slot so it
	// cannot collide with a real duration.
	TimerUnitHours TimerUnit = 2
)
