// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// TestProfileMatchingAgreesWithTheStore pins the two implementations of one
// rule against each other.
//
// "Does this profile match the channel's current values" was decided twice:
// here on raw JSON, and in internal/store/linkprofile on decoded floats. They
// had already drifted on float equality — this side compared with `!=`, the
// store with a relative epsilon — so a value that survived a wire round-trip
// matched one decoder and not the other, and the SPA showed a different active
// profile from the one the store reported.
func TestProfileMatchingAgreesWithTheStore(t *testing.T) {
	t.Parallel()

	f := func(v float64) *float64 { return &v }
	// Computed at run time: Go folds a constant 0.1+0.2 to exactly 0.3, which
	// would hide the very difference this test is about.
	tenth, fifth := 0.1, 0.2
	roundTripped := tenth + fifth
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }

	for _, tc := range []struct {
		name    string
		adapter map[string]profileParamConstraint
		store   map[string]linkprofile.ParamConstraint
		current map[string]any
	}{
		{
			name:    "a float that does not survive a round-trip exactly",
			adapter: map[string]profileParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: raw("0.3")}},
			store:   map[string]linkprofile.ParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: f(0.3)}},
			current: map[string]any{"ON_LEVEL": roundTripped},
		},
		{
			name:    "an exact match",
			adapter: map[string]profileParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: raw("1")}},
			store:   map[string]linkprofile.ParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: f(1)}},
			current: map[string]any{"ON_LEVEL": 1.0},
		},
		{
			name:    "a genuine mismatch",
			adapter: map[string]profileParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: raw("1")}},
			store:   map[string]linkprofile.ParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: f(1)}},
			current: map[string]any{"ON_LEVEL": 0.5},
		},
		{
			name:    "list membership with a rounded member",
			adapter: map[string]profileParamConstraint{"ON_LEVEL": {ConstraintType: "list", Values: []json.RawMessage{raw("0.3"), raw("0.7")}}},
			store:   map[string]linkprofile.ParamConstraint{"ON_LEVEL": {ConstraintType: "list", Values: []float64{0.3, 0.7}}},
			current: map[string]any{"ON_LEVEL": roundTripped},
		},
		{
			// The wire shape this plane prepares before asking: it parses the
			// string, so both sides then see the same number. The store is
			// asked with the prepared value, which is what production does.
			name:    "a numeric string, prepared the way this plane prepares it",
			adapter: map[string]profileParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: raw("1")}},
			store:   map[string]linkprofile.ParamConstraint{"ON_LEVEL": {ConstraintType: "fixed", Value: f(1)}},
			current: map[string]any{"ON_LEVEL": 1.0},
		},
	} {
		got := profileMatches(tc.adapter, tc.current)
		want := linkprofile.ProfileMatches(tc.store, tc.current)
		if got != want {
			t.Errorf("%s: adapter says %v, store says %v", tc.name, got, want)
		}
	}
}
