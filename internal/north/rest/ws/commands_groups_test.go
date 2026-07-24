// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// stubGroupsAdmin is a configurable handlers.GroupsWriter fake for the WS
// group-admin command tests (and the role-gate registration guard).
type stubGroupsAdmin struct {
	created  handlers.CreateGroupRequest
	deleted  int
	types    []handlers.GroupTypeEntry
	members  handlers.SuitableMembersResponse
	createFn func() (handlers.GroupEntry, error)
	err      error
}

func (s *stubGroupsAdmin) CreateGroup(_ context.Context, _ string, req handlers.CreateGroupRequest) (handlers.GroupEntry, error) {
	s.created = req
	if s.createFn != nil {
		return s.createFn()
	}
	return handlers.GroupEntry{ID: 7, Name: req.Name}, s.err
}

func (s *stubGroupsAdmin) UpdateGroup(_ context.Context, _ string, _ int, _ handlers.UpdateGroupRequest) error {
	return s.err
}

func (s *stubGroupsAdmin) DeleteGroup(_ context.Context, _ string, id int) error {
	s.deleted = id
	return s.err
}

func (s *stubGroupsAdmin) SuitableMembers(_ context.Context, _, _ string) (handlers.SuitableMembersResponse, error) {
	return s.members, s.err
}

func (s *stubGroupsAdmin) GroupTypes(_ context.Context, _ string) ([]handlers.GroupTypeEntry, error) {
	return s.types, s.err
}

func TestGroupsWSCommands(t *testing.T) {
	t.Parallel()
	stub := &stubGroupsAdmin{
		types:   []handlers.GroupTypeEntry{{ID: "hmip.heating.group"}},
		members: handlers.SuitableMembersResponse{Assignable: []handlers.SuitableMemberEntry{{Address: "A:1"}}},
	}
	ctx := context.Background()

	// groups.create maps params → handlers.CreateGroupRequest and returns the
	// created group.
	out, err := groupsCreateHandler(stub)(ctx,
		json.RawMessage(`{"type_id":"hmip.heating.group","name":"Bad","members":["A:1"]}`))
	if err != nil {
		t.Fatalf("groups.create: %v", err)
	}
	if g, ok := out.(handlers.GroupEntry); !ok || g.Name != "Bad" {
		t.Fatalf("groups.create result = %#v", out)
	}
	if stub.created.Name != "Bad" || len(stub.created.Members) != 1 {
		t.Errorf("create req = %#v", stub.created)
	}

	// name + type_id are required.
	if _, err := groupsCreateHandler(stub)(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("groups.create with empty body: want error")
	}

	// groups.delete forwards the id.
	if _, err := groupsDeleteHandler(stub)(ctx, json.RawMessage(`{"id":4}`)); err != nil {
		t.Fatalf("groups.delete: %v", err)
	}
	if stub.deleted != 4 {
		t.Errorf("deleted id = %d, want 4", stub.deleted)
	}

	// groups.update requires a name.
	if _, err := groupsUpdateHandler(stub)(ctx, json.RawMessage(`{"id":4}`)); err == nil {
		t.Error("groups.update without name: want error")
	}

	// groups.types passes through.
	if _, err := groupsTypesHandler(stub)(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("groups.types: %v", err)
	}

	// groups.suitable_members requires type_id.
	if _, err := groupsSuitableMembersHandler(stub)(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("groups.suitable_members without type_id: want error")
	}
	if _, err := groupsSuitableMembersHandler(stub)(ctx,
		json.RawMessage(`{"type_id":"hmip.heating.group"}`)); err != nil {
		t.Fatalf("groups.suitable_members: %v", err)
	}
}
