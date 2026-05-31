// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"testing"
)

// TestBuildSubsetGroups_NilDefsReturnsNil ensures no-op on empty input.
func TestBuildSubsetGroups_NilDefsReturnsNil(t *testing.T) {
	got := buildSubsetGroups(nil, nil)
	if got != nil {
		t.Fatalf("nil defs must return nil, got %v", got)
	}
}

// TestBuildSubsetGroups_SingleDefLegacyValues tests the legacy
// single-value SubsetDef form (Values populated, Options empty).
func TestBuildSubsetGroups_SingleDefLegacyValues(t *testing.T) {
	defs := []SubsetDefInput{
		{
			ID:           1,
			NameKey:      "easymode.richtung",
			MemberParams: []string{"JT_OFF", "JT_ON"},
			Values:       map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0},
		},
	}
	current := map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0}
	got := buildSubsetGroups(defs, current)
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	g := got[0]
	if g.ID != "subset_JT_OFF" {
		t.Errorf("id=%q want subset_JT_OFF", g.ID)
	}
	if g.Label != "easymode.richtung" {
		t.Errorf("label=%q want easymode.richtung", g.Label)
	}
	if len(g.MemberParams) != 2 {
		t.Fatalf("member_params=%v want [JT_OFF JT_ON]", g.MemberParams)
	}
	if g.CurrentOptionID == nil || *g.CurrentOptionID != 1 {
		t.Errorf("current_option_id=%v want &1", g.CurrentOptionID)
	}
}

// TestBuildSubsetGroups_MultiOptionForm tests newer multi-option SubsetDefs
// (UC6 Easymode UC6 case).
func TestBuildSubsetGroups_MultiOptionForm(t *testing.T) {
	// Simulates: two virtual "Richtung" options (Auf=1, Ab=2) for the
	// same member parameters — a typical blind/shutter scene preset.
	defs := []SubsetDefInput{
		{
			ID:           0,
			NameKey:      "easymode.richtung",
			MemberParams: []string{"JT_OFF", "JT_ON"},
			Options: []SubsetOptionInput{
				{ID: 1, LabelKey: "easymode.auf", Values: map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0}},
				{ID: 2, LabelKey: "easymode.ab", Values: map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}},
			},
		},
	}
	// Currently "Ab" is active.
	current := map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}
	got := buildSubsetGroups(defs, current)
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	g := got[0]
	if len(g.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(g.Options))
	}
	if g.CurrentOptionID == nil || *g.CurrentOptionID != 2 {
		t.Errorf("current_option_id=%v want &2 (Ab)", g.CurrentOptionID)
	}
}

// TestBuildSubsetGroups_NoMatchCurrentOption ensures CurrentOptionID
// is nil when no option values match the current paramset.
func TestBuildSubsetGroups_NoMatchCurrentOption(t *testing.T) {
	defs := []SubsetDefInput{
		{
			ID:           1,
			NameKey:      "easymode.licht",
			MemberParams: []string{"LEVEL"},
			Values:       map[string]any{"LEVEL": 1.0},
		},
	}
	current := map[string]any{"LEVEL": 0.5} // no exact match
	got := buildSubsetGroups(defs, current)
	if len(got) != 1 {
		t.Fatalf("expected 1 group")
	}
	if got[0].CurrentOptionID != nil {
		t.Errorf("CurrentOptionID must be nil when no option matches")
	}
}

// TestBuildSubsetGroups_MergesSameMemberParams verifies that two
// SubsetDefInput entries with the same member_params are merged into
// one SubsetGroup (matching the Python _build_subset_groups behaviour).
func TestBuildSubsetGroups_MergesSameMemberParams(t *testing.T) {
	defs := []SubsetDefInput{
		{
			ID:           1,
			NameKey:      "easymode.auf",
			MemberParams: []string{"JT_OFF", "JT_ON"},
			Values:       map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0},
		},
		{
			ID:           2,
			NameKey:      "easymode.ab",
			MemberParams: []string{"JT_OFF", "JT_ON"},
			Values:       map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0},
		},
	}
	current := map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}
	got := buildSubsetGroups(defs, current)
	if len(got) != 1 {
		t.Fatalf("same member_params must be merged → expected 1 group, got %d", len(got))
	}
	g := got[0]
	if len(g.Options) != 2 {
		t.Fatalf("merged group must have 2 options, got %d", len(g.Options))
	}
	if g.CurrentOptionID == nil || *g.CurrentOptionID != 2 {
		t.Errorf("active option should be 2 (ab), got %v", g.CurrentOptionID)
	}
}

// TestGenerateBuildsSubsetGroupsFromInput verifies that Generate()
// populates Schema.SubsetGroups when SubsetDefs are provided.
func TestGenerateBuildsSubsetGroupsFromInput(t *testing.T) {
	in := GenerateInput{
		ChannelAddress: "0001ABCD:1",
		ChannelType:    "BLIND",
		SubsetDefs: []SubsetDefInput{
			{
				ID:           1,
				NameKey:      "easymode.richtung",
				MemberParams: []string{"JT_OFF", "JT_ON"},
				Options: []SubsetOptionInput{
					{ID: 1, LabelKey: "easymode.auf", Values: map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0}},
					{ID: 2, LabelKey: "easymode.ab", Values: map[string]any{"JT_OFF": 2.0, "JT_ON": 2.0}},
				},
			},
		},
		CurrentValues: map[string]any{"JT_OFF": 1.0, "JT_ON": 1.0},
	}
	s := Generate(in)
	if len(s.SubsetGroups) != 1 {
		t.Fatalf("Generate must populate SubsetGroups, got %d", len(s.SubsetGroups))
	}
	if s.SubsetGroups[0].CurrentOptionID == nil || *s.SubsetGroups[0].CurrentOptionID != 1 {
		t.Errorf("current_option_id=%v want &1", s.SubsetGroups[0].CurrentOptionID)
	}
}
