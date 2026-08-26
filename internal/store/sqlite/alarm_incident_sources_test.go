// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

func freshAlarmIncidentSourceStore(t *testing.T) (*AlarmIncidentSourceStore, *AlarmIncidentStore) {
	t.Helper()
	db := openTestDB(t, "alarm_incident_sources.db")
	return NewAlarmIncidentSourceStore(db), NewAlarmIncidentStore(db)
}

func baseAlarmIncidentSource(incidentID int64, ref string, atMS int64) AlarmIncidentSource {
	return AlarmIncidentSource{
		IncidentID:     incidentID,
		ZoneID:         "zone-1",
		Ref:            ref,
		CentralName:    "ccu-main",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABC0123456:1",
		DeviceAddress:  "ABC0123456",
		Parameter:      "STATE",
		SensorID:       "sensor-1",
		Name:           "Fenster Küche",
		SensorType:     "window",
		Class:          "intrusion",
		Cause:          "sensor",
		AtMS:           atMS,
	}
}

// TestAlarmIncidentSourceStoreAppendListByIncidentRoundTrip verifies that
// every field of an appended source survives the Append -> ListByIncident
// round trip.
func TestAlarmIncidentSourceStoreAppendListByIncidentRoundTrip(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	row := baseAlarmIncidentSource(1, "ccu-main|HmIP-RF|ABC0123456:1|STATE", 1000)
	if err := s.Append(ctx, row); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.ListByIncident(ctx, 1)
	if err != nil {
		t.Fatalf("ListByIncident: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	row.ID = got[0].ID
	if got[0] != row {
		t.Errorf("ListByIncident[0]=%+v want %+v", got[0], row)
	}
}

// TestAlarmIncidentSourceStoreAppendIsIdempotentByRef verifies that
// appending the same (incident_id, ref) twice yields exactly one row, and
// that the FIRST at_ms is kept — a re-activating detector must not
// inflate the list or drift its timestamp forward.
func TestAlarmIncidentSourceStoreAppendIsIdempotentByRef(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	ref := "ccu-main|HmIP-RF|ABC0123456:1|STATE"
	first := baseAlarmIncidentSource(1, ref, 1000)
	if err := s.Append(ctx, first); err != nil {
		t.Fatalf("Append 1: %v", err)
	}

	second := baseAlarmIncidentSource(1, ref, 9999)
	second.Name = "renamed after first observation"
	if err := s.Append(ctx, second); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	got, err := s.ListByIncident(ctx, 1)
	if err != nil {
		t.Fatalf("ListByIncident: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (re-activation must not inflate the list)", len(got))
	}
	if got[0].AtMS != 1000 {
		t.Errorf("AtMS=%d want 1000 (first observation time must be kept)", got[0].AtMS)
	}
	if got[0].Name != "Fenster Küche" {
		t.Errorf("Name=%q want unchanged from the first append", got[0].Name)
	}
}

// TestAlarmIncidentSourceStoreDifferentRefsOrderedByAtMS verifies that two
// distinct refs under the same incident produce two rows, returned oldest
// first.
func TestAlarmIncidentSourceStoreDifferentRefsOrderedByAtMS(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	later := baseAlarmIncidentSource(1, "ccu-main|HmIP-RF|ABC0123456:1|STATE", 2000)
	earlier := baseAlarmIncidentSource(1, "ccu-main|HmIP-RF|DEF0654321:1|STATE", 1000)
	// Append out of chronological order to prove the ordering comes
	// from at_ms, not insertion order.
	if err := s.Append(ctx, later); err != nil {
		t.Fatalf("Append later: %v", err)
	}
	if err := s.Append(ctx, earlier); err != nil {
		t.Fatalf("Append earlier: %v", err)
	}

	got, err := s.ListByIncident(ctx, 1)
	if err != nil {
		t.Fatalf("ListByIncident: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Ref != earlier.Ref || got[1].Ref != later.Ref {
		t.Errorf("order = [%q, %q], want earlier before later", got[0].Ref, got[1].Ref)
	}
}

// TestAlarmIncidentSourceStoreListByIncidentsBatches verifies ListByIncidents
// keys its result by incident ID across several incidents in one call.
func TestAlarmIncidentSourceStoreListByIncidentsBatches(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, baseAlarmIncidentSource(1, "ref-a", 1000)); err != nil {
		t.Fatalf("Append incident 1: %v", err)
	}
	if err := s.Append(ctx, baseAlarmIncidentSource(2, "ref-b", 2000)); err != nil {
		t.Fatalf("Append incident 2: %v", err)
	}
	if err := s.Append(ctx, baseAlarmIncidentSource(2, "ref-c", 3000)); err != nil {
		t.Fatalf("Append incident 2 second row: %v", err)
	}

	got, err := s.ListByIncidents(ctx, []int64{1, 2})
	if err != nil {
		t.Fatalf("ListByIncidents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(map)=%d want 2", len(got))
	}
	if len(got[1]) != 1 || got[1][0].Ref != "ref-a" {
		t.Errorf("got[1]=%+v want one row with Ref=ref-a", got[1])
	}
	if len(got[2]) != 2 {
		t.Fatalf("got[2] len=%d want 2", len(got[2]))
	}
	if got[2][0].Ref != "ref-b" || got[2][1].Ref != "ref-c" {
		t.Errorf("got[2] order = [%q, %q], want [ref-b, ref-c]", got[2][0].Ref, got[2][1].Ref)
	}
}

// TestAlarmIncidentSourceStoreListByIncidentsEmptyInput verifies an empty
// incident ID slice returns an empty map without querying the database.
func TestAlarmIncidentSourceStoreListByIncidentsEmptyInput(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	got, err := s.ListByIncidents(ctx, nil)
	if err != nil {
		t.Fatalf("ListByIncidents: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d want 0", len(got))
	}
}

// TestAlarmIncidentSourceStoreListByIncidentsUnknownIDs verifies that
// incident IDs with no rows yield no map entries (not zero-value entries).
func TestAlarmIncidentSourceStoreListByIncidentsUnknownIDs(t *testing.T) {
	s, _ := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	if err := s.Append(ctx, baseAlarmIncidentSource(1, "ref-a", 1000)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.ListByIncidents(ctx, []int64{1, 999999})
	if err != nil {
		t.Fatalf("ListByIncidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(map)=%d want 1 (unknown id must not appear)", len(got))
	}
	if _, ok := got[999999]; ok {
		t.Error("got[999999] present, want no entry for an unknown incident")
	}
}

// TestAlarmIncidentSourceStorePurgeOrphansDeletesOnlyOrphans verifies
// PurgeOrphans deletes source rows whose incident no longer exists and
// leaves rows whose incident still exists untouched.
func TestAlarmIncidentSourceStorePurgeOrphansDeletesOnlyOrphans(t *testing.T) {
	sources, incidents := freshAlarmIncidentSourceStore(t)
	ctx := context.Background()

	survivingID, err := incidents.Create(ctx, baseAlarmIncident("zone-1", 1000))
	if err != nil {
		t.Fatalf("Create surviving incident: %v", err)
	}
	deletedID, err := incidents.Create(ctx, baseAlarmIncident("zone-1", 2000))
	if err != nil {
		t.Fatalf("Create doomed incident: %v", err)
	}

	if err := sources.Append(ctx, baseAlarmIncidentSource(survivingID, "ref-a", 1000)); err != nil {
		t.Fatalf("Append surviving source: %v", err)
	}
	if err := sources.Append(ctx, baseAlarmIncidentSource(deletedID, "ref-b", 2000)); err != nil {
		t.Fatalf("Append orphan-to-be source: %v", err)
	}

	if err := incidents.Close(ctx, deletedID, 3000, "restored"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := incidents.PurgeClosedBefore(ctx, 4000); err != nil {
		t.Fatalf("PurgeClosedBefore: %v", err)
	}
	if _, ok, err := incidents.Get(ctx, deletedID); err != nil {
		t.Fatalf("Get deletedID: %v", err)
	} else if ok {
		t.Fatal("setup invariant broken: deletedID incident must be gone before purging sources")
	}

	n, err := sources.PurgeOrphans(ctx)
	if err != nil {
		t.Fatalf("PurgeOrphans: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeOrphans returned %d want 1", n)
	}

	survivingRows, err := sources.ListByIncident(ctx, survivingID)
	if err != nil {
		t.Fatalf("ListByIncident surviving: %v", err)
	}
	if len(survivingRows) != 1 {
		t.Errorf("surviving incident sources len=%d want 1 (must not be purged)", len(survivingRows))
	}

	orphanRows, err := sources.ListByIncident(ctx, deletedID)
	if err != nil {
		t.Fatalf("ListByIncident orphan: %v", err)
	}
	if len(orphanRows) != 0 {
		t.Errorf("orphan incident sources len=%d want 0 (must be purged)", len(orphanRows))
	}
}
