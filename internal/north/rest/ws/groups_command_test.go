// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

// Tests for the `groups.list` command (groupsListHandler / GroupsQuery),
// registered by RegisterExtendedCommands when ExtendedCommandsConfig.Groups
// is non-nil.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// stubGroupsQuery implements GroupsQuery for tests: it records the
// requested central and returns a configurable entries slice.
type stubGroupsQuery struct {
	lastCentral string
	entries     []handlers.GroupCentralEntry
}

func (s *stubGroupsQuery) List(_ context.Context, central string) ([]handlers.GroupCentralEntry, error) {
	s.lastCentral = central
	return s.entries, nil
}

func TestGroupsList_HappyPath(t *testing.T) {
	t.Parallel()
	q := &stubGroupsQuery{entries: []handlers.GroupCentralEntry{
		{
			Central: "ccu-01",
			Groups: []handlers.GroupEntry{
				{ID: 3, Name: "Kitchen", TypeID: "HEATING"},
			},
		},
	}}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Groups: q})

	out := dispatch(t, r, "groups.list", map[string]any{}).(map[string]any)
	entries, ok := out["entries"].([]handlers.GroupCentralEntry)
	if !ok {
		t.Fatalf("expected entries key of type []handlers.GroupCentralEntry, got %T", out["entries"])
	}
	if len(entries) != 1 {
		t.Fatalf("entries len=%d, want 1", len(entries))
	}
	if entries[0].Central != "ccu-01" {
		t.Errorf("Central = %q, want ccu-01", entries[0].Central)
	}
	if len(entries[0].Groups) != 1 || entries[0].Groups[0].Name != "Kitchen" {
		t.Errorf("Groups = %+v", entries[0].Groups)
	}
	if q.lastCentral != "" {
		t.Errorf("lastCentral = %q, want empty (aggregate)", q.lastCentral)
	}
}

func TestGroupsList_ForwardsCentralParam(t *testing.T) {
	t.Parallel()
	q := &stubGroupsQuery{}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Groups: q})

	out := dispatch(t, r, "groups.list", map[string]any{"central": "ccu-02"}).(map[string]any)
	if q.lastCentral != "ccu-02" {
		t.Fatalf("central param not forwarded, got %q", q.lastCentral)
	}
	entries, ok := out["entries"].([]handlers.GroupCentralEntry)
	if !ok {
		t.Fatalf("expected entries key of type []handlers.GroupCentralEntry, got %T", out["entries"])
	}
	if len(entries) != 0 {
		t.Fatalf("entries len=%d, want 0", len(entries))
	}
}

// TestGroupsList_NilEntriesBecomeEmptyArray asserts the handler substitutes
// a non-nil empty slice when the query returns nil, mirroring the REST
// ListGroups handler's own null-safety contract.
func TestGroupsList_NilEntriesBecomeEmptyArray(t *testing.T) {
	t.Parallel()
	q := &stubGroupsQuery{entries: nil}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Groups: q})

	out := dispatch(t, r, "groups.list", map[string]any{}).(map[string]any)
	entries, ok := out["entries"].([]handlers.GroupCentralEntry)
	if !ok {
		t.Fatalf("expected entries key of type []handlers.GroupCentralEntry, got %T", out["entries"])
	}
	if entries == nil {
		t.Fatal("entries must be a non-nil empty slice, not nil")
	}
	if len(entries) != 0 {
		t.Fatalf("entries len=%d, want 0", len(entries))
	}
}

// TestGroupsList_NotRegisteredWhenGroupsNil asserts that RegisterExtendedCommands
// skips `groups.list` entirely when ExtendedCommandsConfig.Groups is nil —
// every other sub-config field is nil-guarded the same way.
func TestGroupsList_NotRegisteredWhenGroupsNil(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{})

	for _, name := range r.Commands() {
		if name == "groups.list" {
			t.Fatal("groups.list must not be registered when Groups is nil")
		}
	}

	dispatchExpectErr(t, r, "groups.list", map[string]any{}, "")
}
