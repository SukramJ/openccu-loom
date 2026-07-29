// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

func freshAreaStore(t *testing.T) *AreaStore {
	t.Helper()
	return NewAreaStore(openTestDB(t, "areas.db"))
}

func baseAreaRow(id string) AreaRow {
	return AreaRow{
		ID:          id,
		Name:        "Ground Floor",
		Position:    1,
		CreatedAtMS: 1000,
		UpdatedAtMS: 1000,
	}
}

// TestAreaStoreUpsertInsertRoundTrip verifies that Upsert on a new id
// inserts a row and every field survives the Upsert -> Get round trip.
func TestAreaStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	row := baseAreaRow("area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "area-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true for inserted row")
	}
	if got != row {
		t.Errorf("Get=%+v want %+v", got, row)
	}
}

// TestAreaStoreGetMissingReturnsFalse verifies that Get on an unknown id
// returns the zero value, false, nil (no error).
func TestAreaStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AreaRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAreaStoreUpsertUpdatePreservesCreatedAt verifies that a second
// Upsert call on the same id updates mutable fields but never overwrites
// created_at_ms.
func TestAreaStoreUpsertUpdatePreservesCreatedAt(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	row := baseAreaRow("area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := row
	updated.Name = "Renamed"
	updated.Position = 5
	updated.CreatedAtMS = 9999 // must be ignored on update
	updated.UpdatedAtMS = 2000
	if err := s.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, ok, err := s.Get(ctx, "area-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.Name != "Renamed" {
		t.Errorf("Name=%q want Renamed", got.Name)
	}
	if got.Position != 5 {
		t.Errorf("Position=%d want 5", got.Position)
	}
	if got.UpdatedAtMS != 2000 {
		t.Errorf("UpdatedAtMS=%d want 2000", got.UpdatedAtMS)
	}
	if got.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS=%d want 1000 (must be preserved from insert)", got.CreatedAtMS)
	}
}

// TestAreaStoreGetAllOrdering verifies GetAll orders by position then name.
func TestAreaStoreGetAllOrdering(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	rows := []AreaRow{
		{ID: "c", Name: "Charlie", Position: 2, CreatedAtMS: 1, UpdatedAtMS: 1},
		{ID: "a", Name: "Alpha", Position: 1, CreatedAtMS: 1, UpdatedAtMS: 1},
		{ID: "b", Name: "Bravo", Position: 1, CreatedAtMS: 1, UpdatedAtMS: 1},
	}
	for _, r := range rows {
		if err := s.Upsert(ctx, r); err != nil {
			t.Fatalf("Upsert %s: %v", r.ID, err)
		}
	}

	got, err := s.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	wantOrder := []string{"a", "b", "c"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAreaStoreDeleteCascadesAssignments verifies Delete removes both
// the area row and every room_areas row assigned to it, leaving other
// areas' assignments untouched.
func TestAreaStoreDeleteCascadesAssignments(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAreaRow("area-1")); err != nil {
		t.Fatalf("Upsert area-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAreaRow("area-2")); err != nil {
		t.Fatalf("Upsert area-2: %v", err)
	}
	if err := s.ReplaceRooms(ctx, "area-1", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-1"},
	}); err != nil {
		t.Fatalf("ReplaceRooms area-1: %v", err)
	}
	if err := s.ReplaceRooms(ctx, "area-2", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Garage", AreaID: "area-2"},
	}); err != nil {
		t.Fatalf("ReplaceRooms area-2: %v", err)
	}

	if err := s.Delete(ctx, "area-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, ok, err := s.Get(ctx, "area-1"); err != nil || ok {
		t.Fatalf("area-1 still exists after Delete: ok=%v err=%v", ok, err)
	}
	assignments, err := s.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments=%+v want exactly area-2's Garage row to survive", assignments)
	}
	if assignments[0].AreaID != "area-2" || assignments[0].RoomName != "Garage" {
		t.Errorf("surviving assignment=%+v want area-2/Garage", assignments[0])
	}
}

// TestAreaStoreReplaceRoomsFullSet verifies ReplaceRooms sets exactly the
// given room set, dropping rows omitted from a later call.
func TestAreaStoreReplaceRoomsFullSet(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAreaRow("area-1")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.ReplaceRooms(ctx, "area-1", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-1"},
		{CentralName: "ccu1", RoomName: "Hallway", AreaID: "area-1"},
	}); err != nil {
		t.Fatalf("ReplaceRooms 1: %v", err)
	}
	// Second call omits Hallway — it must be unassigned, not merged.
	if err := s.ReplaceRooms(ctx, "area-1", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-1"},
	}); err != nil {
		t.Fatalf("ReplaceRooms 2: %v", err)
	}

	assignments, err := s.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].RoomName != "Kitchen" {
		t.Errorf("assignments=%+v want exactly [Kitchen]", assignments)
	}
}

// TestAreaStoreReplaceRoomsMovesRoomFromAnotherArea verifies the one
// area per room invariant: assigning a room already owned by another
// area via ReplaceRooms moves it rather than erroring or duplicating.
func TestAreaStoreReplaceRoomsMovesRoomFromAnotherArea(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAreaRow("area-1")); err != nil {
		t.Fatalf("Upsert area-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAreaRow("area-2")); err != nil {
		t.Fatalf("Upsert area-2: %v", err)
	}
	if err := s.ReplaceRooms(ctx, "area-1", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-1"},
	}); err != nil {
		t.Fatalf("ReplaceRooms area-1: %v", err)
	}

	// area-2 claims the same (central, room) pair — it must move.
	if err := s.ReplaceRooms(ctx, "area-2", []RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-2"},
	}); err != nil {
		t.Fatalf("ReplaceRooms area-2: %v", err)
	}

	assignments, err := s.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments=%+v want exactly 1 row (the pair moved, not duplicated)", assignments)
	}
	if assignments[0].AreaID != "area-2" {
		t.Errorf("AreaID=%q want area-2 (the room must have moved)", assignments[0].AreaID)
	}
}

// TestAreaStoreListAssignmentsOrdering verifies ListAssignments orders by
// central then room, across multiple areas.
func TestAreaStoreListAssignmentsOrdering(t *testing.T) {
	s := freshAreaStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAreaRow("area-1")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.ReplaceRooms(ctx, "area-1", []RoomAreaRow{
		{CentralName: "ccu2", RoomName: "Attic", AreaID: "area-1"},
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "area-1"},
		{CentralName: "ccu1", RoomName: "Garage", AreaID: "area-1"},
	}); err != nil {
		t.Fatalf("ReplaceRooms: %v", err)
	}

	got, err := s.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	wantOrder := [][2]string{{"ccu1", "Garage"}, {"ccu1", "Kitchen"}, {"ccu2", "Attic"}}
	for i, want := range wantOrder {
		if got[i].CentralName != want[0] || got[i].RoomName != want[1] {
			t.Errorf("got[%d]=(%q,%q) want (%q,%q)", i, got[i].CentralName, got[i].RoomName, want[0], want[1])
		}
	}
}
