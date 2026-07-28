// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshAlarmStateStore(t *testing.T) *AlarmStateStore {
	t.Helper()
	return NewAlarmStateStore(openTestDB(t, "alarm_state.db"))
}

func baseAlarmStateRow(zoneID string) AlarmStateRow {
	return AlarmStateRow{
		ZoneID:      zoneID,
		State:       hmenum.AlarmZoneStateDisarmed,
		Mode:        hmenum.AlarmModeDisarmed,
		BypassJSON:  `["sensor-1"]`,
		IncidentID:  0,
		TimersJSON:  `[]`,
		ContextJSON: `{"open_sensors":[]}`,
		UpdatedAtMS: 1000,
	}
}

// TestAlarmStateStoreUpsertInsertRoundTrip verifies that Upsert on a new
// zone inserts a row and every field, including ContextJSON, survives the
// Upsert -> Get round trip.
func TestAlarmStateStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAlarmStateStore(t)
	ctx := context.Background()

	row := baseAlarmStateRow("zone-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "zone-1")
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

// TestAlarmStateStoreUpsertUpdateRoundTrip verifies that a second Upsert on
// the same zone overwrites every field, including ContextJSON — unlike the
// relational tables, alarm_state has no created_at_ms to preserve.
func TestAlarmStateStoreUpsertUpdateRoundTrip(t *testing.T) {
	s := freshAlarmStateStore(t)
	ctx := context.Background()

	row := baseAlarmStateRow("zone-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := AlarmStateRow{
		ZoneID:      "zone-1",
		State:       hmenum.AlarmZoneStateArmed,
		Mode:        hmenum.AlarmModeFull,
		BypassJSON:  `[]`,
		IncidentID:  7,
		TimersJSON:  `[{"kind":"exit","deadline_ms":5000}]`,
		ContextJSON: `{"open_sensors":["sensor-2"],"pending_cause":"sensor-2"}`,
		UpdatedAtMS: 2000,
	}
	if err := s.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, ok, err := s.Get(ctx, "zone-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got != updated {
		t.Errorf("Get=%+v want %+v", got, updated)
	}
}

// TestAlarmStateStoreGetMissingReturnsFalse verifies that Get on an unknown
// zone returns the zero value, false, nil.
func TestAlarmStateStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmStateStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AlarmStateRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAlarmStateStoreGetAllOrderedByZoneID verifies GetAll returns every
// zone state ordered by zone_id.
func TestAlarmStateStoreGetAllOrderedByZoneID(t *testing.T) {
	s := freshAlarmStateStore(t)
	ctx := context.Background()

	for _, id := range []string{"zone-c", "zone-a", "zone-b"} {
		if err := s.Upsert(ctx, baseAlarmStateRow(id)); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}

	got, err := s.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	wantOrder := []string{"zone-a", "zone-b", "zone-c"}
	for i, id := range wantOrder {
		if got[i].ZoneID != id {
			t.Errorf("got[%d].ZoneID=%q want %q", i, got[i].ZoneID, id)
		}
	}
}

// TestAlarmStateStoreDelete verifies Delete removes exactly the targeted
// zone's state row.
func TestAlarmStateStoreDelete(t *testing.T) {
	s := freshAlarmStateStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseAlarmStateRow("zone-1")); err != nil {
		t.Fatalf("Upsert zone-1: %v", err)
	}
	if err := s.Upsert(ctx, baseAlarmStateRow("zone-2")); err != nil {
		t.Fatalf("Upsert zone-2: %v", err)
	}

	if err := s.Delete(ctx, "zone-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := s.Get(ctx, "zone-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Error("zone-1 state still exists after Delete")
	}

	_, ok, err = s.Get(ctx, "zone-2")
	if err != nil {
		t.Fatalf("Get zone-2: %v", err)
	}
	if !ok {
		t.Error("zone-2 state must survive Delete of zone-1")
	}
}
