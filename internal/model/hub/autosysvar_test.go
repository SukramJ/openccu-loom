// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestClassifyAutoSysvarPrefersTheLongestToken pins the matching rule the
// classification depends on.
//
// The CCU's base tokens are substrings of the specific ones —
// svEnergyCounter ⊂ svEnergyCounterFeedIn, svHmIPRainCounter ⊂
// svHmIPRainCounterToday. A first-match scan classifies every specific variant
// as its base, so today's rainfall is published as the all-time total: a
// plausible number, in the right unit, simply wrong. Nothing errors.
func TestClassifyAutoSysvarPrefersTheLongestToken(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		want hub.AutoSysvarKind
		unit string
	}{
		{"svEnergyCounter", hub.AutoSysvarEnergyCounter, "Wh"},
		{"svEnergyCounterFeedIn", hub.AutoSysvarEnergyCounterFeedIn, "Wh"},
		{"svHmIPRainCounter", hub.AutoSysvarRainCounter, "mm"},
		{"svHmIPRainCounterToday", hub.AutoSysvarRainCounterToday, "mm"},
		{"svHmIPRainCounterYesterday", hub.AutoSysvarRainCounterYesterday, "mm"},
		{"svHmIPSunshineCounter", hub.AutoSysvarSunshineCounter, "min"},
		{"svHmIPSunshineCounterToday", hub.AutoSysvarSunshineCounterToday, "min"},
		// The CCU prefixes these with the interface, so a real name is longer
		// than the bare token.
		{"svEnergyCounter_HmIP-RF", hub.AutoSysvarEnergyCounter, "Wh"},
	} {
		got, ok := hub.ClassifyAutoSysvar(c.name)
		if !ok {
			t.Errorf("%s: not classified", c.name)
			continue
		}
		if got.Kind != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, got.Kind, c.want)
		}
		if got.Unit != c.unit {
			t.Errorf("%s: unit = %q, want %q", c.name, got.Unit, c.unit)
		}
		if !got.Cumulative {
			t.Errorf("%s: must be cumulative — read as an instantaneous sample it produces a mean, silently", c.name)
		}
	}
}

// TestClassifyAutoSysvarLeavesOperatorNamesAlone keeps the negative half: an
// operator-named variable carries no derivable semantics and must not be
// classified, or it would be renamed out from under its owner.
func TestClassifyAutoSysvarLeavesOperatorNamesAlone(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Außentemperatur", "Urlaub", "", "svSomethingElse"} {
		if _, ok := hub.ClassifyAutoSysvar(name); ok {
			t.Errorf("%q must not be classified as auto-generated", name)
		}
	}
}
