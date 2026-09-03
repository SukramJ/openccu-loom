// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// The profile-index bound has one exported owner,
// [weekprofile.MinProfileIndex] / [weekprofile.MaxProfileIndex]. The profile
// *key* grammar has a different one, [schedule.IsValidProfileKey], and it
// cannot read those constants: weekprofile already imports schedule, so the
// reverse edge is an import cycle and schedule keeps its own copy of the
// range.
//
// This test is the tie between the two. It asserts the behaviour rather than
// the constants — schedule's copy is package-local by necessity — so moving
// either bound without the other lands here.
func TestScheduleProfileKeyRangeMatchesTheDomainBound(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		n    int
		want bool
	}{
		{n: weekprofile.MinProfileIndex - 1, want: false},
		{n: weekprofile.MinProfileIndex, want: true},
		{n: weekprofile.MaxProfileIndex, want: true},
		{n: weekprofile.MaxProfileIndex + 1, want: false},
	} {
		key := fmt.Sprintf("P%d", tc.n)
		if got := schedule.IsValidProfileKey(key); got != tc.want {
			t.Errorf("schedule.IsValidProfileKey(%q) = %v, want %v (weekprofile bound is %d..%d)",
				key, got, tc.want, weekprofile.MinProfileIndex, weekprofile.MaxProfileIndex)
		}
		if got := weekprofile.ValidProfileIndex(tc.n); got != tc.want {
			t.Errorf("weekprofile.ValidProfileIndex(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}
