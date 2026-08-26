// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ---- helpers ----

func testKey(centralName, address string, channel int, kind store.DPKind, dpKey string) store.EndpointKey {
	return store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: address,
		ChannelNo:     channel,
		DPKind:        kind,
		DPKey:         dpKey,
	}
}

// ---- GetEndpoint ----

func TestGetEndpoint_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	_, err := s.GetEndpoint(context.Background(), testKey("c1", "A:1", 1, store.DPKindCustom, "STATE"))
	if !errors.Is(err, store.ErrEndpointNotFound) {
		t.Fatalf("expected ErrEndpointNotFound, got %v", err)
	}
}

func TestGetEndpoint_Found(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c1", "A:1", 1, store.DPKindCustom, "STATE")
	rec := store.EndpointRecord{Key: key, EndpointID: 5, DeviceType: 0x0100}
	if err := s.UpsertEndpoint(ctx, rec); err != nil {
		t.Fatalf("UpsertEndpoint: %v", err)
	}

	got, err := s.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.EndpointID != 5 {
		t.Errorf("EndpointID = %d, want 5", got.EndpointID)
	}
	if got.DeviceType != 0x0100 {
		t.Errorf("DeviceType = 0x%04X, want 0x0100", got.DeviceType)
	}
}

// ---- ListEndpoints ----

func TestListEndpoints_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	got, err := s.ListEndpoints(context.Background(), "")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestListEndpoints_FilterByCentral(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"a", "b", "b"} {
		key := testKey(centralName, "DEV:1", i+2, store.DPKindGeneric, "V")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: uint16(i + 2), DeviceType: 1}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpoint: %v", err)
		}
	}

	got, err := s.ListEndpoints(ctx, "b")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestListEndpoints_NoFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"x", "y"} {
		key := testKey(centralName, "DEV:1", i+2, store.DPKindGeneric, "V")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: uint16(i + 2), DeviceType: 1}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpoint: %v", err)
		}
	}

	got, err := s.ListEndpoints(ctx, "")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// ---- UpsertEndpoint / update path ----

func TestUpsertEndpoint_Update(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c1", "B:2", 1, store.DPKindMeasurement, "TEMP")
	if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: 10, DeviceType: 1}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: 20, DeviceType: 2}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.EndpointID != 20 {
		t.Errorf("EndpointID after update = %d, want 20", got.EndpointID)
	}
}

// ---- RemoveEndpoint ----

func TestRemoveEndpoint_Existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c1", "C:3", 1, store.DPKindCalculated, "SUM")
	if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: 7, DeviceType: 1}); err != nil {
		t.Fatalf("UpsertEndpoint: %v", err)
	}
	if err := s.RemoveEndpoint(ctx, key); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	_, err := s.GetEndpoint(ctx, key)
	if !errors.Is(err, store.ErrEndpointNotFound) {
		t.Errorf("expected ErrEndpointNotFound after remove, got %v", err)
	}
}

func TestRemoveEndpoint_NonExisting_NoError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	key := testKey("c1", "D:4", 1, store.DPKindCombined, "X")
	if err := s.RemoveEndpoint(context.Background(), key); err != nil {
		t.Fatalf("RemoveEndpoint on missing key: %v", err)
	}
}

// ---- AssignEndpointID ----

func TestAssignEndpointID_StartsAt2(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	id, err := s.AssignEndpointID(context.Background())
	if err != nil {
		t.Fatalf("AssignEndpointID: %v", err)
	}
	if id != 2 {
		t.Errorf("first assigned ID = %d, want 2 (0=root, 1=aggregator)", id)
	}
}

func TestAssignEndpointID_Increments(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	// Assign and immediately insert so the next call gets the next ID.
	id1, err := s.AssignEndpointID(ctx)
	if err != nil {
		t.Fatalf("first AssignEndpointID: %v", err)
	}
	key := testKey("c1", "E:5", 1, store.DPKindCustom, "K1")
	if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: id1}); err != nil {
		t.Fatalf("UpsertEndpoint: %v", err)
	}

	id2, err := s.AssignEndpointID(ctx)
	if err != nil {
		t.Fatalf("second AssignEndpointID: %v", err)
	}
	if id2 != id1+1 {
		t.Errorf("id2=%d, want id1+1=%d", id2, id1+1)
	}
}

// ---- UpsertEndpointAssigning ----

func TestUpsertEndpointAssigning_AutoAssign(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c2", "F:6", 1, store.DPKindMeasurement, "HUM")
	id, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, EndpointID: 0, DeviceType: 3})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if id < 2 {
		t.Errorf("assigned ID = %d, want ≥ 2", id)
	}
}

func TestUpsertEndpointAssigning_CallerProvided(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c2", "G:7", 1, store.DPKindCustom, "SW")
	id, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, EndpointID: 42, DeviceType: 4})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if id != 42 {
		t.Errorf("effective ID = %d, want 42", id)
	}
}

// TestAssignEndpointID_SkipsHoles verifies that a hole in the stored numbers
// is not filled: allocation runs off the high-water mark, so IDs 2, 3, 5 in
// the table yield 6 and leave 4 retired. Reissuing 4 would hand a removed
// device's endpoint number — and therefore its cached controller identity —
// to an unrelated device.
func TestAssignEndpointID_SkipsHoles(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	for i, id := range []uint16{2, 3, 5} {
		key := testKey("gap", "H:1", i+1, store.DPKindCustom, "G")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: id}); err != nil {
			t.Fatalf("UpsertEndpoint id=%d: %v", id, err)
		}
	}

	id, err := s.AssignEndpointID(ctx)
	if err != nil {
		t.Fatalf("AssignEndpointID: %v", err)
	}
	if id != 6 {
		t.Errorf("assigned ID = %d, want 6 (the hole at 4 stays retired)", id)
	}
}

// TestUpsertEndpointAssigning_Roundtrip_Verify confirms that UpsertEndpointAssigning
// persists in a way GetEndpoint can retrieve.
func TestUpsertEndpointAssigning_Roundtrip_Verify(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	key := testKey("c3", "J:1", 1, store.DPKindCalculated, "VOLT")
	id, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, EndpointID: 0, DeviceType: 9})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}

	got, err := s.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.EndpointID != id {
		t.Errorf("stored ID = %d, assigned = %d", got.EndpointID, id)
	}
	if got.DeviceType != 9 {
		t.Errorf("DeviceType = %d, want 9", got.DeviceType)
	}
}

// TestEndpoints_AllDPKinds_Roundtrip verifies all five DPKind constants survive
// a DB round-trip via UpsertEndpoint.
func TestEndpoints_AllDPKinds_Roundtrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	kinds := []store.DPKind{
		store.DPKindCustom,
		store.DPKindGeneric,
		store.DPKindCalculated,
		store.DPKindCombined,
		store.DPKindMeasurement,
	}
	for i, kind := range kinds {
		key := store.EndpointKey{
			CentralName:   "dktest",
			DeviceAddress: "A:1",
			ChannelNo:     i + 2,
			DPKind:        kind,
			DPKey:         "K",
		}
		rec := store.EndpointRecord{Key: key, EndpointID: uint16(i + 2), DeviceType: uint16(i)} //nolint:gosec // loop index is small and non-negative; uint16 conversions are safe
		if err := s.UpsertEndpoint(ctx, rec); err != nil {
			t.Fatalf("UpsertEndpoint kind=%s: %v", kind, err)
		}
		got, err := s.GetEndpoint(ctx, key)
		if err != nil {
			t.Fatalf("GetEndpoint kind=%s: %v", kind, err)
		}
		if got.Key.DPKind != kind {
			t.Errorf("DPKind = %s, want %s", got.Key.DPKind, kind)
		}
	}
}

// TestListEndpoints_OrderedByID verifies that ListEndpoints returns rows in
// ascending endpoint_id order.
func TestListEndpoints_OrderedByID(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	// Insert out-of-order IDs.
	for _, id := range []uint16{5, 3, 4} {
		key := testKey("ord", "B:1", int(id), store.DPKindCustom, "V")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: id}); err != nil {
			t.Fatalf("UpsertEndpoint id=%d: %v", id, err)
		}
	}

	got, err := s.ListEndpoints(ctx, "ord")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	// Must be ordered ascending by endpoint_id.
	for i := 1; i < len(got); i++ {
		if got[i].EndpointID < got[i-1].EndpointID {
			t.Errorf("result not sorted: got[%d].EndpointID=%d < got[%d].EndpointID=%d",
				i, got[i].EndpointID, i-1, got[i-1].EndpointID)
		}
	}
}

// TestEndpoints_RemoveIdempotent verifies that RemoveEndpoint for an absent key
// does not error.
func TestEndpoints_RemoveIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	key := testKey("rem", "B:1", 3, store.DPKindGeneric, "X")
	// Remove before insert — must not error.
	if err := s.RemoveEndpoint(ctx, key); err != nil {
		t.Fatalf("RemoveEndpoint missing: %v", err)
	}
}

// TestEndpoints_UpsertAssigningAutoID verifies that UpsertEndpointAssigning
// allocates a fresh ID >= 2 when EndpointID == 0.
func TestEndpoints_UpsertAssigningAutoID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	key := testKey("auto", "C:1", 1, store.DPKindCustom, "K")
	id, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, EndpointID: 0})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if id < 2 {
		t.Errorf("assigned ID=%d, want ≥2 (bridged endpoint range)", id)
	}

	// Second call for a different key must not return the same ID.
	key2 := testKey("auto", "C:1", 2, store.DPKindCustom, "K")
	id2, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key2, EndpointID: 0})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning 2: %v", err)
	}
	if id2 == id {
		t.Errorf("second auto-assign returned same ID=%d", id)
	}
}

// TestEndpoints_UpsertAssigningExplicitID verifies that UpsertEndpointAssigning
// honours an explicit non-zero EndpointID.
func TestEndpoints_UpsertAssigningExplicitID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	key := testKey("expl", "D:1", 4, store.DPKindMeasurement, "TEMP")
	id, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, EndpointID: 42, DeviceType: 0x0302})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning explicit: %v", err)
	}
	if id != 42 {
		t.Errorf("effective ID=%d want 42", id)
	}

	got, err := s.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.DeviceType != 0x0302 {
		t.Errorf("DeviceType=%04x want 0302", got.DeviceType)
	}
}

// TestEndpoints_ListNoFilter verifies that ListEndpoints with empty centralName
// returns all rows across all centrals.
func TestEndpoints_ListNoFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	for i, centralName := range []string{"c1", "c2", "c1"} {
		key := testKey(centralName, "E:1", i+2, store.DPKindCustom, "V")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: uint16(i + 2)}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpoint[%d]: %v", i, err)
		}
	}

	list, err := s.ListEndpoints(ctx, "")
	if err != nil {
		t.Fatalf("ListEndpoints no-filter: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len=%d want 3", len(list))
	}
}
