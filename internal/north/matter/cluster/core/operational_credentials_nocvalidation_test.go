// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

// Behaviour tests for the NOC-validation parity added to AddNOC /
// UpdateNOC / RemoveFabric: subject-public-key verification against the
// pending CSR key, the (FabricID, RootPublicKey) fabric-conflict guard,
// UpdateNOC's chain-verification-against-the-stored-root + FabricID-pin
// checks, and the onFabricWithdraw hook fired on a NodeID-changing
// UpdateNOC and on RemoveFabric.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// TestAddNOC_MintedOverWrongKeyRejectedAsInvalidPublicKey verifies that
// AddNOC rejects a NOC minted over a key other than the pending CSR key
// with NOCStatusInvalidPublicKey. Mirrors matter.js Fabric.ts:524-526
// (PublicKeyError → InvalidPublicKey) and chip FabricTable.cpp:890
// `existingOpKey->Pubkey().Matches(nocPubKey)`.
func TestAddNOC_MintedOverWrongKeyRejectedAsInvalidPublicKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oc := newOpcreds(t)

	// buildTestNOCAndRoot mints nocRaw over a throwaway key that is NOT
	// the pending CSR key CSRRequest below installs — the exact shape
	// this guard exists to reject.
	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}
	if _, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{CSRNonce: make([]byte, 32)}, 0); err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	}, 0)
	if err != nil {
		t.Fatalf("AddNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidPublicKey {
		t.Fatalf("StatusCode = %d (%s), want NOCStatusInvalidPublicKey (%d)",
			nr.StatusCode, nr.DebugText, core.NOCStatusInvalidPublicKey)
	}
}

// TestAddNOC_SameFabricIDAndRootTwiceRejectedAsFabricConflict verifies
// that installing a second NOC for the same (FabricID, RootPublicKey)
// pair is rejected with NOCStatusFabricConflict, even from a fresh
// FailSafe/CSR cycle. Mirrors matter.js FailsafeContext.ts:255-259
// (globalId hash over fabricId + root public key) and chip's
// pre-AddFabric duplicate check.
func TestAddNOC_SameFabricIDAndRootTwiceRejectedAsFabricConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	rootRaw, rootPriv, _ := commissionTestFabric(ctx, t, oc)

	// A fabric-conflict retry starts a fresh FailSafe window in
	// production (GeneralCommissioning.ArmFailSafe calls
	// ClearPendingState); simulate that directly since this test does
	// not wire GeneralCommissioning.
	oc.ClearPendingState()

	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate (round 2): %v", err)
	}
	pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
	nocRaw2 := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw2,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	}, 0)
	if err != nil {
		t.Fatalf("AddNOC (round 2): %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusFabricConflict {
		t.Fatalf("StatusCode = %d (%s), want NOCStatusFabricConflict (%d)",
			nr.StatusCode, nr.DebugText, core.NOCStatusFabricConflict)
	}
}

// TestUpdateNOC_SignedByDifferentRootRejectedAsInvalidNOC verifies that
// UpdateNOC validates the new NOC's chain against the fabric's STORED
// root — a NOC signed by an unrelated root fails chain verification and
// is rejected with NOCStatusInvalidNOC. Mirrors matter.js
// FailsafeContext.ts:217-224 buildUpdatedFabric → Fabric.ts:508-538
// setOperationalCert and chip FabricTable.cpp:854-882.
func TestUpdateNOC_SignedByDifferentRootRejectedAsInvalidNOC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, _, fabricIndex := commissionTestFabric(ctx, t, oc)

	otherRootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey other root: %v", err)
	}

	fabCtx := im.WithFabricFilter(ctx, true, fabricIndex)
	nocRaw := mintUpdateNOC(fabCtx, t, oc, otherRootPriv, testDefaultFabricID, testDefaultNodeID)

	resp, err := oc.MatterInvoke(fabCtx, 0x07, core.UpdateNOCRequest{NOCValue: nocRaw}, 0)
	if err != nil {
		t.Fatalf("UpdateNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidNOC {
		t.Fatalf("StatusCode = %d (%s), want NOCStatusInvalidNOC (%d)",
			nr.StatusCode, nr.DebugText, core.NOCStatusInvalidNOC)
	}
}

// TestUpdateNOC_MintedOverWrongKeyRejectedAsInvalidPublicKey verifies
// that UpdateNOC rejects a NOC minted over a key other than the pending
// IsForUpdateNOC CSR key, even when correctly signed by the fabric's
// real root. Mirrors matter.js Fabric.ts:524-526 and chip
// FabricTable.cpp:890.
func TestUpdateNOC_MintedOverWrongKeyRejectedAsInvalidPublicKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, rootPriv, fabricIndex := commissionTestFabric(ctx, t, oc)

	fabCtx := im.WithFabricFilter(ctx, true, fabricIndex)
	// Issue the update CSR (sets a pending key) but mint the NOC over an
	// unrelated throwaway key instead of the one the CSR carried.
	if _, err := oc.MatterInvoke(fabCtx, 0x04, core.CSRRequest{
		CSRNonce:       make([]byte, 32),
		IsForUpdateNOC: true,
	}, 0); err != nil {
		t.Fatalf("CSRRequest (IsForUpdateNOC): %v", err)
	}
	wrongPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey wrong key: %v", err)
	}
	nocRaw := buildCoreSignedCert(t, wrongPriv, false, rootPriv)

	resp, err := oc.MatterInvoke(fabCtx, 0x07, core.UpdateNOCRequest{NOCValue: nocRaw}, 0)
	if err != nil {
		t.Fatalf("UpdateNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidPublicKey {
		t.Fatalf("StatusCode = %d (%s), want NOCStatusInvalidPublicKey (%d)",
			nr.StatusCode, nr.DebugText, core.NOCStatusInvalidPublicKey)
	}
}

// TestUpdateNOC_DifferentFabricIDRejectedAsInvalidNOC verifies that
// UpdateNOC rejects a NOC whose subject FabricID differs from the
// fabric being updated. Matter §11.18.6.9 forbids UpdateNOC from moving
// a fabric to a different FabricID; mirrors chip FabricTable.cpp:854
// (fabricIdToValidate pins the existing FabricId).
func TestUpdateNOC_DifferentFabricIDRejectedAsInvalidNOC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, rootPriv, fabricIndex := commissionTestFabric(ctx, t, oc)

	fabCtx := im.WithFabricFilter(ctx, true, fabricIndex)
	const differentFabricID = testDefaultFabricID + 1
	nocRaw := mintUpdateNOC(fabCtx, t, oc, rootPriv, differentFabricID, testDefaultNodeID)

	resp, err := oc.MatterInvoke(fabCtx, 0x07, core.UpdateNOCRequest{NOCValue: nocRaw}, 0)
	if err != nil {
		t.Fatalf("UpdateNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidNOC {
		t.Fatalf("StatusCode = %d (%s), want NOCStatusInvalidNOC (%d)",
			nr.StatusCode, nr.DebugText, core.NOCStatusInvalidNOC)
	}
}

// TestUpdateNOC_NewNodeIDUpdatesStoreAndFiresWithdraw verifies that an
// UpdateNOC whose new NOC carries a different operational NodeID (a)
// updates the fabric row's NodeID via [store.Store.UpdateFabricNodeID]
// and (b) fires onFabricWithdraw with the OLD (CompressedID, NodeID) so
// the stale mDNS operational instance is retracted. Mirrors matter.js
// Fabric.ts:543 (nodeId lifted from the new NOC) + DeviceAdvertiser.ts:
// 65-76 (close the fabric's advertisement when nodeId changed, then
// re-advertise).
func TestUpdateNOC_NewNodeIDUpdatesStoreAndFiresWithdraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var withdrawFired bool
	var withdrawnCompressedID [8]byte
	var withdrawnNodeID uint64

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{
		SupportedFabrics: 5,
		OnFabricWithdraw: func(_ context.Context, compressedID [8]byte, nodeID uint64) {
			withdrawFired = true
			withdrawnCompressedID = compressedID
			withdrawnNodeID = nodeID
		},
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, rootPriv, fabricIndex := commissionTestFabric(ctx, t, oc)

	fabBefore, err := fs.GetFabric(ctx, fabricIndex)
	if err != nil {
		t.Fatalf("GetFabric before UpdateNOC: %v", err)
	}

	const newNodeID = testDefaultNodeID + 1
	fabCtx := im.WithFabricFilter(ctx, true, fabricIndex)
	nocRaw := mintUpdateNOC(fabCtx, t, oc, rootPriv, testDefaultFabricID, newNodeID)

	resp, err := oc.MatterInvoke(fabCtx, 0x07, core.UpdateNOCRequest{NOCValue: nocRaw}, 0)
	if err != nil {
		t.Fatalf("UpdateNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("UpdateNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}

	fabAfter, err := fs.GetFabric(ctx, fabricIndex)
	if err != nil {
		t.Fatalf("GetFabric after UpdateNOC: %v", err)
	}
	if fabAfter.NodeID != newNodeID {
		t.Errorf("fabric NodeID after UpdateNOC = 0x%016X, want 0x%016X", fabAfter.NodeID, newNodeID)
	}
	// Everything else on the fabric row must be untouched by the NodeID
	// update.
	if fabAfter.FabricID != fabBefore.FabricID {
		t.Errorf("fabric FabricID changed: got 0x%016X, want unchanged 0x%016X", fabAfter.FabricID, fabBefore.FabricID)
	}
	if fabAfter.CompressedID != fabBefore.CompressedID {
		t.Errorf("fabric CompressedID changed: got %x, want unchanged %x", fabAfter.CompressedID, fabBefore.CompressedID)
	}

	if !withdrawFired {
		t.Fatal("OnFabricWithdraw did not fire after a NodeID-changing UpdateNOC")
	}
	if withdrawnNodeID != fabBefore.NodeID {
		t.Errorf("OnFabricWithdraw nodeID = 0x%016X, want the OLD nodeID 0x%016X", withdrawnNodeID, fabBefore.NodeID)
	}
	if withdrawnCompressedID != fabBefore.CompressedID {
		t.Errorf("OnFabricWithdraw compressedID = %x, want %x", withdrawnCompressedID, fabBefore.CompressedID)
	}
}

// TestRemoveFabric_FiresWithdrawBeforeMDNSReannounce verifies that
// RemoveFabric fires onFabricWithdraw with the removed fabric's
// (CompressedID, NodeID) BEFORE firing onMDNSReannounce — a plain
// republish cannot retire the stale operational record, so the
// withdraw must land first. Mirrors matter.js DeviceAdvertiser.ts:
// 84-86 (fabrics.events.deleting → Advertisement.cancelAll, ahead of
// the reannounce triggered by Fabric.remove()).
func TestRemoveFabric_FiresWithdrawBeforeMDNSReannounce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var order []string
	var withdrawnCompressedID [8]byte
	var withdrawnNodeID uint64

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{
		SupportedFabrics: 5,
		OnFabricWithdraw: func(_ context.Context, compressedID [8]byte, nodeID uint64) {
			order = append(order, "withdraw")
			withdrawnCompressedID = compressedID
			withdrawnNodeID = nodeID
		},
		OnMDNSReannounce: func(_ context.Context) {
			order = append(order, "reannounce")
		},
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	_, _, fabricIndex := commissionTestFabric(ctx, t, oc)

	fabBefore, err := fs.GetFabric(ctx, fabricIndex)
	if err != nil {
		t.Fatalf("GetFabric before RemoveFabric: %v", err)
	}

	resp, err := oc.MatterInvoke(ctx, 0x0A, core.RemoveFabricRequest{FabricIndex: fabricIndex}, 0)
	if err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("RemoveFabric StatusCode = %d, want OK", nr.StatusCode)
	}

	if len(order) != 2 || order[0] != "withdraw" || order[1] != "reannounce" {
		t.Fatalf("hook fire order = %v, want [withdraw reannounce]", order)
	}
	if withdrawnNodeID != fabBefore.NodeID {
		t.Errorf("OnFabricWithdraw nodeID = 0x%016X, want 0x%016X", withdrawnNodeID, fabBefore.NodeID)
	}
	if withdrawnCompressedID != fabBefore.CompressedID {
		t.Errorf("OnFabricWithdraw compressedID = %x, want %x", withdrawnCompressedID, fabBefore.CompressedID)
	}
}
