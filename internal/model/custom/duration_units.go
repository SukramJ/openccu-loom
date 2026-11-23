// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"math"
	"slices"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The labels of a timer unit VALUE_LIST. The captured HmIP-RGBW, HmIP-BSL and
// HmIP-MP3P paramset descriptions declare DURATION_UNIT and RAMP_TIME_UNIT as
// ["S", "M", "H", "10MS"]; HmIP-ASIR-2 channel 3 declares DURATION_UNIT as
// ["S", "M", "H"].
const (
	unitLabel10ms    = "10MS"
	unitLabelSeconds = "S"
	unitLabelMinutes = "M"
	unitLabelHours   = "H"
)

// durationStep is one unit a timer pair can be expressed in: its label, the
// length of one count, and the ordinal it has on a channel whose unit
// parameter declares no list.
type durationStep struct {
	label    string
	size     time.Duration
	fallback hmenum.TimerUnit
}

// durationSteps runs from the finest unit to the coarsest — the order in which
// the encoder tries them.
var durationSteps = [...]durationStep{
	{unitLabel10ms, 10 * time.Millisecond, hmenum.TimerUnit10ms},
	{unitLabelSeconds, time.Second, hmenum.TimerUnitSeconds},
	{unitLabelMinutes, time.Minute, hmenum.TimerUnitMinutes},
	{unitLabelHours, time.Hour, hmenum.TimerUnitHours},
}

// timerNotUsedDuration is [TimerNotUsed] as a duration, the form the encoder
// compares against.
const timerNotUsedDuration = time.Duration(TimerNotUsed * float64(time.Second))

// DurationUnits is what one timer pair's unit parameter (DURATION_UNIT,
// RAMP_TIME_UNIT) declares: which units the channel accepts and
// at which ordinal. The wire carries the position in the parameter's own
// VALUE_LIST, so the ordinals are read from that list, never assumed.
//
// The zero value is a unit parameter without a declared list. It encodes in
// S, M and H at the [hmenum.TimerUnit] ordinals and never in 10MS, because
// nothing says the channel offers it; it still decodes a reported 10MS.
type DurationUnits struct {
	list []string
}

// DurationUnitsFromList returns the units a VALUE_LIST declares. A list that
// does not name S, M and H is not a timer unit list — every DURATION_UNIT and
// RAMP_TIME_UNIT in the captured descriptor corpus names all three — and is
// treated as no list at all.
func DurationUnitsFromList(list []string) DurationUnits {
	for _, base := range []string{unitLabelSeconds, unitLabelMinutes, unitLabelHours} {
		if !slices.Contains(list, base) {
			return DurationUnits{}
		}
	}
	return DurationUnits{list: slices.Clone(list)}
}

// DurationUnitsOf returns the units unitParam declares on ch: the zero value
// when the channel, the parameter or its VALUE_LIST is missing.
func DurationUnitsOf(ch *device.Channel, unitParam hmenum.Parameter) DurationUnits {
	if ch == nil || unitParam == "" {
		return DurationUnits{}
	}
	dp := ch.Parameter(unitParam)
	if dp == nil {
		return DurationUnits{}
	}
	return DurationUnitsFromList(dp.ParameterData().ValueList)
}

// OffersTenMilliseconds reports whether the channel declares the 10MS unit.
func (u DurationUnits) OffersTenMilliseconds() bool {
	return slices.Contains(u.list, unitLabel10ms)
}

// ordinal is the wire ordinal of step s on this channel, and whether the
// channel accepts it. decoding admits 10MS on a channel without a list: such
// a channel was never sent 10MS from here, but a pair reported in it still
// means hundredths of a second.
func (u DurationUnits) ordinal(s durationStep, decoding bool) (int32, bool) {
	if u.list == nil {
		if s.label == unitLabel10ms && !decoding {
			return 0, false
		}
		return int32(s.fallback), true //nolint:gosec // an hmenum.TimerUnit constant, 0..3
	}
	i := slices.Index(u.list, s.label)
	if i < 0 {
		return 0, false
	}
	return int32(i), true //nolint:gosec // an index into a VALUE_LIST of a handful of entries
}

// Encode maps d onto the (value, unit) pair the CCU's timer parameters take.
// The unit comes back as int32 because every caller stages it straight into a
// wire paramset.
//
// The unit is the finest one that keeps the count within the value
// parameter's declared maximum ([timeUnitThreshold]): 10MS where the channel
// declares it and d is not a whole number of seconds, then S, M, H. A whole
// second stays in seconds, so the wire for whole-second durations is the same
// whether or not the channel offers 10MS.
//
// Every unit rounds half-up to its nearest step — 16373 s is (273, M) — and a
// positive duration is at least one step, so a short timer never collapses to
// a zero count. Zero and negative durations are (0, S). The disabled-timer
// sentinel [TimerNotUsed] — 111600 s, which is 31 h — is (31, H): the pair
// the CCU's own dialogs write for "permanently on" when the time limit is
// unchecked (OpenCCU-Base
// src/webui/www_source/webui/js/dialog/dimmerCombinedParamDialog.js:571-572;
// the help text, www/webui/js/lang/en/translate.lang.help.js:117, reads "A
// value of 31 hours means 'permanently on'"). Sent as the seconds count with
// the hour unit, 111600 h, it is past DURATION_VALUE's MAX of 16343 and kept
// a signal light dark (notes/parity/by_design.md, A2-BD10).
//
// Rationale and divergence from the reference:
// notes/parity/by_design.md, entry BD-Timer-RoundsToNearestStep.
func (u DurationUnits) Encode(d time.Duration) (value, unit int32) {
	seconds := durationSteps[1]
	if d <= 0 {
		ord, _ := u.ordinal(seconds, false)
		return 0, ord
	}
	if d == timerNotUsedDuration {
		ord, _ := u.ordinal(durationSteps[3], false)
		return int32(timerNotUsedDuration / time.Hour), ord
	}
	var count int64
	var ord int32
	for _, s := range durationSteps {
		o, ok := u.ordinal(s, false)
		if !ok {
			continue
		}
		if s.size < time.Second && d%time.Second == 0 {
			continue
		}
		count, ord = max(roundHalfUp(d, s.size), 1), o
		if count <= timeUnitThreshold {
			break
		}
	}
	return int32(min(count, math.MaxInt32)), ord //nolint:gosec // bounded by min
}

// Seconds converts a (value, unit) pair read from the wire to seconds. It
// reports false when unit is not an ordinal this channel's list names.
func (u DurationUnits) Seconds(value float64, unit int32) (float64, bool) {
	for _, s := range durationSteps {
		if o, ok := u.ordinal(s, true); ok && o == unit {
			if s.size < time.Second {
				return value / float64(time.Second/s.size), true
			}
			return value * float64(s.size/time.Second), true
		}
	}
	return 0, false
}

// roundHalfUp divides d by step and rounds the quotient half-up, in integer
// arithmetic so no float error decides a boundary.
func roundHalfUp(d, step time.Duration) int64 {
	q, r := d/step, d%step
	if 2*r >= step {
		q++
	}
	return int64(q)
}
