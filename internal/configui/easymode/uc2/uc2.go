// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package uc2 implements the Easymode "Conditional Visibility" use
// case: parameters carry a `VisibleWhen` clause that the UI evaluates
// against the current observed values to decide whether to render
// them.
package uc2

import (
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
)

// Rule describes one visibility clause. When `Trigger`'s value equals
// `TriggerValue`, the parameters in `Show` are visible and those in
// `Hide` are invisible. The visibility is OR-ed across all rules
// addressing the same target parameter — any rule that includes it in
// `Show` makes it visible.
type Rule struct {
	Trigger      string
	TriggerValue any
	Show         []string
	Hide         []string
}

// UseCase is the UC2 strategy. The zero value is unusable; callers
// construct it via [New] to bind the rule set.
type UseCase struct {
	rules []Rule
}

// New constructs a UC2 [UseCase] bound to a fixed rule set.
func New(rules []Rule) *UseCase {
	return &UseCase{rules: append([]Rule(nil), rules...)}
}

// ID implements [easymode.UseCase].
func (*UseCase) ID() string { return "uc2" }

// Resolve attaches the visibility clauses to each affected parameter.
// The frontend evaluates them at render time. Idempotent — repeated
// Resolve calls overwrite the previous clause set.
func (u *UseCase) Resolve(_ easymode.ResolveContext, schema *configui.Schema) error {
	if schema == nil {
		return nil
	}
	idx := indexParams(schema)
	for _, r := range u.rules {
		clause := map[string]any{"trigger": r.Trigger, "value": r.TriggerValue}
		for _, p := range r.Show {
			if param, ok := idx[p]; ok {
				param.VisibleWhen = clause
			}
		}
		for _, p := range r.Hide {
			if param, ok := idx[p]; ok {
				// "hidden when X==v" expressed as inverted clause.
				param.VisibleWhen = map[string]any{"trigger": r.Trigger, "value": r.TriggerValue, "invert": true}
			}
		}
	}
	return nil
}

// Validate is a no-op for UC2 — visibility is a render-time concern,
// not a write-time correctness check.
func (*UseCase) Validate(_ easymode.ResolveContext, _ *configui.Schema) []easymode.Issue {
	return nil
}

// Apply has no patches to emit for UC2.
func (*UseCase) Apply(_ easymode.ResolveContext, _ *configui.Schema, _ map[string]any) (easymode.PatchSet, error) {
	return nil, nil
}

// IsVisible evaluates the rules against ctx.CurrentValues and returns
// whether parameter would be rendered. Useful for backend filters
// (e.g. when serialising a snapshot).
func (u *UseCase) IsVisible(ctx easymode.ResolveContext, parameter string) bool {
	visible := true
	for _, r := range u.rules {
		val, has := ctx.CurrentValues[r.Trigger]
		match := has && eq(val, r.TriggerValue)
		for _, p := range r.Show {
			if p == parameter {
				if !match {
					visible = false
				}
			}
		}
		for _, p := range r.Hide {
			if p == parameter && match {
				visible = false
			}
		}
	}
	return visible
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

func eq(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	// Compare numerics laxly: int vs. float64 from JSON is common.
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return a == b
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
