// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"errors"
	"testing"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// ---- helpers ----

func testKey(centralName, address string, channel int, kind matterendpoint.DPKind, dpKey string) matterendpoint.SourceKey {
	return matterendpoint.SourceKey{
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
	s := matterendpoint.New(db)
	_, err := s.GetEndpoint(context.Background(), testKey("c1", "A:1", 1, matterendpoint.DPKindCustom, "STATE"))
	if !errors.Is(err, fabricendpoint.ErrNotFound) {
		t.Fatalf("expected ErrEndpointNotFound, got %v", err)
	}
}

func TestGetEndpoint_Found(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "A:1", 1, matterendpoint.DPKindCustom, "STATE")
	rec := fabricendpoint.Record{Key: key, EndpointID: 5, DeviceType: 0x0100}
	if _, err := s.UpsertEndpointAssigning(ctx, rec); err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
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
	s := matterendpoint.New(db)
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
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"a", "b", "b"} {
		key := testKey(centralName, "DEV:1", i+2, matterendpoint.DPKindGeneric, "V")
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: uint16(i + 2), DeviceType: 1}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpointAssigning: %v", err)
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
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"x", "y"} {
		key := testKey(centralName, "DEV:1", i+2, matterendpoint.DPKindGeneric, "V")
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: uint16(i + 2), DeviceType: 1}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpointAssigning: %v", err)
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

// ---- UpsertEndpointAssigning / update path ----

func TestUpsertEndpointAssigning_Update(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "B:2", 1, matterendpoint.DPKindMeasurement, "TEMP")
	if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 10, DeviceType: 1}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 10, DeviceType: 2})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if id != 10 {
		t.Errorf("effective ID after update = %d, want 10", id)
	}
	got, err := s.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.EndpointID != 10 {
		t.Errorf("EndpointID after update = %d, want 10", got.EndpointID)
	}
	if got.DeviceType != 2 {
		t.Errorf("DeviceType after update = %d, want 2", got.DeviceType)
	}
}

// TestUpsertRefusesKeyThatDoesNotDecode pins that a key whose rendering
// would not split back into itself is refused and leaves no row: the store
// persists only the rendering, so such a row could never be listed back.
func TestUpsertRefusesKeyThatDoesNotDecode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := testKey("c1", "AB|1", 1, matterendpoint.DPKindCustom, "STATE")
	_, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key})
	if !errors.Is(err, matterendpoint.ErrKeyNotDecodable) {
		t.Fatalf("err = %v, want ErrKeyNotDecodable", err)
	}
	list, err := s.ListEndpoints(ctx, "")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("refused key left %d row(s), want 0", len(list))
	}
}

// ---- RemoveEndpoint ----

func TestRemoveEndpoint_Existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "C:3", 1, matterendpoint.DPKindCalculated, "SUM")
	if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 7, DeviceType: 1}); err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if err := s.RemoveEndpoint(ctx, key); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	_, err := s.GetEndpoint(ctx, key)
	if !errors.Is(err, fabricendpoint.ErrNotFound) {
		t.Errorf("expected ErrEndpointNotFound after remove, got %v", err)
	}
}

func TestRemoveEndpoint_NonExisting_NoError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	key := testKey("c1", "D:4", 1, matterendpoint.DPKindCombined, "X")
	if err := s.RemoveEndpoint(context.Background(), key); err != nil {
		t.Fatalf("RemoveEndpoint on missing key: %v", err)
	}
}

// ---- UpsertEndpointAssigning ----

func TestUpsertEndpointAssigning_AutoAssign(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c2", "F:6", 1, matterendpoint.DPKindMeasurement, "HUM")
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 0, DeviceType: 3})
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
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c2", "G:7", 1, matterendpoint.DPKindCustom, "SW")
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 42, DeviceType: 4})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if id != 42 {
		t.Errorf("effective ID = %d, want 42", id)
	}
}

// TestUpsertEndpointAssigning_Roundtrip_Verify confirms that UpsertEndpointAssigning
// persists in a way GetEndpoint can retrieve.
func TestUpsertEndpointAssigning_Roundtrip_Verify(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c3", "J:1", 1, matterendpoint.DPKindCalculated, "VOLT")
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 0, DeviceType: 9})
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
// a DB round-trip via UpsertEndpointAssigning.
func TestEndpoints_AllDPKinds_Roundtrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	kinds := []matterendpoint.DPKind{
		matterendpoint.DPKindCustom,
		matterendpoint.DPKindGeneric,
		matterendpoint.DPKindCalculated,
		matterendpoint.DPKindCombined,
		matterendpoint.DPKindMeasurement,
	}
	for i, kind := range kinds {
		key := matterendpoint.SourceKey{
			CentralName:   "dktest",
			DeviceAddress: "A:1",
			ChannelNo:     i + 2,
			DPKind:        kind,
			DPKey:         "K",
		}
		rec := fabricendpoint.Record{Key: key, EndpointID: uint16(i + 2), DeviceType: uint16(i)} //nolint:gosec // loop index is small and non-negative; uint16 conversions are safe
		if _, err := s.UpsertEndpointAssigning(ctx, rec); err != nil {
			t.Fatalf("UpsertEndpointAssigning kind=%s: %v", kind, err)
		}
		got, err := s.GetEndpoint(ctx, key)
		if err != nil {
			t.Fatalf("GetEndpoint kind=%s: %v", kind, err)
		}
		gotKey, ok := got.Key.(matterendpoint.SourceKey)
		if !ok {
			t.Fatalf("returned key is %T, want %T", got.Key, matterendpoint.SourceKey{})
		}
		if gotKey.DPKind != kind {
			t.Errorf("DPKind = %s, want %s", gotKey.DPKind, kind)
		}
	}
}

// TestListEndpoints_OrderedByID verifies that ListEndpoints returns rows in
// ascending endpoint_id order.
func TestListEndpoints_OrderedByID(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	// Insert out-of-order IDs.
	for _, id := range []uint16{5, 3, 4} {
		key := testKey("ord", "B:1", int(id), matterendpoint.DPKindCustom, "V")
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: id}); err != nil {
			t.Fatalf("UpsertEndpointAssigning id=%d: %v", id, err)
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
	s := matterendpoint.New(openTestDB(t))

	key := testKey("rem", "B:1", 3, matterendpoint.DPKindGeneric, "X")
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
	s := matterendpoint.New(openTestDB(t))

	key := testKey("auto", "C:1", 1, matterendpoint.DPKindCustom, "K")
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 0})
	if err != nil {
		t.Fatalf("UpsertEndpointAssigning: %v", err)
	}
	if id < 2 {
		t.Errorf("assigned ID=%d, want ≥2 (bridged endpoint range)", id)
	}

	// Second call for a different key must not return the same ID.
	key2 := testKey("auto", "C:1", 2, matterendpoint.DPKindCustom, "K")
	id2, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key2, EndpointID: 0})
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
	s := matterendpoint.New(openTestDB(t))

	key := testKey("expl", "D:1", 4, matterendpoint.DPKindMeasurement, "TEMP")
	id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: 42, DeviceType: 0x0302})
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
	s := matterendpoint.New(openTestDB(t))

	for i, centralName := range []string{"c1", "c2", "c1"} {
		key := testKey(centralName, "E:1", i+2, matterendpoint.DPKindCustom, "V")
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, EndpointID: uint16(i + 2)}); err != nil { //nolint:gosec // loop index is small and positive; uint16 conversion is safe
			t.Fatalf("UpsertEndpointAssigning[%d]: %v", i, err)
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

// ---- DeviceKeys ----

// TestDeviceKeysSelectsOneDeviceOfOneCentral holds the lookup a deleted
// device's endpoint numbers are forgotten through: every stored key of the
// device in its central, and nothing of a sibling device or of the same
// address under another central.
func TestDeviceKeysSelectsOneDeviceOfOneCentral(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	want := []matterendpoint.SourceKey{
		testKey("c1", "DEV0000001", 1, matterendpoint.DPKindCustom, "switch"),
		testKey("c1", "DEV0000001", 2, matterendpoint.DPKindCustom, "switch"),
	}
	others := []matterendpoint.SourceKey{
		testKey("c1", "DEV0000002", 1, matterendpoint.DPKindCustom, "switch"),
		testKey("c2", "DEV0000001", 1, matterendpoint.DPKindCustom, "switch"),
	}
	for _, k := range append(append([]matterendpoint.SourceKey{}, want...), others...) {
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: k, DeviceType: 0x010A}); err != nil {
			t.Fatalf("UpsertEndpointAssigning(%s): %v", k, err)
		}
	}

	got, err := s.DeviceKeys(ctx, "c1", "DEV0000001")
	if err != nil {
		t.Fatalf("DeviceKeys: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("DeviceKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DeviceKeys[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if none, err := s.DeviceKeys(ctx, "c1", "DEV0000099"); err != nil || len(none) != 0 {
		t.Errorf("DeviceKeys(unknown device) = %v, %v; want none, nil", none, err)
	}
}
