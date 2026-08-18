// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEveryMultiplierAgreesWithTheReportedUnit is the guard the multiplier
// surface never had: each side was pinned on its own, so a factor could
// convert into one unit while [CleanupUnit] reported another and nothing
// failed.
//
// The pair is what a consumer sees. A data point publishes its value, the
// unit it names, and the factor that turns one into the other; whoever
// multiplies lands in the unit the data point claims — or the reading is
// mislabelled. TIME_OF_OPERATION shipped exactly that way: the factor
// converts seconds to days while the unit stayed the CCU's seconds, so a
// consumer applying it showed a day count labelled `s`.
//
// The expectation tables are deliberately hand-maintained rather than
// derived: deriving the target unit from the same maps under test would
// assert nothing. Adding a multiplier rule without declaring what unit it
// converts into fails here, which is the point.
func TestEveryMultiplierAgreesWithTheReportedUnit(t *testing.T) {
	t.Parallel()

	// The unit a value lands in after the raw-unit-keyed multiplier.
	wantUnitForRawUnit := map[string]string{
		"100%": "%",
	}
	for rawUnit, mult := range multiplierUnit {
		want, declared := wantUnitForRawUnit[rawUnit]
		if !declared {
			t.Errorf("multiplierUnit has %q (×%v) but no declared target unit — "+
				"add it to wantUnitForRawUnit so the pair stays checkable", rawUnit, mult)
			continue
		}
		// An empty parameter carries no per-parameter override, so this
		// exercises the raw-unit path the multiplier keys off.
		if got := CleanupUnit("", rawUnit); got != want {
			t.Errorf("raw unit %q multiplies by %v into %q, but CleanupUnit reports %q — "+
				"a consumer that applies the factor mislabels the reading", rawUnit, mult, want, got)
		}
	}

	// The unit a value lands in after the parameter-keyed multiplier.
	wantUnitForParam := map[hmenum.Parameter]string{
		hmenum.ParameterTimeOfOperation: "d",
	}
	for param, mult := range multiplierByParam {
		want, declared := wantUnitForParam[param]
		if !declared {
			t.Errorf("multiplierByParam has %q (×%v) but no declared target unit — "+
				"add it to wantUnitForParam so the pair stays checkable", param, mult)
			continue
		}
		// The raw unit stands in for whatever the CCU reports; a
		// per-parameter multiplier must not depend on it, so the override
		// has to win over any spelling that arrives.
		for _, rawUnit := range []string{"s", "", "100%"} {
			if got := CleanupUnit(param, rawUnit); got != want {
				t.Errorf("%s multiplies by %v into %q, but CleanupUnit(raw %q) reports %q — "+
					"a consumer that applies the factor mislabels the reading",
					param, mult, want, rawUnit, got)
			}
		}
	}
}
