// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestToMinutesMinutesToTimeStrRoundtrip verifies that ToMinutes and
// MinutesToTimeStr are inverse functions for every quarter-hour between
// 00:00 and 24:00.
func TestToMinutesMinutesToTimeStrRoundtrip(t *testing.T) {
	t.Parallel()
	for mins := 0; mins <= 24*60; mins += 15 {
		s, err := MinutesToTimeStr(mins)
		if err != nil {
			t.Errorf("MinutesToTimeStr(%d): %v", mins, err)
			continue
		}
		got := ToMinutes(s)
		if got != mins {
			t.Errorf("roundtrip(%d): MinutesToTimeStr=%q ToMinutes=%d", mins, s, got)
		}
	}
}

// TestToMinutesKnownValues covers a handful of canonical clock values.
func TestToMinutesKnownValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		want int
	}{
		{"00:00", 0},
		{"06:00", 360},
		{"08:30", 510},
		{"22:45", 1365},
		{"24:00", 1440},
	}
	for _, tc := range cases {
		got := ToMinutes(tc.s)
		if got != tc.want {
			t.Errorf("ToMinutes(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// TestToMinutesInvalidInput verifies that everything outside the climate
// time grammar returns -1 — unparseable input and an out-of-range clock
// alike. An hour above 24 used to parse here as a plain minute count, which
// is how "25:00" reached the device as 1500 minutes and was then read back
// differently by each plane.
func TestToMinutesInvalidInput(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "abc", "bad", "xy", "25:00", "12:60"} {
		if got := ToMinutes(s); got >= 0 {
			t.Errorf("ToMinutes(%q) = %d, want -1", s, got)
		}
	}
}

// TestToMinutesCorrectsHour24 is the counterpart: hour 24 with a non-zero
// minute is not invalid input but a corrected one, and this wire bridge has
// to agree with the grammar rather than drop the slot. 24:00 stays 1440 --
// it is the weekday terminator, not a time to correct.
func TestToMinutesCorrectsHour24(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want int
	}{
		{in: "24:01", want: 1435},
		{in: "24:30", want: 1435},
		{in: "24:59", want: 1435},
		{in: "24:00", want: 1440},
	} {
		if got := ToMinutes(tc.in); got != tc.want {
			t.Errorf("ToMinutes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestMinutesToTimeStrOutOfRange verifies an error is returned for out-of-range
// minute counts.
func TestMinutesToTimeStrOutOfRange(t *testing.T) {
	t.Parallel()
	for _, mins := range []int{-1, 1441, 9999} {
		if _, err := MinutesToTimeStr(mins); err == nil {
			t.Errorf("MinutesToTimeStr(%d): expected error, got nil", mins)
		}
	}
}
