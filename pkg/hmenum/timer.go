// SPDX-License-Identifier: Apache-2.0
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
// which is where the four constants below get their numbers. A channel that
// declares its own list is encoded and decoded by that list
// (internal/model/custom DurationUnits); these constants serve a channel
// whose unit parameter carries no list.
// loom:reachable:reason="the ordinal type of the CCU DURATION_UNIT wire field: internal/model/custom/duration_units.go reads TimerUnitSeconds/Minutes/Hours/10ms as the ordinals of a channel without a declared list, and internal/model/combined/timer.go carries it as toTimerUnit's return; a numeric type whose methods production never calls, which the analyzer's type heuristic (reachable only via its methods) cannot see used"
type TimerUnit int

// TimerUnit values, in DURATION_UNIT VALUE_LIST order.
const (
	// TimerUnitSeconds is VALUE_LIST entry "S" and the parameter's DEFAULT.
	TimerUnitSeconds TimerUnit = 0
	// TimerUnitMinutes is VALUE_LIST entry "M".
	TimerUnitMinutes TimerUnit = 1
	// TimerUnitHours is VALUE_LIST entry "H". It also carries the
	// disabled-timer sentinel, which is written in the hours slot so it
	// cannot collide with a real duration.
	TimerUnitHours TimerUnit = 2
	// TimerUnit10ms is VALUE_LIST entry "10MS": a count of hundredths of a
	// second. Not every channel offers it — HmIP-ASIR-2 channel 3 declares
	// DURATION_UNIT as ["S", "M", "H"] — so an encoder may choose it only
	// where the channel's own list carries it.
	TimerUnit10ms TimerUnit = 3
)
