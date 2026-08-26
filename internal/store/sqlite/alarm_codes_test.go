// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

func freshAlarmCodeStore(t *testing.T) *AlarmCodeStore {
	t.Helper()
	return NewAlarmCodeStore(openTestDB(t, "alarm_codes.db"))
}

func basePINCodeRow(id string) AlarmCodeRow {
	return AlarmCodeRow{
		ID:           id,
		Name:         "Markus",
		Kind:         "pin",
		Hash:         "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0MTY$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNo",
		Duress:       false,
		PermsJSON:    `{"arm":true,"disarm":true,"silence":false}`,
		ZonesJSON:    `[]`,
		BindingJSON:  `{}`,
		ValidFromMS:  0,
		ValidUntilMS: 0,
		Enabled:      true,
		CreatedAtMS:  1000,
		UpdatedAtMS:  1000,
	}
}

// TestAlarmCodeStoreUpsertInsertRoundTrip verifies that every field of a
// newly inserted PIN code row survives the Upsert -> Get round trip,
// including the bool -> INTEGER conversions.
func TestAlarmCodeStoreUpsertInsertRoundTrip(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	row := basePINCodeRow("code-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "code-1")
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

// TestAlarmCodeStoreUpsertHardwareBindingEmptyHash verifies that a
// keypad_slot / remote_key row (no PIN secret) round-trips with an
// empty Hash.
func TestAlarmCodeStoreUpsertHardwareBindingEmptyHash(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	row := AlarmCodeRow{
		ID:          "keypad-1",
		Name:        "Front Door Keypad Slot 1",
		Kind:        "keypad_slot",
		PermsJSON:   `{"arm":true,"disarm":true,"silence":false}`,
		ZonesJSON:   `["zone-1"]`,
		BindingJSON: `{"central":"ccu1","device_address":"0001ABCD","slot":1,"arm_mode":"full","zone_id":"zone-1"}`,
		Enabled:     true,
		CreatedAtMS: 1000,
		UpdatedAtMS: 1000,
	}
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Get(ctx, "keypad-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.Hash != "" {
		t.Errorf("Hash=%q want empty for a hardware-binding kind", got.Hash)
	}
	if got.Kind != "keypad_slot" {
		t.Errorf("Kind=%q want keypad_slot", got.Kind)
	}
	if got != row {
		t.Errorf("Get=%+v want %+v", got, row)
	}
}

// TestAlarmCodeStoreGetMissingReturnsFalse verifies that Get on an
// unknown id returns the zero value, false, nil.
func TestAlarmCodeStoreGetMissingReturnsFalse(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	got, ok, err := s.Get(ctx, "ghost")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get: want ok=false for missing row")
	}
	if got != (AlarmCodeRow{}) {
		t.Errorf("Get on miss returned non-zero row: %+v", got)
	}
}

// TestAlarmCodeStoreUpsertUpdatePreservesCreatedAt verifies that a
// second Upsert on the same id updates mutable fields but never
// overwrites created_at_ms.
func TestAlarmCodeStoreUpsertUpdatePreservesCreatedAt(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	row := basePINCodeRow("code-1")
	if err := s.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	updated := row
	updated.Name = "Renamed"
	updated.Duress = true
	updated.Enabled = false
	updated.CreatedAtMS = 9999 // must be ignored on update
	updated.UpdatedAtMS = 2000
	if err := s.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, ok, err := s.Get(ctx, "code-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want ok=true")
	}
	if got.Name != "Renamed" {
		t.Errorf("Name=%q want Renamed", got.Name)
	}
	if !got.Duress {
		t.Error("Duress=false want true")
	}
	if got.Enabled {
		t.Error("Enabled=true want false")
	}
	if got.UpdatedAtMS != 2000 {
		t.Errorf("UpdatedAtMS=%d want 2000", got.UpdatedAtMS)
	}
	if got.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS=%d want 1000 (must be preserved from insert)", got.CreatedAtMS)
	}
}

// TestAlarmCodeStoreGetAllOrdering verifies GetAll orders by name then
// id and returns every row.
func TestAlarmCodeStoreGetAllOrdering(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	rows := []AlarmCodeRow{
		withCodeName(basePINCodeRow("c-b"), "Bravo"),
		withCodeName(basePINCodeRow("c-a"), "Alpha"),
		withCodeName(basePINCodeRow("c-c"), "Charlie"),
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
	wantOrder := []string{"c-a", "c-b", "c-c"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

// TestAlarmCodeStoreDelete verifies Delete removes exactly the
// targeted row.
func TestAlarmCodeStoreDelete(t *testing.T) {
	s := freshAlarmCodeStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, basePINCodeRow("c-1")); err != nil {
		t.Fatalf("Upsert c-1: %v", err)
	}
	if err := s.Upsert(ctx, basePINCodeRow("c-2")); err != nil {
		t.Fatalf("Upsert c-2: %v", err)
	}

	if err := s.Delete(ctx, "c-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, err := s.Get(ctx, "c-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Error("c-1 still exists after Delete")
	}

	_, ok, err = s.Get(ctx, "c-2")
	if err != nil {
		t.Fatalf("Get c-2: %v", err)
	}
	if !ok {
		t.Error("c-2 must survive Delete of c-1")
	}
}

func withCodeName(row AlarmCodeRow, name string) AlarmCodeRow {
	row.Name = name
	return row
}
