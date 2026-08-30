// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package masterprofile

import "testing"

// TestFixedParamsCountsWhatTheMatcherCounts holds the apply path's parameter
// set equal to the matcher's.
//
// scoreProfile treats an ABSENT constraint_type as fixed — the upstream JSON
// omits it for a plain fixed value. The WebSocket apply path selected only the
// literal "fixed", so a profile could be recognised on a parameter and then
// decline to write it: the response reported the profile applied while the
// device kept its old value, with no error on either side.
func TestFixedParamsCountsWhatTheMatcherCounts(t *testing.T) {
	t.Parallel()
	v := func(f float64) *float64 { return &f }
	p := Profile{Params: map[string]ParamConstraint{
		"EXPLICIT_FIXED": {ConstraintType: "fixed", Value: v(1)},
		// The shape the upstream JSON actually emits for a plain value.
		"IMPLICIT_FIXED": {ConstraintType: "", Value: v(2)},
		// Pins nothing.
		"NO_VALUE": {ConstraintType: "fixed"},
		"RANGE":    {ConstraintType: "range", Value: v(3)},
	}}

	got := p.FixedParams()
	if _, ok := got["EXPLICIT_FIXED"]; !ok {
		t.Error("an explicitly fixed parameter must be written")
	}
	if _, ok := got["IMPLICIT_FIXED"]; !ok {
		t.Error("a parameter with an absent constraint_type is fixed to the matcher, so it must be written too")
	}
	if _, ok := got["NO_VALUE"]; ok {
		t.Error("a constraint without a value pins nothing")
	}
	if _, ok := got["RANGE"]; ok {
		t.Error("a range constraint does not pin a value")
	}
	if len(got) != 2 {
		t.Errorf("FixedParams returned %d entries, want 2: %v", len(got), got)
	}
}
