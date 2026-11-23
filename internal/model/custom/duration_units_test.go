// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type encodeCase struct {
	name      string
	d         time.Duration
	wantValue int32
	wantUnit  int32
}

func runEncodeCases(t *testing.T, u DurationUnits, cases []encodeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v, unit := u.Encode(c.d)
			if v != c.wantValue || unit != c.wantUnit {
				t.Errorf("Encode(%v) = (%d, %d), want (%d, %d)", c.d, v, unit, c.wantValue, c.wantUnit)
			}
		})
	}
}

// TestDurationUnitsEncodeThresholds pins where a channel without a declared
// list moves from one unit to the next: only once the count passes the value
// parameter's maximum of 16343, so 61 s stays (61, S) and is not (1, M).
func TestDurationUnitsEncodeThresholds(t *testing.T) {
	t.Parallel()
	s, m, h := int32(hmenum.TimerUnitSeconds), int32(hmenum.TimerUnitMinutes), int32(hmenum.TimerUnitHours)
	runEncodeCases(t, DurationUnits{}, []encodeCase{
		{"61 s stays in seconds", 61 * time.Second, 61, s},
		{"5 min stays in seconds", 5 * time.Minute, 300, s},
		{"2 h stays in seconds", 2 * time.Hour, 7200, s},
		{"the largest seconds count", 16343 * time.Second, 16343, s},
		{"one count more moves to minutes", 16344 * time.Second, 272, m},
		{"5 h in minutes", 5 * time.Hour, 300, m},
		{"past the minutes maximum moves to hours", (16344*60 + 1) * time.Second, 272, h},
		{"1000 h", 1000 * time.Hour, 1000, h},
		{"zero", 0, 0, s},
		{"negative", -time.Second, 0, s},
		{"the disabled-timer sentinel is the firmware's permanent 31 h", time.Duration(TimerNotUsed) * time.Second, 31, h},
	})
}

// TestDurationUnitsRoundHalfUpToTheNearestStep pins the rounding rule in every
// unit: half a step and more rounds up, less rounds down, and a positive
// duration is never less than one step. The 272.5 and 980.57 minute cases are
// the ones where truncation and rounding disagree.
func TestDurationUnitsRoundHalfUpToTheNearestStep(t *testing.T) {
	t.Parallel()
	s, m := int32(hmenum.TimerUnitSeconds), int32(hmenum.TimerUnitMinutes)
	runEncodeCases(t, DurationUnits{}, []encodeCase{
		{"272.88 min rounds up", 16373 * time.Second, 273, m},
		{"272.5 min rounds up", 16350 * time.Second, 273, m},
		{"980.57 min rounds up", 58834 * time.Second, 981, m},
		{"272.4 min rounds down", 16344 * time.Second, 272, m},
		{"1.5 s rounds up", 1500 * time.Millisecond, 2, s},
		{"1.4 s rounds down", 1400 * time.Millisecond, 1, s},
		{"0.4 s is one step", 400 * time.Millisecond, 1, s},
		{"1 ns is one step", time.Nanosecond, 1, s},
	})
}

// TestDurationUnitsUseTenMillisecondsWhereTheListOffersIt pins the 10MS rule
// on the list HmIP-RGBW, HmIP-BSL and HmIP-MP3P declare: a duration that is
// not a whole second goes out in hundredths while the count fits; a whole
// second stays in seconds.
func TestDurationUnitsUseTenMillisecondsWhereTheListOffersIt(t *testing.T) {
	t.Parallel()
	u := DurationUnitsFromList([]string{"S", "M", "H", "10MS"})
	if !u.OffersTenMilliseconds() {
		t.Fatal("the list carries 10MS but OffersTenMilliseconds reports false")
	}
	runEncodeCases(t, u, []encodeCase{
		{"half a second", 500 * time.Millisecond, 50, 3},
		{"half a 10MS step rounds up", 5 * time.Millisecond, 1, 3},
		{"below half a step is still one step", 4 * time.Millisecond, 1, 3},
		{"1.005 s rounds up", 1005 * time.Millisecond, 101, 3},
		{"the largest 10MS count", 163430 * time.Millisecond, 16343, 3},
		{"past the largest count falls back to seconds", 163435 * time.Millisecond, 163, 0},
		{"a whole second stays in seconds", 5 * time.Second, 5, 0},
		{"a whole minute stays in seconds", time.Minute, 60, 0},
	})
}

// TestDurationUnitsReadTheOrdinalsFromTheList proves the ordinals come from
// the channel's list and not from a fixed numbering: in a list in another
// order, every unit moves with its label, in both directions.
func TestDurationUnitsReadTheOrdinalsFromTheList(t *testing.T) {
	t.Parallel()
	u := DurationUnitsFromList([]string{"10MS", "H", "M", "S"})
	runEncodeCases(t, u, []encodeCase{
		{"10MS at position 0", 500 * time.Millisecond, 50, 0},
		{"S at position 3", 30 * time.Second, 30, 3},
		{"M at position 2", 16344 * time.Second, 272, 2},
		{"H at position 1 for the sentinel", time.Duration(TimerNotUsed) * time.Second, 31, 1},
	})
	for _, c := range []struct {
		value float64
		unit  int32
		want  float64
	}{{50, 0, 0.5}, {2, 1, 7200}, {3, 2, 180}, {30, 3, 30}} {
		if got, ok := u.Seconds(c.value, c.unit); !ok || got != c.want {
			t.Errorf("Seconds(%v, %d) = (%v, %v), want %v", c.value, c.unit, got, ok, c.want)
		}
	}
}

// TestDurationUnitsWithoutTenMilliseconds pins a list that ends at H, as
// HmIP-ASIR-2 channel 3 declares DURATION_UNIT: no encode reaches ordinal 3,
// and a reported ordinal 3 is not a unit of this channel.
func TestDurationUnitsWithoutTenMilliseconds(t *testing.T) {
	t.Parallel()
	u := DurationUnitsFromList([]string{"S", "M", "H"})
	if u.OffersTenMilliseconds() {
		t.Fatal("the list carries no 10MS but OffersTenMilliseconds reports true")
	}
	runEncodeCases(t, u, []encodeCase{
		{"half a second is one second", 500 * time.Millisecond, 1, 0},
		{"1.5 s rounds up", 1500 * time.Millisecond, 2, 0},
	})
	if _, ok := u.Seconds(50, 3); ok {
		t.Error("Seconds(50, 3) decoded an ordinal the channel's list does not have")
	}
}

// TestDurationUnitsWithoutAListDecodeTenMilliseconds pins the zero value's
// read half: it never encodes 10MS, but a pair reported in 10MS by a device
// or another client still reads as hundredths of a second.
func TestDurationUnitsWithoutAListDecodeTenMilliseconds(t *testing.T) {
	t.Parallel()
	var u DurationUnits
	if v, unit := u.Encode(500 * time.Millisecond); unit == int32(hmenum.TimerUnit10ms) {
		t.Errorf("Encode(500ms) without a declared list chose 10MS: (%d, %d)", v, unit)
	}
	if got, ok := u.Seconds(50, int32(hmenum.TimerUnit10ms)); !ok || got != 0.5 {
		t.Errorf("Seconds(50, 10MS) = (%v, %v), want 0.5", got, ok)
	}
	if _, ok := u.Seconds(5, 99); ok {
		t.Error("Seconds(5, 99) decoded an ordinal no unit has")
	}
}

// TestDurationUnitsFromListIgnoresANonTimerList pins the documented boundary:
// a list that does not name S, M and H is not a timer unit list and reads as
// no list at all.
func TestDurationUnitsFromListIgnoresANonTimerList(t *testing.T) {
	t.Parallel()
	u := DurationUnitsFromList([]string{"100MS", "1S", "5S", "10S", "1M", "5M", "10M", "H"})
	if u.OffersTenMilliseconds() || u.list != nil {
		t.Errorf("a list without S and M was taken as a timer unit list: %v", u.list)
	}
	if got := DurationUnitsOf(nil, hmenum.ParameterDurationUnit); got.list != nil {
		t.Errorf("DurationUnitsOf(nil) = %v, want no list", got.list)
	}
}
