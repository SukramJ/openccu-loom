// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"
)

// TestParameterGrouperSetOtherTitle exercises the SetOtherTitle method,
// including the no-op path with an empty string.
func TestParameterGrouperSetOtherTitle(t *testing.T) {
	g := NewParameterGrouper(nil)
	// Default title should not be empty.
	before := g.otherTitle
	// Set a custom title.
	g.SetOtherTitle("Sonstige")
	if g.otherTitle != "Sonstige" {
		t.Fatalf("SetOtherTitle = %q, want Sonstige", g.otherTitle)
	}
	// Setting empty string is a no-op.
	g.SetOtherTitle("")
	if g.otherTitle != "Sonstige" {
		t.Fatalf("SetOtherTitle(\"\") changed title: %q", g.otherTitle)
	}
	_ = before
}

// TestToFloat64AllBranches exercises the toFloat64 helper for all supported
// types and the fallback.
func TestToFloat64AllBranches(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(1.5), 1.5, true},
		{float32(2.0), 2.0, true},
		{int(3), 3.0, true},
		{int64(4), 4.0, true},
		{int32(5), 5.0, true},
		{"str", 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := toFloat64(tc.in)
		if ok != tc.ok {
			t.Errorf("toFloat64(%v) ok=%v want %v", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Errorf("toFloat64(%v) = %g want %g", tc.in, got, tc.want)
		}
	}
}

// TestSessionValidateCrossConstraintsEmpty verifies that passing an empty
// slice of constraints returns nil immediately.
func TestSessionValidateCrossConstraintsEmpty(t *testing.T) {
	s := NewSession(nil, nil)
	issues := s.ValidateCrossConstraints(nil)
	if issues != nil {
		t.Fatalf("expected nil, got %v", issues)
	}
}

// TestSessionValidateCrossConstraintsNonEmpty exercises the path where at
// least one constraint is evaluated using the real rule engine.
func TestSessionValidateCrossConstraintsNonEmpty(t *testing.T) {
	// With A=5, B=10 and rule "lte" on A ≤ B — no violation.
	s := NewSession(nil, map[string]any{"A": float64(5), "B": float64(10)})
	c := CrossValidationConstraint{
		RuleID:          "r1",
		Rule:            "lte",
		AppliesToParams: []string{"A", "B"},
		ParamA:          "A",
		ParamB:          "B",
	}
	issues := s.ValidateCrossConstraints([]CrossValidationConstraint{c})
	if len(issues) != 0 {
		t.Fatalf("expected no issues (A<=B), got %v", issues)
	}

	// Now A > B → violation.
	s2 := NewSession(nil, map[string]any{"A": float64(15), "B": float64(10)})
	issues = s2.ValidateCrossConstraints([]CrossValidationConstraint{c})
	if len(issues) == 0 {
		t.Fatal("expected violation (A>B), got none")
	}
}
