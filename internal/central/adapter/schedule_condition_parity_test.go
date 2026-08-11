// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	schedulemodel "github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// TestScheduleConditionVocabularyMatchesTheCCU pins both translations of
// the `<NN>_WP_CONDITION` integer against the device.
//
// The daemon converts that one CCU field in two places — the REST
// schedules domain and the week-profile model — and they disagreed on
// six of the eight values. The CCU's own editor is the authority; its
// option list is, in order, Fixed, Astro, FixedIfBeforeAstro,
// AstroIfBeforeFixed, FixedIfAfterAstro, AstroIfAfterFixed,
// EarliestOfFixedAndAstro, LatestOfFixedAndAstro (`arOptions` in
// WebUI/www/config/easymodes/js/HmIPWeeklyProgram.js).
//
// The week-profile side named condition 2 "astro_before_fixed" where the
// device means "fixed if before astro" — the two roles swapped — and
// called 6 and 7 "between" and "or" where the device selects the
// earlier or later of the two times. A schedule read through that path
// reported a rule the device does not implement, and one written through
// it selected a different rule than the name promised.
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
		if got := scheduleConditionByID[id]; got != name {
			t.Errorf("REST vocabulary: condition %d = %q, want %q", id, got, name)
		}
		if got := string(weekprofile.ConditionForWire(id)); got != name {
			t.Errorf("week-profile vocabulary: condition %d = %q, want %q", id, got, name)
		}
	}

	// Both directions, so a write selects what its name promises.
	for id, name := range want {
		if got := scheduleConditionIDByName[name]; got != id {
			t.Errorf("REST reverse: %q = %d, want %d", name, got, id)
		}
		if got := weekprofile.WireForCondition(schedulemodel.Condition(name)); got != id {
			t.Errorf("week-profile reverse: %q = %d, want %d", name, got, id)
		}
	}
}
