// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"context"
	"errors"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestSavePersistentSubscription_RoundTrip verifies that a subscription
// record survives a save → load round-trip through the SQLite store.
func TestSavePersistentSubscription_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	intervals, err := store.MarshalIntervals(30, 3600)
	if err != nil {
		t.Fatalf("MarshalIntervals: %v", err)
	}

	rec := store.PersistentSubscriptionRecord{
		FabricIndex:   1,
		NodeID:        0xCAFEBABEDEADBEEF,
		PathsJSON:     `[{"Endpoint":1,"Cluster":6,"Attribute":0,"HasEndpoint":true,"HasCluster":true,"HasAttribute":true}]`,
		IntervalsJSON: intervals,
	}

	id, err := s.SavePersistentSubscription(ctx, rec)
	if err != nil {
		t.Fatalf("SavePersistentSubscription: %v", err)
	}
	if id == 0 {
		t.Fatal("SavePersistentSubscription returned id=0")
	}

	loaded, err := s.GetPersistentSubscription(ctx, id)
	if err != nil {
		t.Fatalf("GetPersistentSubscription: %v", err)
	}
	if loaded.FabricIndex != rec.FabricIndex {
		t.Errorf("FabricIndex = %d, want %d", loaded.FabricIndex, rec.FabricIndex)
	}
	if loaded.NodeID != rec.NodeID {
		t.Errorf("NodeID = 0x%016X, want 0x%016X", loaded.NodeID, rec.NodeID)
	}
	if loaded.PathsJSON != rec.PathsJSON {
		t.Errorf("PathsJSON = %q, want %q", loaded.PathsJSON, rec.PathsJSON)
	}
	if loaded.IntervalsJSON != rec.IntervalsJSON {
		t.Errorf("IntervalsJSON = %q, want %q", loaded.IntervalsJSON, rec.IntervalsJSON)
	}
}

// TestLoadPersistentSubscriptions_Empty returns an empty slice (not an
// error) when no rows have been saved.
func TestLoadPersistentSubscriptions_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	rows, err := s.LoadPersistentSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LoadPersistentSubscriptions on empty DB: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

// TestLoadPersistentSubscriptions_MultipleRows verifies ordering and
// completeness when several rows are present.
func TestLoadPersistentSubscriptions_MultipleRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	intervals1, _ := store.MarshalIntervals(10, 600)
	intervals2, _ := store.MarshalIntervals(30, 3600)

	recs := []store.PersistentSubscriptionRecord{
		{FabricIndex: 1, NodeID: 0x0000000000000001, PathsJSON: `[]`, IntervalsJSON: intervals1},
		{FabricIndex: 1, NodeID: 0x0000000000000002, PathsJSON: `[]`, IntervalsJSON: intervals2},
		{FabricIndex: 2, NodeID: 0x0000000000000003, PathsJSON: `[]`, IntervalsJSON: intervals1},
	}
	ids := make([]int64, 0, len(recs))
	for _, r := range recs {
		id, err := s.SavePersistentSubscription(ctx, r)
		if err != nil {
			t.Fatalf("SavePersistentSubscription: %v", err)
		}
		ids = append(ids, id)
	}

	loaded, err := s.LoadPersistentSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LoadPersistentSubscriptions: %v", err)
	}
	if len(loaded) != len(recs) {
		t.Fatalf("got %d rows, want %d", len(loaded), len(recs))
	}
	// Rows must be in ascending ID order.
	for i := 1; i < len(loaded); i++ {
		if loaded[i].ID <= loaded[i-1].ID {
			t.Errorf("rows not in ascending ID order: loaded[%d].ID=%d <= loaded[%d].ID=%d",
				i, loaded[i].ID, i-1, loaded[i-1].ID)
		}
	}
	// NodeID round-trip check.
	for i, got := range loaded {
		if got.NodeID != recs[i].NodeID {
			t.Errorf("row[%d] NodeID=0x%016X, want 0x%016X", i, got.NodeID, recs[i].NodeID)
		}
	}
	_ = ids
}

// TestDeletePersistentSubscription_RemovesRow verifies that Delete
// removes a single row and GetPersistentSubscription returns
// ErrPersistentSubscriptionNotFound afterward.
func TestDeletePersistentSubscription_RemovesRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	intervals, _ := store.MarshalIntervals(5, 300)
	id, err := s.SavePersistentSubscription(ctx, store.PersistentSubscriptionRecord{
		FabricIndex:   1,
		NodeID:        0xABCD,
		PathsJSON:     `[]`,
		IntervalsJSON: intervals,
	})
	if err != nil {
		t.Fatalf("SavePersistentSubscription: %v", err)
	}

	if err := s.DeletePersistentSubscription(ctx, id); err != nil {
		t.Fatalf("DeletePersistentSubscription: %v", err)
	}

	_, err = s.GetPersistentSubscription(ctx, id)
	if !errors.Is(err, store.ErrPersistentSubscriptionNotFound) {
		t.Errorf("GetPersistentSubscription after delete: err=%v, want ErrPersistentSubscriptionNotFound", err)
	}
}

// TestDeletePersistentSubscription_Idempotent verifies that deleting a
// non-existent row does not return an error.
func TestDeletePersistentSubscription_Idempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.DeletePersistentSubscription(ctx, 99999); err != nil {
		t.Errorf("DeletePersistentSubscription(missing id): unexpected error: %v", err)
	}
}

// TestDeletePersistentSubscriptionsByFabric_RemovesOnlyTargetFabric
// asserts that fabric-scoped delete leaves rows from other fabrics intact.
func TestDeletePersistentSubscriptionsByFabric_RemovesOnlyTargetFabric(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.New(openTestDB(t))

	intervals, _ := store.MarshalIntervals(10, 600)
	save := func(fabric uint8, nodeID uint64) {
		t.Helper()
		_, err := s.SavePersistentSubscription(ctx, store.PersistentSubscriptionRecord{
			FabricIndex:   fabric,
			NodeID:        nodeID,
			PathsJSON:     `[]`,
			IntervalsJSON: intervals,
		})
		if err != nil {
			t.Fatalf("SavePersistentSubscription(fabric=%d): %v", fabric, err)
		}
	}

	save(1, 0x1001)
	save(1, 0x1002)
	save(2, 0x2001)

	if err := s.DeletePersistentSubscriptionsByFabric(ctx, 1); err != nil {
		t.Fatalf("DeletePersistentSubscriptionsByFabric: %v", err)
	}

	remaining, err := s.LoadPersistentSubscriptions(ctx)
	if err != nil {
		t.Fatalf("LoadPersistentSubscriptions: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("got %d rows after fabric-1 delete, want 1 (only fabric-2 row should remain)", len(remaining))
	}
	if remaining[0].FabricIndex != 2 {
		t.Errorf("remaining row FabricIndex=%d, want 2", remaining[0].FabricIndex)
	}
	if remaining[0].NodeID != 0x2001 {
		t.Errorf("remaining row NodeID=0x%016X, want 0x2001", remaining[0].NodeID)
	}
}

// TestMarshalUnmarshalIntervals pins the JSON round-trip for the cadence
// helper.
func TestMarshalUnmarshalIntervals(t *testing.T) {
	t.Parallel()

	s, err := store.MarshalIntervals(30, 3600)
	if err != nil {
		t.Fatalf("MarshalIntervals: %v", err)
	}

	v, err := store.UnmarshalIntervals(s)
	if err != nil {
		t.Fatalf("UnmarshalIntervals: %v", err)
	}
	if v.Min != 30 {
		t.Errorf("Min = %d, want 30", v.Min)
	}
	if v.Max != 3600 {
		t.Errorf("Max = %d, want 3600", v.Max)
	}
}
