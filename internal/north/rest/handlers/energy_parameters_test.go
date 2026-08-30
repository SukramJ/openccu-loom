// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"testing"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestEnergyFoldHandlesEveryQueriedParameter pins the handler's fold against
// the set the store actually queries.
//
// The store's set is the filter; this handler's switch is the dispatch. A
// parameter added to the filter arrives as rows the fold must handle — and a
// row that matches no case still creates its bucket and registers its device,
// so the endpoint answers with the device present and consumed_wh,
// feed_in_wh and avg_power_w all zero. No error, no log, no 4xx: a metered
// device reads as one that consumed nothing.
func TestEnergyFoldHandlesEveryQueriedParameter(t *testing.T) {
	t.Parallel()
	queried := sqlitestore.EnergyParameters()
	if len(queried) == 0 {
		t.Fatal("the store queries no energy parameter — the guard lost its subject")
	}
	folded := map[string]bool{
		energyParameterPower:               true,
		energyParameterEnergyCounter:       true,
		energyParameterEnergyCounterFeedIn: true,
	}
	for _, p := range queried {
		if !folded[p] {
			t.Errorf("the store queries %q but the fold has no case for it — its rows would land as zeroes", p)
		}
	}
	for p := range folded {
		found := false
		for _, q := range queried {
			if q == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the fold handles %q but the store never queries it — dead branch", p)
		}
	}
}
