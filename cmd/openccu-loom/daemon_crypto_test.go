// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/secure/sigma"
	matterstore "github.com/SukramJ/go-fabric/store"
)

// ── deriveOperationalIPK ──────────────────────────────────────────────────────

func TestDeriveOperationalIPK_ValidInput(t *testing.T) {
	t.Parallel()
	rawIPK := make([]byte, 16)
	for i := range rawIPK {
		rawIPK[i] = byte(i + 1)
	}
	var compressedID [8]byte
	compressedID[0] = 0xCA
	compressedID[7] = 0xFE

	out, err := deriveOperationalIPK(rawIPK, compressedID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must be 16 bytes and non-zero.
	allZero := true
	for _, b := range out {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("expected non-zero derived IPK")
	}
}

func TestDeriveOperationalIPK_WrongLength_Errors(t *testing.T) {
	t.Parallel()
	// 15 bytes → should error.
	_, err := deriveOperationalIPK(make([]byte, 15), [8]byte{})
	if err == nil {
		t.Fatal("expected error for 15-byte raw IPK")
	}
}

func TestDeriveOperationalIPK_Deterministic(t *testing.T) {
	t.Parallel()
	rawIPK := bytes.Repeat([]byte{0xAB}, 16)
	var id [8]byte
	id[0] = 0x11

	a, err := deriveOperationalIPK(rawIPK, id)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	b, err := deriveOperationalIPK(rawIPK, id)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if a != b {
		t.Errorf("not deterministic: %x != %x", a, b)
	}
}

// ── privKeyFromScalar ─────────────────────────────────────────────────────────

func TestPrivKeyFromScalar_ValidInput(t *testing.T) {
	t.Parallel()
	// Build a random-ish scalar (non-zero, < curve order for P-256).
	// Use a fixed value so the test is deterministic.
	scalar := make([]byte, 32)
	for i := range scalar {
		scalar[i] = byte(i + 1)
	}
	priv, err := privKeyFromScalar(scalar)
	if err != nil {
		t.Fatalf("privKeyFromScalar: %v", err)
	}
	if priv == nil {
		t.Fatal("expected non-nil private key")
	}
	gotScalar, err := priv.Bytes()
	if err != nil {
		t.Fatalf("priv.Bytes: %v", err)
	}
	if !bytes.Equal(gotScalar, scalar) {
		t.Errorf("private scalar = %x, want %x", gotScalar, scalar)
	}
	// The public half is derived, so the only thing to assert about it is
	// that it exists in the canonical uncompressed shape the CASE paths
	// put on the wire.
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("pub.Bytes: %v", err)
	}
	if len(pub) != 65 || pub[0] != 0x04 {
		t.Errorf("public key = %d bytes prefixed %#x, want 65 bytes prefixed 0x04", len(pub), pub[0])
	}
}

func TestPrivKeyFromScalar_WrongLength_Errors(t *testing.T) {
	t.Parallel()
	_, err := privKeyFromScalar(make([]byte, 31))
	if err == nil {
		t.Fatal("expected error for 31-byte scalar")
	}
}

// TestPrivKeyFromScalar_OutOfRangeScalar_IsRejected pins that a stored
// scalar outside [1, n-1] is refused rather than turned into a key.
//
// The predecessor checked the length and nothing else, so a zero scalar —
// an empty or truncated column in the matter store — produced a
// "private key" whose public point is the identity. CASE would then run
// its whole handshake and sign with it, and every signature it produced
// would be worthless while the daemon reported a healthy fabric. The
// scalar is now parsed, and parsing is what validates it.
func TestPrivKeyFromScalar_OutOfRangeScalar_IsRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		scalar []byte
	}{
		{"zero", make([]byte, 32)},
		{"curve order", p256Order()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := privKeyFromScalar(tc.scalar); err == nil {
				t.Fatalf("%s scalar was accepted as a CASE identity", tc.name)
			}
		})
	}
}

// p256Order returns n, the order of the P-256 base point, as the 32-byte
// scalar the matter store would hold. n itself is the first value above
// the valid range.
func p256Order() []byte {
	return elliptic.P256().Params().N.Bytes()
}

// ── pickFabric ────────────────────────────────────────────────────────────────

func TestPickFabric_WantFabricIDMatch(t *testing.T) {
	t.Parallel()
	fabrics := []matterstore.FabricRecord{
		{FabricID: 0xCAFE, FabricIndex: 3},
		{FabricID: 0xBEEF, FabricIndex: 1},
		{FabricID: 0xDEAD, FabricIndex: 2},
	}
	got := pickFabric(fabrics, 0xBEEF)
	if got == nil || got.FabricID != 0xBEEF {
		t.Errorf("pickFabric: got %+v, want FabricID=0xBEEF", got)
	}
}

func TestPickFabric_WantFabricIDNoMatch_FallsBackToLowest(t *testing.T) {
	t.Parallel()
	fabrics := []matterstore.FabricRecord{
		{FabricID: 0xCAFE, FabricIndex: 3},
		{FabricID: 0xBEEF, FabricIndex: 1},
		{FabricID: 0xDEAD, FabricIndex: 2},
	}
	// 0x9999 is not in the list → lowest FabricIndex (1 → FabricID 0xBEEF).
	got := pickFabric(fabrics, 0x9999)
	if got == nil || got.FabricIndex != 1 {
		t.Errorf("pickFabric fallback: got FabricIndex=%d, want 1", got.FabricIndex)
	}
}

func TestPickFabric_WantFabricIDZero_FallsBackToLowest(t *testing.T) {
	t.Parallel()
	fabrics := []matterstore.FabricRecord{
		{FabricID: 0xCAFE, FabricIndex: 5},
		{FabricID: 0xBEEF, FabricIndex: 2},
	}
	// 0 → always picks lowest FabricIndex.
	got := pickFabric(fabrics, 0)
	if got == nil || got.FabricIndex != 2 {
		t.Errorf("pickFabric zero: got FabricIndex=%d, want 2", got.FabricIndex)
	}
}

func TestPickFabric_SingleEntry(t *testing.T) {
	t.Parallel()
	fabrics := []matterstore.FabricRecord{
		{FabricID: 0xDEAD, FabricIndex: 7},
	}
	got := pickFabric(fabrics, 0)
	if got == nil || got.FabricID != 0xDEAD {
		t.Errorf("pickFabric single: got %+v", got)
	}
}

// ── loadFabricRootPublicKey ───────────────────────────────────────────────────

func TestLoadFabricRootPublicKey_NilStore_Errors(t *testing.T) {
	t.Parallel()
	_, err := loadFabricRootPublicKey(context.Background(), nil, 1)
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestLoadFabricRootPublicKey_LiveStore_UnknownFabric_Errors(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	// FabricIndex 99 doesn't exist.
	_, err := loadFabricRootPublicKey(context.Background(), store, 99)
	if err == nil {
		t.Fatal("expected error for unknown fabric index")
	}
}

// ── loadAdditionalFabricsForCase ──────────────────────────────────────────────

func TestLoadAdditionalFabricsForCase_NilStore_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	caseFabrics := map[uint8]*caseFabricEntry{}
	var mu sync.RWMutex
	got := loadAdditionalFabricsForCase(context.Background(), nil, 1, caseFabrics, &mu, logger)
	if got != 0 {
		t.Errorf("expected 0 for nil store, got %d", got)
	}
}

func TestLoadAdditionalFabricsForCase_EmptyStore_ReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	caseFabrics := map[uint8]*caseFabricEntry{}
	var mu sync.RWMutex
	// Empty store → no fabrics to load.
	got := loadAdditionalFabricsForCase(context.Background(), store, 1, caseFabrics, &mu, logger)
	if got != 0 {
		t.Errorf("expected 0 for empty store, got %d", got)
	}
}

// ── caseDestinationResolver.ResolveSigma1Destination ─────────────────────────

func TestCaseDestinationResolver_NilFabrics_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: nil,
		logger:  slog.Default(),
	}
	var dest [32]byte
	var random [32]byte
	_, _, ok := resolver.ResolveSigma1Destination(dest, random)
	if ok {
		t.Error("expected ok=false for nil fabrics map")
	}
}

func TestCaseDestinationResolver_EmptyFabrics_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	empty := map[uint8]*caseFabricEntry{}
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &empty,
		logger:  slog.Default(),
	}
	var dest [32]byte
	var random [32]byte
	_, _, ok := resolver.ResolveSigma1Destination(dest, random)
	if ok {
		t.Error("expected ok=false for empty fabrics map")
	}
}

func TestCaseDestinationResolver_NilEntryInMap_Skipped(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	m := map[uint8]*caseFabricEntry{
		1: nil, // nil entry — must be skipped without panic
	}
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  slog.Default(),
	}
	var dest [32]byte
	var random [32]byte
	_, _, ok := resolver.ResolveSigma1Destination(dest, random)
	if ok {
		t.Error("expected ok=false when only entry is nil")
	}
}

// TestCaseDestinationResolver_MatchingDestination exercises the
// cand == destinationID success path and the `r.logger != nil` debug log.
func TestCaseDestinationResolver_MatchingDestination_ReturnsTrue(t *testing.T) {
	t.Parallel()

	// Build a synthetic caseFabricEntry whose ComputeDestinationID output
	// we can precompute so we can pass the matching dest to ResolveSigma1Destination.
	var opIPK [16]byte
	for i := range opIPK {
		opIPK[i] = byte(i + 1)
	}
	var initiatorRandom [sigma.RandomSize]byte
	for i := range initiatorRandom {
		initiatorRandom[i] = byte(i + 0x10)
	}
	// Use a 65-byte uncompressed P-256 point (0x04 || 32 zero bytes || 32 zero bytes).
	rootPub := make([]byte, 65)
	rootPub[0] = 0x04

	const fabricID uint64 = 0x1122334455667788
	const nodeID uint64 = 0xAABBCCDDEEFF0011

	expectedDest := sigma.ComputeDestinationID(opIPK, initiatorRandom, rootPub, fabricID, nodeID)

	identity := &sigma.Identity{
		IPK:      opIPK,
		FabricID: fabricID,
		NodeID:   nodeID,
	}
	entry := &caseFabricEntry{
		identity:      identity,
		rootPublicKey: rootPub,
		fabricIndex:   1,
	}

	var mu sync.RWMutex
	m := map[uint8]*caseFabricEntry{1: entry}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  logger,
	}

	retIdentity, _, ok := resolver.ResolveSigma1Destination(expectedDest, initiatorRandom)
	if !ok {
		t.Fatal("expected ok=true for matching destination")
	}
	// The resolver returns a copy stamped with the specific IPK
	// candidate that matched (Matter §11.2.10.6 IPK-rotation grace
	// window: the matching epoch key may not be the newest one), not
	// the original pointer — compare the fields this test populates
	// instead of pointer identity.
	if retIdentity == nil ||
		retIdentity.IPK != identity.IPK ||
		retIdentity.FabricID != identity.FabricID ||
		retIdentity.NodeID != identity.NodeID {
		t.Error("returned identity mismatch")
	}
	// The logger.Debug call should have fired — check for the key.
	if !bytes.Contains(buf.Bytes(), []byte("matter.bridge.case.identity_resolved")) {
		t.Errorf("expected identity_resolved debug log; got:\n%s", buf.String())
	}
}

// TestCaseDestinationResolver_NoMatchingDestination_LogsUnresolved exercises
// the `r.logger != nil` unresolved debug log at the end.
func TestCaseDestinationResolver_NoMatchingDestination_LogsUnresolved(t *testing.T) {
	t.Parallel()

	// Create a real entry with wrong destination so the loop ends without match.
	var opIPK [16]byte
	opIPK[0] = 0xAB
	rootPub := make([]byte, 65)
	rootPub[0] = 0x04
	identity := &sigma.Identity{
		IPK:      opIPK,
		FabricID: 0x1,
		NodeID:   0x2,
	}
	entry := &caseFabricEntry{
		identity:      identity,
		rootPublicKey: rootPub,
		fabricIndex:   1,
	}

	var mu sync.RWMutex
	m := map[uint8]*caseFabricEntry{1: entry}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  logger,
	}

	var wrongDest [32]byte
	var random [sigma.RandomSize]byte
	_, _, ok := resolver.ResolveSigma1Destination(wrongDest, random)
	if ok {
		t.Error("expected ok=false for non-matching destination")
	}
	if !bytes.Contains(buf.Bytes(), []byte("matter.bridge.case.identity_unresolved")) {
		t.Errorf("expected identity_unresolved debug log; got:\n%s", buf.String())
	}
}

// ── caseDestinationResolver.ResolveFabricIndex ───────────────────────────────
//
// ResolveFabricIndex is the Sigma2_Resume-path companion lookup: the
// resumption record names its fabric directly (no DestinationID to
// recompute), so the resolver is just a map read keyed by fabric index.

func TestCaseDestinationResolver_ResolveFabricIndex_NilFabrics_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: nil,
		logger:  slog.Default(),
	}
	_, _, ok := resolver.ResolveFabricIndex(7)
	if ok {
		t.Error("expected ok=false for nil fabrics map pointer")
	}
}

func TestCaseDestinationResolver_ResolveFabricIndex_UnknownIndex_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	m := map[uint8]*caseFabricEntry{
		1: {identity: &sigma.Identity{FabricIndex: 1}, verifier: trustAnyPeerVerifier{}, fabricIndex: 1},
	}
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  slog.Default(),
	}
	_, _, ok := resolver.ResolveFabricIndex(7)
	if ok {
		t.Error("expected ok=false for an index absent from the fabrics map")
	}
}

func TestCaseDestinationResolver_ResolveFabricIndex_NilEntry_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	m := map[uint8]*caseFabricEntry{
		7: nil, // entry present but nil — must be treated as a miss, not a panic
	}
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  slog.Default(),
	}
	_, _, ok := resolver.ResolveFabricIndex(7)
	if ok {
		t.Error("expected ok=false for a nil entry at the requested fabric index")
	}
}

func TestCaseDestinationResolver_ResolveFabricIndex_Hit_ReturnsIdentityAndVerifier(t *testing.T) {
	t.Parallel()
	var mu sync.RWMutex
	identity := &sigma.Identity{FabricIndex: 7}
	verifier := trustAnyPeerVerifier{}
	m := map[uint8]*caseFabricEntry{
		7: {identity: identity, verifier: verifier, fabricIndex: 7},
	}
	resolver := caseDestinationResolver{
		mu:      &mu,
		fabrics: &m,
		logger:  slog.Default(),
	}
	gotIdentity, gotVerifier, ok := resolver.ResolveFabricIndex(7)
	if !ok {
		t.Fatal("expected ok=true for a present fabric index")
	}
	if gotIdentity != identity {
		t.Error("returned identity does not match the stored entry")
	}
	if gotVerifier != verifier {
		t.Error("returned verifier does not match the stored entry")
	}
}
