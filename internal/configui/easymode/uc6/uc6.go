// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package uc6 implements the Easymode "Subset Group" use case:
// several MASTER parameters are manipulated together via a single
// virtual selector — picking option "Hoch" expands into a fixed set
// of per-parameter writes.
package uc6

import (
	"fmt"
	"maps"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
)

// Option is one selectable preset inside a [Group]. Picking the
// option causes the runtime to write every (key, value) pair in
// `Values` to the CCU.
type Option struct {
	ID     int
	Label  string
	Values map[string]any
}

// Group bundles the parameters that form a virtual selector.
type Group struct {
	ID           string
	Label        string
	MemberParams []string
	Options      []Option
}

// UseCase is the UC6 strategy.
type UseCase struct {
	groups []Group
}

// New constructs a UC6 [UseCase] bound to a fixed group set.
func New(groups []Group) *UseCase {
	return &UseCase{groups: append([]Group(nil), groups...)}
}

// ID implements [easymode.UseCase].
func (*UseCase) ID() string { return "uc6" }

// Resolve emits the [configui.SubsetGroup] entries on the schema and
// tags every member parameter with its group id. Idempotent.
func (u *UseCase) Resolve(ctx easymode.ResolveContext, schema *configui.Schema) error {
	if schema == nil {
		return nil
	}
	idx := indexParams(schema)

	// Determine the active option per group (if all member params
	// currently match exactly one option).
	current := make(map[string]*int, len(u.groups))
	for _, g := range u.groups {
		current[g.ID] = u.detectActiveOption(g, ctx.CurrentValues)
	}

	out := make([]configui.SubsetGroup, 0, len(u.groups))
	for _, g := range u.groups {
		options := make([]configui.SubsetOption, 0, len(g.Options))
		for _, opt := range g.Options {
			options = append(options, configui.SubsetOption{
				ID:     opt.ID,
				Label:  opt.Label,
				Values: copyMap(opt.Values),
			})
		}
		out = append(out, configui.SubsetGroup{
			ID:              g.ID,
			Label:           g.Label,
			MemberParams:    append([]string(nil), g.MemberParams...),
			Options:         options,
			CurrentOptionID: current[g.ID],
		})
		for _, name := range g.MemberParams {
			if p, ok := idx[name]; ok {
				p.SubsetGroupID = g.ID
			}
		}
	}
	schema.SubsetGroups = out
	return nil
}

// Validate ensures that any caller-supplied option id refers to an
// existing option in the addressed group.
func (u *UseCase) Validate(ctx easymode.ResolveContext, _ *configui.Schema) []easymode.Issue {
	var issues []easymode.Issue
	for _, g := range u.groups {
		key := g.ID + ".option_id"
		raw, ok := ctx.CurrentValues[key]
		if !ok {
			continue
		}
		id, ok := toInt(raw)
		if !ok {
			issues = append(issues, easymode.Issue{
				Severity:  "error",
				Parameter: key,
				Message:   "subset option id must be an integer",
				Code:      "uc6_invalid_option_id",
			})
			continue
		}
		if !u.hasOption(g, id) {
			issues = append(issues, easymode.Issue{
				Severity:  "error",
				Parameter: key,
				Message:   fmt.Sprintf("option id %d not in group %s", id, g.ID),
				Code:      "uc6_unknown_option",
			})
		}
	}
	return issues
}

// Apply expands each `<group_id>.option_id = N` entry in values into
// the per-parameter writes the option declares. Unknown groups /
// options return an error so callers do not silently miss writes.
func (u *UseCase) Apply(_ easymode.ResolveContext, _ *configui.Schema, values map[string]any) (easymode.PatchSet, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := easymode.PatchSet{}
	for _, g := range u.groups {
		key := g.ID + ".option_id"
		raw, ok := values[key]
		if !ok {
			continue
		}
		id, ok := toInt(raw)
		if !ok {
			return nil, fmt.Errorf("uc6: %s must be int, got %T", key, raw)
		}
		opt, ok := u.option(g, id)
		if !ok {
			return nil, fmt.Errorf("uc6: option %d unknown for group %s", id, g.ID)
		}
		maps.Copy(out, opt.Values)
	}
	return out, nil
}

func (u *UseCase) detectActiveOption(g Group, values map[string]any) *int {
	if len(values) == 0 {
		return nil
	}
	for _, opt := range g.Options {
		match := true
		for k, want := range opt.Values {
			got, ok := values[k]
			if !ok || !looseEqual(got, want) {
				match = false
				break
			}
		}
		if match {
			id := opt.ID
			return &id
		}
	}
	return nil
}

func (u *UseCase) hasOption(g Group, id int) bool {
	_, ok := u.option(g, id)
	return ok
}

func (u *UseCase) option(g Group, id int) (Option, bool) {
	for _, opt := range g.Options {
		if opt.ID == id {
			return opt, true
		}
	}
	return Option{}, false
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

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	}
	return 0, false
}

func looseEqual(a, b any) bool {
	if a == b {
		return true
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	return aok && bok && af == bf
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
