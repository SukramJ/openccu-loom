// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// weekprofileSlotsTimeCase is one row of the climate time grammar. The
// expected verdict is written out per row rather than derived from any of
// the implementations: a table that only asks whether the layers agree
// stays green when they agree on the wrong answer.
type weekprofileSlotsTimeCase struct {
	in      string
	accept  bool
	minutes int
}

// TestWeekprofileSlotsClimateTimeGrammarIsOneAcceptanceSet pins the climate
// HH:MM grammar against explicit expectations on every layer that reads or
// writes one.
//
// The grammar used to be spelled once per layer with three different
// acceptance sets. "24:30" is the row that mattered: the write path took it
// as 1470 minutes and put it in the device's MASTER paramset, and the two
// read paths then disagreed about what the device held — one clamped it to
// "24:00", the other dropped the slot without a word. An hour above 23 has
// no wall-clock meaning; the only value above the last minute of the day is
// the end-of-day marker itself.
func TestWeekprofileSlotsClimateTimeGrammarIsOneAcceptanceSet(t *testing.T) {
	t.Parallel()

	cases := []weekprofileSlotsTimeCase{
		{in: "00:00", accept: true, minutes: 0},
		{in: "01:30", accept: true, minutes: 90},
		{in: "1:30", accept: true, minutes: 90},
		{in: "08:30", accept: true, minutes: 510},
		{in: "23:59", accept: true, minutes: 1439},
		{in: "24:00", accept: true, minutes: 1440},
		// An hour of 24 with a non-zero minute is corrected, not refused:
		// every layer must land on 23:55, which is what makes it one
		// acceptance set rather than a clamp in whichever layer happens to
		// see the value first. 24:00 above keeps 1440, the terminator.
		{in: "24:01", accept: true, minutes: 1435},
		{in: "24:30", accept: true, minutes: 1435},
		{in: "25:00", accept: false},
		{in: "12:60", accept: false},
		{in: "8:5", accept: false},
		{in: "0830", accept: false},
		{in: "", accept: false},
		{in: "abc", accept: false},
		{in: "12", accept: false},
	}

	for _, tc := range cases {
		got, err := schedule.ParseClimateTime(tc.in)
		switch {
		case tc.accept && err != nil:
			t.Errorf("schedule.ParseClimateTime(%q) = error %v, want %d", tc.in, err, tc.minutes)
		case tc.accept && got != tc.minutes:
			t.Errorf("schedule.ParseClimateTime(%q) = %d, want %d", tc.in, got, tc.minutes)
		case !tc.accept && err == nil:
			t.Errorf("schedule.ParseClimateTime(%q) = %d, want rejected", tc.in, got)
		}

		// The wire bridge in weekprofile must reach the same verdict; its
		// -1 sentinel is the shape the sort comparators there depend on.
		wantWire := -1
		if tc.accept {
			wantWire = tc.minutes
		}
		if got := weekprofile.ToMinutes(tc.in); got != wantWire {
			t.Errorf("weekprofile.ToMinutes(%q) = %d, want %d", tc.in, got, wantWire)
		}

		// The typed domain validator is the third reader of the same
		// grammar. It runs on every period the REST write path accepts.
		p := schedule.ClimatePeriod{StartTime: "00:00", EndTime: tc.in, Temperature: 21}
		validErr := p.Validate()
		if !tc.accept && validErr == nil {
			t.Errorf("ClimatePeriod{EndTime: %q}.Validate() = nil, want an error", tc.in)
		}
		if tc.accept && tc.minutes > 0 && validErr != nil {
			t.Errorf("ClimatePeriod{EndTime: %q}.Validate() = %v, want nil", tc.in, validErr)
		}

		// ParseSlotTime is what turns a CCU ENDTIME into the string that
		// is then compared by identity all over weekprofile, so it has to
		// reject the same set AND canonicalise what it accepts.
		slotStr, slotErr := weekprofile.ParseSlotTime(tc.in)
		if !tc.accept && slotErr == nil {
			t.Errorf("weekprofile.ParseSlotTime(%q) = %q, want an error", tc.in, slotStr)
		}
		if tc.accept {
			if slotErr != nil {
				t.Errorf("weekprofile.ParseSlotTime(%q) = error %v", tc.in, slotErr)
				continue
			}
			want, err := schedule.FormatClimateTime(tc.minutes)
			if err != nil {
				t.Fatalf("schedule.FormatClimateTime(%d): %v", tc.minutes, err)
			}
			if slotStr != want {
				t.Errorf("weekprofile.ParseSlotTime(%q) = %q, want canonical %q", tc.in, slotStr, want)
			}
		}
	}
}

// TestWeekprofileSlotsClimateTimeRoundTrip pins that the formatter and the
// parser are inverse over the whole legal range, marker included. Without
// this, a formatter that clamps and a parser that rejects can both look
// correct in isolation while the pair loses data.
func TestWeekprofileSlotsClimateTimeRoundTrip(t *testing.T) {
	t.Parallel()
	for mins := 0; mins <= schedule.ClimateEndOfDayMinutes; mins++ {
		s, err := schedule.FormatClimateTime(mins)
		if err != nil {
			t.Fatalf("schedule.FormatClimateTime(%d): %v", mins, err)
		}
		back, err := schedule.ParseClimateTime(s)
		if err != nil {
			t.Fatalf("schedule.ParseClimateTime(%q): %v", s, err)
		}
		if back != mins {
			t.Fatalf("round trip %d -> %q -> %d", mins, s, back)
		}
	}
	for _, bad := range []int{-1, schedule.ClimateEndOfDayMinutes + 1, 9999} {
		if s, err := schedule.FormatClimateTime(bad); err == nil {
			t.Errorf("schedule.FormatClimateTime(%d) = %q, want an error", bad, s)
		}
	}
}
