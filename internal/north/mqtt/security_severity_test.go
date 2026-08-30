// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSecuritySeverityOptionsFollowTheDomainLadder pins the enum sensor's
// declared options to the domain's severity ladder, in the domain's order.
//
// Home Assistant refuses a value an enum sensor did not declare. The options
// used to be typed out here as string literals beside hmenum's own ladder, so
// a rung added to the domain would have been published as a state HA drops on
// the floor — silently, because a rejected enum value is not an error the
// daemon ever sees.
func TestSecuritySeverityOptionsFollowTheDomainLadder(t *testing.T) {
	t.Parallel()
	got := securitySeverityOptions()
	ladder := hmenum.SecuritySeverities()
	if len(ladder) == 0 {
		t.Fatal("the domain ladder is empty — the guard lost its subject")
	}
	if len(got) != len(ladder) {
		t.Fatalf("options has %d entries, the ladder has %d: %v vs %v", len(got), len(ladder), got, ladder)
	}
	for i, sev := range ladder {
		if got[i] != string(sev) {
			t.Errorf("option %d is %q, the ladder says %q — order carries meaning here", i, got[i], sev)
		}
	}
}
