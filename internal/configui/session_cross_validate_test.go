// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"strings"
	"testing"
)

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

// TestSessionValidateCrossConstraintsUnknownRule verifies that a
// constraint carrying an unrecognised Rule value is reported as a
// validation issue instead of being silently treated as "no violation".
func TestSessionValidateCrossConstraintsUnknownRule(t *testing.T) {
	s := NewSession(nil, map[string]any{"A": float64(5), "B": float64(10)})
	c := CrossValidationConstraint{
		RuleID:          "r-typo",
		Rule:            CrossValidationRule("gtee"), // typo'd rule name
		AppliesToParams: []string{"A", "B"},
		ParamA:          "A",
		ParamB:          "B",
	}
	issues := s.ValidateCrossConstraints([]CrossValidationConstraint{c})
	if len(issues) == 0 {
		t.Fatal("expected an issue for an unknown cross-validation rule, got none")
	}
	if !strings.Contains(issues[0].Reason, "gtee") {
		t.Errorf("issue reason %q does not name the offending rule", issues[0].Reason)
	}
}
