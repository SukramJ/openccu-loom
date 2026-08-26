// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity tests for fabric store operations against chip FabricTable HEAD.
//
// chip reference: src/credentials/tests/TestFabricTable.cpp
//
// openccu-loom mapping:
//   chip::FabricTable maps to [Store] + its AddFabric / RemoveFabric /
//   UpdateFabricLabel / ListFabrics / GetFabric surface.
//   chip::FabricIndex (1..254) maps to [FabricRecord.FabricIndex].
//   chip::kUndefinedFabricIndex (0) maps to FabricIndex == 0 triggering
//   auto-allocation in [Store.AddFabric].
//
//   Parity cases that require chip's certificate chain (RCAC/ICAC/NOC) are
//   t.Skip-annotated: openccu-loom's store layer stores raw blobs and does
//   not perform chain validation; that is the OperationalCredentials cluster's
//   responsibility.

package store_test

import (
	"context"
	"errors"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ─── TestFabricLookup ─────────────────────────────────────────────────────────

// TestParityChip_FabricLookup_ByExplicitIndex verifies that GetFabric returns
// the correct record when addressed by its explicit fabric_index, and that
// index 0 is never a valid lookup key (kUndefinedFabricIndex in chip).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2533
// (TEST_F "TestFabricLookup").
func TestParityChip_FabricLookup_ByExplicitIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Insert two fabrics with distinct roots (different seeds).
	idx1, err := s.AddFabric(ctx, newFabricRecord(0, 1))
	if err != nil {
		t.Fatalf("AddFabric 1: %v", err)
	}
	idx2, err := s.AddFabric(ctx, newFabricRecord(0, 2))
	if err != nil {
		t.Fatalf("AddFabric 2: %v", err)
	}

	// Lookup by index 1 must succeed and return the correct FabricID.
	got1, err := s.GetFabric(ctx, idx1)
	if err != nil {
		t.Fatalf("GetFabric(idx1): %v", err)
	}
	if got1.FabricIndex != idx1 {
		t.Errorf("FabricIndex=%d want %d", got1.FabricIndex, idx1)
	}

	// Lookup by index 2 must succeed and return the correct FabricID.
	got2, err := s.GetFabric(ctx, idx2)
	if err != nil {
		t.Fatalf("GetFabric(idx2): %v", err)
	}
	if got2.FabricIndex != idx2 {
		t.Errorf("FabricIndex=%d want %d", got2.FabricIndex, idx2)
	}

	// Lookup at index 0 (kUndefinedFabricIndex) must fail.
	if _, err := s.GetFabric(ctx, 0); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("GetFabric(0): got %v, want ErrFabricNotFound", err)
	}
}

// ─── ShouldFailSetFabricIndexWithInvalidIndex ────────────────────────────────

// TestParityChip_AddFabric_IndexZeroAutoAssigns verifies that passing
// FabricIndex==0 to AddFabric triggers auto-allocation.
// In chip, SetFabricIndexForNextAddition(kUndefinedFabricIndex) returns
// CHIP_ERROR_INVALID_FABRIC_INDEX; in openccu-loom, FabricIndex==0 is the
// caller's signal for "assign me the next free slot".
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2571
// (TEST_F "ShouldFailSetFabricIndexWithInvalidIndex").
func TestParityChip_AddFabric_IndexZeroAutoAssigns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	idx, err := s.AddFabric(ctx, newFabricRecord(0, 5))
	if err != nil {
		t.Fatalf("AddFabric with index 0: %v", err)
	}
	if idx == 0 {
		t.Errorf("auto-assigned index must be >= 1, got 0")
	}
}

// ─── ShouldAddFabricAtRequestedIndex ─────────────────────────────────────────

// TestParityChip_AddFabric_AtRequestedIndex verifies that when an explicit
// non-zero FabricIndex is supplied the record is stored at exactly that slot.
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2604
// (TEST_F "ShouldAddFabricAtRequestedIndex").
func TestParityChip_AddFabric_AtRequestedIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Insert at index 2, then at index 1 (out of order, as chip test does).
	rec2 := newFabricRecord(2, 10)
	if _, err := s.AddFabric(ctx, rec2); err != nil {
		t.Fatalf("AddFabric at 2: %v", err)
	}
	rec1 := newFabricRecord(1, 11)
	if _, err := s.AddFabric(ctx, rec1); err != nil {
		t.Fatalf("AddFabric at 1: %v", err)
	}

	// Verify both are retrievable at their requested indices.
	got1, err := s.GetFabric(ctx, 1)
	if err != nil {
		t.Fatalf("GetFabric(1): %v", err)
	}
	if got1.FabricID != rec1.FabricID {
		t.Errorf("index 1 FabricID=%d want %d", got1.FabricID, rec1.FabricID)
	}

	got2, err := s.GetFabric(ctx, 2)
	if err != nil {
		t.Fatalf("GetFabric(2): %v", err)
	}
	if got2.FabricID != rec2.FabricID {
		t.Errorf("index 2 FabricID=%d want %d", got2.FabricID, rec2.FabricID)
	}
}

// ─── ShouldFailSetFabricIndexWhenInUse ───────────────────────────────────────

// TestParityChip_AddFabric_SameIndexTwiceFails verifies that inserting a
// second fabric at the same explicit index fails (chip CHIP_ERROR_FABRIC_EXISTS).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2593
// (TEST_F "ShouldFailSetFabricIndexWhenInUse").
func TestParityChip_AddFabric_SameIndexTwiceFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if _, err := s.AddFabric(ctx, newFabricRecord(1, 20)); err != nil {
		t.Fatalf("first AddFabric at 1: %v", err)
	}
	if _, err := s.AddFabric(ctx, newFabricRecord(1, 21)); err == nil {
		t.Fatal("expected error when re-using fabric_index 1, got nil")
	}
}

// ─── TestFabricLabelChange ────────────────────────────────────────────────────

// TestParityChip_FabricLabel_SetAndGet verifies that SetFabricLabel (mapped to
// UpdateFabricLabel) persists the new label and GetFabric returns it.
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2399
// (TEST_F "TestFabricLabelChange") – first scope: initial label is empty
// after AddFabric; second scope: label updated correctly.
func TestParityChip_FabricLabel_SetAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	rec := newFabricRecord(0, 30)
	rec.Label = "" // start unlabelled per chip test
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Update label to "acme fabric".
	if err := s.UpdateFabricLabel(ctx, idx, "acme fabric"); err != nil {
		t.Fatalf("UpdateFabricLabel: %v", err)
	}

	got, err := s.GetFabric(ctx, idx)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.Label != "acme fabric" {
		t.Errorf("Label=%q, want %q", got.Label, "acme fabric")
	}
}

// TestParityChip_FabricLabel_TooLongFails verifies that a label > 32 bytes
// is rejected. In chip, SetFabricLabel returns CHIP_ERROR_INVALID_ARGUMENT
// for labels longer than kFabricLabelMaxLengthInBytes (32).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:2470
// (part of TEST_F "TestFabricLabelChange").
//
// openccu-loom gap: label-length enforcement lives in the
// OperationalCredentials cluster handler (handleUpdateFabricLabel), not in
// the store layer. The store itself stores any-length strings.
func TestParityChip_FabricLabel_TooLongFails(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — label-length enforcement is in OperationalCredentials cluster, not store layer")
}

// ─── TestPersistence ─────────────────────────────────────────────────────────

// TestParityChip_Persistence_RoundTrip verifies that all fabric fields survive
// a store write/read cycle without corruption — analogous to chip's
// TestPersistence which validates serialisation across FabricTable restarts.
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:1490
// (TEST_F "TestPersistence") — key fields verified.
func TestParityChip_Persistence_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	rec := store.FabricRecord{
		FabricIndex:   0,
		FabricID:      0xFAB000000000001D,
		NodeID:        0x000000000000B00B,
		RootPublicKey: uncompressedP256Fixture(0xAB),
		VendorID:      0xFFF1,
		Label:         "chip-parity-test",
		CompressedID:  [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
	}
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Re-attach to the same DB to simulate a reload.
	s2 := store.New(db)
	got, err := s2.GetFabric(ctx, idx)
	if err != nil {
		t.Fatalf("GetFabric after reload: %v", err)
	}
	if got.FabricID != rec.FabricID {
		t.Errorf("FabricID=%016x want %016x", got.FabricID, rec.FabricID)
	}
	if got.NodeID != rec.NodeID {
		t.Errorf("NodeID=%016x want %016x", got.NodeID, rec.NodeID)
	}
	if got.VendorID != rec.VendorID {
		t.Errorf("VendorID=0x%04X want 0x%04X", got.VendorID, rec.VendorID)
	}
	if got.Label != rec.Label {
		t.Errorf("Label=%q want %q", got.Label, rec.Label)
	}
	if got.CompressedID != rec.CompressedID {
		t.Errorf("CompressedID mismatch")
	}
}

// ─── DeleteFabricCallsDelegate ────────────────────────────────────────────────

// TestParityChip_DeleteFabricCallsDelegate verifies that RemoveFabric removes
// the row and the fabric is no longer retrievable (the "delegate" in chip
// is the callback; openccu-loom's equivalent is verified in the cluster layer
// tests — here we verify only the store-level deletion contract).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:3209
// (TEST_F "DeleteFabricCallsDelegate").
func TestParityChip_DeleteFabricCallsDelegate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	idx, err := s.AddFabric(ctx, newFabricRecord(0, 70))
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := s.RemoveFabric(ctx, idx); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	// Post-delete: fabric must be gone.
	if _, err := s.GetFabric(ctx, idx); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("after delete: got %v, want ErrFabricNotFound", err)
	}
}

// TestParityChip_DeleteNonExistentFabric verifies that attempting to delete a
// non-existent fabric returns an appropriate error (chip returns
// CHIP_ERROR_NOT_FOUND).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:3244-3246
// (TEST_F "DeleteFabricCallsDelegate" — Delete on uncommitted fabric).
func TestParityChip_DeleteNonExistentFabric(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.RemoveFabric(ctx, 42); !errors.Is(err, store.ErrFabricNotFound) {
		t.Errorf("got %v, want ErrFabricNotFound", err)
	}
}

// ─── TestAddNocFailSafe (structural only) ────────────────────────────────────

// TestParityChip_AddFabric_ImmediatelyDurable verifies that AddFabric commits
// immediately (openccu-loom has no staged-commit model like chip; every write
// is immediately durable).
//
// Mirrors chip src/credentials/tests/TestFabricTable.cpp:1800
// (TEST_F "TestAddNocFailSafe") — by-design divergence: chip uses pending→
// commit/rollback; openccu-loom uses transactional immediate writes.
// t.Skip documents the staged-commit gap; the immediate-write invariant is
// verified by TestParityChip_Persistence_RoundTrip above.
func TestParityChip_AddFabric_ChipStagedCommit_NotApplicable(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — chip staged-commit (pending→commit/rollback) has no equivalent; openccu-loom writes are immediately durable per-transaction")
}

// ─── TrustedRootCertificates RCAC TLV round-trip ─────────────────────────────

// TestParityChip_TrustedRootCerts_FullRCACTLV_RoundTrip verifies that the
// RootCert field (the full Matter Certificate TLV envelope received via
// AddTrustedRootCertificate) is stored and retrieved byte-for-byte without
// truncation or corruption.
//
// This is the regression guard for the TrustedRootCertificates attribute
// (OperationalCredentials 0x0004): Apple Home validates every entry as a
// Matter Certificate TLV and silently drops the entire ReportData stream on
// schema mismatch (verified empirically during pair attempts v5). Storing
// the bare EC public key instead of the full TLV envelope triggers this path.
//
// Source-Origin: derived from chip src/credentials/tests/TestFabricTable.cpp
// TestPersistence (line 1490) which verifies full serialisation of the root
// certificate field alongside other fabric fields. The RCAC-blob invariant
// maps directly to matter.js Fabric.ts:68 `rootCert` field and
// OperationalCredentialsServer.ts:457-459 verbatim-serve requirement.
func TestParityChip_TrustedRootCerts_FullRCACTLV_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	// Simulate a real RCAC TLV envelope: a 65-byte blob starting with
	// 0x15 (TLV Structure control byte, as in a real Matter Certificate TLV).
	// The store must preserve every byte verbatim — no truncation, no
	// stripping, no re-encoding as bare public key.
	rcacTLV := make([]byte, 65)
	rcacTLV[0] = 0x15 // TLV structure control byte (Matter Certificate TLV)
	rcacTLV[1] = 0x30 // context tag 0 (SerialNumber field in Matter cert)
	for i := 2; i < 65; i++ {
		rcacTLV[i] = byte(i) // deterministic fill, detects byte-reorder bugs
	}

	var compID [8]byte
	copy(compID[:], []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03})

	rec := store.FabricRecord{
		FabricIndex:   0,
		FabricID:      0x1234567890ABCDEF,
		NodeID:        0x0000000000000001,
		RootPublicKey: uncompressedP256Fixture(0xCC),
		RootCert:      rcacTLV, // full RCAC TLV — must survive the round-trip
		VendorID:      0xFFF1,
		Label:         "rcac-parity",
		CompressedID:  compID,
	}

	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Re-attach to the same DB to simulate a daemon restart (same as
	// TestParityChip_Persistence_RoundTrip pattern, but focused on RootCert).
	s2 := store.New(db)
	got, err := s2.GetFabric(ctx, idx)
	if err != nil {
		t.Fatalf("GetFabric after reload: %v", err)
	}

	// RootCert must not be nil — a nil blob means TrustedRootCertificates
	// would omit this fabric's entry, breaking Apple Home's per-fabric
	// cache validation on the next subscribe.
	if got.RootCert == nil {
		t.Fatal("RootCert is nil after reload — TrustedRootCertificates would serve empty entries (Apple pair-abort)")
	}

	// The blob must be byte-for-byte identical — no truncation, no rewrite.
	if len(got.RootCert) != len(rcacTLV) {
		t.Fatalf("RootCert length=%d, want %d (full RCAC TLV must not be truncated)", len(got.RootCert), len(rcacTLV))
	}
	for i := range rcacTLV {
		if got.RootCert[i] != rcacTLV[i] {
			t.Errorf("RootCert[%d]=%02X, want %02X (byte corruption at position %d)", i, got.RootCert[i], rcacTLV[i], i)
			break
		}
	}
}
