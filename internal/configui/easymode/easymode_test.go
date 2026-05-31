// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package easymode_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc2"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc5"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc6"
)

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

func TestUC2ResolveAttachesVisibleWhen(t *testing.T) {
	schema := sampleSchema()
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Show:         []string{"RAMP_TIME"},
		Hide:         []string{"ON_TIME"},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	idx := indexParams(schema)
	if idx["RAMP_TIME"].VisibleWhen == nil {
		t.Fatalf("RAMP_TIME should have VisibleWhen attached")
	}
	hide := idx["ON_TIME"].VisibleWhen
	if hide == nil || hide["invert"] != true {
		t.Fatalf("ON_TIME should have inverted VisibleWhen, got %v", hide)
	}
}

func TestUC2IsVisibleEvaluatesValues(t *testing.T) {
	uc := uc2.New([]uc2.Rule{{
		Trigger:      "MODE",
		TriggerValue: 1.0,
		Show:         []string{"RAMP_TIME"},
	}})
	visible := uc.IsVisible(easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 1}}, "RAMP_TIME")
	if !visible {
		t.Fatalf("RAMP_TIME should be visible when MODE==1")
	}
	hidden := uc.IsVisible(easymode.ResolveContext{CurrentValues: map[string]any{"MODE": 0}}, "RAMP_TIME")
	if hidden {
		t.Fatalf("RAMP_TIME should be hidden when MODE==0")
	}
}

func TestUC5ResolveAttachesPresets(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{
		Parameters: []string{"ON_TIME"},
		Presets: []uc5.Preset{
			{Value: 60, Label: "1 Minute"},
			{Value: 300, Label: "5 Minuten"},
		},
		AllowCustom: true,
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	p := indexParams(schema)["ON_TIME"]
	if len(p.Presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(p.Presets))
	}
	if !p.AllowCustomValue {
		t.Fatalf("AllowCustomValue should be true")
	}
}

func TestUC5ValidateRaisesOffPresetWarning(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}, {Value: 300}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 120},
	}, nil)
	if len(issues) != 1 || issues[0].Code != "uc5_value_off_preset" {
		t.Fatalf("expected uc5_value_off_preset issue, got %v", issues)
	}
}

func TestUC5ValidateAcceptsCustomWhenAllowed(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: true,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 120},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("expected no issues for custom-allowed UC5, got %v", issues)
	}
}

func TestUC6ResolveBuildsSubsetGroups(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "shutter_dir",
		Label:        "Richtung",
		MemberParams: []string{"JT_OFF", "JT_ON"},
		Options: []uc6.Option{
			{ID: 1, Label: "Auf", Values: map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0}},
			{ID: 2, Label: "Ab", Values: map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}},
		},
	}})
	if err := uc.Resolve(easymode.ResolveContext{
		CurrentValues: map[string]any{"JT_OFF": 1, "JT_ON": 1},
	}, schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.SubsetGroups) != 1 {
		t.Fatalf("expected one subset group, got %d", len(schema.SubsetGroups))
	}
	g := schema.SubsetGroups[0]
	if g.CurrentOptionID == nil || *g.CurrentOptionID != 1 {
		t.Fatalf("expected active option 1 (Auf), got %v", g.CurrentOptionID)
	}
	if indexParams(schema)["JT_OFF"].SubsetGroupID != "shutter_dir" {
		t.Fatalf("JT_OFF should be tagged with subset group id")
	}
}

func TestUC6ApplyExpandsToParamWrites(t *testing.T) {
	uc := uc6.New([]uc6.Group{{
		ID:           "shutter_dir",
		MemberParams: []string{"JT_OFF", "JT_ON"},
		Options: []uc6.Option{
			{ID: 2, Values: map[string]any{"JT_OFF": 2, "JT_ON": 2}},
		},
	}})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 2 || patches["JT_ON"] != 2 {
		t.Fatalf("expected expanded patches, got %v", patches)
	}
}

func TestUC6ApplyRejectsUnknownOption(t *testing.T) {
	uc := uc6.New([]uc6.Group{{ID: "g", Options: []uc6.Option{{ID: 1}}}})
	_, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{"g.option_id": 99})
	if err == nil {
		t.Fatalf("expected error for unknown option")
	}
}

func TestPipelineRunsAllUCs(t *testing.T) {
	schema := sampleSchema()
	pipe := easymode.NewPipeline(
		uc2.New([]uc2.Rule{{Trigger: "MODE", TriggerValue: 1, Show: []string{"RAMP_TIME"}}}),
		uc5.New([]uc5.Rule{{Parameters: []string{"ON_TIME"}, Presets: []uc5.Preset{{Value: 60}}, AllowCustom: true}}),
		uc6.New([]uc6.Group{{
			ID:           "g",
			MemberParams: []string{"JT_OFF"},
			Options:      []uc6.Option{{ID: 1, Values: map[string]any{"JT_OFF": 1}}},
		}}),
	)
	ctx := easymode.ResolveContext{ChannelType: "BLIND"}
	if err := pipe.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.SubsetGroups) != 1 {
		t.Fatalf("UC6 didn't run")
	}
	if indexParams(schema)["RAMP_TIME"].VisibleWhen == nil {
		t.Fatalf("UC2 didn't run")
	}
	if len(indexParams(schema)["ON_TIME"].Presets) == 0 {
		t.Fatalf("UC5 didn't run")
	}
	patches, err := pipe.Apply(ctx, schema, map[string]any{"g.option_id": 1})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 1 {
		t.Fatalf("UC6 patches missing in pipeline output")
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

// ---------------------------------------------------------------------------
// stubUseCase — minimal UseCase fake for Pipeline path coverage.
// ---------------------------------------------------------------------------

type stubUseCase struct {
	id         string
	resolveErr error
	applyErr   error
	issues     []easymode.Issue
	applyPatch easymode.PatchSet
}

func (s *stubUseCase) ID() string { return s.id }

func (s *stubUseCase) Resolve(_ easymode.ResolveContext, _ *configui.Schema) error {
	return s.resolveErr
}

func (s *stubUseCase) Validate(_ easymode.ResolveContext, _ *configui.Schema) []easymode.Issue {
	return s.issues
}

func (s *stubUseCase) Apply(_ easymode.ResolveContext, _ *configui.Schema, _ map[string]any) (easymode.PatchSet, error) {
	if s.applyErr != nil {
		return nil, s.applyErr
	}
	return s.applyPatch, nil
}

// ---------------------------------------------------------------------------
// Pipeline.Validate — currently 0 % covered.
// ---------------------------------------------------------------------------

func TestPipelineValidateCollectsIssues(t *testing.T) {
	issues1 := []easymode.Issue{{Severity: "error", Parameter: "P1", Code: "e1"}}
	issues2 := []easymode.Issue{{Severity: "warning", Parameter: "P2", Code: "w2"}}

	pipe := easymode.NewPipeline(
		&stubUseCase{id: "a", issues: issues1},
		&stubUseCase{id: "b", issues: issues2},
	)
	schema := sampleSchema()
	got := pipe.Validate(easymode.ResolveContext{}, schema)
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d: %v", len(got), got)
	}
	if got[0].Code != "e1" {
		t.Errorf("issue[0].Code = %q, want %q", got[0].Code, "e1")
	}
	if got[1].Code != "w2" {
		t.Errorf("issue[1].Code = %q, want %q", got[1].Code, "w2")
	}
}

func TestPipelineValidateEmptyPipeline(t *testing.T) {
	pipe := easymode.NewPipeline()
	got := pipe.Validate(easymode.ResolveContext{}, sampleSchema())
	if len(got) != 0 {
		t.Fatalf("expected 0 issues for empty pipeline, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Pipeline.Resolve — error path (75 % → need the short-circuit branch).
// ---------------------------------------------------------------------------

func TestPipelineResolveShortCircuitsOnError(t *testing.T) {
	// A pipeline with a failing first UC must return that error.
	pipe := easymode.NewPipeline(
		&stubUseCase{id: "fail", resolveErr: errors.New("resolve failed")},
		&stubUseCase{id: "ok"},
	)
	err := pipe.Resolve(easymode.ResolveContext{}, sampleSchema())
	if err == nil {
		t.Fatal("expected error from Resolve, got nil")
	}
	if err.Error() != "resolve failed" {
		t.Errorf("error = %q, want %q", err.Error(), "resolve failed")
	}
}

// ---------------------------------------------------------------------------
// Pipeline.Apply — error path (87.5 % → need the return-nil branch).
// ---------------------------------------------------------------------------

func TestPipelineApplyShortCircuitsOnError(t *testing.T) {
	applyErr := errors.New("apply failed")
	pipe := easymode.NewPipeline(
		&stubUseCase{id: "fail", applyErr: applyErr},
	)
	_, err := pipe.Apply(easymode.ResolveContext{}, sampleSchema(), nil)
	if err == nil {
		t.Fatal("expected error from Apply, got nil")
	}
}
