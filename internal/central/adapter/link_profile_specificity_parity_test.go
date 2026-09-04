// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// TestProfileSpecificityAgreesWithTheStore pins the two planes' active-profile
// scores against each other, through each plane's own entry point.
//
// Both resolve which link profile is active — the SPA's schema over raw JSON
// constraints, the store over decoded ones — and each scored specificity with
// its own copy of the same arithmetic. Two scorers can rank two profiles
// differently, and then the operator sees one active profile while everything
// reading the store sees another.
func TestProfileSpecificityAgreesWithTheStore(t *testing.T) {
	t.Parallel()

	f := func(v float64) *float64 { return &v }
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }

	for _, tc := range []struct {
		name    string
		adapter map[string]profileParamConstraint
		store   map[string]linkprofile.ParamConstraint
	}{
		{
			name:    "all fixed",
			adapter: map[string]profileParamConstraint{"A": {ConstraintType: "fixed", Value: raw("1")}, "B": {ConstraintType: "fixed", Value: raw("2")}},
			store:   map[string]linkprofile.ParamConstraint{"A": {ConstraintType: "fixed", Value: f(1)}, "B": {ConstraintType: "fixed", Value: f(2)}},
		},
		{
			name:    "one loose constraint outweighs many fixed ones",
			adapter: map[string]profileParamConstraint{"A": {ConstraintType: "fixed", Value: raw("1")}, "B": {ConstraintType: "range"}},
			store:   map[string]linkprofile.ParamConstraint{"A": {ConstraintType: "fixed", Value: f(1)}, "B": {ConstraintType: "range"}},
		},
		{
			name:    "empty",
			adapter: map[string]profileParamConstraint{},
			store:   map[string]linkprofile.ParamConstraint{},
		},
	} {
		got := profileSpecificity(tc.adapter)
		want := linkprofile.ProfileSpecificityOfConstraints(tc.store)
		if got != want {
			t.Errorf("%s: adapter scores %v, store scores %v", tc.name, got, want)
		}
	}
}
