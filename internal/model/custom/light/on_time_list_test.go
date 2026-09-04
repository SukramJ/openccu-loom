// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"slices"
	"testing"
)

// deviceOnTimeList is the ON_TIME_LIST value list a device declares: 16
// entries, write-only, MIN "100MS" and MAX "PERMANENTLY_ON". It is not a
// contiguous 100 ms ladder — it skips 600, 800 and 900 ms and 4 s, and it
// continues past 5 s to 60 s.
var deviceOnTimeList = []string{
	"100MS", "200MS", "300MS", "400MS", "500MS", "700MS",
	"1S", "2S", "3S", "5S", "7S", "10S", "20S", "40S", "60S",
	"PERMANENTLY_ON",
}

// TestConvertFlashTimeOnlyEmitsLabelsTheDeviceDeclares pins that the chosen
// label is a member of the device's own value list. A label the device does
// not declare is rejected by the CCU's enum conversion, so the whole atomic
// turn-on put_paramset fails.
func TestConvertFlashTimeOnlyEmitsLabelsTheDeviceDeclares(t *testing.T) {
	t.Parallel()

	for ms := 1; ms <= 70_000; ms += 7 {
		got := ConvertFlashTimeToOnTimeList(ms, deviceOnTimeList)
		if !slices.Contains(deviceOnTimeList, got) {
			t.Fatalf("ConvertFlashTimeToOnTimeList(%d) = %q, which the device does not declare", ms, got)
		}
	}
}

// TestConvertFlashTimeChoosesTheNearestDeclaredEntry pins the mapping at the
// boundaries that the previous fixed table got wrong: it invented 600MS,
// 800MS, 900MS and 4S, and it collapsed everything above 5 s to
// PERMANENTLY_ON although the device expresses 7 s through 60 s.
func TestConvertFlashTimeChoosesTheNearestDeclaredEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		ms   int
		want string
	}{
		{100, "100MS"},
		{600, "500MS"},  // equidistant to 500MS and 700MS; the shorter wins
		{620, "700MS"},  // nearest declared entry
		{800, "700MS"},  // 800MS does not exist
		{900, "1S"},     // 900MS does not exist
		{4000, "3S"},    // 4S does not exist; 3S and 5S are equidistant
		{4600, "5S"},    //
		{10_000, "10S"}, // was PERMANENTLY_ON
		{45_000, "40S"}, // was PERMANENTLY_ON
		{60_000, "60S"}, // was PERMANENTLY_ON
		{120_000, "PERMANENTLY_ON"},
		{0, "PERMANENTLY_ON"},
		{-1, "PERMANENTLY_ON"},
	} {
		if got := ConvertFlashTimeToOnTimeList(tc.ms, deviceOnTimeList); got != tc.want {
			t.Errorf("ConvertFlashTimeToOnTimeList(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}
