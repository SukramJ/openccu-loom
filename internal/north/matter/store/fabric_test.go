// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// newFabricRecord builds a minimal FabricRecord for the given index and seed.
// seed is used to produce unique RootPublicKey + FabricID so UNIQUE constraints
// are not inadvertently violated between tests.
func newFabricRecord(fabricIndex uint8, seed byte) store.FabricRecord {
	var compID [8]byte
	compID[0] = seed
	return store.FabricRecord{
		FabricIndex:   fabricIndex,
		FabricID:      uint64(seed) + 1000,
		NodeID:        uint64(seed) + 2000,
		RootPublicKey: uncompressedP256Fixture(seed),
		VendorID:      0x1234,
		Label:         fmt.Sprintf("fabric-%d", seed),
		CompressedID:  compID,
	}
}

// TestFabric_AddAutoAssignsIndex verifies that fabric_index 0 gets 1, 2, 3 …
func TestFabric_AddAutoAssignsIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	for wantIdx := uint8(1); wantIdx <= 3; wantIdx++ {
		rec := newFabricRecord(0, wantIdx)
		got, err := s.AddFabric(ctx, rec)
		if err != nil {
			t.Fatalf("AddFabric seed=%d: %v", wantIdx, err)
		}
		if got != wantIdx {
			t.Errorf("iteration %d: got fabric_index %d, want %d", wantIdx, got, wantIdx)
		}
	}
}

// TestFabric_AddExplicitIndex verifies that an explicit fabric_index is
// honoured.
func TestFabric_AddExplicitIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(42, 10)
	got, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if got != 42 {
		t.Errorf("got fabric_index %d, want 42", got)
	}
}

// TestFabric_AddUniqueConstraint verifies that inserting the same
// (fabric_id, root_public_key) combination twice fails.
func TestFabric_AddUniqueConstraint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(0, 20)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("first AddFabric: %v", err)
	}

	// Same fabric_id + root_public_key — different explicit index to avoid
	// PK collision but still trigger the UNIQUE constraint.
	dup := rec
	dup.FabricIndex = 50
	if _, err := s.AddFabric(ctx, dup); err == nil {
		t.Fatal("expected error on duplicate (fabric_id, root_public_key), got nil")
	}
}

// TestFabric_AddAutoFillsGap verifies that after inserting 1, 2, 4 the next
// auto-assigned index is 3.
func TestFabric_AddAutoFillsGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Insert fabric_index 1, 2, 4 explicitly.
	for _, idx := range []uint8{1, 2, 4} {
		rec := newFabricRecord(idx, idx)
		if _, err := s.AddFabric(ctx, rec); err != nil {
			t.Fatalf("insert index=%d: %v", idx, err)
		}
	}

	// Auto-assign should pick 3.
	rec := newFabricRecord(0, 99)
	got, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric auto: %v", err)
	}
	if got != 3 {
		t.Errorf("got fabric_index %d, want 3", got)
	}
}

// TestFabric_AddExhausted verifies ErrFabricExhausted when all 254 slots are
// used. To keep the test fast we insert 254 rows directly — AddFabric is
// correct-by-definition for the content, so we batch them.
func TestFabric_AddExhausted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Fill indices 1..254.
	for i := uint8(1); i <= 254; i++ {
		rec := newFabricRecord(i, i) // seed == i, so each key is unique
		if _, err := s.AddFabric(ctx, rec); err != nil {
			t.Fatalf("insert index=%d: %v", i, err)
		}
	}

	// Now auto-assign must fail.
	extra := store.FabricRecord{
		FabricIndex:   0,
		FabricID:      9999,
		NodeID:        9999,
		RootPublicKey: uncompressedP256Fixture(0xAB),
		VendorID:      0x00FF,
	}
	_, err := s.AddFabric(ctx, extra)
	if !errors.Is(err, store.ErrFabricExhausted) {
		t.Errorf("got %v, want ErrFabricExhausted", err)
	}
}

// TestFabric_GetHit verifies a successful GetFabric round-trip.
func TestFabric_GetHit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(7, 30)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	got, err := s.GetFabric(ctx, 7)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.FabricIndex != 7 {
		t.Errorf("FabricIndex=%d want 7", got.FabricIndex)
	}
	if got.FabricID != rec.FabricID {
		t.Errorf("FabricID=%d want %d", got.FabricID, rec.FabricID)
	}
	if got.NodeID != rec.NodeID {
		t.Errorf("NodeID=%d want %d", got.NodeID, rec.NodeID)
	}
	if !bytes.Equal(got.RootPublicKey, rec.RootPublicKey) {
		t.Error("RootPublicKey mismatch")
	}
	if got.Label != rec.Label {
		t.Errorf("Label=%q want %q", got.Label, rec.Label)
	}
	if got.CompressedID != rec.CompressedID {
		t.Errorf("CompressedID=%v want %v", got.CompressedID, rec.CompressedID)
	}
}

// TestFabric_GetMiss verifies ErrFabricNotFound for an absent index.
func TestFabric_GetMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	_, err := s.GetFabric(ctx, 99)
	if !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("got %v, want ErrFabricNotFound", err)
	}
}

// TestFabric_ListSortedByIndex verifies that ListFabrics returns rows in
// ascending fabric_index order regardless of insertion order.
func TestFabric_ListSortedByIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Insert in reverse order.
	for _, idx := range []uint8{5, 3, 1, 4, 2} {
		rec := newFabricRecord(idx, idx)
		if _, err := s.AddFabric(ctx, rec); err != nil {
			t.Fatalf("insert index=%d: %v", idx, err)
		}
	}

	list, err := s.ListFabrics(ctx)
	if err != nil {
		t.Fatalf("ListFabrics: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("len=%d want 5", len(list))
	}
	for i, f := range list {
		wantIdx := uint8(i + 1)
		if f.FabricIndex != wantIdx {
			t.Errorf("list[%d].FabricIndex=%d want %d", i, f.FabricIndex, wantIdx)
		}
	}
}

// TestFabric_UpdateLabel verifies that UpdateFabricLabel rewrites the label
// and that a miss returns ErrFabricNotFound.
func TestFabric_UpdateLabel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(2, 40)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := s.UpdateFabricLabel(ctx, 2, "updated-label"); err != nil {
		t.Fatalf("UpdateFabricLabel: %v", err)
	}
	got, err := s.GetFabric(ctx, 2)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.Label != "updated-label" {
		t.Errorf("Label=%q want updated-label", got.Label)
	}

	// Miss.
	if err := s.UpdateFabricLabel(ctx, 99, "x"); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("miss: got %v, want ErrFabricNotFound", err)
	}
}

// TestFabric_UpdateNodeID verifies that UpdateFabricNodeID rewrites the
// NodeID for an existing fabric, leaves every other field untouched, and
// returns [store.ErrFabricNotFound] for a missing index. UpdateNOC calls
// this when the commissioner's new NOC carries a different operational
// NodeID for the same fabric (Matter §11.18.6.9); the stored row must
// follow so destinationID resolution and the operational mDNS instance
// name stay in sync with the installed certificate.
func TestFabric_UpdateNodeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(6, 45)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	const newNodeID = uint64(0x0102030405060708)
	if err := s.UpdateFabricNodeID(ctx, 6, newNodeID); err != nil {
		t.Fatalf("UpdateFabricNodeID: %v", err)
	}

	got, err := s.GetFabric(ctx, 6)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.NodeID != newNodeID {
		t.Errorf("NodeID=%#016x want %#016x", got.NodeID, newNodeID)
	}
	// Every other field must be unchanged.
	if got.FabricID != rec.FabricID {
		t.Errorf("FabricID changed: got %d, want unchanged %d", got.FabricID, rec.FabricID)
	}
	if !bytes.Equal(got.RootPublicKey, rec.RootPublicKey) {
		t.Errorf("RootPublicKey changed: got %x, want unchanged %x", got.RootPublicKey, rec.RootPublicKey)
	}
	if got.VendorID != rec.VendorID {
		t.Errorf("VendorID changed: got %d, want unchanged %d", got.VendorID, rec.VendorID)
	}
	if got.Label != rec.Label {
		t.Errorf("Label changed: got %q, want unchanged %q", got.Label, rec.Label)
	}
	if got.CompressedID != rec.CompressedID {
		t.Errorf("CompressedID changed: got %x, want unchanged %x", got.CompressedID, rec.CompressedID)
	}

	// Miss.
	if err := s.UpdateFabricNodeID(ctx, 99, newNodeID); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("miss: got %v, want ErrFabricNotFound", err)
	}
}

// TestFabric_RemoveHitAndMiss verifies RemoveFabric hit and miss paths.
func TestFabric_RemoveHitAndMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(3, 50)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := s.RemoveFabric(ctx, 3); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	// Should be gone.
	if _, err := s.GetFabric(ctx, 3); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("after remove: got %v, want ErrFabricNotFound", err)
	}

	// Second remove is a miss.
	if err := s.RemoveFabric(ctx, 3); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("double remove: got %v, want ErrFabricNotFound", err)
	}
}

// TestFabric_RemoveCascades verifies that deleting a fabric wipes its child
// rows from matter_node_identities, matter_group_keys, matter_group_key_map
// and matter_acl_entries via FK CASCADE.
func TestFabric_RemoveCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Insert a fabric.
	rec := newFabricRecord(10, 60)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Attach an identity.
	id := store.IdentityRecord{
		FabricIndex: 10,
		NOC:         []byte("noc"),
		PrivateKey:  []byte("privkey-32-bytes-padded-to-32byt"),
		IPK:         []byte("ipk-16-bytes-pad"),
	}
	if err := s.UpsertIdentity(ctx, id); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	// Attach a group key set.
	gks := store.GroupKeySet{
		FabricIndex:    10,
		GroupKeySetID:  0,
		SecurityPolicy: store.SecurityPolicyTrustFirst,
		EpochKey0:      []byte("epochkey0-16byte"),
		EpochStart0:    1_000_000,
	}
	if err := s.UpsertGroupKeySet(ctx, gks); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	// Attach a mapping.
	if err := s.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
		FabricIndex: 10, GroupID: 1, GroupKeySetID: 0,
	}); err != nil {
		t.Fatalf("SetGroupKeyMapping: %v", err)
	}

	// Attach an ACL.
	if err := s.ReplaceACL(ctx, 10, []store.ACLEntry{
		{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE, Subjects: []uint64{1}},
	}); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	// Remove the fabric.
	if err := s.RemoveFabric(ctx, 10); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	// Verify child tables are empty.
	tables := []string{
		"matter_node_identities",
		"matter_group_keys",
		"matter_group_key_map",
		"matter_acl_entries",
	}
	for _, table := range tables {
		var n int
		if err := db.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE fabric_index = 10",
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("CASCADE failed: %s still has %d rows for fabric_index=10", table, n)
		}
	}
}

// TestFabric_MSBRoundTrip verifies that FabricID and NodeID with the MSB set
// survive a write/read cycle without sign-conversion corruption.
func TestFabric_MSBRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	const maxUint64 = uint64(0xFFFF_FFFF_FFFF_FFFF)
	rec := store.FabricRecord{
		FabricIndex:   1,
		FabricID:      maxUint64,
		NodeID:        maxUint64 - 1,
		RootPublicKey: uncompressedP256Fixture(0xFE),
		VendorID:      0xFFFF,
		Label:         "msb-test",
	}
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	got, err := s.GetFabric(ctx, 1)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.FabricID != maxUint64 {
		t.Errorf("FabricID=%016x want %016x", got.FabricID, maxUint64)
	}
	if got.NodeID != maxUint64-1 {
		t.Errorf("NodeID=%016x want %016x", got.NodeID, maxUint64-1)
	}
}

// ─── monotonic fabric-index counter ──────────────────────────────────────────

// TestFabric_MonotonicCounter_NoReuseAfterRemove verifies that after
// RemoveFabric(1) + AddFabric the newly allocated index is NOT 1 (which
// was just removed) but a higher monotonically-advancing value. This
// mirrors matter.js FabricManager.ts #nextFabricIndex behaviour.
//
// The old scan-based allocator always returned the lowest free slot
// (1 after remove), which can confuse Apple Home's per-fabric cache
// keyed by fabricIndex.
func TestFabric_MonotonicCounter_NoReuseAfterRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Insert fabric 1, 2.
	idx1, err := s.AddFabric(ctx, newFabricRecord(0, 1))
	if err != nil {
		t.Fatalf("AddFabric 1: %v", err)
	}
	if idx1 != 1 {
		t.Fatalf("expected index 1 for first fabric, got %d", idx1)
	}
	idx2, err := s.AddFabric(ctx, newFabricRecord(0, 2))
	if err != nil {
		t.Fatalf("AddFabric 2: %v", err)
	}
	if idx2 != 2 {
		t.Fatalf("expected index 2 for second fabric, got %d", idx2)
	}

	// Remove fabric 1 — index 1 is now free.
	if err := s.RemoveFabric(ctx, 1); err != nil {
		t.Fatalf("RemoveFabric 1: %v", err)
	}

	// The next auto-allocated index must NOT be 1 (re-use avoidance).
	idx3, err := s.AddFabric(ctx, newFabricRecord(0, 3))
	if err != nil {
		t.Fatalf("AddFabric 3: %v", err)
	}
	if idx3 == 1 {
		t.Errorf("AddFabric re-used index 1 immediately after removal (Apple Home cache conflict)")
	}
}

// TestFabric_MonotonicCounter_SequentialAllocation verifies that three
// successive auto-allocated fabrics get distinct, increasing indices.
func TestFabric_MonotonicCounter_SequentialAllocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	seen := make(map[uint8]bool)
	for i := uint8(1); i <= 5; i++ {
		idx, err := s.AddFabric(ctx, newFabricRecord(0, i))
		if err != nil {
			t.Fatalf("AddFabric iteration %d: %v", i, err)
		}
		if seen[idx] {
			t.Errorf("duplicate fabric index %d allocated", idx)
		}
		seen[idx] = true
	}
}

// ─── fabric metadata counter allocation ──────────────────────────────────────

// TestAddFabric_MetadataCounterOutOfRange verifies that AddFabric falls back to
// the scan-based allocator when the persisted next_fabric_index counter holds an
// out-of-range value (0 or > 254). The out-of-range value makes
// getNextFabricIndexFromMetadata return an error, which causes AddFabric to call
// nextFreeFabricIndex instead — and that must succeed on an empty table.
func TestAddFabric_MetadataCounterOutOfRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Corrupt the persisted counter by writing an out-of-range value.
	// 0 is invalid per Matter §11.18.5 (fabric_index must be 1..254).
	_, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 0)
		 ON CONFLICT(key) DO UPDATE SET value = 0`)
	if err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}

	// AddFabric with auto-assign (index=0) must fall back to the scan path
	// and return index 1 from an empty table.
	idx, err := s.AddFabric(ctx, newFabricRecord(0, 50))
	if err != nil {
		t.Fatalf("AddFabric after corrupt counter: %v", err)
	}
	if idx < 1 || idx > 254 {
		t.Errorf("AddFabric returned out-of-range index %d", idx)
	}
}

// TestAddFabric_MetadataCounterTooHigh verifies the same fallback when the
// counter holds 255 (above the 1..254 valid range).
func TestAddFabric_MetadataCounterTooHigh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	_, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 255)
		 ON CONFLICT(key) DO UPDATE SET value = 255`)
	if err != nil {
		t.Fatalf("corrupt metadata high: %v", err)
	}

	idx, err := s.AddFabric(ctx, newFabricRecord(0, 51))
	if err != nil {
		t.Fatalf("AddFabric after high counter: %v", err)
	}
	if idx < 1 || idx > 254 {
		t.Errorf("AddFabric returned out-of-range index %d", idx)
	}
}

// TestAddFabric_ScanFallbackGapDetection verifies that nextFreeFabricIndex (the
// scan fallback) correctly identifies a gap when the counter path is unavailable.
// We corrupt the counter to force the scan path, then insert fabric 1 and 3
// explicitly — the scan should return 2 (the first gap).
func TestAddFabric_ScanFallbackGapDetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Pre-insert fabric 1 and 3 explicitly.
	if _, err := s.AddFabric(ctx, newFabricRecord(1, 60)); err != nil {
		t.Fatalf("AddFabric(1): %v", err)
	}
	if _, err := s.AddFabric(ctx, newFabricRecord(3, 62)); err != nil {
		t.Fatalf("AddFabric(3): %v", err)
	}

	// Now corrupt the counter so the scan fallback is triggered.
	_, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 0)
		 ON CONFLICT(key) DO UPDATE SET value = 0`)
	if err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}

	// The scan path should find index 2 (gap between 1 and 3).
	idx, err := s.AddFabric(ctx, newFabricRecord(0, 61))
	if err != nil {
		t.Fatalf("AddFabric scan fallback: %v", err)
	}
	if idx != 2 {
		t.Errorf("scan fallback returned %d, want 2", idx)
	}
}

// TestAddFabric_ScanFallbackAllSlotsFullExhausted verifies that when the
// counter is corrupt AND all 254 slots are occupied, AddFabric returns
// ErrFabricExhausted via the scan fallback path.
func TestAddFabric_ScanFallbackAllSlotsFullExhausted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Fill all 254 slots.
	for i := uint8(1); i <= 254; i++ {
		if _, err := s.AddFabric(ctx, newFabricRecord(i, i)); err != nil {
			t.Fatalf("fill slot %d: %v", i, err)
		}
	}

	// Corrupt the counter so nextFreeFabricIndex (scan fallback) is used.
	_, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 0)
		 ON CONFLICT(key) DO UPDATE SET value = 0`)
	if err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}

	_, err = s.AddFabric(ctx, store.FabricRecord{
		FabricIndex:   0,
		FabricID:      9999,
		NodeID:        9999,
		RootPublicKey: uncompressedP256Fixture(0xFF),
	})
	if !errors.Is(err, store.ErrFabricExhausted) {
		t.Errorf("scan fallback exhausted: got %v, want ErrFabricExhausted", err)
	}
}

// TestAddFabric_MissingMetadataRow verifies that AddFabric still allocates
// index 1 when the matter_metadata row for next_fabric_index is absent
// (pre-migration-013 schema). This exercises the sql.ErrNoRows branch in
// getNextFabricIndexFromMetadata which returns 1 as the default.
func TestAddFabric_MissingMetadataRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Remove the seeded metadata row to simulate a pre-013 schema.
	if _, err := db.ExecContext(ctx, `DELETE FROM matter_metadata WHERE key = 'next_fabric_index'`); err != nil {
		t.Fatalf("delete metadata: %v", err)
	}

	// AddFabric must still succeed, using 1 as the default start value.
	idx, err := s.AddFabric(ctx, newFabricRecord(0, 70))
	if err != nil {
		t.Fatalf("AddFabric without metadata row: %v", err)
	}
	if idx < 1 || idx > 254 {
		t.Errorf("index %d out of range, want 1..254", idx)
	}
}

// TestAddFabric_CounterExhaustedAllSlotsFull verifies that
// nextFabricIndexFromCounter returns ErrFabricExhausted when the metadata
// counter is valid but all 254 slots are occupied. This exercises the
// `return 0, ErrFabricExhausted` path at the bottom of
// nextFabricIndexFromCounter (after the for-loop exhausts all candidates).
func TestAddFabric_CounterExhaustedAllSlotsFull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Fill all 254 slots.
	for i := uint8(1); i <= 254; i++ {
		if _, err := s.AddFabric(ctx, newFabricRecord(i, i)); err != nil {
			t.Fatalf("fill slot %d: %v", i, err)
		}
	}

	// Set the counter to a valid value (e.g. 1). This means
	// nextFabricIndexFromCounter will run normally but find every candidate
	// occupied, hit the loop exhaustion, and return ErrFabricExhausted at
	// its own `return 0, ErrFabricExhausted` line — without falling back to
	// the scan path (because the counter read succeeded).
	_, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 1)
		 ON CONFLICT(key) DO UPDATE SET value = 1`)
	if err != nil {
		t.Fatalf("set counter: %v", err)
	}

	_, err = s.AddFabric(ctx, store.FabricRecord{
		FabricIndex:   0,
		FabricID:      88888,
		NodeID:        88888,
		RootPublicKey: uncompressedP256Fixture(0xEE),
	})
	if !errors.Is(err, store.ErrFabricExhausted) {
		t.Errorf("counter path exhausted: got %v, want ErrFabricExhausted", err)
	}
}

// ─── additional fabric round-trip cases ──────────────────────────────────────

// TestFabric_RootCertRoundTrip verifies that RootCert bytes survive an
// AddFabric → GetFabric round-trip.
func TestFabric_RootCertRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := store.FabricRecord{
		FabricIndex:   1,
		FabricID:      0xABCD1234,
		NodeID:        0x11223344,
		RootPublicKey: uncompressedP256Fixture(0x01),
		RootCert:      []byte("fake-matter-cert-tlv-bytes"),
		VendorID:      0x1234,
		Label:         "cert-test",
	}
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	got, err := s.GetFabric(ctx, 1)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if !bytes.Equal(got.RootCert, rec.RootCert) {
		t.Errorf("RootCert=%q want %q", got.RootCert, rec.RootCert)
	}
}

// TestFabric_ListFabricsEmpty verifies that ListFabrics on an empty DB returns
// an empty slice without error.
func TestFabric_ListFabricsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	list, err := s.ListFabrics(ctx)
	if err != nil {
		t.Fatalf("ListFabrics: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d entries", len(list))
	}
}

// TestFabric_UpdateLabelRoundTrip verifies that UpdateFabricLabel rewrites the
// label and GetFabric returns the new value.
func TestFabric_UpdateLabelRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if _, err := s.AddFabric(ctx, store.FabricRecord{
		FabricIndex:   5,
		FabricID:      555,
		NodeID:        666,
		RootPublicKey: uncompressedP256Fixture(0x55),
		Label:         "old",
	}); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := s.UpdateFabricLabel(ctx, 5, "new"); err != nil {
		t.Fatalf("UpdateFabricLabel: %v", err)
	}
	got, err := s.GetFabric(ctx, 5)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.Label != "new" {
		t.Errorf("Label=%q want 'new'", got.Label)
	}
}

// TestFabric_RemoveMissingFabricReturnsError verifies that RemoveFabric for an
// absent index returns ErrFabricNotFound.
func TestFabric_RemoveMissingFabricReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	err := s.RemoveFabric(ctx, 200)
	if !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("RemoveFabric missing: got %v, want ErrFabricNotFound", err)
	}
}
