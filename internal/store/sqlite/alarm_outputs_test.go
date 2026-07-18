// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshAlarmOutputStore(t *testing.T) *AlarmOutputStore {
	t.Helper()
	return NewAlarmOutputStore(openTestDB(t, "alarm_outputs.db"))
}

func baseAlarmOutputRow(id, areaID string) AlarmOutputRow {
	return AlarmOutputRow{
		ID:             id,
		AreaID:         areaID,
		Class:          hmenum.AlarmOutputClassAcousticSiren,
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0002ABCD:1",
		Name:           "Outdoor Siren",
		ConfigJSON:     `{"duration_s":180}`,
		CreatedAtMS:    1000,
		UpdatedAtMS:    1000,
	}
}

// TestAlarmOutputStoreUpsertInsertRoundTrip verifies that every field of a
// newly inserted output row survives the Upsert -> Get round trip, including
// the Class enum conversion.
func TestAlarmOutputStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	row := baseAlarmOutputRow("output-1", "area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "output-1")
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

// TestAlarmOutputStoreUpsertNotificationEmptyDataPointFields verifies that
// a notification output (no CCU data-point identity) round-trips with
// empty CentralName/InterfaceID/ChannelAddress.
func TestAlarmOutputStoreUpsertNotificationEmptyDataPointFields(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	row := AlarmOutputRow{
		ID:          "notif-1",
		AreaID:      "area-1",
		Class:       hmenum.AlarmOutputClassNotification,
		Name:        "Push notification",
		ConfigJSON:  `{"target":"webhook"}`,
		CreatedAtMS: 1000,
		UpdatedAtMS: 1000,
	}
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "notif-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.CentralName != "" || got.InterfaceID != "" || got.ChannelAddress != "" {
		t.Errorf("data-point fields not empty: %+v", got)
	}
	if got.Class != hmenum.AlarmOutputClassNotification {
		t.Errorf("Class=%q want %q", got.Class, hmenum.AlarmOutputClassNotification)
	}
}

// TestAlarmOutputStoreGetMissingReturnsFalse verifies that Get on an
// unknown id returns the zero value, false, nil.
func TestAlarmOutputStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AlarmOutputRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAlarmOutputStoreUpsertUpdatePreservesCreatedAt verifies that a second
// Upsert on the same id updates mutable fields (including class and area
// reassignment) but never overwrites created_at_ms.
func TestAlarmOutputStoreUpsertUpdatePreservesCreatedAt(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	row := baseAlarmOutputRow("output-1", "area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := row
	updated.AreaID = "area-2"
	updated.Class = hmenum.AlarmOutputClassAlarmLight
	updated.Name = "Renamed Light"
	updated.CreatedAtMS = 9999 // must be ignored on update
	updated.UpdatedAtMS = 2000
	if err := s.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, ok, err := s.Get(ctx, "output-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.AreaID != "area-2" {
		t.Errorf("AreaID=%q want area-2", got.AreaID)
	}
	if got.Class != hmenum.AlarmOutputClassAlarmLight {
		t.Errorf("Class=%q want %q", got.Class, hmenum.AlarmOutputClassAlarmLight)
	}
	if got.Name != "Renamed Light" {
		t.Errorf("Name=%q want %q", got.Name, "Renamed Light")
	}
	if got.UpdatedAtMS != 2000 {
		t.Errorf("UpdatedAtMS=%d want 2000", got.UpdatedAtMS)
	}
	if got.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS=%d want 1000 (must be preserved from insert)", got.CreatedAtMS)
	}
}

// TestAlarmOutputStoreListByAreaOrdering verifies ListByArea only returns
// outputs of the given area, ordered by name then id.
func TestAlarmOutputStoreListByAreaOrdering(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	rows := []AlarmOutputRow{
		withOutputName(baseAlarmOutputRow("o-c", "area-1"), "Charlie"),
		withOutputName(baseAlarmOutputRow("o-a", "area-1"), "Alpha"),
		withOutputName(baseAlarmOutputRow("o-b", "area-1"), "Bravo"),
		withOutputName(baseAlarmOutputRow("o-other", "area-2"), "Alpha"),
	}
	for _, r := range rows {
		if err := s.Upsert(ctx, r); err != nil {
			t.Fatalf("Upsert %s: %v", r.ID, err)
		}
	}

	got, err := s.ListByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("ListByArea: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	wantOrder := []string{"o-a", "o-b", "o-c"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAlarmOutputStoreGetAllOrdering verifies GetAll orders by area, then
// name, then id, and includes outputs from every area.
func TestAlarmOutputStoreGetAllOrdering(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	rows := []AlarmOutputRow{
		withOutputName(baseAlarmOutputRow("o-2b", "area-2"), "Bravo"),
		withOutputName(baseAlarmOutputRow("o-1b", "area-1"), "Bravo"),
		withOutputName(baseAlarmOutputRow("o-1a", "area-1"), "Alpha"),
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
	wantOrder := []string{"o-1a", "o-1b", "o-2b"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAlarmOutputStoreDelete verifies Delete removes exactly the targeted
// row.
func TestAlarmOutputStoreDelete(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAlarmOutputRow("o-1", "area-1")); err != nil {
		t.Fatalf("Upsert o-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAlarmOutputRow("o-2", "area-1")); err != nil {
		t.Fatalf("Upsert o-2: %v", err)
	}

	if err := s.Delete(ctx, "o-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := s.Get(ctx, "o-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Error("o-1 still exists after Delete")
	}

	_, ok, err = s.Get(ctx, "o-2")
	if err != nil {
		t.Fatalf("Get o-2: %v", err)
	}
	if !ok {
		t.Error("o-2 must survive Delete of o-1")
	}
}

// TestAlarmOutputStoreDeleteByArea verifies DeleteByArea removes only the
// rows of the targeted area and returns the correct row count.
func TestAlarmOutputStoreDeleteByArea(t *testing.T) {
	s := freshAlarmOutputStore(t)
	ctx := context.Background()

	for _, id := range []string{"o-1", "o-2", "o-3"} {
		if err := s.Upsert(ctx, baseAlarmOutputRow(id, "area-1")); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}
	if err := s.Upsert(ctx, baseAlarmOutputRow("o-other", "area-2")); err != nil {
		t.Fatalf("Upsert o-other: %v", err)
	}

	n, err := s.DeleteByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("DeleteByArea: %v", err)
	}
	if n != 3 {
		t.Errorf("DeleteByArea returned %d want 3", n)
	}

	remaining, err := s.ListByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("ListByArea area-1: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("area-1 has %d rows left, want 0", len(remaining))
	}

	other, err := s.ListByArea(ctx, "area-2")
	if err != nil {
		t.Fatalf("ListByArea area-2: %v", err)
	}
	if len(other) != 1 {
		t.Errorf("area-2 has %d rows, want 1 (must survive)", len(other))
	}
}

func withOutputName(row AlarmOutputRow, name string) AlarmOutputRow {
	row.Name = name
	return row
}
