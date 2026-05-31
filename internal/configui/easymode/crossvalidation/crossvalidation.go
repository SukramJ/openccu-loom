// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package crossvalidation implements the Easymode cross-parameter validation
// rules: relations between several parameters that must hold for the form to
// be considered submittable.
package crossvalidation

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
)

// Rule names.
const (
	RuleGTE      = "gte"       // param_a >= param_b
	RuleLTE      = "lte"       // param_a <= param_b
	RuleBetween  = "between"   // min_param <= param <= max_param
	RuleNotEqual = "not_equal" // param_a != param_b
)

// UseCase is the Easymode cross-validation strategy. It does not
// resolve schema metadata (the FormSchemaGenerator already attaches
// the constraint list); its sole job is to validate user-supplied
// values against the registered rules.
type UseCase struct {
	rules []configui.CrossValidationConstraint
}

// New constructs a cross-validation UseCase bound to the given rule
// set. The schema's `cross_validation` payload is the canonical
// source — the form-schema generator emits it once at form-render
// time, and the UseCase carries the same data so [Validate] can
// evaluate without re-parsing the schema on every call.
func New(rules []configui.CrossValidationConstraint) *UseCase {
	return &UseCase{rules: append([]configui.CrossValidationConstraint(nil), rules...)}
}

// ID implements [easymode.UseCase].
func (*UseCase) ID() string { return "cross_validation" }

// Resolve attaches the rule list to the schema so the frontend can
// surface error messages in real time. Idempotent.
func (u *UseCase) Resolve(_ easymode.ResolveContext, schema *configui.Schema) error {
	if schema == nil {
		return nil
	}
	if len(u.rules) == 0 && len(schema.CrossValidation) == 0 {
		return nil
	}
	out := make([]configui.CrossValidationConstraint, 0, len(u.rules))
	out = append(out, u.rules...)
	schema.CrossValidation = out
	return nil
}

// Validate evaluates every rule against ctx.CurrentValues and returns the
// issues it found. A missing input parameter is treated as "no opinion" —
// only rules whose required values are all present run.
func (u *UseCase) Validate(ctx easymode.ResolveContext, _ *configui.Schema) []easymode.Issue {
	if len(ctx.CurrentValues) == 0 {
		return nil
	}
	var issues []easymode.Issue
	for i := range u.rules {
		if violation := evaluateRule(u.rules[i], ctx.CurrentValues); violation != "" {
			issues = append(issues, easymode.Issue{
				Severity:  "error",
				Parameter: ruleSubject(u.rules[i]),
				Message:   violation,
				Code:      u.rules[i].ErrorKey,
			})
		}
	}
	return issues
}

// Apply has nothing to write back — cross-validation is purely a
// validation concern.
func (*UseCase) Apply(_ easymode.ResolveContext, _ *configui.Schema, _ map[string]any) (easymode.PatchSet, error) {
	return nil, nil
}

func evaluateRule(r configui.CrossValidationConstraint, values map[string]any) string {
	switch r.Rule {
	case RuleGTE, RuleLTE, RuleNotEqual:
		a, aOK := numericFrom(values, r.ParamA)
		b, bOK := numericFrom(values, r.ParamB)
		if !aOK || !bOK {
			return ""
		}
		switch r.Rule {
		case RuleGTE:
			if a < b {
				return fmt.Sprintf("%s (%v) must be >= %s (%v)", r.ParamA, a, r.ParamB, b)
			}
		case RuleLTE:
			if a > b {
				return fmt.Sprintf("%s (%v) must be <= %s (%v)", r.ParamA, a, r.ParamB, b)
			}
		case RuleNotEqual:
			if a == b {
				return fmt.Sprintf("%s (%v) must differ from %s", r.ParamA, a, r.ParamB)
			}
		}
	case RuleBetween:
		v, vOK := numericFrom(values, r.Param)
		minV, minOK := numericFrom(values, r.MinParam)
		maxV, maxOK := numericFrom(values, r.MaxParam)
		if !vOK || !minOK || !maxOK {
			return ""
		}
		if v < minV || v > maxV {
			return fmt.Sprintf("%s (%v) must be between %s (%v) and %s (%v)",
				r.Param, v, r.MinParam, minV, r.MaxParam, maxV)
		}
	}
	return ""
}

func ruleSubject(r configui.CrossValidationConstraint) string {
	if r.Param != "" {
		return r.Param
	}
	if r.ParamA != "" {
		return r.ParamA
	}
	if len(r.AppliesToParams) > 0 {
		return r.AppliesToParams[0]
	}
	return ""
}

func numericFrom(values map[string]any, key string) (float64, bool) {
	if key == "" {
		return 0, false
	}
	v, ok := values[key]
	if !ok || v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}
