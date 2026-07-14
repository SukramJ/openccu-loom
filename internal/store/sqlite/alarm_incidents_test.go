// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func freshAlarmIncidentStore(t *testing.T) *AlarmIncidentStore {
	t.Helper()
	return NewAlarmIncidentStore(openTestDB(t, "alarm_incidents.db"))
}

func baseAlarmIncident(areaID string, startedAtMS int64) AlarmIncident {
	return AlarmIncident{
		AreaID:            areaID,
		Mode:              hmenum.AlarmModeFull,
		CauseJSON:         `{"sensor_id":"sensor-1"}`,
		StartedAtMS:       startedAtMS,
		TriggerDeadlineMS: startedAtMS + 60000,
		Silenced:          false,
		SilencedAtMS:      0,
		SilencedBy:        "",
		RetriggerCycles:   0,
		AcousticMS:        0,
		RestoreRefires:    0,
		ClosedAtMS:        0,
		CloseReason:       "",
	}
}

// TestAlarmIncidentStoreCreateGetRoundTrip verifies that every field of a
// newly created incident survives the Create -> Get round trip.
func TestAlarmIncidentStoreCreateGetRoundTrip(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	inc := baseAlarmIncident("area-1", 1000)
	id, err := s.Create(ctx, inc)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id=%d want >0", id)
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	inc.ID = id
	if got != inc {
		t.Errorf("Get=%+v want %+v", got, inc)
	}
}

// TestAlarmIncidentStoreGetMissingReturnsFalse verifies that Get on an
// unknown id returns the zero value, false, nil.
func TestAlarmIncidentStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, 999999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing id")
	}
	if got != (AlarmIncident{}) {
		t.Errorf("Get on miss returned non-zero incident: %+v", got)
	}
}

// TestAlarmIncidentStoreGetOpenByAreaNoneOpen verifies GetOpenByArea
// returns false when the area has no incidents at all.
func TestAlarmIncidentStoreGetOpenByAreaNoneOpen(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	_, ok, err := s.GetOpenByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("GetOpenByArea: %v", err)
	}
	if ok {
		t.Fatal("GetOpenByArea: want ok=false for area with no incidents")
	}
}

// TestAlarmIncidentStoreGetOpenByAreaOneOpen verifies GetOpenByArea finds
// the single open incident of an area.
func TestAlarmIncidentStoreGetOpenByAreaOneOpen(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := s.GetOpenByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("GetOpenByArea: %v", err)
	}
	if !ok {
		t.Fatal("GetOpenByArea: want ok=true")
	}
	if got.ID != id {
		t.Errorf("got.ID=%d want %d", got.ID, id)
	}
}

// TestAlarmIncidentStoreGetOpenByAreaClosedOnlyReturnsFalse verifies that
// GetOpenByArea does not return a closed incident.
func TestAlarmIncidentStoreGetOpenByAreaClosedOnlyReturnsFalse(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Close(ctx, id, 2000, "restored"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, ok, err := s.GetOpenByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("GetOpenByArea: %v", err)
	}
	if ok {
		t.Fatal("GetOpenByArea: want ok=false when the only incident is closed")
	}
}

// TestAlarmIncidentStoreGetOpenByAreaTwoOpenNewestWins verifies that if
// historical corruption ever leaves two open incidents for one area, the
// newest (highest id) one wins.
func TestAlarmIncidentStoreGetOpenByAreaTwoOpenNewestWins(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	id2, err := s.Create(ctx, baseAlarmIncident("area-1", 2000))
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	got, ok, err := s.GetOpenByArea(ctx, "area-1")
	if err != nil {
		t.Fatalf("GetOpenByArea: %v", err)
	}
	if !ok {
		t.Fatal("GetOpenByArea: want ok=true")
	}
	if got.ID != id2 {
		t.Errorf("got.ID=%d want newest id=%d", got.ID, id2)
	}
}

// TestAlarmIncidentStoreListByArea verifies ListByArea returns incidents of
// the given area newest-first, and that limit caps the result.
func TestAlarmIncidentStoreListByArea(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	ids := make([]int64, 0, 3)
	for i := range 3 {
		id, err := s.Create(ctx, baseAlarmIncident("area-1", int64(1000+i)))
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if _, err := s.Create(ctx, baseAlarmIncident("area-2", 5000)); err != nil {
		t.Fatalf("Create area-2: %v", err)
	}

	got, err := s.ListByArea(ctx, "area-1", 0)
	if err != nil {
		t.Fatalf("ListByArea: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	for i, e := range got {
		want := ids[len(ids)-1-i]
		if e.ID != want {
			t.Errorf("got[%d].ID=%d want %d (newest-first)", i, e.ID, want)
		}
	}

	limited, err := s.ListByArea(ctx, "area-1", 2)
	if err != nil {
		t.Fatalf("ListByArea limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len=%d want 2", len(limited))
	}
}

// TestAlarmIncidentStoreMarkSilencedFirstCallWins verifies that MarkSilenced
// sets the flag, timestamp, and actor, and that a second call with a
// different timestamp/actor does not overwrite the first — the CASE WHEN
// guard in the SQL protects the original silence attribution.
func TestAlarmIncidentStoreMarkSilencedFirstCallWins(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.MarkSilenced(ctx, id, 1500, "operator-a"); err != nil {
		t.Fatalf("MarkSilenced 1: %v", err)
	}
	if err := s.MarkSilenced(ctx, id, 9999, "operator-b"); err != nil {
		t.Fatalf("MarkSilenced 2: %v", err)
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if !got.Silenced {
		t.Error("Silenced=false want true")
	}
	if got.SilencedAtMS != 1500 {
		t.Errorf("SilencedAtMS=%d want 1500 (first call wins)", got.SilencedAtMS)
	}
	if got.SilencedBy != "operator-a" {
		t.Errorf("SilencedBy=%q want operator-a (first call wins)", got.SilencedBy)
	}
}

// TestAlarmIncidentStoreAddAcousticMSAccumulates verifies that repeated
// AddAcousticMS calls sum into the cumulative ledger.
func TestAlarmIncidentStoreAddAcousticMSAccumulates(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, delta := range []int64{500, 1500, 2000} {
		if err := s.AddAcousticMS(ctx, id, delta); err != nil {
			t.Fatalf("AddAcousticMS(%d): %v", delta, err)
		}
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.AcousticMS != 4000 {
		t.Errorf("AcousticMS=%d want 4000", got.AcousticMS)
	}
}

// TestAlarmIncidentStoreIncrementRetriggerCycles verifies the counter
// increments by one per call.
func TestAlarmIncidentStoreIncrementRetriggerCycles(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for range 3 {
		if err := s.IncrementRetriggerCycles(ctx, id); err != nil {
			t.Fatalf("IncrementRetriggerCycles: %v", err)
		}
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.RetriggerCycles != 3 {
		t.Errorf("RetriggerCycles=%d want 3", got.RetriggerCycles)
	}
}

// TestAlarmIncidentStoreIncrementRestoreRefires verifies the counter
// increments by one per call.
func TestAlarmIncidentStoreIncrementRestoreRefires(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for range 2 {
		if err := s.IncrementRestoreRefires(ctx, id); err != nil {
			t.Fatalf("IncrementRestoreRefires: %v", err)
		}
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.RestoreRefires != 2 {
		t.Errorf("RestoreRefires=%d want 2", got.RestoreRefires)
	}
}

// TestAlarmIncidentStoreSetTriggerDeadline verifies the deadline is
// overwritten by each call (a new re-trigger cycle extends it).
func TestAlarmIncidentStoreSetTriggerDeadline(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SetTriggerDeadline(ctx, id, 5000); err != nil {
		t.Fatalf("SetTriggerDeadline: %v", err)
	}
	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.TriggerDeadlineMS != 5000 {
		t.Errorf("TriggerDeadlineMS=%d want 5000", got.TriggerDeadlineMS)
	}

	if err := s.SetTriggerDeadline(ctx, id, 8000); err != nil {
		t.Fatalf("SetTriggerDeadline 2: %v", err)
	}
	got, _, err = s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if got.TriggerDeadlineMS != 8000 {
		t.Errorf("TriggerDeadlineMS=%d want 8000 (must be overwritten)", got.TriggerDeadlineMS)
	}
}

// TestAlarmIncidentStoreCloseFirstCallWins verifies Close sets
// closed_at_ms/close_reason, and that a second Close call with a different
// reason is a no-op — the first close wins.
func TestAlarmIncidentStoreCloseFirstCallWins(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	id, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Close(ctx, id, 5000, "restored"); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	if err := s.Close(ctx, id, 9999, "manual"); err != nil {
		t.Fatalf("Close 2: %v", err)
	}

	got, ok, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.ClosedAtMS != 5000 {
		t.Errorf("ClosedAtMS=%d want 5000 (first close wins)", got.ClosedAtMS)
	}
	if got.CloseReason != "restored" {
		t.Errorf("CloseReason=%q want restored (first close wins)", got.CloseReason)
	}
}

// TestAlarmIncidentStorePurgeClosedBefore verifies PurgeClosedBefore
// deletes only closed incidents whose closed_at_ms is before cutoffMS,
// never touching open incidents, and returns the deleted count.
func TestAlarmIncidentStorePurgeClosedBefore(t *testing.T) {
	s := freshAlarmIncidentStore(t)
	ctx := context.Background()

	closedOld, err := s.Create(ctx, baseAlarmIncident("area-1", 1000))
	if err != nil {
		t.Fatalf("Create closedOld: %v", err)
	}
	if err := s.Close(ctx, closedOld, 1500, "restored"); err != nil {
		t.Fatalf("Close closedOld: %v", err)
	}

	closedRecent, err := s.Create(ctx, baseAlarmIncident("area-1", 2000))
	if err != nil {
		t.Fatalf("Create closedRecent: %v", err)
	}
	if err := s.Close(ctx, closedRecent, 5000, "restored"); err != nil {
		t.Fatalf("Close closedRecent: %v", err)
	}

	stillOpen, err := s.Create(ctx, baseAlarmIncident("area-2", 3000))
	if err != nil {
		t.Fatalf("Create stillOpen: %v", err)
	}

	n, err := s.PurgeClosedBefore(ctx, 3000)
	if err != nil {
		t.Fatalf("PurgeClosedBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeClosedBefore returned %d want 1", n)
	}

	_, ok, err := s.Get(ctx, closedOld)
	if err != nil {
		t.Fatalf("Get closedOld: %v", err)
	}
	if ok {
		t.Error("closedOld must be purged")
	}

	_, ok, err = s.Get(ctx, closedRecent)
	if err != nil {
		t.Fatalf("Get closedRecent: %v", err)
	}
	if !ok {
		t.Error("closedRecent must survive (closed_at_ms=5000 >= cutoff=3000)")
	}

	_, ok, err = s.Get(ctx, stillOpen)
	if err != nil {
		t.Fatalf("Get stillOpen: %v", err)
	}
	if !ok {
		t.Error("stillOpen must never be purged regardless of cutoff")
	}
}
