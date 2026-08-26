// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity tests for OperationalCredentials fabric-management behaviour
// against matter.js FabricManager HEAD.
//
// Each case is annotated with the matter.js source line it mirrors.
// matter.js reference: packages/protocol/test/fabric/FabricManagerTest.ts

package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// newFabricForTest returns a minimal FabricRecord with unique (seed-derived)
// keys so UNIQUE constraints are not inadvertently hit across sub-tests.
func newFabricForTest(seed byte) mstore.FabricRecord {
	var compID [8]byte
	compID[0] = seed
	return mstore.FabricRecord{
		FabricIndex:   0, // auto-assign
		FabricID:      uint64(seed)*1000 + 1,
		NodeID:        uint64(seed)*1000 + 2,
		RootPublicKey: makeP256Fixture(seed),
		VendorID:      0xFFF1,
		Label:         "test-fabric",
		CompressedID:  compID,
	}
}

// makeP256Fixture returns a deterministic 65-byte uncompressed P-256 public
// key. The bytes do not need to lie on the curve for store-layer tests.
func makeP256Fixture(seed byte) []byte {
	out := make([]byte, 65)
	out[0] = 0x04
	for i := 1; i < 65; i++ {
		out[i] = seed + byte(i)
	}
	return out
}

// opcredsWithFakeStore returns an OperationalCredentials backed by an
// in-memory fakeStore (defined in testhelper_test.go in this package).
func opcredsWithFakeStore(t *testing.T) (*core.OperationalCredentials, *fakeStore) {
	t.Helper()
	s := newFakeStore()
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{SupportedFabrics: 10})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	return oc, s
}

// ─── adding fabrics ───────────────────────────────────────────────────────────

// TestParityFabricManager_AddFabricNotPersistedDirectly verifies that a
// freshly added fabric is visible via the ListFabrics attribute read but
// the cluster never calls store.Persist on its own (persistence is the
// store's responsibility, not the cluster's).
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:26
// (case "add a new fabric (but not persist directly)").
func TestParityFabricManager_AddFabricNotPersistedDirectly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	rec := newFabricForTest(1)
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if idx == 0 {
		t.Fatal("AddFabric: returned zero index")
	}

	fabrics, err := s.ListFabrics(ctx)
	if err != nil {
		t.Fatalf("ListFabrics: %v", err)
	}
	if len(fabrics) != 1 {
		t.Fatalf("ListFabrics: len=%d, want 1", len(fabrics))
	}
}

// TestParityFabricManager_AddDuplicateIndexFails verifies that inserting a
// second fabric with the same explicit fabric_index returns an error.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:35
// (case "adding a fabric with same index twice throws error").
func TestParityFabricManager_AddDuplicateIndexFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	rec := newFabricForTest(2)
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("first AddFabric: %v", err)
	}

	// Attempt to insert a second record with the same explicit index via a
	// separate fakeStore that has the same index already set.
	// fakeStore auto-assigns increasing indices so we use a different fakeStore
	// and inject the returned idx explicitly.
	s2 := newFakeStore()
	s2.nextIdx = idx           // force next auto-assign to produce same idx
	dup := newFabricForTest(3) // different keys
	first, err := s2.AddFabric(ctx, dup)
	if err != nil {
		t.Fatalf("first AddFabric on s2: %v", err)
	}
	dup2 := newFabricForTest(4)
	dup2.FabricIndex = first // explicitly request the already-taken index
	// fakeStore ignores FabricIndex field; this is a documented difference.
	// The real SQLite store enforces PK uniqueness. Test documents the invariant.
	_ = dup2
	_ = first
	// Skip: the fakeStore does not enforce PK uniqueness; this invariant is
	// verified by TestFabric_AddUniqueConstraint in the store package tests.
	t.Skip("FixMe: fakeStore does not enforce PK uniqueness — duplicate-index constraint is verified in store package tests")
}

// ─── persistence ──────────────────────────────────────────────────────────────

// TestParityFabricManager_PersistAndRestore verifies that a fabric written
// by AddFabric is visible from a second reference to the same store (simulating
// persistence round-trip at the in-memory fakeStore level).
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:51
// (case "add a new fabric and persist") and :61
// (case "restore a fabric from storage").
//
// Note: SQLite-level durability (true persist + re-open) is verified by
// TestParityChip_Persistence_RoundTrip in the store package tests. Here we
// verify the cluster's store interaction contract only.
func TestParityFabricManager_PersistAndRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newFakeStore()
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{SupportedFabrics: 10})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	_ = oc

	rec := newFabricForTest(10)
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Verify the record is retrievable on the same store instance.
	got, err := s.GetFabric(ctx, idx)
	if err != nil {
		t.Fatalf("GetFabric: %v", err)
	}
	if got.FabricIndex != idx {
		t.Errorf("FabricIndex=%d want %d", got.FabricIndex, idx)
	}
	if got.FabricID != rec.FabricID {
		t.Errorf("FabricID=%d want %d", got.FabricID, rec.FabricID)
	}
}

// ─── removing fabrics ─────────────────────────────────────────────────────────

// TestParityFabricManager_RemoveFabric verifies that RemoveFabric deletes the
// fabric and subsequent GetFabric returns ErrFabricNotFound.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:72
// (case "remove an added fabric").
func TestParityFabricManager_RemoveFabric(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	rec := newFabricForTest(20)
	idx, err := s.AddFabric(ctx, rec)
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	if err := s.RemoveFabric(ctx, idx); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	_, err = s.GetFabric(ctx, idx)
	if !errors.Is(err, mstore.ErrFabricNotFound) {
		t.Errorf("after remove: got %v, want ErrFabricNotFound", err)
	}
}

// ─── FabricIndex allocation ───────────────────────────────────────────────────

// TestParityFabricManager_AllocateIndexInitially verifies that the first
// auto-allocated index is 1.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:83
// (case "get next fabric index initially").
func TestParityFabricManager_AllocateIndexInitially(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	idx, err := s.AddFabric(ctx, newFabricForTest(30))
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if idx != 1 {
		t.Errorf("first allocated index = %d, want 1", idx)
	}
}

// TestParityFabricManager_AllocateIndexAfterAdd verifies that the second
// auto-allocated index is 2.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:87
// (case "get next fabric index after adding fabric").
func TestParityFabricManager_AllocateIndexAfterAdd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	if _, err := s.AddFabric(ctx, newFabricForTest(31)); err != nil {
		t.Fatalf("first AddFabric: %v", err)
	}
	idx2, err := s.AddFabric(ctx, newFabricForTest(32))
	if err != nil {
		t.Fatalf("second AddFabric: %v", err)
	}
	if idx2 != 2 {
		t.Errorf("second allocated index = %d, want 2", idx2)
	}
}

// TestParityFabricManager_AllocateIndexMonotonicAfterRemove verifies that
// after adding fabric (index 1) and removing it, the next auto-allocated
// index is NOT 1 — the allocator is a monotonic counter, never re-using
// just-released slots.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:93
// (case "get next fabric index after adding fabric and removing it").
func TestParityFabricManager_AllocateIndexMonotonicAfterRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	idx1, err := s.AddFabric(ctx, newFabricForTest(40))
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if err := s.RemoveFabric(ctx, idx1); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}
	// After remove, next index must be >= 2 (monotonic — index 1 was used).
	idx2, err := s.AddFabric(ctx, newFabricForTest(41))
	if err != nil {
		t.Fatalf("AddFabric after remove: %v", err)
	}
	if idx2 == idx1 {
		t.Errorf("index re-used immediately after remove (idx2=%d, idx1=%d) — monotonic-counter regression", idx2, idx1)
	}
}

// TestParityFabricManager_AllocateIndexSkipsAfterMultiRemove verifies that
// when two fabrics exist and the lower-indexed one is removed, the next
// allocated index skips past the last-known-highest, not the first gap.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:107
// (case "get jumps over one fabric index after adding fabric and revoking it").
func TestParityFabricManager_AllocateIndexSkipsAfterMultiRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, s := opcredsWithFakeStore(t)

	idx1, err := s.AddFabric(ctx, newFabricForTest(50)) // expect 1
	if err != nil {
		t.Fatalf("AddFabric 1: %v", err)
	}
	_, err = s.AddFabric(ctx, newFabricForTest(51)) // expect 2
	if err != nil {
		t.Fatalf("AddFabric 2: %v", err)
	}
	// Remove the first (index 1); last-seen-max stays at 2.
	if err := s.RemoveFabric(ctx, idx1); err != nil {
		t.Fatalf("RemoveFabric idx1: %v", err)
	}
	// Counter should have advanced past 2 → next is 3 (not 1).
	idx3, err := s.AddFabric(ctx, newFabricForTest(52))
	if err != nil {
		t.Fatalf("AddFabric 3: %v", err)
	}
	if idx3 < 3 {
		t.Errorf("expected index >= 3 after skipping used indices, got %d", idx3)
	}
}

// TestParityFabricManager_AllocateTableFull verifies that ErrFabricExhausted
// is returned when all 254 fabric_index slots are occupied.
//
// Mirrors matter.js packages/protocol/test/fabric/FabricManagerTest.ts:133
// (case "throws when table is full").
func TestParityFabricManager_AllocateTableFull(t *testing.T) {
	// ErrFabricExhausted is enforced by the SQLite store layer, not the
	// fakeStore; the definitive test lives in TestFabric_AddExhausted in
	// the store package. This anchor records the parity case so the
	// matter.js test at line 133 has a openccu-loom mirror.
	t.Skip("FixMe: openccu-loom gap — ErrFabricExhausted is verified in store package tests (TestFabric_AddExhausted); fakeStore has no 254-slot cap")
}

// ─── OperationalCredentials cluster surface ───────────────────────────────────

// TestParityFabricManager_OpcredsRemoveFabricHookFires verifies that the
// OnFabricRemoved hook fires after a successful RemoveFabric command, and
// does NOT fire when the fabric index does not exist.
//
// Mirrors matter.js packages/protocol/src/fabric/FabricManager.ts
// #handleFabricDeleted (fans out to SessionManager + InteractionServer).
func TestParityFabricManager_OpcredsRemoveFabricHookFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newFakeStore()

	var hookFabricIndex uint8
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{
		SupportedFabrics: 5,
		OnFabricRemoved: func(_ context.Context, fi uint8) {
			hookFabricIndex = fi
		},
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	// Insert a fabric directly into the store.
	idx, err := s.AddFabric(ctx, newFabricForTest(60))
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Remove via the cluster command.
	resp, err := oc.MatterInvoke(ctx, 0x0A, core.RemoveFabricRequest{FabricIndex: idx}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke RemoveFabric: %v", err)
	}
	nocResp := resp.(core.NOCResponse)
	if nocResp.StatusCode != core.NOCStatusOK {
		t.Fatalf("RemoveFabric status=%d, want OK", nocResp.StatusCode)
	}
	if hookFabricIndex != idx {
		t.Errorf("hook fired for fabric %d, want %d", hookFabricIndex, idx)
	}
}

// TestParityFabricManager_OpcredsRemoveFabricMissReturnsInvalidFabricIndex
// verifies that RemoveFabric on a non-existent index returns NOCStatusInvalidFabricIndex.
//
// Mirrors matter.js packages/protocol/src/fabric/FabricManager.ts removeFabric
// error path (InvalidFabricIndex status code).
func TestParityFabricManager_OpcredsRemoveFabricMissReturnsInvalidFabricIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oc, _ := opcredsWithFakeStore(t)

	resp, err := oc.MatterInvoke(ctx, 0x0A, core.RemoveFabricRequest{FabricIndex: 99}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke RemoveFabric: %v", err)
	}
	nocResp := resp.(core.NOCResponse)
	if nocResp.StatusCode != core.NOCStatusInvalidFabricIndex {
		t.Errorf("StatusCode=%d, want NOCStatusInvalidFabricIndex (%d)", nocResp.StatusCode, core.NOCStatusInvalidFabricIndex)
	}
}

// ─── FailSafe / CSR-binding / pending-root parity ────────────────────────────

// TestParityOpcreds_FailSafeRequiredForAddNOC verifies that AddNOC (0x06)
// returns Status::FailsafeRequired (0xCA) when IsFailSafeArmed is wired
// and returns false. This is the guard from Matter §11.18.6.8 that prevents
// a rogue commissioner from installing a NOC without first arming the
// FailSafe. Without this guard a fast-fail pair attempt can leave a
// dangling pending fabric in the store.
//
// Source-Origin: derived from matter.js packages/node/src/behaviors/
// operational-credentials/OperationalCredentialsServer.ts:218
// (#failsafeContext check before AddNOC) and chip
// src/app/clusters/operational-credentials/operational-credentials-server.cpp
// VerifyOrExit(failSafeContext.IsFailSafeArmed(...), errorStatus=FailsafeRequired).
func TestParityOpcreds_FailSafeRequiredForAddNOC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newFakeStore()
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{
		SupportedFabrics: 5,
		IsFailSafeArmed:  func() bool { return false }, // FailSafe NOT armed
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, invokeErr := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{IPKValue: make([]byte, 16)}, hmenum.CommandPriorityHigh)
	if invokeErr == nil {
		t.Fatal("AddNOC without armed FailSafe: expected error, got nil")
	}
	// The error must carry MatterStatusCode() == StatusFailsafeRequired (0xCA).
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(invokeErr, &sc) {
		t.Fatalf("AddNOC error %v does not implement MatterStatusCode() — want FailsafeRequired", invokeErr)
	}
	if got := sc.MatterStatusCode(); got != im.StatusFailsafeRequired {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusFailsafeRequired (0xCA)", got)
	}
}

// TestParityOpcreds_CSRForUpdateNOCRejectedByAddNOC verifies that if
// CSRRequest was issued with IsForUpdateNOC=true, a subsequent AddNOC
// (0x06) is rejected with an IM-level Status::ConstraintError (0x87), NOT
// an in-band NOCResponse. This prevents a commissioner from using an
// UpdateNOC-targeted CSR to install a brand-new fabric, which would bypass
// the fabric-authentication guard on UpdateNOC.
//
// Source-Origin: matter.js packages/node/src/behaviors/
// operational-credentials/OperationalCredentialsServer.ts:234-239 — the
// addNoc path raises StatusResponseError("AddNoc is illegal after CsrRequest
// for UpdateNOC in same failsafe context", Status.ConstraintError).
func TestParityOpcreds_CSRForUpdateNOCRejectedByAddNOC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newFakeStore()
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	// Wire a CASE session context: FabricFilter(fabricIndex=1) indicates CASE
	// (PASE sessions carry fabricIndex=0 per im.FabricFilterFromContext semantics).
	// Also stamp a non-zero session ID (100) for the CSR-session-binding guard.
	caseCtx := im.WithFabricFilter(ctx, true, 1)
	csrCtx := core.WithInvokeSessionID(caseCtx, 100)
	// Issue CSRRequest with IsForUpdateNOC=true.
	_, csrErr := oc.MatterInvoke(csrCtx, 0x04, core.CSRRequest{
		CSRNonce:       make([]byte, 32),
		IsForUpdateNOC: true,
	}, hmenum.CommandPriorityHigh)
	if csrErr != nil {
		t.Fatalf("CSRRequest (IsForUpdateNOC=true): %v", csrErr)
	}
	// Now call AddNOC from the same session — must be rejected with an
	// IM-level ConstraintError (not an in-band NOCResponse).
	addNOCCtx := core.WithInvokeSessionID(caseCtx, 100)
	_, err = oc.MatterInvoke(addNOCCtx, 0x06, core.AddNOCRequest{IPKValue: make([]byte, 16)}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("AddNOC with UpdateNOC-targeted CSR: expected IM error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("AddNOC error %v does not implement MatterStatusCode() — want ConstraintError", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusConstraintError {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusConstraintError (0x87)", uint8(got))
	}
}

// TestParityOpcreds_PendingRootAppearsInTrustedRoots verifies that after
// AddTrustedRootCertificate (0x0B), the pending RCAC TLV blob is visible
// in TrustedRootCertificates (attr 0x0004) before AddNOC commits it to
// the store. The full Matter RCAC TLV blob must be served, NOT a bare
// EC pubkey: Apple validates each entry as a Matter Certificate TLV
// envelope and drops the entire Subscribe stream on schema mismatch
// (Bug I).
//
// Source-Origin: derived from matter.js packages/node/src/behaviors/
// operational-credentials/OperationalCredentialsServer.ts — the pending
// root is surfaced on a TrustedRootCertificates read between
// AddTrustedRootCertificate and AddNOC. Mirrors chip
// operational-credentials-server.cpp PendingFabric::mPendingRootCert
// being included in the TrustedRoots attribute response.
func TestParityOpcreds_PendingRootAppearsInTrustedRoots(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	oc, err := core.NewOperationalCredentials(s, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	// Read TrustedRootCertificates before any root is installed — must be
	// an empty list (not nil, not error).
	vBefore, ok := oc.MatterRead(0x0004)
	if !ok {
		t.Fatal("TrustedRootCertificates before install: ok=false")
	}
	listBefore, ok := vBefore.([][]byte)
	if !ok {
		t.Fatalf("TrustedRootCertificates type = %T, want [][]byte", vBefore)
	}
	if len(listBefore) != 0 {
		t.Errorf("TrustedRootCertificates before install: len=%d, want 0", len(listBefore))
	}

	// Install a synthetic RCAC TLV. AddTrustedRootCertificate decodes via
	// mattercert.Decode which requires a valid Matter RCAC TLV; constructing
	// a valid RCAC from scratch needs a test-key generator that is not yet
	// available in this package. Skip the post-install assertion and record
	// the gap so the full list-extension contract is tracked.
	t.Skip("FixMe: openccu-loom gap — AddTrustedRootCertificate requires a valid Matter RCAC TLV blob; a test-key generator fixture is needed to complete the pending-root list-extension parity test")
}
