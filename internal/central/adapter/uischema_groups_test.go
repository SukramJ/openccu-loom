// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// uischema_groups_test.go covers the group-building logic in
// uischema_groups.go.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ============================================================
// groupLabelForLocale tests
// ============================================================

func TestGroupLabelForLocaleEn(t *testing.T) {
	t.Parallel()
	if got := groupLabelForLocale("en", "Settings", "Einstellungen"); got != "Settings" {
		t.Errorf("en → %q, want Settings", got)
	}
}

func TestGroupLabelForLocaleDe(t *testing.T) {
	t.Parallel()
	if got := groupLabelForLocale("de", "Settings", "Einstellungen"); got != "Einstellungen" {
		t.Errorf("de → %q, want Einstellungen", got)
	}
}

func TestGroupLabelForLocaleUnknown(t *testing.T) {
	t.Parallel()
	// Unknown locale falls through to English.
	if got := groupLabelForLocale("fr", "Settings", "Einstellungen"); got != "Settings" {
		t.Errorf("fr → %q, want Settings (en fallback)", got)
	}
}

// ============================================================
// otherGroupLabel tests
// ============================================================

func TestOtherGroupLabelEn(t *testing.T) {
	t.Parallel()
	if got := otherGroupLabel("en"); got != "Other Settings" {
		t.Errorf("otherGroupLabel(en) = %q, want Other Settings", got)
	}
}

func TestOtherGroupLabelDe(t *testing.T) {
	t.Parallel()
	if got := otherGroupLabel("de"); got != "Sonstige Einstellungen" {
		t.Errorf("otherGroupLabel(de) = %q, want Sonstige Einstellungen", got)
	}
}

func TestOtherGroupLabelUnknownFallsBackToEn(t *testing.T) {
	t.Parallel()
	if got := otherGroupLabel("fr"); got != "Other Settings" {
		t.Errorf("otherGroupLabel(fr) = %q, want Other Settings (en fallback)", got)
	}
}

// ============================================================
// buildGroups / semanticGroups tests
// ============================================================

func buildTestParams(names ...string) []hmapi.UISchemaParameter {
	out := make([]hmapi.UISchemaParameter, len(names))
	for i, n := range names {
		out[i] = hmapi.UISchemaParameter{Name: n}
	}
	return out
}

func TestBuildGroupsSemanticGroups(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterGroups: []ccudata.ParameterGroupDef{
			{ID: "temps", LabelKey: "temps_label", Parameters: []string{"SETPOINT", "ECO_TEMP"}},
		},
	}
	params := buildTestParams("SETPOINT", "ECO_TEMP", "EXTRA_PARAM")
	groups := a.buildGroups("en", meta, params)

	if len(groups) < 2 {
		t.Fatalf("expected at least 2 groups (semantic + other), got %d", len(groups))
	}
	if groups[0].ID != "temps" {
		t.Errorf("first group ID = %q, want temps", groups[0].ID)
	}
	// "other" group should contain EXTRA_PARAM.
	found := false
	for _, g := range groups {
		if g.ID == "other" {
			for _, p := range g.Parameters {
				if p == "EXTRA_PARAM" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("EXTRA_PARAM must appear in 'other' group")
	}
}

func TestBuildGroupsSemanticGroupsAllAssigned(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterGroups: []ccudata.ParameterGroupDef{
			{ID: "all", LabelKey: "", Parameters: []string{"LEVEL", "STATE"}},
		},
	}
	params := buildTestParams("LEVEL", "STATE")
	groups := a.buildGroups("en", meta, params)

	// No "other" group since all params are assigned.
	for _, g := range groups {
		if g.ID == "other" {
			t.Error("no other group when all params are assigned")
		}
	}
}

func TestBuildGroupsOrderedSingleGroup(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterOrder: []string{"STATE", "LEVEL"},
	}
	params := buildTestParams("LEVEL", "STATE", "EXTRA")
	groups := a.buildGroups("en", meta, params)

	if len(groups) != 1 {
		t.Fatalf("ordered-single-group: expected 1 group, got %d", len(groups))
	}
	if groups[0].ID != "all" {
		t.Errorf("group ID = %q, want all", groups[0].ID)
	}
}

func TestBuildGroupsOrderedSingleGroupDe(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterOrder: []string{"STATE"},
	}
	params := buildTestParams("STATE")
	groups := a.buildGroups("de", meta, params)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Label != "Einstellungen" {
		t.Errorf("de label = %q, want Einstellungen", groups[0].Label)
	}
}

func TestBuildGroupsOrderedEmptyParams(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterOrder: []string{"X", "Y"},
	}
	// None of the ordered params are in the available set.
	params := buildTestParams()
	groups := a.buildGroups("en", meta, params)
	if len(groups) != 0 {
		t.Errorf("empty params → 0 groups, got %d", len(groups))
	}
}

func TestBuildGroupsPatternGroups(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	// No meta → pattern groups.
	params := buildTestParams("DST_START_HOUR", "TEMPERATURE_COMFORT", "LEVEL")
	groups := a.buildGroups("en", nil, params)

	groupIDs := map[string]bool{}
	for _, g := range groups {
		groupIDs[g.ID] = true
	}
	if !groupIDs["dst"] {
		t.Error("DST_START_HOUR must be in dst group")
	}
	if !groupIDs["temperature"] {
		t.Error("TEMPERATURE_COMFORT must be in temperature group")
	}
	if !groupIDs["other"] {
		t.Error("LEVEL must be in other group")
	}
}

func TestBuildGroupsPatternGroupsAllOther(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	// No pattern matches → everything in other.
	params := buildTestParams("LEVEL", "STATE")
	groups := a.buildGroups("en", nil, params)

	if len(groups) != 1 || groups[0].ID != "other" {
		t.Errorf("all unmatched → single other group, got %v", groups)
	}
}

func TestBuildGroupsPatternGroupsEmpty(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	groups := a.buildGroups("en", nil, nil)
	if len(groups) != 0 {
		t.Errorf("empty names → 0 groups, got %d", len(groups))
	}
}

// ============================================================
// semanticGroups: empty parameter group filtered
// ============================================================

func TestSemanticGroupsSkipsEmptyGroup(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	meta := &ccudata.SenderTypeMetadata{
		ParameterGroups: []ccudata.ParameterGroupDef{
			{ID: "empty_group", LabelKey: "", Parameters: []string{"MISSING"}},
			{ID: "real_group", LabelKey: "", Parameters: []string{"LEVEL"}},
		},
	}
	available := stringSet([]string{"LEVEL"})
	groups := a.semanticGroups("en", meta, available)

	for _, g := range groups {
		if g.ID == "empty_group" {
			t.Error("group with no available params must be skipped")
		}
	}
}
