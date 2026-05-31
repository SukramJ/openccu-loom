// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package uc5 implements the Easymode "Option Presets" use case: numeric
// parameters carry a curated list of suggested values (optionally together
// with custom-value escape hatch).
package uc5

import (
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
)

// Preset is one suggested value with an optional display label.
type Preset struct {
	Value any
	Label string
}

// Rule binds a preset list to one or more parameters of a channel.
type Rule struct {
	Parameters  []string
	Presets     []Preset
	AllowCustom bool
}

// UseCase is the UC5 strategy.
type UseCase struct {
	rules []Rule
}

// New constructs a UC5 [UseCase] bound to a fixed rule set.
func New(rules []Rule) *UseCase {
	return &UseCase{rules: append([]Rule(nil), rules...)}
}

// ID implements [easymode.UseCase].
func (*UseCase) ID() string { return "uc5" }

// Resolve injects the preset list and `allow_custom_value` flag into
// each addressed parameter. Idempotent.
func (u *UseCase) Resolve(_ easymode.ResolveContext, schema *configui.Schema) error {
	if schema == nil {
		return nil
	}
	idx := indexParams(schema)
	for _, r := range u.rules {
		serialised := make([]map[string]any, 0, len(r.Presets))
		for _, p := range r.Presets {
			entry := map[string]any{"value": p.Value}
			if p.Label != "" {
				entry["label"] = p.Label
			}
			serialised = append(serialised, entry)
		}
		for _, name := range r.Parameters {
			if p, ok := idx[name]; ok {
				p.Presets = serialised
				p.AllowCustomValue = r.AllowCustom
			}
		}
	}
	return nil
}

// Validate raises a warning when the supplied value for a UC5-bound
// parameter is not in the preset list and the rule disallows custom
// values.
func (u *UseCase) Validate(ctx easymode.ResolveContext, _ *configui.Schema) []easymode.Issue {
	if len(ctx.CurrentValues) == 0 {
		return nil
	}
	var issues []easymode.Issue
	for _, r := range u.rules {
		if r.AllowCustom {
			continue
		}
		for _, name := range r.Parameters {
			val, ok := ctx.CurrentValues[name]
			if !ok {
				continue
			}
			if !inPresets(val, r.Presets) {
				issues = append(issues, easymode.Issue{
					Severity:  "warning",
					Parameter: name,
					Message:   "value not in preset list",
					Code:      "uc5_value_off_preset",
				})
			}
		}
	}
	return issues
}

// Apply has no patches to emit — UC5 is purely UI metadata.
func (*UseCase) Apply(_ easymode.ResolveContext, _ *configui.Schema, _ map[string]any) (easymode.PatchSet, error) {
	return nil, nil
}

func inPresets(v any, presets []Preset) bool {
	for _, p := range presets {
		if p.Value == v {
			return true
		}
		// Loose numeric comparison.
		af, aok := toFloat(p.Value)
		bf, bok := toFloat(v)
		if aok && bok && af == bf {
			return true
		}
	}
	return false
}

func indexParams(schema *configui.Schema) map[string]*configui.FormParameter {
	idx := make(map[string]*configui.FormParameter)
	for si := range schema.Sections {
		for pi := range schema.Sections[si].Parameters {
			p := &schema.Sections[si].Parameters[pi]
			idx[p.ID] = p
		}
	}
	return idx
}

func toFloat(v any) (float64, bool) {
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
