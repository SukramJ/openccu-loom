// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// rawJSON converts a Go value to json.RawMessage for use as Min/Max/Default.
func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func baseDescs() map[string]hmproto.ParameterData {
	return map[string]hmproto.ParameterData{
		"MIN_TEMP": {
			Type: hmenum.ParameterTypeFloat,
			Min:  rawJSON(4.5),
			Max:  rawJSON(30.5),
		},
		"MAX_TEMP": {
			Type: hmenum.ParameterTypeFloat,
			Min:  rawJSON(4.5),
			Max:  rawJSON(30.5),
		},
		"MODE": {
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"AUTO", "MANUAL", "BOOST"},
		},
		"LABEL": {
			Type: hmenum.ParameterTypeString,
		},
		"ACTIVE": {
			Type: hmenum.ParameterTypeBool,
		},
	}
}

// ── Validate: single-parameter checks ─────────────────────────────────────────

func TestSessionValidate_AllValid(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{
		"MIN_TEMP": 10.0,
		"MAX_TEMP": 25.0,
		"MODE":     0,
		"LABEL":    "living",
		"ACTIVE":   true,
	})
	issues := s.Validate(nil)
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}
}

func TestSessionValidate_BelowMin(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"MIN_TEMP": 1.0})
	s.Set("MIN_TEMP", 1.0)
	issues := s.Validate(nil)
	if len(issues) == 0 {
		t.Fatal("expected bound-violation issue for MIN_TEMP < 4.5")
	}
	if issues[0].Parameter != "MIN_TEMP" {
		t.Errorf("expected issue on MIN_TEMP, got %q", issues[0].Parameter)
	}
}

func TestSessionValidate_AboveMax(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"MAX_TEMP": 99.0})
	issues := s.Validate(nil)
	if len(issues) == 0 {
		t.Fatal("expected bound-violation issue for MAX_TEMP > 30.5")
	}
	if issues[0].Parameter != "MAX_TEMP" {
		t.Errorf("expected issue on MAX_TEMP, got %q", issues[0].Parameter)
	}
}

func TestSessionValidate_EnumOutOfRange(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"MODE": 99})
	issues := s.Validate(nil)
	if len(issues) == 0 {
		t.Fatal("expected enum-range issue for MODE=99")
	}
}

func TestSessionValidate_EnumStringNotInList(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"MODE": "HOLIDAY"})
	issues := s.Validate(nil)
	if len(issues) == 0 {
		t.Fatal("expected enum-member issue for MODE=HOLIDAY")
	}
}

func TestSessionValidate_WrongTypeBool(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"ACTIVE": 1})
	issues := s.Validate(nil)
	if len(issues) == 0 {
		t.Fatal("expected type error for ACTIVE=1 (expected bool)")
	}
}

func TestSessionValidate_UnknownParameter(t *testing.T) {
	t.Parallel()
	// Inject an unknown parameter into current values by calling Set.
	descs := map[string]hmproto.ParameterData{
		"MIN_TEMP": {Type: hmenum.ParameterTypeFloat},
	}
	// Build session with a value for a param not in descriptions.
	s := configui.NewSession(descs, map[string]any{"MIN_TEMP": 5.0, "GHOST": 42.0})
	issues := s.Validate(nil)
	found := false
	for _, iss := range issues {
		if iss.Parameter == "GHOST" {
			found = true
		}
	}
	if !found {
		t.Error("expected issue for unknown parameter GHOST")
	}
}

// ── Validate: cross-parameter constraints ─────────────────────────────────────

func TestSessionValidate_CrossConstraintViolated(t *testing.T) {
	t.Parallel()
	// MIN_TEMP must be <= MAX_TEMP (lte rule: MIN_TEMP <= MAX_TEMP)
	constraints := []configui.CrossValidationConstraint{
		{
			RuleID:          "min_lte_max",
			Rule:            "lte",
			AppliesToParams: []string{"MIN_TEMP", "MAX_TEMP"},
			ErrorKey:        "min_must_be_lte_max",
			ParamA:          "MIN_TEMP",
			ParamB:          "MAX_TEMP",
		},
	}
	s := configui.NewSession(baseDescs(), map[string]any{
		"MIN_TEMP": 28.0, // > MAX_TEMP
		"MAX_TEMP": 20.0,
	})
	issues := s.Validate(constraints)
	if len(issues) == 0 {
		t.Fatal("expected cross-validation issue MIN_TEMP > MAX_TEMP")
	}
	if issues[0].Parameter != "MIN_TEMP" {
		t.Errorf("expected subject MIN_TEMP, got %q", issues[0].Parameter)
	}
}

func TestSessionValidate_CrossConstraintPasses(t *testing.T) {
	t.Parallel()
	constraints := []configui.CrossValidationConstraint{
		{
			RuleID:          "min_lte_max",
			Rule:            "lte",
			AppliesToParams: []string{"MIN_TEMP", "MAX_TEMP"},
			ErrorKey:        "min_must_be_lte_max",
			ParamA:          "MIN_TEMP",
			ParamB:          "MAX_TEMP",
		},
	}
	s := configui.NewSession(baseDescs(), map[string]any{
		"MIN_TEMP": 10.0,
		"MAX_TEMP": 25.0,
	})
	issues := s.Validate(constraints)
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}
}

func TestSessionValidate_CrossConstraintSkippedWhenParamMissing(t *testing.T) {
	t.Parallel()
	// Rule references MAX_TEMP but session has no MAX_TEMP value — should skip.
	constraints := []configui.CrossValidationConstraint{
		{
			RuleID:          "min_lte_max",
			Rule:            "lte",
			AppliesToParams: []string{"MIN_TEMP", "MAX_TEMP"},
			ParamA:          "MIN_TEMP",
			ParamB:          "MAX_TEMP",
		},
	}
	s := configui.NewSession(baseDescs(), map[string]any{"MIN_TEMP": 28.0}) // no MAX_TEMP
	issues := s.Validate(constraints)
	if len(issues) != 0 {
		t.Errorf("expected no issues when referenced param is missing, got %v", issues)
	}
}

// ── ValidateChanges ───────────────────────────────────────────────────────────

func TestSessionValidateChanges_NoChanges(t *testing.T) {
	t.Parallel()
	s := configui.NewSession(baseDescs(), map[string]any{"MIN_TEMP": 10.0, "MAX_TEMP": 25.0})
	// No Set calls — no changes.
	issues := s.ValidateChanges(nil)
	if issues != nil {
		t.Errorf("expected nil issues when no changes, got %v", issues)
	}
}

func TestSessionValidateChanges_ValidatesOnlyChanged(t *testing.T) {
	t.Parallel()
	// MAX_TEMP has a valid initial value; only MIN_TEMP is changed to an
	// invalid value.
	s := configui.NewSession(baseDescs(), map[string]any{
		"MIN_TEMP": 10.0,
		"MAX_TEMP": 25.0,
	})
	s.Set("MIN_TEMP", 2.0) // below Min=4.5
	issues := s.ValidateChanges(nil)
	if len(issues) == 0 {
		t.Fatal("expected issue for changed MIN_TEMP=2.0 below min")
	}
	for _, iss := range issues {
		if iss.Parameter == "MAX_TEMP" {
			t.Error("MAX_TEMP was not changed; should not appear in ValidateChanges")
		}
	}
}

func TestSessionValidateChanges_CrossValidatesAgainstFullState(t *testing.T) {
	t.Parallel()
	// Initial: MIN_TEMP=10, MAX_TEMP=25.
	// Change MIN_TEMP to 28 — now MIN > MAX (cross-violation against unchanged MAX).
	constraints := []configui.CrossValidationConstraint{
		{
			RuleID:          "min_lte_max",
			Rule:            "lte",
			AppliesToParams: []string{"MIN_TEMP", "MAX_TEMP"},
			ParamA:          "MIN_TEMP",
			ParamB:          "MAX_TEMP",
		},
	}
	s := configui.NewSession(baseDescs(), map[string]any{
		"MIN_TEMP": 10.0,
		"MAX_TEMP": 25.0,
	})
	s.Set("MIN_TEMP", 28.0)
	issues := s.ValidateChanges(constraints)
	if len(issues) == 0 {
		t.Fatal("expected cross-validation issue: changed MIN_TEMP=28 > unchanged MAX_TEMP=25")
	}
	if issues[0].Parameter != "MIN_TEMP" {
		t.Errorf("expected subject MIN_TEMP, got %q", issues[0].Parameter)
	}
}
