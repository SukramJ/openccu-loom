// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// dispatcher_uischema_helpers_test.go covers pure helpers in
// custom_dp_dispatcher.go (extractRow, paramString, paramStringOptional)
// and uischema_adapter.go (resolveSubsetOptions, buildSubsetGroups,
// isSchedulePattern, lookupChannel).

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// ============================================================
// extractRow
// ============================================================

func TestExtractRowMissingID(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected error for missing id param")
	}
}

func TestExtractRowBadIDType(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": "not-an-int"})
	if err == nil {
		t.Fatal("expected error for string id")
	}
}

func TestExtractRowMinimal(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(1)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.ID != 1 {
		t.Errorf("row.ID = %d, want 1", row.ID)
	}
}

func TestExtractRowWithText(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(2), "text": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Text != "hello" {
		t.Errorf("row.Text = %q, want hello", row.Text)
	}
}

func TestExtractRowTextNotString(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": int64(1), "text": 42})
	if err == nil {
		t.Fatal("expected error for non-string text")
	}
}

func TestExtractRowWithIcon(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(3), "icon": "home"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Icon != "home" {
		t.Errorf("row.Icon = %q, want home", row.Icon)
	}
}

func TestExtractRowIconNotString(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": int64(1), "icon": 99})
	if err == nil {
		t.Fatal("expected error for non-string icon")
	}
}

func TestExtractRowWithAlignment(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(1), "alignment": "CENTER"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Alignment == nil || *row.Alignment != "CENTER" {
		t.Fatalf("alignment = %v, want CENTER", row.Alignment)
	}
}

func TestExtractRowAlignmentBadType(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": int64(1), "alignment": int64(1)})
	if err == nil {
		t.Fatal("expected error for non-string alignment")
	}
}

func TestExtractRowWithTextColor(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(1), "text_color": "BLACK"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.TextColor == nil || *row.TextColor != "BLACK" {
		t.Errorf("text_color = %v, want BLACK", row.TextColor)
	}
}

func TestExtractRowTextColorBadType(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": int64(1), "text_color": int64(255)})
	if err == nil {
		t.Fatal("expected error for non-string text_color")
	}
}

func TestExtractRowWithBackgroundColor(t *testing.T) {
	t.Parallel()
	row, err := extractRow(map[string]any{"id": int64(1), "background_color": "WHITE"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.BackgroundColor == nil || *row.BackgroundColor != "WHITE" {
		t.Fatalf("background_color = %v, want WHITE", row.BackgroundColor)
	}
}

func TestExtractRowBackgroundColorBadType(t *testing.T) {
	t.Parallel()
	_, err := extractRow(map[string]any{"id": int64(1), "background_color": struct{}{}})
	if err == nil {
		t.Fatal("expected error for struct background_color")
	}
}

// ============================================================
// paramString
// ============================================================

func TestParamStringPresent(t *testing.T) {
	t.Parallel()
	s, err := paramString(map[string]any{"key": "value"}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "value" {
		t.Errorf("paramString = %q, want value", s)
	}
}

func TestParamStringMissing(t *testing.T) {
	t.Parallel()
	_, err := paramString(map[string]any{}, "key")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestParamStringWrongType(t *testing.T) {
	t.Parallel()
	_, err := paramString(map[string]any{"key": 42}, "key")
	if err == nil {
		t.Fatal("expected error for non-string value")
	}
}

// ============================================================
// resolveSubsetOptions
// ============================================================

func TestResolveSubsetOptionsNewStyle(t *testing.T) {
	t.Parallel()
	ss := ccudata.SubsetDef{
		ID:      1,
		NameKey: "mode",
		Options: []ccudata.SubsetOption{
			{ID: 1, LabelKey: "opt_a", Values: map[string]any{"PARAM": 0}},
			{ID: 2, LabelKey: "opt_b", Values: map[string]any{"PARAM": 1}},
		},
	}
	opts := resolveSubsetOptions(ss)
	if len(opts) != 2 {
		t.Fatalf("resolveSubsetOptions new-style = %d, want 2", len(opts))
	}
	if opts[0].Label != "opt_a" {
		t.Errorf("opts[0].Label = %q, want opt_a", opts[0].Label)
	}
}

func TestResolveSubsetOptionsLegacySingleOption(t *testing.T) {
	t.Parallel()
	ss := ccudata.SubsetDef{
		ID:      1,
		NameKey: "mode",
		Values:  map[string]any{"PARAM": 0},
	}
	opts := resolveSubsetOptions(ss)
	if len(opts) != 1 {
		t.Fatalf("resolveSubsetOptions legacy = %d, want 1", len(opts))
	}
	if opts[0].Label != "mode" {
		t.Errorf("opts[0].Label = %q, want mode", opts[0].Label)
	}
}

func TestResolveSubsetOptionsEmpty(t *testing.T) {
	t.Parallel()
	ss := ccudata.SubsetDef{ID: 1, NameKey: "mode"}
	opts := resolveSubsetOptions(ss)
	if len(opts) != 0 {
		t.Errorf("resolveSubsetOptions empty = %d, want 0", len(opts))
	}
}

// ============================================================
// buildSubsetGroups
// ============================================================

func TestBuildSubsetGroupsEmpty(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	groups := a.buildSubsetGroups("en", nil, nil)
	if groups != nil {
		t.Errorf("buildSubsetGroups nil subsets = %v, want nil", groups)
	}
}

func TestBuildSubsetGroupsSkipsEmptyMemberParams(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	subsets := []ccudata.SubsetDef{
		{ID: 1, NameKey: "mode"}, // no MemberParams
		{ID: 2, NameKey: "mode2", MemberParams: []string{"LEVEL"}, Values: map[string]any{"LEVEL": 0}},
	}
	groups := a.buildSubsetGroups("en", subsets, nil)
	if len(groups) != 1 {
		t.Errorf("buildSubsetGroups with empty member = %d groups, want 1", len(groups))
	}
}

func TestBuildSubsetGroupsMergesSameMembers(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	subsets := []ccudata.SubsetDef{
		{ID: 1, NameKey: "opt_a", MemberParams: []string{"P1", "P2"}, Values: map[string]any{"P1": 0}},
		{ID: 2, NameKey: "opt_b", MemberParams: []string{"P1", "P2"}, Values: map[string]any{"P1": 1}},
	}
	groups := a.buildSubsetGroups("en", subsets, nil)
	if len(groups) != 1 {
		t.Fatalf("same member set must merge into 1 group, got %d", len(groups))
	}
	if len(groups[0].Options) != 2 {
		t.Errorf("merged group has %d options, want 2", len(groups[0].Options))
	}
}

func TestBuildSubsetGroupsActiveOptionDetection(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	subsets := []ccudata.SubsetDef{
		{ID: 1, NameKey: "opt_off", MemberParams: []string{"STATE"}, Values: map[string]any{"STATE": false}},
		{ID: 2, NameKey: "opt_on", MemberParams: []string{"STATE"}, Values: map[string]any{"STATE": true}},
	}
	params := []handlers.UISchemaParameter{
		{Name: "STATE", Value: true, Observed: true},
	}
	groups := a.buildSubsetGroups("en", subsets, params)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].CurrentOptionID == nil {
		t.Fatal("CurrentOptionID must be set when observed value matches")
	}
}

// ============================================================
// lookupChannel (nil registry)
// ============================================================

func TestLookupChannelNilRegistry(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	dev, ch := a.lookupChannel("DEV001", 1)
	if dev != nil || ch != nil {
		t.Errorf("lookupChannel nil registry = (%v, %v), want both nil", dev, ch)
	}
}

func TestLookupChannelNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := &UISchemaAdapter{registry: reg}
	dev, ch := a.lookupChannel("NOSUCHDEV", 1)
	if dev != nil || ch != nil {
		t.Errorf("lookupChannel not found = (%v, %v), want both nil", dev, ch)
	}
}

// ============================================================
// EventBridge Stop (nil-registry)
// ============================================================

func TestEventBridgeStopNilRegistry(t *testing.T) {
	t.Parallel()
	b := NewEventBridge(nil, nil, nil)
	b.Stop() // must not panic
}
