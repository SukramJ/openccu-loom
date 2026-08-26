// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	schedulemodel "github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// TestScheduleConditionVocabularyMatchesTheCCU pins the translation of
// the `<NN>_WP_CONDITION` integer against the device.
//
// The CCU's own editor is the authority; its option list is, in order,
// Fixed, Astro, FixedIfBeforeAstro, AstroIfBeforeFixed, FixedIfAfterAstro,
// AstroIfAfterFixed, EarliestOfFixedAndAstro, LatestOfFixedAndAstro
// (`arOptions` in WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js).
//
// The daemon translated that one field in two places and they disagreed
// on six of the eight values: condition 2 was called "astro_before_fixed"
// where the device means "fixed if before astro" — the two roles swapped
// — and 6 and 7 were called "between" and "or" where the device selects
// the earlier or later of the two times. A schedule read through that
// path reported a rule the device does not implement, and one written
// through it selected a different rule than the name promised.
//
// There is one table now, so this test pins it against the device rather
// than the two against each other. The guard that the second table does
// not grow back is TestSimpleScheduleReadAgreesAcrossSurfaces.
func TestScheduleConditionVocabularyMatchesTheCCU(t *testing.T) {
	t.Parallel()

	// Straight from the CCU editor's option list, in its order.
	want := map[int]string{
		0: "fixed_time",
		1: "astro",
		2: "fixed_if_before_astro",
		3: "astro_if_before_fixed",
		4: "fixed_if_after_astro",
		5: "astro_if_after_fixed",
		6: "earliest_of_fixed_and_astro",
		7: "latest_of_fixed_and_astro",
	}

	for id, name := range want {
		if got := string(weekprofile.ConditionForWire(id)); got != name {
			t.Errorf("condition %d = %q, want %q", id, got, name)
		}
	}

	// Both directions, so a write selects what its name promises.
	for id, name := range want {
		if got := weekprofile.WireForCondition(schedulemodel.Condition(name)); got != id {
			t.Errorf("reverse: %q = %d, want %d", name, got, id)
		}
		if !weekprofile.ConditionIsKnown(schedulemodel.Condition(name)) {
			t.Errorf("%q is not recognised as a condition", name)
		}
	}

	// The vocabulary is closed: a name the device does not offer must be
	// rejected rather than silently written as condition 0. The REST
	// surface takes the condition as a free string, so this is the only
	// thing standing between a typo and a schedule that fires at the
	// wrong time.
	for _, bogus := range []string{"astro_before_fixed", "between", "or", "FIXED_TIME", "nonsense"} {
		if weekprofile.ConditionIsKnown(schedulemodel.Condition(bogus)) {
			t.Errorf("%q is accepted as a condition but the CCU has no such option", bogus)
		}
	}
}
