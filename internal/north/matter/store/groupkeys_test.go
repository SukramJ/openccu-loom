// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store_test

import (
	"context"
	"errors"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// epochKey returns a deterministic 16-byte epoch key for test use.
func epochKey(seed byte) []byte {
	k := make([]byte, 16)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// TestGroupKey_UpsertSlot0Only verifies a key-set with only epoch 0.
func TestGroupKey_UpsertSlot0Only(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 1)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  0,
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      epochKey(0x10),
		EpochStart0:    1_000_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	got, err := s.GetGroupKeySet(ctx, 1, 0)
	if err != nil {
		t.Fatalf("GetGroupKeySet: %v", err)
	}
	if got.EpochKey1 != nil {
		t.Errorf("EpochKey1=%v want nil", got.EpochKey1)
	}
	if got.EpochKey2 != nil {
		t.Errorf("EpochKey2=%v want nil", got.EpochKey2)
	}
	if got.EpochStart0 != 1_000_000 {
		t.Errorf("EpochStart0=%d want 1_000_000", got.EpochStart0)
	}
}

// TestGroupKey_UpsertSlots01 verifies a key-set with epoch 0 + epoch 1.
func TestGroupKey_UpsertSlots01(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 2)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  1,
		SecurityPolicy: store.SecurityPolicyCacheAndSync,
		EpochKey0:      epochKey(0x20),
		EpochStart0:    2_000_000,
		EpochKey1:      epochKey(0x21),
		EpochStart1:    2_100_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	got, err := s.GetGroupKeySet(ctx, 1, 1)
	if err != nil {
		t.Fatalf("GetGroupKeySet: %v", err)
	}
	if got.EpochKey1 == nil {
		t.Fatal("EpochKey1 is nil, want non-nil")
	}
	if got.EpochStart1 != 2_100_000 {
		t.Errorf("EpochStart1=%d want 2_100_000", got.EpochStart1)
	}
	if got.EpochKey2 != nil {
		t.Errorf("EpochKey2=%v want nil", got.EpochKey2)
	}
}

// TestGroupKey_UpsertAllSlots verifies a key-set with all three epoch slots.
func TestGroupKey_UpsertAllSlots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 3)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  2,
		SecurityPolicy: store.SecurityPolicyCacheAndSync,
		EpochKey0:      epochKey(0x30),
		EpochStart0:    3_000_000,
		EpochKey1:      epochKey(0x31),
		EpochStart1:    3_100_000,
		EpochKey2:      epochKey(0x32),
		EpochStart2:    3_200_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	got, err := s.GetGroupKeySet(ctx, 1, 2)
	if err != nil {
		t.Fatalf("GetGroupKeySet: %v", err)
	}
	if got.EpochKey2 == nil {
		t.Fatal("EpochKey2 is nil, want non-nil")
	}
	if got.EpochStart2 != 3_200_000 {
		t.Errorf("EpochStart2=%d want 3_200_000", got.EpochStart2)
	}
}

// TestGroupKey_NullableEpochStart verifies that when EpochKey1 is nil the
// EpochStart1 column is also NULL (not the zero value 0).
func TestGroupKey_NullableEpochStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)
	addTestFabric(t, s, 1, 4)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  0,
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      epochKey(0x40),
		EpochStart0:    4_000_000,
		EpochKey1:      nil,   // slot 1 empty
		EpochStart1:    99999, // must not be stored
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	// Verify directly via SQL that epoch_start_1 is NULL.
	var epochStart1 *int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT epoch_start_1 FROM matter_group_keys WHERE fabric_index=1 AND group_key_set_id=0`,
	).Scan(&epochStart1); err != nil {
		t.Fatalf("SQL scan: %v", err)
	}
	if epochStart1 != nil {
		t.Errorf("epoch_start_1=%d want NULL", *epochStart1)
	}

	// Round-trip via API: EpochStart1 must be 0 (zero value) not 99999.
	got, err := s.GetGroupKeySet(ctx, 1, 0)
	if err != nil {
		t.Fatalf("GetGroupKeySet: %v", err)
	}
	if got.EpochKey1 != nil {
		t.Errorf("EpochKey1=%v want nil", got.EpochKey1)
	}
	if got.EpochStart1 != 0 {
		t.Errorf("EpochStart1=%d want 0 (zero value for absent slot)", got.EpochStart1)
	}
}

// TestGroupKey_GetMiss verifies ErrGroupKeySetNotFound for an absent entry.
func TestGroupKey_GetMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 5)

	_, err := s.GetGroupKeySet(ctx, 1, 99)
	if !errors.Is(err, store.ErrGroupKeySetNotFound) {
		t.Errorf("got %v, want ErrGroupKeySetNotFound", err)
	}
}

// TestGroupKey_ListSortedByID verifies ListGroupKeySets order.
func TestGroupKey_ListSortedByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 6)

	ids := []uint16{5, 2, 8, 1, 3}
	for _, id := range ids {
		gks := store.GroupKeySet{
			FabricIndex:   1,
			GroupKeySetID: id,
			EpochKey0:     epochKey(byte(id)), //nolint:gosec // G115: id is a small GroupKeySetID; test values fit uint8
			EpochStart0:   uint64(id) * 1000,
		}
		if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
			t.Fatalf("UpsertGroupKeySet id=%d: %v", id, err)
		}
	}

	list, err := s.ListGroupKeySets(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeySets: %v", err)
	}
	if len(list) != len(ids) {
		t.Fatalf("len=%d want %d", len(list), len(ids))
	}
	// Expect sorted 1, 2, 3, 5, 8.
	expected := []uint16{1, 2, 3, 5, 8}
	for i, gks := range list {
		if gks.GroupKeySetID != expected[i] {
			t.Errorf("list[%d].GroupKeySetID=%d want %d", i, gks.GroupKeySetID, expected[i])
		}
	}
}

// TestGroupKey_RemoveCascadesToMapping verifies that RemoveGroupKeySet
// also wipes dependent rows in matter_group_key_map.
func TestGroupKey_RemoveCascadesToMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)
	addTestFabric(t, s, 1, 7)

	gks := store.GroupKeySet{
		FabricIndex:   1,
		GroupKeySetID: 0,
		EpochKey0:     epochKey(0x70),
		EpochStart0:   7_000_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
		FabricIndex: 1, GroupID: 42, GroupKeySetID: 0,
	}); err != nil {
		t.Fatalf("SetGroupKeyMapping: %v", err)
	}

	if err := s.RemoveGroupKeySet(ctx, 1, 0); err != nil {
		t.Fatalf("RemoveGroupKeySet: %v", err)
	}

	// The mapping must be gone.
	var n int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM matter_group_key_map WHERE fabric_index=1 AND group_id=42`,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("CASCADE failed: matter_group_key_map still has %d rows", n)
	}
}

// TestGroupKey_SecurityPolicyRoundTrip verifies both SecurityPolicy values
// survive a write/read cycle.
func TestGroupKey_SecurityPolicyRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 8)

	tests := []struct {
		id     uint16
		policy store.SecurityPolicy
	}{
		{0, store.SecurityPolicyTrustFirst},
		{1, store.SecurityPolicyCacheAndSync},
	}
	for _, tc := range tests {
		gks := store.GroupKeySet{
			FabricIndex:    1,
			GroupKeySetID:  tc.id,
			SecurityPolicy: tc.policy,
			EpochKey0:      epochKey(byte(tc.id) + 0x80), //nolint:gosec // G115: tc.id is a small GroupKeySetID; test values fit uint8
			EpochStart0:    uint64(tc.id) * 100,
		}
		if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
			t.Fatalf("UpsertGroupKeySet policy=%d: %v", tc.policy, err)
		}
		got, err := s.GetGroupKeySet(ctx, 1, tc.id)
		if err != nil {
			t.Fatalf("GetGroupKeySet: %v", err)
		}
		if got.SecurityPolicy != tc.policy {
			t.Errorf("id=%d: SecurityPolicy=%d want %d", tc.id, got.SecurityPolicy, tc.policy)
		}
	}
}

// TestGroupKeyMapping_SetAndUpdate verifies upsert semantics: same group_id
// can be remapped to a new group_key_set_id.
func TestGroupKeyMapping_SetAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 9)

	// Two key-sets.
	for _, id := range []uint16{0, 1} {
		gks := store.GroupKeySet{
			FabricIndex:   1,
			GroupKeySetID: id,
			EpochKey0:     epochKey(byte(id) + 0x90), //nolint:gosec // G115: id is 0 or 1; byte(id)+0x90 is 0x90 or 0x91, fits uint8
			EpochStart0:   uint64(id) * 1000,
		}
		if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
			t.Fatalf("UpsertGroupKeySet id=%d: %v", id, err)
		}
	}

	// Map group 5 → key-set 0.
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
		FabricIndex: 1, GroupID: 5, GroupKeySetID: 0,
	}); err != nil {
		t.Fatalf("SetGroupKeyMapping first: %v", err)
	}

	// Remap group 5 → key-set 1.
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
		FabricIndex: 1, GroupID: 5, GroupKeySetID: 1,
	}); err != nil {
		t.Fatalf("SetGroupKeyMapping update: %v", err)
	}

	mappings, err := s.ListGroupKeyMappings(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeyMappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("len=%d want 1", len(mappings))
	}
	if mappings[0].GroupKeySetID != 1 {
		t.Errorf("GroupKeySetID=%d want 1 (updated)", mappings[0].GroupKeySetID)
	}
}

// TestGroupKeyMapping_Remove verifies RemoveGroupKeyMapping.
func TestGroupKeyMapping_Remove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 10)

	gks := store.GroupKeySet{
		FabricIndex:   1,
		GroupKeySetID: 0,
		EpochKey0:     epochKey(0xA0),
		EpochStart0:   10_000_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
		FabricIndex: 1, GroupID: 7, GroupKeySetID: 0,
	}); err != nil {
		t.Fatalf("SetGroupKeyMapping: %v", err)
	}

	if err := s.RemoveGroupKeyMapping(ctx, 1, 7); err != nil {
		t.Fatalf("RemoveGroupKeyMapping: %v", err)
	}

	mappings, err := s.ListGroupKeyMappings(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeyMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("len=%d want 0", len(mappings))
	}
}

// TestGroupKeyMapping_ListSortedByGroupID verifies ListGroupKeyMappings order.
func TestGroupKeyMapping_ListSortedByGroupID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 11)

	gks := store.GroupKeySet{
		FabricIndex:   1,
		GroupKeySetID: 0,
		EpochKey0:     epochKey(0xB0),
		EpochStart0:   11_000_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	// Insert in reverse order.
	for _, gid := range []uint16{9, 3, 7, 1, 5} {
		if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
			FabricIndex: 1, GroupID: gid, GroupKeySetID: 0,
		}); err != nil {
			t.Fatalf("SetGroupKeyMapping gid=%d: %v", gid, err)
		}
	}

	list, err := s.ListGroupKeyMappings(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeyMappings: %v", err)
	}
	expected := []uint16{1, 3, 5, 7, 9}
	for i, m := range list {
		if m.GroupID != expected[i] {
			t.Errorf("list[%d].GroupID=%d want %d", i, m.GroupID, expected[i])
		}
	}
}

// TestRemoveGroupKeySet_Existing removes an existing key set and verifies it
// is gone afterward.
func TestRemoveGroupKeySet_Existing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 1)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  77,
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      epochKey(0x01),
		EpochStart0:    100,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}
	if err := s.RemoveGroupKeySet(ctx, 1, 77); err != nil {
		t.Fatalf("RemoveGroupKeySet: %v", err)
	}
	_, err := s.GetGroupKeySet(ctx, 1, 77)
	if err == nil {
		t.Fatal("expected error after RemoveGroupKeySet, got nil")
	}
}

// TestSetGroupKeyMapping verifies SetGroupKeyMapping upsert and remove.
func TestSetGroupKeyMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 1)

	gks := store.GroupKeySet{
		FabricIndex:    1,
		GroupKeySetID:  0,
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      epochKey(0x20),
		EpochStart0:    200,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{FabricIndex: 1, GroupID: 0x0101, GroupKeySetID: 0}); err != nil {
		t.Fatalf("SetGroupKeyMapping: %v", err)
	}
	if err := s.RemoveGroupKeyMapping(ctx, 1, 0x0101); err != nil {
		t.Fatalf("RemoveGroupKeyMapping: %v", err)
	}
}

// TestGroupKeys_RemoveGroupKeySetIdempotent verifies that removing a key-set
// that never existed does not error.
func TestGroupKeys_RemoveGroupKeySetIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x40)

	// Remove a key-set that never existed — must not error.
	if err := s.RemoveGroupKeySet(ctx, 1, 999); err != nil {
		t.Fatalf("RemoveGroupKeySet missing: %v", err)
	}
}

// TestGroupKeys_RemoveGroupKeyMappingIdempotent verifies that removing a group
// key mapping that never existed does not error.
func TestGroupKeys_RemoveGroupKeyMappingIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x41)

	// Remove a mapping that never existed — must not error.
	if err := s.RemoveGroupKeyMapping(ctx, 1, 0xFFFF); err != nil {
		t.Fatalf("RemoveGroupKeyMapping missing: %v", err)
	}
}

// TestGroupKeys_ListGroupKeyMappingsEmpty verifies that ListGroupKeyMappings on
// a fabric with no mappings returns an empty slice.
func TestGroupKeys_ListGroupKeyMappingsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x42)

	list, err := s.ListGroupKeyMappings(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeyMappings: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len=%d want 0", len(list))
	}
}

// TestGroupKeys_ListGroupKeySetsEmpty verifies that ListGroupKeySets on a fabric
// with no key sets returns an empty slice.
func TestGroupKeys_ListGroupKeySetsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x43)

	list, err := s.ListGroupKeySets(ctx, 1)
	if err != nil {
		t.Fatalf("ListGroupKeySets: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len=%d want 0", len(list))
	}
}
