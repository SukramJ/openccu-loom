// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// TestToMinutesInvalidInput verifies non-parseable strings return -1.
// Note: ToMinutes does not bounds-check hours — "25:00" parses as 1500, not -1.
// Only truly unparseable input (empty, non-numeric, too short) returns -1.
func TestToMinutesInvalidInput(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "abc", "bad", "xy"} {
		if got := ToMinutes(s); got >= 0 {
			t.Errorf("ToMinutes(%q) = %d, want -1", s, got)
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
