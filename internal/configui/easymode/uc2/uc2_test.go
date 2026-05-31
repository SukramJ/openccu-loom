// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package uc2_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc2"
)

// sampleSchema builds a minimal schema with five parameters for testing.
func sampleSchema() *configui.Schema {
	return &configui.Schema{
		ChannelType: "BLIND",
		Sections: []configui.FormSection{
			{
				ID:    "main",
				Title: "Hauptparameter",
				Parameters: []configui.FormParameter{
					{ID: "ON_TIME"},
					{ID: "MODE"},
					{ID: "RAMP_TIME"},
					{ID: "JT_OFF"},
					{ID: "JT_ON"},
				},
			},
		},
	}
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

func TestID(t *testing.T) {
	uc := uc2.New(nil)
	if uc.ID() != "uc2" {
		t.Fatalf("got %q", uc.ID())
	}
}

func TestResolveNilSchema(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}}})
	if err := uc.Resolve(easymode.ResolveContext{}, nil); err != nil {
		t.Fatalf("expected nil error for nil schema, got %v", err)
	}
}

func TestResolveShow(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Show:         []string{"RAMP_TIME"},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	p := indexParams(schema)["RAMP_TIME"]
	if p.VisibleWhen == nil {
		t.Fatal("RAMP_TIME should have VisibleWhen")
	}
	if p.VisibleWhen["trigger"] != "MODE" {
		t.Fatalf("trigger=%v", p.VisibleWhen["trigger"])
	}
	if p.VisibleWhen["value"] != 1.0 {
		t.Fatalf("value=%v", p.VisibleWhen["value"])
	}
	if _, has := p.VisibleWhen["invert"]; has {
		t.Fatal("Show rule must not set invert")
	}
}

func TestResolveHide(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Hide:         []string{"ON_TIME"},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	p := indexParams(schema)["ON_TIME"]
	if p.VisibleWhen == nil {
		t.Fatal("ON_TIME should have VisibleWhen")
	}
	if p.VisibleWhen["invert"] != true {
		t.Fatalf("invert=%v", p.VisibleWhen["invert"])
	}
}

func TestResolveIgnoresUnknownParam(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1,
		Show:         []string{"DOES_NOT_EXIST"},
		Hide:         []string{"ALSO_MISSING"},
	}})
	// Must not panic.
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
}

func TestResolveIdempotent(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}}})
	for i := range 3 {
		if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	p := indexParams(schema)["RAMP_TIME"]
	if p.VisibleWhen == nil {
		t.Fatal("VisibleWhen must be set after idempotent resolves")
	}
}

func TestValidateReturnsNil(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}}})
	issues := uc.Validate(easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 1}}, sampleSchema())
	if issues != nil {
		t.Fatalf("UC2 Validate must always return nil, got %v", issues)
	}
}

func TestApplyReturnsNil(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}}})
	patches, err := uc.Apply(easymode.ResolveContext{}, sampleSchema(), map[string]any{"MODE": 2})
	if err != nil || patches != nil {
		t.Fatalf("UC2 Apply must return (nil, nil), got (%v, %v)", patches, err)
	}
}

// IsVisible tests.

func TestIsVisibleShowMatchTrue(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 1}} // int vs float64 — loose match
	if !uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("RAMP_TIME must be visible when MODE==1")
	}
}

func TestIsVisibleShowNoMatchFalse(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 0}}
	if uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("RAMP_TIME must be hidden when MODE!=1")
	}
}

func TestIsVisibleHideMatchFalse(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 2,
		Hide:         []string{"ON_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 2}}
	if uc.IsVisible(ctx, "ON_TIME") {
		t.Fatal("ON_TIME must be hidden when trigger matches Hide rule")
	}
}

func TestIsVisibleHideNoMatchTrue(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 2,
		Hide:         []string{"ON_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 0}}
	if !uc.IsVisible(ctx, "ON_TIME") {
		t.Fatal("ON_TIME must be visible when Hide trigger does not match")
	}
}

func TestIsVisibleTriggerMissingFromContext(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1,
		Show:         []string{"RAMP_TIME"},
	}})
	// No MODE in CurrentValues.
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{}}
	// parameter under Show with no matching trigger → becomes hidden.
	if uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("RAMP_TIME should be hidden when trigger key absent")
	}
}

func TestIsVisibleParameterNotInAnyRule(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1,
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 1}}
	// JT_OFF has no rule → defaults to visible.
	if !uc.IsVisible(ctx, "JT_OFF") {
		t.Fatal("parameter with no rule must be visible by default")
	}
}

func TestIsVisibleNilTriggerValue(t *testing.T) {
	// Trigger value nil: only matches if current value is also nil.
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: nil,
		Show:         []string{"RAMP_TIME"},
	}})
	ctxNil := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": nil}}
	if !uc.IsVisible(ctxNil, "RAMP_TIME") {
		t.Fatal("visible when trigger value is nil and current is also nil")
	}
	ctxNonNil := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 1}}
	if uc.IsVisible(ctxNonNil, "RAMP_TIME") {
		t.Fatal("hidden when trigger value nil but current is non-nil")
	}
}

func TestIsVisibleFloat32TriggerValue(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: float32(3),
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": float64(3)}}
	if !uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("float32(3) must match float64(3) via loose comparison")
	}
}

func TestIsVisibleInt32TriggerValue(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: int32(5),
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": int64(5)}}
	if !uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("int32(5) must match int64(5)")
	}
}

func TestIsVisibleStringComparison(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "STATE",
		TriggerValue: "active",
		Show:         []string{"RAMP_TIME"},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"STATE": "active"}}
	if !uc.IsVisible(ctx, "RAMP_TIME") {
		t.Fatal("string equality must work")
	}
}

func TestResolveMultipleRules(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{
		{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}},
		{Trigger: "MODE", TriggerValue: 2, Hide: []string{"ON_TIME"}},
	})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	idx := indexParams(schema)
	if idx["RAMP_TIME"].VisibleWhen == nil {
		t.Fatal("RAMP_TIME missing VisibleWhen")
	}
	if idx["ON_TIME"].VisibleWhen == nil {
		t.Fatal("ON_TIME missing VisibleWhen")
	}
}
