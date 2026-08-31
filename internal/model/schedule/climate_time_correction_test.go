// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule

import "testing"

// TestParseClimateTimeCorrectingClampsHour24 pins the one correction the
// climate grammar performs, and the two boundaries that are deliberately not
// the CCU editor's behaviour.
func TestParseClimateTimeCorrectingClampsHour24(t *testing.T) {
	cases := []struct {
		in      string
		minutes int
		applied string
		wantErr bool
	}{
		// The correction itself: the CCU editor rewrites hour 24 to 23:55.
		{in: "24:01", minutes: ClimateLastMinuteOfDay, applied: "23:55"},
		{in: "24:30", minutes: ClimateLastMinuteOfDay, applied: "23:55"},
		{in: "24:59", minutes: ClimateLastMinuteOfDay, applied: "23:55"},
		// The end-of-day marker keeps its meaning: 1440 is the firmware's own
		// weekday terminator, so correcting it would destroy the terminator.
		{in: ClimateEndOfDay, minutes: ClimateEndOfDayMinutes, applied: ""},
		// Ordinary times are untouched and report no correction.
		{in: "00:00", minutes: 0, applied: ""},
		{in: "23:55", minutes: ClimateLastMinuteOfDay, applied: ""},
		{in: "7:30", minutes: 7*60 + 30, applied: ""},
		// Above hour 24 stays an error, matching the firmware's range check.
		{in: "25:00", wantErr: true},
		{in: "99:00", wantErr: true},
		{in: "24:60", wantErr: true},
		{in: "2430", wantErr: true},
	}
	for _, c := range cases {
		m, applied, err := ParseClimateTimeCorrecting(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseClimateTimeCorrecting(%q) = (%d, %q, nil), want an error", c.in, m, applied)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseClimateTimeCorrecting(%q): %v", c.in, err)
			continue
		}
		if m != c.minutes || applied != c.applied {
			t.Errorf("ParseClimateTimeCorrecting(%q) = (%d, %q), want (%d, %q)",
				c.in, m, applied, c.minutes, c.applied)
		}
	}
}

// TestParseClimateTimeAcceptsWhatTheCorrectingFormAccepts keeps the two entry
// points on one acceptance set. Two spellings of this grammar that disagree is
// the exact defect the single acceptance set was introduced to remove, so a
// divergence here must fail rather than be discovered on a device.
func TestParseClimateTimeAcceptsWhatTheCorrectingFormAccepts(t *testing.T) {
	for _, in := range []string{
		"00:00", "7:30", "23:55", "23:59", ClimateEndOfDay,
		"24:01", "24:30", "24:59", "25:00", "24:60", "", ":", "2430", "-1:00",
	} {
		m1, err1 := ParseClimateTime(in)
		m2, _, err2 := ParseClimateTimeCorrecting(in)
		if (err1 == nil) != (err2 == nil) {
			t.Errorf("%q: ParseClimateTime err=%v but ParseClimateTimeCorrecting err=%v", in, err1, err2)
			continue
		}
		if err1 == nil && m1 != m2 {
			t.Errorf("%q: ParseClimateTime = %d, ParseClimateTimeCorrecting = %d", in, m1, m2)
		}
	}
}

// TestCorrectedTimeSurvivesTheRoundTrip is the property the correction exists
// for: a corrected time must be one every read path can render back. An
// accepted value that FormatClimateTime rejects would reproduce the original
// defect -- a slot saved by the operator that silently disappears on the next
// read -- one layer further in.
func TestCorrectedTimeSurvivesTheRoundTrip(t *testing.T) {
	m, applied, err := ParseClimateTimeCorrecting("24:30")
	if err != nil {
		t.Fatalf("ParseClimateTimeCorrecting: %v", err)
	}
	rendered, err := FormatClimateTime(m)
	if err != nil {
		t.Fatalf("FormatClimateTime(%d): %v", m, err)
	}
	if rendered != applied {
		t.Errorf("FormatClimateTime(%d) = %q, want the applied form %q", m, rendered, applied)
	}
	back, _, err := ParseClimateTimeCorrecting(rendered)
	if err != nil || back != m {
		t.Errorf("re-parsing %q = (%d, %v), want (%d, nil)", rendered, back, err, m)
	}
}
