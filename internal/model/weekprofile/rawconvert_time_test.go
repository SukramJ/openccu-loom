// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestSplitHHMMRejectsMalformedTimes pins the switching-time parse that
// feeds a group's FIXED_HOUR / FIXED_MINUTE pair.
//
// The string arrives from a REST body and reaches the CCU as two
// integers, so anything this accepts becomes a switching time on a real
// device. A lenient parse is worse than a rejection here: "1:2" read as
// 01:02 silently schedules an entry an hour and a half from where the
// caller meant it, and nothing downstream re-checks.
func TestSplitHHMMRejectsMalformedTimes(t *testing.T) {
	t.Parallel()

	valid := []struct {
		in        string
		hour, min int
	}{
		{"08:30", 8, 30},
		{"9:05", 9, 5},
		{"00:00", 0, 0},
		{"23:59", 23, 59},
	}
	for _, tc := range valid {
		h, m, err := splitHHMM(tc.in)
		if err != nil {
			t.Errorf("splitHHMM(%q): unexpected error %v", tc.in, err)
			continue
		}
		if h != tc.hour || m != tc.min {
			t.Errorf("splitHHMM(%q) = (%d, %d), want (%d, %d)", tc.in, h, m, tc.hour, tc.min)
		}
	}

	invalid := []string{
		"1:2",    // too short to be a time
		"123:45", // too long
		"0830",   // no separator
		"25:00",  // hour past midnight
		"08:60",  // minute past the hour
		"ab:30",  // non-numeric hour
		"08:xy",  // non-numeric minute
		"",
	}
	for _, in := range invalid {
		if _, _, err := splitHHMM(in); err == nil {
			t.Errorf("splitHHMM(%q) accepted a malformed time", in)
		}
	}
}
