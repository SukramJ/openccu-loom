// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

func freshAlarmAreaStore(t *testing.T) *AlarmAreaStore {
	t.Helper()
	return NewAlarmAreaStore(openTestDB(t, "alarm_areas.db"))
}

func baseAlarmAreaRow(id string) AlarmAreaRow {
	return AlarmAreaRow{
		ID:          id,
		Name:        "Ground Floor",
		Position:    1,
		ConfigJSON:  `{"full":{"exit_delay_s":30}}`,
		CreatedAtMS: 1000,
		UpdatedAtMS: 1000,
	}
}

// TestAlarmAreaStoreUpsertInsertRoundTrip verifies that Upsert on a new id
// inserts a row and every field survives the Upsert -> Get round trip.
func TestAlarmAreaStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAlarmAreaStore(t)
	ctx := context.Background()

	row := baseAlarmAreaRow("area-1")
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

// TestAlarmAreaStoreGetMissingReturnsFalse verifies that Get on an unknown
// id returns the zero value, false, nil (no error).
func TestAlarmAreaStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmAreaStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AlarmAreaRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAlarmAreaStoreUpsertUpdatePreservesCreatedAt verifies that a second
// Upsert call on the same id updates mutable fields but never overwrites
// created_at_ms.
func TestAlarmAreaStoreUpsertUpdatePreservesCreatedAt(t *testing.T) {
	s := freshAlarmAreaStore(t)
	ctx := context.Background()

	row := baseAlarmAreaRow("area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := row
	updated.Name = "Renamed"
	updated.Position = 5
	updated.ConfigJSON = `{"full":{"exit_delay_s":60}}`
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
	if got.ConfigJSON != updated.ConfigJSON {
		t.Errorf("ConfigJSON=%q want %q", got.ConfigJSON, updated.ConfigJSON)
	}
	if got.UpdatedAtMS != 2000 {
		t.Errorf("UpdatedAtMS=%d want 2000", got.UpdatedAtMS)
	}
	if got.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS=%d want 1000 (must be preserved from insert)", got.CreatedAtMS)
	}
}

// TestAlarmAreaStoreGetAllOrdering verifies GetAll orders by position then
// name.
func TestAlarmAreaStoreGetAllOrdering(t *testing.T) {
	s := freshAlarmAreaStore(t)
	ctx := context.Background()

	rows := []AlarmAreaRow{
		{ID: "c", Name: "Charlie", Position: 2, ConfigJSON: "{}", CreatedAtMS: 1, UpdatedAtMS: 1},
		{ID: "a", Name: "Alpha", Position: 1, ConfigJSON: "{}", CreatedAtMS: 1, UpdatedAtMS: 1},
		{ID: "b", Name: "Bravo", Position: 1, ConfigJSON: "{}", CreatedAtMS: 1, UpdatedAtMS: 1},
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

// TestAlarmAreaStoreDelete verifies Delete removes exactly the targeted row.
func TestAlarmAreaStoreDelete(t *testing.T) {
	s := freshAlarmAreaStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAlarmAreaRow("area-1")); err != nil {
		t.Fatalf("Upsert area-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAlarmAreaRow("area-2")); err != nil {
		t.Fatalf("Upsert area-2: %v", err)
	}

	if err := s.Delete(ctx, "area-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := s.Get(ctx, "area-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Error("area-1 still exists after Delete")
	}

	_, ok, err = s.Get(ctx, "area-2")
	if err != nil {
		t.Fatalf("Get area-2: %v", err)
	}
	if !ok {
		t.Error("area-2 must survive Delete of area-1")
	}
}
