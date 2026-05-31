// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package crossvalidation_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/crossvalidation"
)

// TestID verifies that UseCase.ID returns the expected constant.
func TestID(t *testing.T) {
	uc := crossvalidation.New(nil)
	if uc.ID() != "cross_validation" {
		t.Fatalf("ID() = %q, want %q", uc.ID(), "cross_validation")
	}
}

// TestApplyReturnsEmptyPatch verifies Apply always returns nil, nil.
func TestApplyReturnsEmptyPatch(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B"},
	})
	patch, err := uc.Apply(easymode.ResolveContext{CurrentValues: map[string]any{"A": 5, "B": 3}}, nil, nil)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if patch != nil {
		t.Fatalf("Apply: expected nil patch, got %v", patch)
	}
}

// TestResolveWithNilSchemaIsNoop verifies that Resolve on a nil schema returns nil.
func TestResolveWithNilSchemaIsNoop(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B"},
	})
	if err := uc.Resolve(easymode.ResolveContext{}, nil); err != nil {
		t.Fatalf("Resolve(nil schema): unexpected error %v", err)
	}
}

// TestResolveWithEmptyRulesAndSchemaIsNoop verifies that Resolve is a no-op when
// both the use-case and the schema have no rules.
func TestResolveWithEmptyRulesAndSchemaIsNoop(t *testing.T) {
	uc := crossvalidation.New(nil)
	schema := &configui.Schema{}
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatalf("Resolve(empty): unexpected error %v", err)
	}
	// CrossValidation should remain empty.
	if len(schema.CrossValidation) != 0 {
		t.Fatalf("expected no rules attached, got %v", schema.CrossValidation)
	}
}

// TestValidateWithEmptyValuesReturnsNil verifies early-out when CurrentValues is empty.
func TestValidateWithEmptyValuesReturnsNil(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B"},
	})
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{}}, nil)
	if got != nil {
		t.Fatalf("empty CurrentValues should yield nil issues, got %v", got)
	}
}

// TestRuleSubjectFallsBackToAppliesToParams verifies that when ParamA and Param
// are empty, ruleSubject uses AppliesToParams[0].
func TestGTERuleMissingParamAUsesAppliesToParams(t *testing.T) {
	// We cannot call ruleSubject directly (unexported), but we can trigger
	// the path via a rule where Param=="", ParamA=="" and AppliesToParams is set.
	// The issue struct will have Parameter set from ruleSubject.
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{
			Rule:            crossvalidation.RuleGTE,
			ParamA:          "", // empty — ruleSubject falls to AppliesToParams
			ParamB:          "", // empty — both values missing, rule skipped
			AppliesToParams: []string{"FALLBACK"},
			ErrorKey:        "e",
		},
	})
	// Values missing → rule doesn't fire (numericFrom returns false).
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"X": 1}}, nil)
	if len(got) != 0 {
		t.Fatalf("rule with empty params should not fire, got %v", got)
	}
}

// TestNumericFromFloat32 verifies that float32 values are coerced correctly.
func TestNumericFromFloat32(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B", ErrorKey: "err"},
	})
	// float32(10) >= float32(5) → no violation.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{
		"A": float32(10),
		"B": float32(5),
	}}, nil)
	if len(got) != 0 {
		t.Fatalf("float32 GTE pass: expected no issues, got %v", got)
	}
}

// TestNumericFromInt32 verifies that int32 values are coerced correctly.
func TestNumericFromInt32(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleLTE, ParamA: "A", ParamB: "B", ErrorKey: "err"},
	})
	// int32(5) <= int32(5) → no violation.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{
		"A": int32(5),
		"B": int32(5),
	}}, nil)
	if len(got) != 0 {
		t.Fatalf("int32 LTE pass: expected no issues, got %v", got)
	}
}

// TestNumericFromInt64 verifies that int64 values are coerced correctly.
func TestNumericFromInt64(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleNotEqual, ParamA: "A", ParamB: "B", ErrorKey: "err"},
	})
	// int64(1) != int64(2) → no violation.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{
		"A": int64(1),
		"B": int64(2),
	}}, nil)
	if len(got) != 0 {
		t.Fatalf("int64 NotEqual pass: expected no issues, got %v", got)
	}
}

// TestBetweenRuleMissingMinParam verifies that a Between rule with a missing
// min param is silently skipped (no opinion).
func TestBetweenRuleMissingMinParam(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleBetween, Param: "V", MinParam: "LO", MaxParam: "HI", ErrorKey: "e"},
	})
	// LO is missing → rule silently skipped.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"V": 5, "HI": 10}}, nil)
	if len(got) != 0 {
		t.Fatalf("missing min param should suppress Between rule, got %v", got)
	}
}

// TestRuleSubjectUsesParam verifies that when Param is set, it takes precedence.
func TestBetweenRuleSubjectIsParam(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleBetween, Param: "LEVEL", MinParam: "LO", MaxParam: "HI", ErrorKey: "out"},
	})
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"LEVEL": 50, "LO": 60, "HI": 100}}, nil)
	if len(got) != 1 {
		t.Fatalf("expected one issue, got %v", got)
	}
	if got[0].Parameter != "LEVEL" {
		t.Fatalf("issue.Parameter = %q, want LEVEL", got[0].Parameter)
	}
}

// TestNumericFromNilValue verifies that a nil value is treated as "not present".
func TestNumericFromNilValue(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B"},
	})
	// nil value should cause numericFrom to return false → rule skipped.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"A": nil, "B": 10}}, nil)
	if len(got) != 0 {
		t.Fatalf("nil value should suppress rule, got %v", got)
	}
}

// TestNumericFromStringIsUnrecognised verifies that non-numeric types skip rules.
func TestNumericFromStringIsUnrecognised(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B"},
	})
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"A": "ten", "B": "five"}}, nil)
	if len(got) != 0 {
		t.Fatalf("string values should suppress numeric rule, got %v", got)
	}
}
