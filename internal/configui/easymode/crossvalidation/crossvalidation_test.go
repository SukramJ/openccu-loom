// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package crossvalidation_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/crossvalidation"
)

func TestGTERulePassesAndFails(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{RuleID: "max_ge_min", Rule: crossvalidation.RuleGTE, ParamA: "MAX_TEMP", ParamB: "MIN_TEMP", ErrorKey: "max_below_min"},
	})
	// Pass when MAX >= MIN.
	if got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MAX_TEMP": 30, "MIN_TEMP": 10}}, nil); len(got) != 0 {
		t.Fatalf("expected no issue, got %v", got)
	}
	// Fail when MAX < MIN.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MAX_TEMP": 5, "MIN_TEMP": 10}}, nil)
	if len(got) != 1 || got[0].Code != "max_below_min" {
		t.Fatalf("expected one issue with code max_below_min, got %v", got)
	}
}

func TestLTERule(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleLTE, ParamA: "MIN_TEMP", ParamB: "MAX_TEMP", ErrorKey: "min_above_max"},
	})
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MIN_TEMP": 25, "MAX_TEMP": 20}}, nil)
	if len(got) != 1 {
		t.Fatalf("expected one issue, got %v", got)
	}
}

func TestBetweenRule(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleBetween, Param: "OPEN", MinParam: "LOW", MaxParam: "HIGH", ErrorKey: "open_out_of_range"},
	})
	if got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"OPEN": 50, "LOW": 10, "HIGH": 100}}, nil); len(got) != 0 {
		t.Fatalf("between-pass expected, got %v", got)
	}
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"OPEN": 5, "LOW": 10, "HIGH": 100}}, nil)
	if len(got) != 1 {
		t.Fatalf("expected out-of-range issue, got %v", got)
	}
}

func TestNotEqualRule(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleNotEqual, ParamA: "ON_TIME", ParamB: "OFF_TIME", ErrorKey: "on_off_equal"},
	})
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"ON_TIME": 60, "OFF_TIME": 60}}, nil)
	if len(got) != 1 {
		t.Fatalf("expected equality violation, got %v", got)
	}
}

func TestPartialValuesSkipRule(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "MAX_TEMP", ParamB: "MIN_TEMP", ErrorKey: "x"},
	})
	// Only one value present → no rule fires.
	got := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MAX_TEMP": 30}}, nil)
	if len(got) != 0 {
		t.Fatalf("partial values should suppress validation, got %v", got)
	}
}

func TestResolveAttachesRules(t *testing.T) {
	rules := []configui.CrossValidationConstraint{
		{RuleID: "x", Rule: crossvalidation.RuleGTE, ParamA: "A", ParamB: "B", ErrorKey: "y"},
	}
	uc := crossvalidation.New(rules)
	schema := &configui.Schema{}
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.CrossValidation) != 1 || schema.CrossValidation[0].RuleID != "x" {
		t.Fatalf("rules not attached to schema: %v", schema.CrossValidation)
	}
}

func TestPipelineWithCrossValidation(t *testing.T) {
	uc := crossvalidation.New([]configui.CrossValidationConstraint{
		{Rule: crossvalidation.RuleGTE, ParamA: "MAX", ParamB: "MIN", ErrorKey: "max_below_min"},
	})
	pipe := easymode.NewPipeline(uc)
	schema := &configui.Schema{}
	if err := pipe.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	issues := pipe.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MAX": 1, "MIN": 5}}, schema)
	if len(issues) != 1 {
		t.Fatalf("expected one issue from pipeline, got %v", issues)
	}
}
