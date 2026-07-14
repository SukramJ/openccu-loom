// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshAlarmSensorStore(t *testing.T) *AlarmSensorStore {
	t.Helper()
	return NewAlarmSensorStore(openTestDB(t, "alarm_sensors.db"))
}

func baseAlarmSensorRow(id, areaID string) AlarmSensorRow {
	return AlarmSensorRow{
		ID:             id,
		AreaID:         areaID,
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABCD:1",
		Parameter:      "STATE",
		SensorType:     hmenum.AlarmSensorTypeDoor,
		Name:           "Front Door",
		ConfigJSON:     `{"modes":["full","perimeter"]}`,
		CreatedAtMS:    1000,
		UpdatedAtMS:    1000,
	}
}

// TestAlarmSensorStoreUpsertInsertRoundTrip verifies that every field of a
// newly inserted sensor row survives the Upsert -> Get round trip, including
// the SensorType enum conversion.
func TestAlarmSensorStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	row := baseAlarmSensorRow("sensor-1", "area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "sensor-1")
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

// TestAlarmSensorStoreGetMissingReturnsFalse verifies that Get on an unknown
// id returns the zero value, false, nil.
func TestAlarmSensorStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AlarmSensorRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAlarmSensorStoreUpsertUpdatePreservesCreatedAt verifies that a second
// Upsert on the same id updates mutable fields (including area
// reassignment and sensor type) but never overwrites created_at_ms.
func TestAlarmSensorStoreUpsertUpdatePreservesCreatedAt(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	row := baseAlarmSensorRow("sensor-1", "area-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := row
	updated.AreaID = "area-2"
	updated.SensorType = hmenum.AlarmSensorTypeWindow
	updated.Name = "Renamed Window"
	updated.CreatedAtMS = 9999 // must be ignored on update
	updated.UpdatedAtMS = 2000
	if err := s.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, ok, err := s.Get(ctx, "sensor-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.AreaID != "area-2" {
		t.Errorf("AreaID=%q want area-2", got.AreaID)
	}
	if got.SensorType != hmenum.AlarmSensorTypeWindow {
		t.Errorf("SensorType=%q want %q", got.SensorType, hmenum.AlarmSensorTypeWindow)
	}
	if got.Name != "Renamed Window" {
		t.Errorf("Name=%q want %q", got.Name, "Renamed Window")
	}
	if got.UpdatedAtMS != 2000 {
		t.Errorf("UpdatedAtMS=%d want 2000", got.UpdatedAtMS)
	}
	if got.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS=%d want 1000 (must be preserved from insert)", got.CreatedAtMS)
	}
}

// TestAlarmSensorStoreListByAreaOrdering verifies ListByArea only returns
// sensors of the given area, ordered by name then id.
func TestAlarmSensorStoreListByAreaOrdering(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	rows := []AlarmSensorRow{
		withSensorName(baseAlarmSensorRow("s-c", "area-1"), "Charlie"),
		withSensorName(baseAlarmSensorRow("s-a", "area-1"), "Alpha"),
		withSensorName(baseAlarmSensorRow("s-b", "area-1"), "Bravo"),
		withSensorName(baseAlarmSensorRow("s-other", "area-2"), "Alpha"),
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
	wantOrder := []string{"s-a", "s-b", "s-c"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAlarmSensorStoreGetAllOrdering verifies GetAll orders by area, then
// name, then id, and includes sensors from every area.
func TestAlarmSensorStoreGetAllOrdering(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	rows := []AlarmSensorRow{
		withSensorName(baseAlarmSensorRow("s-2b", "area-2"), "Bravo"),
		withSensorName(baseAlarmSensorRow("s-1b", "area-1"), "Bravo"),
		withSensorName(baseAlarmSensorRow("s-1a", "area-1"), "Alpha"),
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
	wantOrder := []string{"s-1a", "s-1b", "s-2b"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAlarmSensorStoreDelete verifies Delete removes exactly the targeted
// row.
func TestAlarmSensorStoreDelete(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAlarmSensorRow("s-1", "area-1")); err != nil {
		t.Fatalf("Upsert s-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAlarmSensorRow("s-2", "area-1")); err != nil {
		t.Fatalf("Upsert s-2: %v", err)
	}

	if err := s.Delete(ctx, "s-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := s.Get(ctx, "s-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Error("s-1 still exists after Delete")
	}

	_, ok, err = s.Get(ctx, "s-2")
	if err != nil {
		t.Fatalf("Get s-2: %v", err)
	}
	if !ok {
		t.Error("s-2 must survive Delete of s-1")
	}
}

// TestAlarmSensorStoreDeleteByArea verifies DeleteByArea removes only the
// rows of the targeted area and returns the correct row count.
func TestAlarmSensorStoreDeleteByArea(t *testing.T) {
	s := freshAlarmSensorStore(t)
	ctx := context.Background()

	for _, id := range []string{"s-1", "s-2", "s-3"} {
		if err := s.Upsert(ctx, baseAlarmSensorRow(id, "area-1")); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}
	if err := s.Upsert(ctx, baseAlarmSensorRow("s-other", "area-2")); err != nil {
		t.Fatalf("Upsert s-other: %v", err)
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

func withSensorName(row AlarmSensorRow, name string) AlarmSensorRow {
	row.Name = name
	return row
}
