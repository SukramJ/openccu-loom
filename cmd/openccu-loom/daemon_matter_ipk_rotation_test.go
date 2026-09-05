// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/secure/sigma"
	matterstore "github.com/SukramJ/go-fabric/store"
)

// ── ipkOperationalCandidates ──────────────────────────────────────────────

// TestIpkOperationalCandidates_ReturnsEveryEpochKey verifies that a
// fabric whose GroupKeySetID 0 carries two epoch keys (mid-rotation,
// Matter §11.2.10.6) yields one operational-IPK candidate per epoch
// key. Mirrors matter.js FabricGroups.ts:125-156 (`setFromGroupKeySet`
// HKDF-derives an operational key for every present EpochKeyN) and
// FabricManager.ts:302-317 (`findFabricFromDestinationId` tries every
// one).
func TestIpkOperationalCandidates_ReturnsEveryEpochKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)

	var compressedID [8]byte
	copy(compressedID[:], []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})

	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0x1234000000000001,
		NodeID:        0x1,
		CompressedID:  compressedID,
		RootPublicKey: make([]byte, 65),
		VendorID:      0xFFF1,
		Label:         "rotation-fabric",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	epochKey0 := make([]byte, 16)
	epochKey1 := make([]byte, 16)
	for i := range epochKey0 {
		epochKey0[i] = byte(i + 1) // new IPK
		epochKey1[i] = byte(i + 100)
	}
	if err := store.UpsertGroupKeySet(ctx, matterstore.GroupKeySet{
		FabricIndex:    idx,
		GroupKeySetID:  0,
		SecurityPolicy: matterstore.SecurityPolicyTrustFirst,
		EpochKey0:      epochKey0,
		EpochStart0:    0,
		EpochKey1:      epochKey1,
		EpochStart1:    1000,
	}); err != nil {
		t.Fatalf("UpsertGroupKeySet: %v", err)
	}

	got, err := ipkOperationalCandidates(ctx, store, idx, compressedID, []byte("unused-fallback-1234567"))
	if err != nil {
		t.Fatalf("ipkOperationalCandidates: %v", err)
	}
	wantKey0, err := deriveOperationalIPK(epochKey0, compressedID)
	if err != nil {
		t.Fatalf("deriveOperationalIPK(epochKey0): %v", err)
	}
	wantKey1, err := deriveOperationalIPK(epochKey1, compressedID)
	if err != nil {
		t.Fatalf("deriveOperationalIPK(epochKey1): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(candidates) = %d, want 2 (EpochKey0 + EpochKey1)", len(got))
	}
	if got[0] != wantKey0 {
		t.Errorf("candidates[0] = %x, want %x (derived from EpochKey0)", got[0], wantKey0)
	}
	if got[1] != wantKey1 {
		t.Errorf("candidates[1] = %x, want %x (derived from EpochKey1)", got[1], wantKey1)
	}
}

// TestIpkOperationalCandidates_FallsBackToRawIPK verifies that a
// fabric with no persisted GroupKeySetID 0 row (pre-rotation, or an
// identity loaded before the AddNOC write path seeded one) falls back
// to deriving a single candidate from the raw IPK, matching the
// pre-fix behaviour.
func TestIpkOperationalCandidates_FallsBackToRawIPK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)

	var compressedID [8]byte
	copy(compressedID[:], []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x01, 0x02})
	rawIPK := make([]byte, 16)
	for i := range rawIPK {
		rawIPK[i] = byte(i)
	}

	got, err := ipkOperationalCandidates(ctx, store, 7, compressedID, rawIPK)
	if err != nil {
		t.Fatalf("ipkOperationalCandidates: %v", err)
	}
	want, err := deriveOperationalIPK(rawIPK, compressedID)
	if err != nil {
		t.Fatalf("deriveOperationalIPK: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 (raw-IPK fallback)", len(got))
	}
	if got[0] != want {
		t.Errorf("candidates[0] = %x, want %x", got[0], want)
	}
}

// ── caseDestinationResolver: multi-candidate matching ─────────────────────

// TestResolveSigma1Destination_MatchesRotatedIPKCandidate reproduces
// an in-progress IPK rotation: a fabric's GroupKeySetID 0 carries two
// epoch keys (the new one at index 0, the still-valid old one at
// index 1) and an inbound Sigma1's DestinationID was computed by the
// initiator using the OLD key. A resolver that only tries the newest
// candidate (the pre-fix behaviour) never matches; chip's
// CASESession.cpp:958-976 (`FindLocalNodeFromDestinationId`) and
// matter.js FabricManager.ts:302-317 both try every key of keyset 0.
// The fix must also report back the SPECIFIC candidate that matched
// (not the newest) because Sigma2/Sigma3 salts are keyed on it, per
// chip CASESession.cpp:975-976 (`ipkSpan` is copied into `mIPK` on
// match, then reused unmodified for `ConstructSaltSigma2/3`).
func TestResolveSigma1Destination_MatchesRotatedIPKCandidate(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPublicKey := make([]byte, 65)
	rootPublicKey[0] = 0x04 // uncompressed-point marker; only the prefix matters for this test

	newIPK := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	oldIPK := [16]byte{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115}

	identity := &sigma.Identity{
		PrivateKey:  priv,
		NodeID:      0x1122334455667788,
		FabricID:    0xAABBCCDD00112233,
		FabricIndex: 1,
		IPK:         newIPK, // the "current" key — must NOT be the one the resolver reports on an old-key match
	}
	entry := &caseFabricEntry{
		identity:      identity,
		rootPublicKey: rootPublicKey,
		fabricIndex:   1,
		ipkCandidates: [][16]byte{newIPK, oldIPK},
	}

	var initiatorRandom [sigma.RandomSize]byte
	for i := range initiatorRandom {
		initiatorRandom[i] = byte(i)
	}
	// The initiator (still inside the rotation grace window) signs its
	// Sigma1 DestinationID with the OLD epoch key.
	destinationID := sigma.ComputeDestinationID(oldIPK, initiatorRandom, rootPublicKey, identity.FabricID, identity.NodeID)

	fabrics := map[uint8]*caseFabricEntry{1: entry}
	var mu sync.RWMutex
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &fabrics,
		logger:  slog.New(slog.DiscardHandler),
	}

	resolved, _, ok := resolver.ResolveSigma1Destination(destinationID, initiatorRandom)
	if !ok {
		t.Fatal("expected a match against the old (pre-rotation) IPK candidate, got no match")
	}
	if resolved.IPK != oldIPK {
		t.Errorf("resolved.IPK = %x, want %x (the epoch key that actually matched, per chip CASESession.cpp FindLocalNodeFromDestinationId)", resolved.IPK, oldIPK)
	}
}
