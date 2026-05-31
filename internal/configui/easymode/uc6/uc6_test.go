// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package uc6_test

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode"
	"github.com/SukramJ/openccu-loom/internal/configui/easymode/uc6"
)

func sampleSchema() *configui.Schema {
	return &configui.Schema{
		ChannelType: "BLIND",
		Sections: []configui.FormSection{
			{
				ID: "main",
				Parameters: []configui.FormParameter{
					{ID: "JT_OFF"},
					{ID: "JT_ON"},
					{ID: "MODE"},
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

func twoOptionGroup() uc6.Group {
	return uc6.Group{
		ID:           "shutter_dir",
		Label:        "Richtung",
		MemberParams: []string{"JT_OFF", "JT_ON"},
		Options: []uc6.Option{
			{ID: 1, Label: "Auf", Values: map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0}},
			{ID: 2, Label: "Ab", Values: map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}},
		},
	}
}

func TestID(t *testing.T) {
	uc := uc6.New(nil)
	if uc.ID() != "uc6" {
		t.Fatalf("got %q", uc.ID())
	}
}

// Resolve tests.

func TestResolveNilSchema(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	if err := uc.Resolve(easymode.ResolveContext{}, nil); err != nil {
		t.Fatalf("nil schema must not error: %v", err)
	}
}

func TestResolveBuildsSubsetGroups(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.SubsetGroups) != 1 {
		t.Fatalf("expected 1 subset group, got %d", len(schema.SubsetGroups))
	}
	g := schema.SubsetGroups[0]
	if g.ID != "shutter_dir" {
		t.Fatalf("id=%q", g.ID)
	}
	if g.Label != "Richtung" {
		t.Fatalf("label=%q", g.Label)
	}
	if len(g.Options) != 2 {
		t.Fatalf("options=%d", len(g.Options))
	}
}

func TestResolveTagsMemberParams(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	idx := indexParams(schema)
	if idx["JT_OFF"].SubsetGroupID != "shutter_dir" {
		t.Fatalf("JT_OFF SubsetGroupID=%q", idx["JT_OFF"].SubsetGroupID)
	}
	if idx["JT_ON"].SubsetGroupID != "shutter_dir" {
		t.Fatalf("JT_ON SubsetGroupID=%q", idx["JT_ON"].SubsetGroupID)
	}
	if idx["MODE"].SubsetGroupID != "" {
		t.Fatal("MODE must not be tagged")
	}
}

func TestResolveDetectsActiveOption(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"JT_OFF": 1, "JT_ON": 1}}
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	g := schema.SubsetGroups[0]
	if g.CurrentOptionID == nil || *g.CurrentOptionID != 1 {
		t.Fatalf("expected active option 1 (Auf), got %v", g.CurrentOptionID)
	}
}

func TestResolveNoActiveOptionWhenNoMatch(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	// Values that don't match any option.
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"JT_OFF": 99, "JT_ON": 99}}
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID != nil {
		t.Fatal("CurrentOptionID must be nil when no option matches")
	}
}

func TestResolveNoActiveOptionWhenEmptyValues(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID != nil {
		t.Fatal("CurrentOptionID must be nil when CurrentValues is empty")
	}
}

func TestResolveMultipleGroups(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{
		twoOptionGroup(),
		{
			ID:           "mode_group",
			MemberParams: []string{"MODE"},
			Options:      []uc6.Option{{ID: 10, Values: map[string]any{"MODE": 0}}},
		},
	})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.SubsetGroups) != 2 {
		t.Fatalf("expected 2 subset groups, got %d", len(schema.SubsetGroups))
	}
}

func TestResolveIdempotent(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	for i := range 3 {
		if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if len(schema.SubsetGroups) != 1 {
		t.Fatal("idempotent resolve must keep exactly one group")
	}
}

func TestResolveUnknownMemberParamIgnored(t *testing.T) {
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"DOES_NOT_EXIST"},
		Options:      []uc6.Option{{ID: 1, Values: map[string]any{}}},
	}})
	if err := uc.Resolve(easymode.ResolveContext{}, schema); err != nil {
		t.Fatal(err)
	}
}

func TestResolveActiveOptionLooseFloatCompare(t *testing.T) {
	// Option values are int; CurrentValues provides float64 (JSON decode).
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"JT_OFF"},
		Options:      []uc6.Option{{ID: 5, Values: map[string]any{"JT_OFF": 3}}},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"JT_OFF": float64(3)}}
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID == nil || *schema.SubsetGroups[0].CurrentOptionID != 5 {
		t.Fatal("loose float compare must detect active option")
	}
}

// Validate tests.

func TestValidateNoCurrentValues(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	issues := uc.Validate(easymode.ResolveContext{}, nil)
	if len(issues) != 0 {
		t.Fatalf("no issues expected when key absent, got %v", issues)
	}
}

func TestValidateValidOptionID(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"shutter_dir.option_id": 1},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("no issues for valid option id, got %v", issues)
	}
}

func TestValidateUnknownOptionIDError(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"shutter_dir.option_id": 99},
	}, nil)
	if len(issues) != 1 || issues[0].Code != "uc6_unknown_option" {
		t.Fatalf("expected uc6_unknown_option, got %v", issues)
	}
	if issues[0].Severity != "error" {
		t.Fatalf("severity=%q", issues[0].Severity)
	}
}

func TestValidateInvalidOptionIDType(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"shutter_dir.option_id": "bad"},
	}, nil)
	if len(issues) != 1 || issues[0].Code != "uc6_invalid_option_id" {
		t.Fatalf("expected uc6_invalid_option_id, got %v", issues)
	}
}

func TestValidateFloat64OptionID(t *testing.T) {
	// JSON decodes numbers as float64; the validator must accept it.
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	issues := uc.Validate(easymode.ResolveContext{
		CurrentValues: map[string]any{"shutter_dir.option_id": float64(2)},
	}, nil)
	if len(issues) != 0 {
		t.Fatalf("float64 option id must be accepted, got %v", issues)
	}
}

// Apply tests.

func TestApplyEmptyValues(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, nil)
	if err != nil || patches != nil {
		t.Fatalf("empty values must return (nil, nil), got (%v, %v)", patches, err)
	}
}

func TestApplyExpandsOption(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 2.0 || patches["JT_ON"] != 2.0 {
		t.Fatalf("expanded patches=%v", patches)
	}
}

func TestApplyInt32OptionID(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": int32(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 1.0 {
		t.Fatalf("int32 option id must work: %v", patches)
	}
}

func TestApplyFloat32OptionID(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": float32(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 2.0 {
		t.Fatalf("float32 option id must work: %v", patches)
	}
}

func TestApplyInvalidOptionIDType(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	_, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": "oops",
	})
	if err == nil {
		t.Fatal("expected error for non-int option id")
	}
	if !strings.Contains(err.Error(), "must be int") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyUnknownOptionIDError(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	_, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": 999,
	})
	if err == nil {
		t.Fatal("expected error for unknown option")
	}
	if !strings.Contains(err.Error(), "option") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplySkipsGroupsNotInValues(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"other_group.option_id": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 0 {
		t.Fatalf("no patches expected for unlisted group key, got %v", patches)
	}
}

func TestApplyMultipleGroupsInValues(t *testing.T) {
	uc := uc6.New([]uc6.Group{
		twoOptionGroup(),
		{
			ID:           "mode_group",
			MemberParams: []string{"MODE"},
			Options:      []uc6.Option{{ID: 10, Values: map[string]any{"MODE": 0}}},
		},
	})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": 1,
		"mode_group.option_id":  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 1.0 || patches["MODE"] != 0 {
		t.Fatalf("multi-group patches=%v", patches)
	}
}

func TestResolveActiveOptionDirectEquality(t *testing.T) {
	// looseEqual: direct a==b path for string values.
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"MODE"},
		Options:      []uc6.Option{{ID: 7, Values: map[string]any{"MODE": "auto"}}},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"MODE": "auto"}}
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID == nil || *schema.SubsetGroups[0].CurrentOptionID != 7 {
		t.Fatal("string equality must detect active option")
	}
}

func TestResolveActiveOptionPartialMismatch(t *testing.T) {
	// Only one member matches → no active option.
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"JT_OFF", "JT_ON"},
		Options:      []uc6.Option{{ID: 1, Values: map[string]any{"JT_OFF": 1, "JT_ON": 1}}},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{"JT_OFF": 1, "JT_ON": 2}}
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID != nil {
		t.Fatal("partial match must not produce active option")
	}
}

func TestResolveActiveOptionMissingMemberKey(t *testing.T) {
	// Member key absent from CurrentValues → no active option.
	schema := sampleSchema()
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"JT_OFF"},
		Options:      []uc6.Option{{ID: 1, Values: map[string]any{"JT_OFF": 1}}},
	}})
	ctx := easymode.ResolveContext{CurrentValues: map[string]any{}} // JT_OFF absent
	if err := uc.Resolve(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.SubsetGroups[0].CurrentOptionID != nil {
		t.Fatal("missing member key must not produce active option")
	}
}

func TestApplyInt64OptionID(t *testing.T) {
	uc := uc6.New([]uc6.Group{twoOptionGroup()})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{
		"shutter_dir.option_id": int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if patches["JT_OFF"] != 1.0 {
		t.Fatalf("int64 option id must work: %v", patches)
	}
}

func TestApplyOptionValuesAreCopied(t *testing.T) {
	// Ensure Apply does not alias the internal map.
	orig := map[string]any{"JT_OFF": "sentinel"}
	uc := uc6.New([]uc6.Group{{
		ID:           "g",
		MemberParams: []string{"JT_OFF"},
		Options:      []uc6.Option{{ID: 1, Values: orig}},
	}})
	patches, err := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{"g.option_id": 1})
	if err != nil {
		t.Fatal(err)
	}
	// Mutate returned patches; must not affect re-Apply.
	patches["JT_OFF"] = "modified"
	patches2, _ := uc.Apply(easymode.ResolveContext{}, nil, map[string]any{"g.option_id": 1})
	if patches2["JT_OFF"] != "sentinel" {
		t.Fatal("option values must be a copy; mutation leaked back")
	}
}
