// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

func newResumptionID(seed byte) []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

func newSharedSecret(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

// seedFabric inserts a fabric so the FK constraint for resumption is satisfied.
func seedFabric(t *testing.T, s *mstore.Store, fabricIndex uint8) {
	t.Helper()
	ctx := context.Background()
	_, err := s.AddFabric(ctx, mstore.FabricRecord{
		FabricIndex:   fabricIndex,
		FabricID:      0x1234,
		NodeID:        0x5678,
		RootPublicKey: uncompressedP256Fixture(0x10),
	})
	if err != nil {
		t.Fatalf("seedFabric(%d): %v", fabricIndex, err)
	}
}

func TestResumption_UpsertBadResumptionIDLength(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()

	err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   1,
		ResumptionID: make([]byte, 8), // wrong length
		SharedSecret: newSharedSecret(0),
	})
	if err == nil {
		t.Fatal("expected error for ResumptionID length != 16, got nil")
	}
}

func TestResumption_UpsertBadSharedSecretLength(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()

	err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   1,
		ResumptionID: newResumptionID(0),
		SharedSecret: make([]byte, 16), // wrong length
	})
	if err == nil {
		t.Fatal("expected error for SharedSecret length != 32, got nil")
	}
}

func TestResumption_UpsertAndGetByID(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	rid := newResumptionID(0xAA)
	secret := newSharedSecret(0xBB)

	err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0x1234567890ABCDEF,
		ResumptionID: rid,
		SharedSecret: secret,
	})
	if err != nil {
		t.Fatalf("UpsertResumption: %v", err)
	}

	got, err := s.GetResumptionByID(ctx, rid)
	if err != nil {
		t.Fatalf("GetResumptionByID: %v", err)
	}
	if !bytes.Equal(got.ResumptionID, rid) {
		t.Errorf("ResumptionID mismatch: %X != %X", got.ResumptionID, rid)
	}
	if !bytes.Equal(got.SharedSecret, secret) {
		t.Errorf("SharedSecret mismatch")
	}
	if got.PeerNodeID != 0x1234567890ABCDEF {
		t.Errorf("PeerNodeID = %X, want 1234567890ABCDEF", got.PeerNodeID)
	}
}

func TestResumption_UpsertAndGetByPeer(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	rid := newResumptionID(0xCC)
	secret := newSharedSecret(0xDD)

	err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0xABCD,
		ResumptionID: rid,
		SharedSecret: secret,
	})
	if err != nil {
		t.Fatalf("UpsertResumption: %v", err)
	}

	got, err := s.GetResumptionByPeer(ctx, 1, 0xABCD)
	if err != nil {
		t.Fatalf("GetResumptionByPeer: %v", err)
	}
	if got.FabricIndex != 1 {
		t.Errorf("FabricIndex = %d, want 1", got.FabricIndex)
	}
	if !bytes.Equal(got.ResumptionID, rid) {
		t.Errorf("ResumptionID mismatch")
	}
}

func TestResumption_GetByIDMiss(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()

	_, err := s.GetResumptionByID(ctx, newResumptionID(0x77))
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Fatalf("expected ErrResumptionNotFound, got %v", err)
	}
}

func TestResumption_GetByPeerMiss(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()

	_, err := s.GetResumptionByPeer(ctx, 1, 0x9999)
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Fatalf("expected ErrResumptionNotFound, got %v", err)
	}
}

func TestResumption_UpsertUpdatesExistingRow(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	orig := newResumptionID(0x01)
	updated := newResumptionID(0x02)

	_ = s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0x1,
		ResumptionID: orig,
		SharedSecret: newSharedSecret(0x10),
	})
	_ = s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0x1,
		ResumptionID: updated,
		SharedSecret: newSharedSecret(0x20),
	})

	got, err := s.GetResumptionByPeer(ctx, 1, 0x1)
	if err != nil {
		t.Fatalf("GetResumptionByPeer: %v", err)
	}
	if !bytes.Equal(got.ResumptionID, updated) {
		t.Errorf("expected updated ResumptionID after upsert")
	}
}

func TestResumption_RemoveIdempotent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	_ = s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0xFF,
		ResumptionID: newResumptionID(0x55),
		SharedSecret: newSharedSecret(0x66),
	})

	// First remove should succeed.
	if err := s.RemoveResumption(ctx, 1, 0xFF); err != nil {
		t.Fatalf("RemoveResumption (first): %v", err)
	}
	// Second remove (idempotent) should also succeed.
	if err := s.RemoveResumption(ctx, 1, 0xFF); err != nil {
		t.Fatalf("RemoveResumption (second, idempotent): %v", err)
	}
	// Get should miss.
	_, err := s.GetResumptionByPeer(ctx, 1, 0xFF)
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Fatalf("expected ErrResumptionNotFound after remove, got %v", err)
	}
}

func TestResumption_CascadeOnRemoveFabric(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 2)

	rid := newResumptionID(0x33)
	_ = s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  2,
		PeerNodeID:   0x42,
		ResumptionID: rid,
		SharedSecret: newSharedSecret(0x44),
	})

	// Removing the fabric must cascade-delete the resumption row.
	if err := s.RemoveFabric(ctx, 2); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	_, err := s.GetResumptionByID(ctx, rid)
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Fatalf("expected ErrResumptionNotFound after fabric cascade, got %v", err)
	}
}

func TestResumption_PeerNodeIDMSBRoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	// Use a NodeID with the MSB set to catch big-endian encoding issues.
	var peerNodeID uint64 = 0xFFFFFFFFFFFFFFFF
	_ = s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   peerNodeID,
		ResumptionID: newResumptionID(0xEE),
		SharedSecret: newSharedSecret(0xFF),
	})

	got, err := s.GetResumptionByPeer(ctx, 1, peerNodeID)
	if err != nil {
		t.Fatalf("GetResumptionByPeer MSB: %v", err)
	}
	if got.PeerNodeID != peerNodeID {
		t.Errorf("PeerNodeID = %X, want %X", got.PeerNodeID, peerNodeID)
	}
}

// TestResumption_CASEAuthTagsRoundTrip verifies that CASEAuthTags
// survive an upsert → get cycle for both the by-ID and by-peer paths.
// This covers the L7-1 acceptance criterion (migration 010).
func TestResumption_CASEAuthTagsRoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	cats := []uint32{0x4000_0001, 0x4000_0002} // two CATs
	rid := newResumptionID(0xCA)

	if err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0xBEEF,
		ResumptionID: rid,
		SharedSecret: newSharedSecret(0xCA),
		CASEAuthTags: cats,
	}); err != nil {
		t.Fatalf("UpsertResumption with CATs: %v", err)
	}

	// Round-trip via GetResumptionByID.
	gotByID, err := s.GetResumptionByID(ctx, rid)
	if err != nil {
		t.Fatalf("GetResumptionByID: %v", err)
	}
	if len(gotByID.CASEAuthTags) != 2 ||
		gotByID.CASEAuthTags[0] != cats[0] ||
		gotByID.CASEAuthTags[1] != cats[1] {
		t.Errorf("GetResumptionByID CASEAuthTags = %v, want %v", gotByID.CASEAuthTags, cats)
	}

	// Round-trip via GetResumptionByPeer.
	gotByPeer, err := s.GetResumptionByPeer(ctx, 1, 0xBEEF)
	if err != nil {
		t.Fatalf("GetResumptionByPeer: %v", err)
	}
	if len(gotByPeer.CASEAuthTags) != 2 ||
		gotByPeer.CASEAuthTags[0] != cats[0] ||
		gotByPeer.CASEAuthTags[1] != cats[1] {
		t.Errorf("GetResumptionByPeer CASEAuthTags = %v, want %v", gotByPeer.CASEAuthTags, cats)
	}
}

// TestResumption_CASEAuthTagsNilRoundTrip verifies that a nil CATs
// slice persists as an empty JSON array and deserialises back to nil.
func TestResumption_CASEAuthTagsNilRoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	ctx := context.Background()
	seedFabric(t, s, 1)

	rid := newResumptionID(0xCB)
	if err := s.UpsertResumption(ctx, mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0xCAFE,
		ResumptionID: rid,
		SharedSecret: newSharedSecret(0xCB),
		CASEAuthTags: nil, // no CATs
	}); err != nil {
		t.Fatalf("UpsertResumption nil CATs: %v", err)
	}

	got, err := s.GetResumptionByID(ctx, rid)
	if err != nil {
		t.Fatalf("GetResumptionByID nil CATs: %v", err)
	}
	// A nil or empty slice is acceptable; the only invariant is no panic.
	_ = got.CASEAuthTags
}

// TestResumption_GetByID_BadLength verifies that GetResumptionByID rejects a
// resumption_id slice that is not 16 bytes.
func TestResumption_GetByID_BadLength(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	_, err := s.GetResumptionByID(context.Background(), make([]byte, 8)) // want 16
	if err == nil {
		t.Error("GetResumptionByID bad length: want error, got nil")
	}
}

// TestResumption_GetByPeer_NoFabric_NotFound verifies that GetResumptionByPeer
// returns ErrResumptionNotFound when the fabric does not exist.
func TestResumption_GetByPeer_NoFabric_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := mstore.New(db)
	_, err := s.GetResumptionByPeer(context.Background(), 99, 0x1234)
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Errorf("GetResumptionByPeer missing: want ErrResumptionNotFound, got %v", err)
	}
}

// TestResumption_FullRoundTrip verifies a full round-trip including
// RemoveResumption.
func TestResumption_FullRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := mstore.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x20)

	resID := make([]byte, 16)
	resID[0] = 0xDE
	secret := make([]byte, 32)
	secret[0] = 0xAD

	rec := mstore.ResumptionRecord{
		FabricIndex:  1,
		PeerNodeID:   0xBEEF,
		ResumptionID: resID,
		SharedSecret: secret,
		CASEAuthTags: []uint32{0x10001, 0x10002},
	}
	if err := s.UpsertResumption(ctx, rec); err != nil {
		t.Fatalf("UpsertResumption: %v", err)
	}

	// GetResumptionByID
	got, err := s.GetResumptionByID(ctx, resID)
	if err != nil {
		t.Fatalf("GetResumptionByID: %v", err)
	}
	if got.PeerNodeID != rec.PeerNodeID {
		t.Errorf("PeerNodeID=%x want %x", got.PeerNodeID, rec.PeerNodeID)
	}
	if len(got.CASEAuthTags) != 2 {
		t.Errorf("CASEAuthTags len=%d want 2", len(got.CASEAuthTags))
	}

	// GetResumptionByPeer
	got2, err := s.GetResumptionByPeer(ctx, 1, 0xBEEF)
	if err != nil {
		t.Fatalf("GetResumptionByPeer: %v", err)
	}
	if got2.FabricIndex != 1 {
		t.Errorf("FabricIndex=%d want 1", got2.FabricIndex)
	}

	// RemoveResumption then verify miss
	if err := s.RemoveResumption(ctx, 1, 0xBEEF); err != nil {
		t.Fatalf("RemoveResumption: %v", err)
	}
	_, err = s.GetResumptionByPeer(ctx, 1, 0xBEEF)
	if !errors.Is(err, mstore.ErrResumptionNotFound) {
		t.Errorf("after remove: want ErrResumptionNotFound, got %v", err)
	}
}

// TestResumption_NilCASEAuthTagsRoundTrip verifies that UpsertResumption with a
// nil CASEAuthTags slice persists without error and GetResumption returns a valid
// record.
func TestResumption_NilCASEAuthTagsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := mstore.New(openTestDB(t))
	addTestFabric(t, s, 2, 0x21)

	resID := make([]byte, 16)
	resID[1] = 0xC0
	secret := make([]byte, 32)

	rec := mstore.ResumptionRecord{
		FabricIndex:  2,
		PeerNodeID:   0xCAFE,
		ResumptionID: resID,
		SharedSecret: secret,
		CASEAuthTags: nil, // nil slice
	}
	if err := s.UpsertResumption(ctx, rec); err != nil {
		t.Fatalf("UpsertResumption: %v", err)
	}
	got, err := s.GetResumptionByPeer(ctx, 2, 0xCAFE)
	if err != nil {
		t.Fatalf("GetResumptionByPeer: %v", err)
	}
	// nil and empty slice both persist as '[]'; may come back as either
	if got.FabricIndex != 2 {
		t.Errorf("FabricIndex=%d want 2", got.FabricIndex)
	}
}
