// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package uc5_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc5"
)

func sampleSchema() *configui.Schema {
	return &configui.Schema{
		ChannelType: "HEATING",
		Sections: []configui.FormSection{
			{
				ID: "main",
				Parameters: []configui.FormParameter{
					{ID: "ON_TIME"},
					{ID: "RAMP_TIME"},
					{ID: "DURATION"},
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
	uc := uc5.New(nil)
	if uc.ID() != "uc5" {
		t.Fatalf("got %q", uc.ID())
	}
}

func TestResolveNilSchema(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters: []string{"ON_TIME"},
		Presets:    []uc5.Preset{{Value: 60}},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, nil); err != nil {
		t.Fatalf("nil schema must not error, got %v", err)
	}
}

func TestResolveAttachesPresets(t *testing.T) {
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
		t.Fatal("AllowCustomValue should be true")
	}
}

func TestResolvePresetsHaveLabel(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{
		Parameters: []string{"ON_TIME"},
		Presets:    []uc5.Preset{{Value: 60, Label: "1m"}, {Value: 120}},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	presets := indexParams(schema)["ON_TIME"].Presets
	if presets[0]["label"] != "1m" {
		t.Fatalf("expected label 1m, got %v", presets[0]["label"])
	}
	if _, has := presets[1]["label"]; has {
		t.Fatal("preset without label must not emit label key")
	}
}

func TestResolveAllowCustomFalse(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: false,
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	if indexParams(schema)["ON_TIME"].AllowCustomValue {
		t.Fatal("AllowCustomValue must be false")
	}
}

func TestResolveUnknownParamIgnored(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{
		Parameters: []string{"DOES_NOT_EXIST"},
		Presets:    []uc5.Preset{{Value: 1}},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMultipleParams(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{
		Parameters: []string{"ON_TIME", "RAMP_TIME"},
		Presets:    []uc5.Preset{{Value: 30}},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	idx := indexParams(schema)
	if len(idx["ON_TIME"].Presets) != 1 || len(idx["RAMP_TIME"].Presets) != 1 {
		t.Fatal("both params must receive presets")
	}
}

func TestResolveIdempotent(t *testing.T) {
	schema := sampleSchema()
	uc := uc5.New([]uc5.Rule{{Parameters: []string{"ON_TIME"}, Presets: []uc5.Preset{{Value: 60}}}})
	for i := range 3 {
		if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if len(indexParams(schema)["ON_TIME"].Presets) != 1 {
		t.Fatal("idempotent resolve must keep exactly one preset")
	}
}

func TestApplyReturnsNil(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{Parameters: []string{"ON_TIME"}, Presets: []uc5.Preset{{Value: 60}}}})
	patches, err := uc.Apply(easymode.ResolveContext{}, sampleSchema(), map[string]any{"ON_TIME": 60})
	if err != nil || patches != nil {
		t.Fatalf("UC5 Apply must return (nil, nil), got (%v, %v)", patches, err)
	}
}

// Validate tests.

func TestValidateNoCurrentValues(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{}, nil)
	if issues != nil {
		t.Fatalf("no issues expected when CurrentValues empty, got %v", issues)
	}
}

func TestValidateOffPresetWarning(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}, {Value: 300}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 120},
	}, nil)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if issues[0].Code != "uc5_value_off_preset" {
		t.Fatalf("code=%q", issues[0].Code)
	}
	if issues[0].Severity != "warning" {
		t.Fatalf("severity=%q", issues[0].Severity)
	}
	if issues[0].Parameter != "ON_TIME" {
		t.Fatalf("parameter=%q", issues[0].Parameter)
	}
}

func TestValidateInPresetNoIssue(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}, {Value: 300}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 60},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("no issue expected for in-preset value, got %v", issues)
	}
}

func TestValidateAllowCustomSkipsCheck(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: true,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 999},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("no issue expected when AllowCustom, got %v", issues)
	}
}

func TestValidateLooseNumericMatch(t *testing.T) {
	// Preset stored as int, current value as float64 (common JSON decode scenario).
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": float64(60)},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("loose match should prevent warning, got %v", issues)
	}
}

func TestValidateParamNotInCurrentValues(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: 60}},
		AllowCustom: false,
	}})
	// CurrentValues has key RAMP_TIME but not ON_TIME.
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"RAMP_TIME": 30},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("no issue expected when param absent from CurrentValues, got %v", issues)
	}
}

func TestValidateFloat32PresetMatch(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: float32(60)}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": float64(60)},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("float32 preset must match float64 current via loose compare, got %v", issues)
	}
}

func TestValidateInt32And64PresetMatch(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: int32(60)}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": int64(60)},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("int32 preset must match int64, got %v", issues)
	}
}

func TestValidateInt64PresetMatch(t *testing.T) {
	uc := uc5.New([]uc5.Rule{{
		Parameters:  []string{"ON_TIME"},
		Presets:     []uc5.Preset{{Value: int64(60)}},
		AllowCustom: false,
	}})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": int(60)},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("int64 preset must match int, got %v", issues)
	}
}

func TestValidateMultipleRulesMultipleIssues(t *testing.T) {
	uc := uc5.New([]uc5.Rule{
		{Parameters: []string{"ON_TIME"}, Presets: []uc5.Preset{{Value: 60}}, AllowCustom: false},
		{Parameters: []string{"RAMP_TIME"}, Presets: []uc5.Preset{{Value: 10}}, AllowCustom: false},
	})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"ON_TIME": 99, "RAMP_TIME": 88},
	}, nil)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d: %v", len(issues), issues)
	}
}
