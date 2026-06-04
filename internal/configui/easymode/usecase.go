// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package easymode

import (
	"maps"

	"github.com/SukramJ/openccu-loom/internal/configui"
)

// ResolveContext bundles the inputs every UseCase consumes when it
// resolves its rules into a form-schema. Concrete UCs only read the
// fields they care about.
type ResolveContext struct {
	// ChannelType is the CCU CHANNEL_TYPE the form belongs to. Most
	// easymode rules are keyed by this value.
	ChannelType string

	// SenderType is the LINK partner's channel type, defaulted to the
	// MASTER sentinel for non-LINK paramsets.
	SenderType string

	// CurrentValues maps parameter name → currently observed value.
	// UCs that need conditional state (UC2 trigger evaluation, UC6
	// active-option detection) consume this.
	CurrentValues map[string]any
}

// Issue describes a single validation finding produced by
// [UseCase.Validate]. Severity is "error" or "warning"; the frontend
// surfaces it next to the affected parameter.
type Issue struct {
	Severity  string
	Parameter string
	Message   string
	Code      string
}

// PatchSet is the output of [UseCase.Apply]: the parameter writes the
// caller must dispatch to the CCU to materialise the user's choice
// (UC6 expansion in particular). Parameters appear once per write.
type PatchSet map[string]any

// UseCase is the strategy interface every Easymode UC implements.
//
// Resolve enriches a [configui.Schema] in-place with the UC's rule
// metadata (e.g. VisibleWhen on parameters, SubsetGroups on the
// schema). Multiple UCs may coexist on the same schema; they should
// be idempotent.
//
// Validate inspects user-supplied values against the UC's rules and
// returns any issues it finds. The schema is read-only here.
//
// Apply expands a "logical" change made via the UC (e.g. picking
// subset option "Hoch") into the per-parameter writes that need to
// reach the CCU. Returns nil when the UC has no expansion to perform
// for the given input.
type UseCase interface {
	ID() string
	Resolve(ctx ResolveContext, schema *configui.Schema) error
	Validate(ctx ResolveContext, schema *configui.Schema) []Issue
	Apply(ctx ResolveContext, schema *configui.Schema, values map[string]any) (PatchSet, error)
}

// Pipeline runs a fixed sequence of [UseCase] instances against a
// [configui.Schema]. Errors short-circuit; the partial schema is left
// for inspection.
type Pipeline struct {
	cases []UseCase
}

// NewPipeline wires a [Pipeline] in declaration order.
func NewPipeline(cases ...UseCase) *Pipeline {
	return &Pipeline{cases: append([]UseCase(nil), cases...)}
}

// Resolve runs every UC's Resolve in order.
func (p *Pipeline) Resolve(ctx ResolveContext, schema *configui.Schema) error {
	for _, uc := range p.cases {
		if err := uc.Resolve(ctx, schema); err != nil {
			return err
		}
	}
	return nil
}

// Validate aggregates issues from every UC, preserving order.
func (p *Pipeline) Validate(ctx ResolveContext, schema *configui.Schema) []Issue {
	out := make([]Issue, 0, len(p.cases))
	for _, uc := range p.cases {
		out = append(out, uc.Validate(ctx, schema)...)
	}
	return out
}

// Apply asks each UC for its patches and merges them. UCs declared
// later in the pipeline overwrite earlier values when the same
// parameter appears.
func (p *Pipeline) Apply(ctx ResolveContext, schema *configui.Schema, values map[string]any) (PatchSet, error) {
	merged := PatchSet{}
	for _, uc := range p.cases {
		patches, err := uc.Apply(ctx, schema, values)
		if err != nil {
			return nil, err
		}
		maps.Copy(merged, patches)
	}
	return merged, nil
}
