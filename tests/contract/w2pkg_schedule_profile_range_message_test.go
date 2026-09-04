// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// The climate-profile index bound has one owner,
// [weekprofile.MaxProfileIndex], and one enforcer,
// [weekprofile.ValidProfileIndex]. It is also *spelled out* in the sentinel
// message a north-bound caller reads back from a 422:
// [hmerr.ErrScheduleCopyProfileRange] ends in "(1..6)".
//
// That string is prose, not enforcement — but it is the only statement of the
// bound the API consumer ever sees, so a bound that moves while the message
// stays behind tells every rejected caller a range that is no longer true.
// TestDomainConstantsHaveASingleSource pins the constant itself; nothing tied
// the message to it, which is exactly the drift this guard closes.
func TestW2PkgScheduleRangeMessageMatchesTheDomainBound(t *testing.T) {
	t.Parallel()

	want := fmt.Sprintf("(%d..%d)", weekprofile.MinProfileIndex, weekprofile.MaxProfileIndex)
	got := hmerr.ErrScheduleCopyProfileRange.Error()
	if !strings.Contains(got, want) {
		t.Errorf("hmerr.ErrScheduleCopyProfileRange = %q, which does not state the bound its own domain "+
			"enforces: weekprofile.MinProfileIndex..MaxProfileIndex is %s. Every 422 the schedule copy path "+
			"raises would quote a range the daemon does not apply", got, want)
	}
}
