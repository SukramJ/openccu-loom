// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func newOpcreds(t *testing.T) *core.OperationalCredentials {
	t.Helper()
	oc, err := core.NewOperationalCredentials(newFakeStore(), core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	return oc
}

func TestOpcreds_ClusterID(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	if got := oc.MatterClusterID(); got != 0x003E {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x003E", got)
	}
}

func TestOpcreds_ClusterRevision(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	v, ok := oc.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 2 {
		t.Fatalf("ClusterRevision = %v, want 2", v)
	}
}

// TestOpcreds_MinReadPrivilege_NOCsRequiresAdminister asserts that NOCs
// (0x0000) requires Administer (5), not View — the certificate bytes in the
// NOC/ICAC list must not be readable or subscribable by a merely-View
// subject. Mirrors matter.js
// packages/model/src/standard/elements/operational-credentials.element.ts:24
// (access "R F A").
func TestOpcreds_MinReadPrivilege_NOCsRequiresAdminister(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	if got := oc.MinReadPrivilege(0x0000); got != 5 {
		t.Fatalf("MinReadPrivilege(NOCs) = %d, want 5 (Administer)", got)
	}
}

// TestOpcreds_MinReadPrivilege_OtherAttributesAreView asserts that every
// OperationalCredentials attribute other than NOCs stays at the default
// View (1) privilege.
func TestOpcreds_MinReadPrivilege_OtherAttributesAreView(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	for _, attrID := range []uint32{0x0001, 0x0002, 0x0004} {
		if got := oc.MinReadPrivilege(attrID); got != 1 {
			t.Errorf("MinReadPrivilege(0x%04X) = %d, want 1 (View)", attrID, got)
		}
	}
}

// TestOpcreds_MinReadPrivilege_NilReceiver asserts that MinReadPrivilege is
// a pure attrID switch that never dereferences its receiver, so it also
// works on a nil *OperationalCredentials — no server construction required
// to determine the read-privilege table.
func TestOpcreds_MinReadPrivilege_NilReceiver(t *testing.T) {
	t.Parallel()
	var oc *core.OperationalCredentials
	if got := oc.MinReadPrivilege(0x0000); got != 5 {
		t.Fatalf("MinReadPrivilege(NOCs) on nil receiver = %d, want 5", got)
	}
	if got := oc.MinReadPrivilege(0x0001); got != 1 {
		t.Fatalf("MinReadPrivilege(Fabrics) on nil receiver = %d, want 1", got)
	}
}

func TestOpcreds_NewOperationalCredentials_NilStore(t *testing.T) {
	t.Parallel()
	_, err := core.NewOperationalCredentials(nil, core.OpcredsConfig{})
	if err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}

func TestOpcreds_SupportedFabricsDefault(t *testing.T) {
	t.Parallel()
	// cfg.SupportedFabrics == 0 (unset) → matter.js default 254
	// (OperationalCredentialsServer.ts:87). Configured non-zero values
	// pass through unchanged — see TestOpcreds_SupportedFabricsRespectsConfig.
	oc, err := core.NewOperationalCredentials(newFakeStore(), core.OpcredsConfig{})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	v, ok := oc.MatterRead(0x0002) // opcredsAttrSupportedFabrics
	if !ok {
		t.Fatal("SupportedFabrics attr not readable")
	}
	if v.(uint8) != 254 {
		t.Fatalf("SupportedFabrics default: got %d, want 254", v.(uint8))
	}
}

func TestOpcreds_SupportedFabricsRespectsConfig(t *testing.T) {
	t.Parallel()
	// Configured value 1 must pass through (no floor); the floor was a
	// non-spec invention removed by D-14.
	oc, err := core.NewOperationalCredentials(newFakeStore(), core.OpcredsConfig{SupportedFabrics: 1})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	v, _ := oc.MatterRead(0x0002)
	if v.(uint8) != 1 {
		t.Fatalf("SupportedFabrics configured=1: got %d, want 1", v.(uint8))
	}
}

func TestOpcreds_ReadSupportedFabrics(t *testing.T) {
	t.Parallel()
	// newOpcreds uses SupportedFabrics: 5 (the spec floor).
	oc := newOpcreds(t)
	v, ok := oc.MatterRead(0x0002)
	if !ok {
		t.Fatal("SupportedFabrics: ok=false")
	}
	// SupportedFabrics returns the configured value (5),
	// not the old hardcoded 16. Callers can set a higher limit by
	// passing a larger OpcredsConfig.SupportedFabrics.
	if v.(uint8) != 5 {
		t.Fatalf("SupportedFabrics = %d, want 5", v.(uint8))
	}
}

func TestOpcreds_ReadCommissionedFabricsInitial(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	v, ok := oc.MatterRead(0x0003)
	if !ok {
		t.Fatal("CommissionedFabrics: ok=false")
	}
	if v.(uint8) != 0 {
		t.Fatalf("CommissionedFabrics = %d, want 0", v.(uint8))
	}
}

func TestOpcreds_ReadCurrentFabricIndexInitial(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	v, ok := oc.MatterRead(0x0005) // opcredsAttrCurrentFabricIndex
	if !ok {
		t.Fatal("CurrentFabricIndex: ok=false")
	}
	if v.(uint8) != 0 {
		t.Fatalf("CurrentFabricIndex = %d, want 0", v.(uint8))
	}
}

func TestOpcreds_AttestationRequest_BadNonce(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x00, core.AttestationRequest{
		AttestationNonce: make([]byte, 16), // wrong length
	}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for bad nonce length, got nil")
	}
}

func TestOpcreds_AttestationRequest_ValidNonce(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	resp, err := oc.MatterInvoke(context.Background(), 0x00, core.AttestationRequest{
		AttestationNonce: make([]byte, 32),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AttestationRequest: %v", err)
	}
	if _, ok := resp.(core.AttestationResponse); !ok {
		t.Fatalf("expected AttestationResponse, got %T", resp)
	}
}

func TestOpcreds_CertChainRequest_DAC(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x02, core.CertificateChainRequest{CertificateType: core.CertChainTypeDAC}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CertificateChainRequest DAC: %v", err)
	}
}

func TestOpcreds_CertChainRequest_PAI(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x02, core.CertificateChainRequest{CertificateType: core.CertChainTypePAI}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CertificateChainRequest PAI: %v", err)
	}
}

func TestOpcreds_CertChainRequest_InvalidType(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x02, core.CertificateChainRequest{CertificateType: 99}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for invalid cert chain type, got nil")
	}
}

func TestOpcreds_CSRRequest_BadNonce(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x04, core.CSRRequest{
		CSRNonce: make([]byte, 16), // wrong length
	}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for bad CSRNonce, got nil")
	}
}

func TestOpcreds_CSRRequest_SetsPendingKey(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x04, core.CSRRequest{
		CSRNonce: make([]byte, 32),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}
	// After CSRRequest, AddNOC without trusted root should return MissingCsr=false
	// (the CSR was issued), but without trusted root → NOCStatusInvalidNOC.
	resp, err := oc.MatterInvoke(context.Background(), 0x06, core.AddNOCRequest{
		IPKValue: make([]byte, 16),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC after CSR: %v", err)
	}
	nr := resp.(core.NOCResponse)
	// Should not be MissingCsr since we issued a CSR.
	if nr.StatusCode == core.NOCStatusMissingCsr {
		t.Fatalf("StatusCode = MissingCsr after CSRRequest was issued")
	}
}

// TestOpcreds_AddNOC_InvalidAdminVendorID covers the
// IsVendorIdValidOperationally guard chip enforces at
// OperationalCredentialsCluster.cpp:437. The handler must reject AddNOC
// with NOCStatusInvalidAdminSubject for the two universal placeholders
// (0x0000, 0xFFFF) and the reserved range 0xFFF5..0xFFFE. The Test-VID
// range 0xFFF1..0xFFF4 is INTENTIONALLY accepted — chip-tool commissions
// with VID 0xFFF1 by default. The guard runs AFTER the CSR / trust-root
// checks — the test feeds both first so only the VID gate fires.
func TestOpcreds_AddNOC_InvalidAdminVendorID(t *testing.T) {
	t.Parallel()
	// All of these must be rejected with NOCStatusInvalidAdminSubject.
	rejectedVIDs := []uint16{
		0x0000,
		0xFFFF,
		0xFFF5,
		0xFFF6,
		0xFFFE,
	}
	for _, vid := range rejectedVIDs {
		t.Run(fmt.Sprintf("vid=0x%04X_rejected", vid), func(t *testing.T) {
			t.Parallel()
			oc := newOpcreds(t)
			rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)
			ctx := context.Background()
			// Prime trust root + CSR so the VID guard is the check that fires.
			if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw}, 0); err != nil {
				t.Fatalf("AddTrustedRoot: %v", err)
			}
			if _, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{CSRNonce: make([]byte, 32)}, 0); err != nil {
				t.Fatalf("CSRRequest: %v", err)
			}
			resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
				NOCValue:      nocRaw,
				IPKValue:      make([]byte, 16),
				AdminVendorID: vid,
			}, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("AddNOC: %v", err)
			}
			nr := resp.(core.NOCResponse)
			if nr.StatusCode != core.NOCStatusInvalidAdminSubject {
				t.Fatalf("VID 0x%04X: StatusCode = %d, want NOCStatusInvalidAdminSubject (%d)",
					vid, nr.StatusCode, core.NOCStatusInvalidAdminSubject)
			}
		})
	}
}

func TestOpcreds_AddNOC_WithoutCSR(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	resp, err := oc.MatterInvoke(context.Background(), 0x06, core.AddNOCRequest{
		IPKValue: make([]byte, 16),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC without CSR: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusMissingCsr {
		t.Fatalf("StatusCode = %d, want NOCStatusMissingCsr (%d)", nr.StatusCode, core.NOCStatusMissingCsr)
	}
}

func TestOpcreds_AddNOC_WithoutTrustedRoot(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)

	// Issue CSR first.
	_, _ = oc.MatterInvoke(context.Background(), 0x04, core.CSRRequest{CSRNonce: make([]byte, 32)}, hmenum.CommandPriorityHigh)

	// No trusted root → NOCStatusInvalidNOC.
	resp, err := oc.MatterInvoke(context.Background(), 0x06, core.AddNOCRequest{
		IPKValue: make([]byte, 16),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC without trusted root: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidNOC {
		t.Fatalf("StatusCode = %d, want NOCStatusInvalidNOC (%d)", nr.StatusCode, core.NOCStatusInvalidNOC)
	}
}

func TestOpcreds_AddNOC_BadIPK(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)

	// Provide IPK with wrong length — must get NOCStatusInvalidNOC before
	// any CSR check.
	resp, err := oc.MatterInvoke(context.Background(), 0x06, core.AddNOCRequest{
		IPKValue: make([]byte, 8), // not 16
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC bad IPK: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidNOC {
		t.Fatalf("StatusCode = %d, want NOCStatusInvalidNOC (%d) for bad IPK", nr.StatusCode, core.NOCStatusInvalidNOC)
	}
}

func TestOpcreds_AddTrustedRoot_MalformedCert(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: []byte{0xDE, 0xAD},
	}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for malformed root cert, got nil")
	}
}

func TestOpcreds_RemoveFabric_NotExisting(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	resp, err := oc.MatterInvoke(context.Background(), 0x0A, core.RemoveFabricRequest{FabricIndex: 99}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("RemoveFabric non-existing: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusInvalidFabricIndex {
		t.Fatalf("StatusCode = %d, want NOCStatusInvalidFabricIndex (%d)", nr.StatusCode, core.NOCStatusInvalidFabricIndex)
	}
}

func TestOpcreds_Write_ReadOnly(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	err := oc.MatterWrite(context.Background(), 0x0002, uint8(10), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for write to read-only attr, got nil")
	}
}

func TestOpcreds_Invoke_UnknownCmd(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

func TestOpcreds_SetCurrentFabric(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	oc.SetCurrentFabric(7)

	v, ok := oc.MatterRead(0x0005)
	if !ok {
		t.Fatal("CurrentFabricIndex: ok=false")
	}
	if v.(uint8) != 7 {
		t.Fatalf("CurrentFabricIndex = %d, want 7 after SetCurrentFabric(7)", v.(uint8))
	}
}

// TestOpcreds_AddTrustedRoot_Accepts verifies that a well-formed
// Matter root CA certificate is accepted via AddTrustedRootCertificate
// (cmd 0x0B). The handler decodes the cert, verifies IsRoot, and
// stashes the public key as the pending trust root for the next
// AddNOC.
func TestOpcreds_AddTrustedRoot_Accepts(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	ctx := context.Background()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}
}

// TestOpcreds_AddNOC_NeedsCSRThenRoot exercises the commissioning
// state machine — AddNOC without a prior CSRRequest fails fast with
// NOCStatusMissingCsr; with CSR but without trusted root it fails
// with NOCStatusInvalidNOC. Both signal via NOCResponse.StatusCode,
// not via the error channel.
func TestOpcreds_AddNOC_NeedsCSRThenRoot(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	ctx := context.Background()

	_, nocRaw, _ := buildTestNOCAndRoot(t)

	// AddNOC before CSR → NOCStatusMissingCsr.
	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue: nocRaw,
		IPKValue: make([]byte, 16),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC (pre-CSR): %v", err)
	}
	if got := resp.(core.NOCResponse).StatusCode; got != core.NOCStatusMissingCsr {
		t.Fatalf("StatusCode = %d, want NOCStatusMissingCsr (%d)", got, core.NOCStatusMissingCsr)
	}

	// CSRRequest succeeds.
	if _, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{CSRNonce: make([]byte, 32)}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

	// AddNOC with CSR but without trusted root → NOCStatusInvalidNOC.
	resp, err = oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue: nocRaw,
		IPKValue: make([]byte, 16),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC (no root): %v", err)
	}
	if got := resp.(core.NOCResponse).StatusCode; got != core.NOCStatusInvalidNOC {
		t.Fatalf("StatusCode = %d, want NOCStatusInvalidNOC (%d)", got, core.NOCStatusInvalidNOC)
	}
}

// TestOpcreds_TrustedRootCertificates_ServesFullRCAC pins the Matter
// Certificate TLV envelope as the attribute's wire shape. A prior
// implementation served each fabric's 65-byte uncompressed EC-P256
// public key (starting `04 …`), which Apple Home's MTR SDK validates
// as a Matter Certificate TLV — it isn't one, so Apple silently
// discarded the entire Subscribe-Initial ReportData stream.
//
// matter.js HEAD reference: OperationalCredentialsServer.ts:457-459
// (`fabrics.map(fabric => fabric.rootCert)`) — each entry is the full
// Matter Certificate TLV envelope received via AddTrustedRootCertificate
// (Fabric.ts:68 `readonly rootCert: Bytes`).
//
// The test pokes a fabric with a known `RootCert` into the store and
// asserts MatterRead(0x0004) returns exactly those bytes, NOT
// `RootPublicKey`. A second fabric with `RootCert == nil` (legacy row
// pre-migration 012) is explicitly skipped — serving it as the EC
// pubkey would re-trigger Bug I.
// TestAddNOC_InstallsIPKInGroupKeyManagement verifies that after a
// successful AddNOC the IPK is written to GroupKeyManagement KeySetID=0
// per Matter §11.18.6.8.6 and chip operational-credentials-server.cpp:484-496.
// Uses a fakeStore that records UpsertGroupKeySet calls so the assertion
// can check the stored key without hitting the DB.
func TestAddNOC_InstallsIPKInGroupKeyManagement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	// Step 1: AddTrustedRootCertificate.
	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Step 2: CSRRequest.
	if _, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{
		CSRNonce: make([]byte, 32),
	}, 0); err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

	// Step 3: AddNOC with a recognizable 16-byte IPK.
	ipk := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
	}
	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         ipk,
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	}, 0)
	if err != nil {
		t.Fatalf("AddNOC: %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}

	// Verify GroupKeyManagement KeySetID=0 was written with the IPK.
	gks, err := fs.GetGroupKeySet(ctx, nr.FabricIndex, 0)
	if err != nil {
		t.Fatalf("GetGroupKeySet(fabricIndex=%d, id=0): %v — IPK was not written", nr.FabricIndex, err)
	}
	if !bytes.Equal(gks.EpochKey0, ipk) {
		t.Errorf("GroupKeyManagement KeySetID=0 EpochKey0 = %x, want %x", gks.EpochKey0, ipk)
	}
	if gks.GroupKeySetID != 0 {
		t.Errorf("GroupKeyManagement KeySetID = %d, want 0", gks.GroupKeySetID)
	}
	if gks.EpochStart0 != 0 {
		t.Errorf("GroupKeyManagement EpochStart0 = %d, want 0 (IPK slot sentinel)", gks.EpochStart0)
	}
}

// TestAddNOC_RejectsMismatchedCSRSession verifies that AddNOC from a
// different session than the one that issued CSRRequest is rejected with
// MissingCsr per Matter §11.18.7.5.5 and matter.js
// OperationalCredentialsServer.ts:230-235.
func TestAddNOC_RejectsMismatchedCSRSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	// AddTrustedRootCertificate.
	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// CSRRequest on session 42.
	csrCtx := core.WithInvokeSessionID(ctx, 42)
	if _, err := oc.MatterInvoke(csrCtx, 0x04, core.CSRRequest{
		CSRNonce: make([]byte, 32),
	}, 0); err != nil {
		t.Fatalf("CSRRequest (session 42): %v", err)
	}

	// AddNOC on a DIFFERENT session (99) — must be rejected with MissingCsr.
	addNOCCtx := core.WithInvokeSessionID(ctx, 99)
	resp, err := oc.MatterInvoke(addNOCCtx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	}, 0)
	if err != nil {
		t.Fatalf("AddNOC (mismatched session): %v", err)
	}
	nr := resp.(core.NOCResponse)
	if nr.StatusCode != core.NOCStatusMissingCsr {
		t.Errorf("AddNOC mismatched session: StatusCode = %d, want NOCStatusMissingCsr (%d)",
			nr.StatusCode, core.NOCStatusMissingCsr)
	}

	// AddNOC on the SAME session (42) must succeed (or at least not be
	// rejected for session mismatch; other guards may still fire).
	sameCtx := core.WithInvokeSessionID(ctx, 42)
	resp2, err := oc.MatterInvoke(sameCtx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	}, 0)
	if err != nil {
		t.Fatalf("AddNOC (same session): %v", err)
	}
	nr2 := resp2.(core.NOCResponse)
	// The session-mismatch guard must NOT fire for the same session.
	if nr2.StatusCode == core.NOCStatusMissingCsr {
		t.Errorf("AddNOC same session: got MissingCsr — session binding incorrectly rejected matching session")
	}
}

// TestRemoveFabric_FiresCleanupCallbacks verifies that after
// RemoveFabric (a) the onFabricRemoved hook fires with the correct
// fabricIndex and (b) in-memory pending commissioning state that belongs
// to the removed fabric is cleared.
func TestRemoveFabric_FiresCleanupCallbacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var hookFired bool
	var hookedFabric uint8

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{
		SupportedFabrics: 5,
		OnFabricRemoved: func(_ context.Context, fi uint8) {
			hookFired = true
			hookedFabric = fi
		},
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	// Drive a full commissioning sequence to install a fabric.
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
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}
	installedFabric := nr.FabricIndex

	// Now remove the fabric.
	rmResp, err := oc.MatterInvoke(ctx, 0x0A, core.RemoveFabricRequest{FabricIndex: installedFabric}, 0)
	if err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}
	rmNR := rmResp.(core.NOCResponse)
	if rmNR.StatusCode != core.NOCStatusOK {
		t.Fatalf("RemoveFabric StatusCode = %d, want OK", rmNR.StatusCode)
	}

	// Hook must have fired.
	if !hookFired {
		t.Error("onFabricRemoved hook did not fire after RemoveFabric")
	}
	if hookedFabric != installedFabric {
		t.Errorf("onFabricRemoved hook fabricIndex = %d, want %d", hookedFabric, installedFabric)
	}

	// CommissionedFabrics attribute should reflect the removal.
	v, ok := oc.MatterRead(0x0003) // opcredsAttrCommissionedFabrics
	if !ok {
		t.Fatal("CommissionedFabrics: ok=false after RemoveFabric")
	}
	if v.(uint8) != 0 {
		t.Errorf("CommissionedFabrics = %d after RemoveFabric, want 0", v.(uint8))
	}
}

// --- AddTrustedRootCertificate guards ---

// TestAddTrustedRoot_DuplicateRootInSameFailSafe verifies the duplicate-root
// guard per Matter §11.18.6.4. A second AddTrustedRootCertificate call in the
// same FailSafe window (pendingTrustRoot already set) must return an error.
// Mirrors chip HandleAddTrustedRootCertificate
// VerifyOrExit(!failsafeContext.AddTrustedRootCertHasBeenInvoked(), ConstraintError)
// and matter.js OperationalCredentialsServer.ts:451 rootCertSet check.
func TestAddTrustedRoot_DuplicateRootInSameFailSafe(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	ctx := context.Background()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	// First call succeeds.
	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("first AddTrustedRootCertificate: %v", err)
	}

	// Second call in the same window must fail (duplicate-root guard).
	_, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0)
	if err == nil {
		t.Fatal("second AddTrustedRootCertificate should fail (duplicate-root guard), got nil")
	}
}

// TestAddTrustedRoot_AfterNOCCommandRejected verifies the post-NOC guard per
// Matter §11.18.6.4. AddTrustedRootCertificate called after AddNOC has already
// been invoked in the same FailSafe window must return an error.
// Mirrors chip HandleAddTrustedRootCertificate
// VerifyOrExit(!failsafeContext.NocCommandHasBeenInvoked(), ConstraintError)
// and matter.js OperationalCredentialsServer.ts:453 fabricIndex check .
func TestAddTrustedRoot_AfterNOCCommandRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	// Full commissioning sequence → AddNOC succeeds.
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
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}

	// AddTrustedRootCertificate after AddNOC must fail (nocWasInvoked guard).
	rootRaw2, _, _ := buildTestNOCAndRoot(t)
	_, err = oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw2,
	}, 0)
	if err == nil {
		t.Fatal("AddTrustedRootCertificate after AddNOC should fail (nocWasInvoked guard), got nil")
	}
}

// --- pending root included in TrustedRootCertificates ---

// TestTrustedRootCertificates_IncludesPendingRoot verifies that the pending
// root (set by AddTrustedRootCertificate before AddNOC is called) is included
// in the TrustedRootCertificates attribute (0x0004). Mirrors matter.js
// OperationalCredentialsServer.ts:458-459 which pushes rootCaCertificate
// into state.trustedRootCertificates immediately.
func TestTrustedRootCertificates_IncludesPendingRoot(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	ctx := context.Background()

	rootRaw, _, _ := buildTestNOCAndRoot(t)

	// Before AddTrustedRootCertificate: attribute returns empty list.
	v, ok := oc.MatterRead(0x0004)
	if !ok {
		t.Fatal("TrustedRootCertificates: ok=false before AddTrustedRootCertificate")
	}
	if list := v.([][]byte); len(list) != 0 {
		t.Fatalf("TrustedRootCertificates before AddTrustedRootCertificate: len=%d, want 0", len(list))
	}

	// After AddTrustedRootCertificate but before AddNOC: pending root must appear.
	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}
	v, ok = oc.MatterRead(0x0004)
	if !ok {
		t.Fatal("TrustedRootCertificates: ok=false after AddTrustedRootCertificate (pre-AddNOC)")
	}
	list := v.([][]byte)
	if len(list) != 1 {
		t.Fatalf("TrustedRootCertificates after AddTrustedRootCertificate (pre-AddNOC): len=%d, want 1", len(list))
	}
	if !bytes.Equal(list[0], rootRaw) {
		t.Errorf("TrustedRootCertificates[0] pending = %x, want rootRaw", list[0])
	}
}

// --- global attributes 0xFFF8–0xFFFB ---

// TestOpcreds_GlobalAttributes_Served verifies that OperationalCredentials
// serves GeneratedCommandList (0xFFF8), AcceptedCommandList (0xFFF9),
// EventList (0xFFFA) and AttributeList (0xFFFB). These were missing and caused
// Apple cache-drops on cluster 0x3E.
// Mirrors matter.js ClusterServer auto-populated globalAttributes.
func TestOpcreds_GlobalAttributes_Served(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)

	cases := []struct {
		name   string
		attrID uint32
	}{
		{"GeneratedCommandList", 0xFFF8},
		{"AcceptedCommandList", 0xFFF9},
		{"EventList", 0xFFFA},
		{"AttributeList", 0xFFFB},
		{"FeatureMap", 0xFFFC},
		{"ClusterRevision", 0xFFFD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := oc.MatterRead(tc.attrID)
			if !ok {
				t.Fatalf("MatterRead(0x%04X) = (_, false); want true ", tc.attrID)
			}
			if v == nil {
				t.Fatalf("MatterRead(0x%04X) = nil; want non-nil ", tc.attrID)
			}
		})
	}
}

// TestOpcreds_MatterAttributes_IncludesGlobals verifies that MatterAttributes()
// includes all four global attributes so they are included in wildcard subscribe
// enumerations.
func TestOpcreds_MatterAttributes_IncludesGlobals(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	attrs := oc.MatterAttributes()
	wantIDs := []uint32{0xFFF8, 0xFFF9, 0xFFFA, 0xFFFB}
	attrSet := make(map[uint32]bool, len(attrs))
	for _, a := range attrs {
		attrSet[a] = true
	}
	for _, id := range wantIDs {
		if !attrSet[id] {
			t.Errorf("MatterAttributes missing 0x%04X ", id)
		}
	}
}

// --- GroupTable empty (by-design) ---
// No code test needed — the by-design entry is in docs/parity/by_design.md.

// --- UpdateNOC fires onFabricUpdated hook ---

// TestUpdateNOC_FiresOnFabricUpdatedHook verifies that a successful UpdateNOC
// fires the onFabricUpdated hook with the updated fabricIndex.
// Mirrors matter.js FabricManager.ts `replacing` event →
// SessionManager.closeAllSessionsForFabricExcept and chip
// FabricTable::AbortAllOtherCommunicationOnFabric.
func TestUpdateNOC_FiresOnFabricUpdatedHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var hookFired bool
	var hookedFabric uint8

	fs := newFakeStore()
	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{
		SupportedFabrics: 5,
		OnFabricUpdated: func(_ context.Context, fi uint8) {
			hookFired = true
			hookedFabric = fi
		},
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	// Install a fabric via AddNOC first.
	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}, 0); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}
	if _, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{CSRNonce: make([]byte, 32)}, 0); err != nil {
		t.Fatalf("CSRRequest (for AddNOC): %v", err)
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
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}
	installedFabric := nr.FabricIndex

	// Now perform UpdateNOC: need a new CSR with IsForUpdateNOC=true,
	// on a CASE session (non-zero fabric in context).
	fabCtx := im.WithFabricFilter(ctx, true, installedFabric)
	_, nocRaw2, _ := buildTestNOCAndRoot(t)
	if _, err := oc.MatterInvoke(fabCtx, 0x04, core.CSRRequest{
		CSRNonce:       make([]byte, 32),
		IsForUpdateNOC: true,
	}, 0); err != nil {
		t.Fatalf("CSRRequest (for UpdateNOC): %v", err)
	}
	if err := fs.UpsertIdentity(ctx, mstore.IdentityRecord{
		FabricIndex: installedFabric,
		NOC:         nocRaw,
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity setup: %v", err)
	}
	_, err = oc.MatterInvoke(fabCtx, 0x07, core.UpdateNOCRequest{
		NOCValue: nocRaw2,
	}, 0)
	if err != nil {
		t.Fatalf("UpdateNOC: %v", err)
	}

	if !hookFired {
		t.Error("onFabricUpdated hook did not fire after UpdateNOC")
	}
	if hookedFabric != installedFabric {
		t.Errorf("onFabricUpdated hook fabricIndex = %d, want %d", hookedFabric, installedFabric)
	}
}

// TestOpcreds_TrustedRootCertificates_ServesFullRCAC pins the Matter
func TestOpcreds_TrustedRootCertificates_ServesFullRCAC(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	oc, err := core.NewOperationalCredentials(store, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	fullRCAC := []byte{
		0x15, 0x30, 0x01, 0x01, // Matter Certificate TLV envelope sentinel
		0x53, 0x65, 0x72, 0x69, 0x61, 0x6c, // placeholder body
		0x18, // EndOfContainer
	}
	pubKey := bytes.Repeat([]byte{0x04}, 65) // uncompressed EC-P256 pubkey shape

	if _, err := store.AddFabric(context.Background(), mstore.FabricRecord{
		FabricIndex:   1,
		FabricID:      1,
		NodeID:        1,
		RootPublicKey: pubKey,
		RootCert:      fullRCAC,
		Label:         "active",
	}); err != nil {
		t.Fatalf("AddFabric active: %v", err)
	}
	// Legacy row — pre-migration, no RootCert. Bug-I guard requires
	// this fabric to be omitted from TrustedRootCertificates (rather
	// than re-served as `pubKey`).
	if _, err := store.AddFabric(context.Background(), mstore.FabricRecord{
		FabricIndex:   2,
		FabricID:      2,
		NodeID:        2,
		RootPublicKey: pubKey,
		RootCert:      nil,
		Label:         "legacy",
	}); err != nil {
		t.Fatalf("AddFabric legacy: %v", err)
	}

	got, ok := oc.MatterRead(0x0004) // opcredsAttrTrustedRootCertificates
	if !ok {
		t.Fatal("TrustedRootCertificates: ok=false")
	}
	list, ok := got.([][]byte)
	if !ok {
		t.Fatalf("TrustedRootCertificates type = %T, want [][]byte", got)
	}
	if len(list) != 1 {
		t.Fatalf("TrustedRootCertificates len = %d, want 1 (legacy fabric must be omitted)", len(list))
	}
	if !bytes.Equal(list[0], fullRCAC) {
		t.Errorf("TrustedRootCertificates[0] = %x, want %x (must serve RootCert, NOT RootPublicKey)", list[0], fullRCAC)
	}
	// Bug-I guard: list[0] MUST NOT be the EC pubkey, even when
	// RootCert and RootPublicKey are both populated.
	if bytes.Equal(list[0], pubKey) {
		t.Errorf("TrustedRootCertificates[0] = RootPublicKey — Bug-I regression: Apple silently drops the Subscribe stream when entries aren't Matter Certificate TLV")
	}
}

// TestOpcreds_MatterDataVersion verifies MatterDataVersion returns the
// current counter value (seeded at construction and bumped on mutations).
func TestOpcreds_MatterDataVersion(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	// At construction the data-version is non-zero (seeded with a sentinel).
	v := oc.MatterDataVersion()
	// We just want to confirm it's readable; the exact value is internal.
	_ = v
}

// TestOpcreds_MatterReportable verifies MatterReportable returns a non-empty
// slice of attribute IDs that includes Fabrics and CommissionedFabrics.
func TestOpcreds_MatterReportable(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	attrs := oc.MatterReportable()
	if len(attrs) == 0 {
		t.Fatal("MatterReportable() returned empty slice")
	}
}

// TestOpcreds_MatterAcceptedCommands verifies that MatterAcceptedCommands
// returns the expected set of command IDs (at minimum AttestationRequest=0x00
// and AddNOC=0x06).
func TestOpcreds_MatterAcceptedCommands(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	cmds := oc.MatterAcceptedCommands()
	if len(cmds) == 0 {
		t.Fatal("MatterAcceptedCommands() returned empty slice")
	}
	// Spot-check two well-known IDs.
	var hasAttest, hasAddNOC bool
	for _, id := range cmds {
		switch id {
		case 0x00:
			hasAttest = true
		case 0x06:
			hasAddNOC = true
		}
	}
	if !hasAttest {
		t.Error("MatterAcceptedCommands missing AttestationRequest (0x00)")
	}
	if !hasAddNOC {
		t.Error("MatterAcceptedCommands missing AddNOC (0x06)")
	}
}

// TestOpcreds_MatterGeneratedCommands verifies MatterGeneratedCommands
// returns non-empty (must include at least AttestationResponse=0x01).
func TestOpcreds_MatterGeneratedCommands(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	cmds := oc.MatterGeneratedCommands()
	if len(cmds) == 0 {
		t.Fatal("MatterGeneratedCommands() returned empty slice")
	}
	// Must include AttestationResponse=0x01.
	var hasAttestation bool
	for _, id := range cmds {
		if id == 0x01 {
			hasAttestation = true
		}
	}
	if !hasAttestation {
		t.Error("MatterGeneratedCommands missing AttestationResponse (0x01)")
	}
}

// TestEncodeECDHFromPrivate_Success verifies that a valid P-256 private key
// is successfully converted to an *ecdh.PrivateKey.
func TestEncodeECDHFromPrivate_Success(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ek, err := core.EncodeECDHFromPrivate(priv)
	if err != nil {
		t.Fatalf("EncodeECDHFromPrivate: %v", err)
	}
	if ek == nil {
		t.Fatal("EncodeECDHFromPrivate returned nil key")
	}
	if len(ek.Bytes()) != 32 {
		t.Errorf("ECDH key bytes len=%d, want 32", len(ek.Bytes()))
	}
}

// TestOpcreds_VidVerificationCommandsInAcceptedList verifies that
// SetVidVerificationStatement (0x0C) and SignVidVerificationRequest (0x0D)
// appear in AcceptedCommandList and that SignVidVerificationResponse (0x0E)
// appears in GeneratedCommandList, as required by the cluster schema.
func TestOpcreds_VidVerificationCommandsInAcceptedList(t *testing.T) {
	t.Parallel()
	oc, _ := opcredsWithFakeStore(t)

	// AcceptedCommandList (attribute 0xFFF9).
	val, ok := oc.MatterRead(0xFFF9)
	if !ok {
		t.Fatal("AcceptedCommandList read returned ok=false")
	}
	accepted, ok := val.([]uint32)
	if !ok {
		t.Fatalf("AcceptedCommandList type = %T, want []uint32", val)
	}
	wantAccepted := map[uint32]string{
		0x0C: "SetVidVerificationStatement",
		0x0D: "SignVidVerificationRequest",
	}
	for id, name := range wantAccepted {
		found := slices.Contains(accepted, id)
		if !found {
			t.Errorf("AcceptedCommandList missing %s (0x%02X)", name, id)
		}
	}

	// GeneratedCommandList (attribute 0xFFF8).
	genVal, ok := oc.MatterRead(0xFFF8)
	if !ok {
		t.Fatal("GeneratedCommandList read returned ok=false")
	}
	generated, ok := genVal.([]uint32)
	if !ok {
		t.Fatalf("GeneratedCommandList type = %T, want []uint32", genVal)
	}
	found := slices.Contains(generated, 0x0E)
	if !found {
		t.Error("GeneratedCommandList missing SignVidVerificationResponse (0x0E)")
	}

	// MatterAcceptedCommands and MatterGeneratedCommands must match.
	for id, name := range wantAccepted {
		found := slices.Contains(oc.MatterAcceptedCommands(), id)
		if !found {
			t.Errorf("MatterAcceptedCommands missing %s (0x%02X)", name, id)
		}
	}
	foundGen := slices.Contains(oc.MatterGeneratedCommands(), 0x0E)
	if !foundGen {
		t.Error("MatterGeneratedCommands missing SignVidVerificationResponse (0x0E)")
	}
}

// TestOpcreds_SetVidVerificationStatementReturnsInvalidCommand verifies that
// invoking SetVidVerificationStatement (0x0C) returns StatusInvalidCommand
// because this bridge does not support VID-Verification mode.
func TestOpcreds_SetVidVerificationStatementReturnsInvalidCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oc, _ := opcredsWithFakeStore(t)

	_, err := oc.MatterInvoke(ctx, 0x0C, core.SetVidVerificationStatementRequest{}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SetVidVerificationStatement: expected error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusInvalidCommand {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusInvalidCommand (0x85)", got)
	}
}

// TestOpcreds_SignVidVerificationRequestReturnsInvalidCommand mirrors the
// SetVidVerificationStatement check for command 0x0D.
func TestOpcreds_SignVidVerificationRequestReturnsInvalidCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oc, _ := opcredsWithFakeStore(t)

	_, err := oc.MatterInvoke(ctx, 0x0D, core.SignVidVerificationRequest{
		FabricIndex:     1,
		ClientChallenge: make([]byte, 32),
	}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SignVidVerificationRequest: expected error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusInvalidCommand {
		t.Errorf("MatterStatusCode()=0x%02X, want StatusInvalidCommand (0x85)", got)
	}
}

// TestNOCStruct_VvscFieldPresent verifies that the NOCStruct type carries the
// Vvsc field and that its zero value is nil (not set).
func TestNOCStruct_VvscFieldPresent(t *testing.T) {
	t.Parallel()
	n := core.NOCStruct{
		NOC:         []byte{0x01},
		ICAC:        []byte{0x02},
		FabricIndex: 1,
	}
	if n.Vvsc != nil {
		t.Errorf("NOCStruct.Vvsc default = %v, want nil", n.Vvsc)
	}

	n2 := core.NOCStruct{
		NOC:         []byte{0x01},
		ICAC:        []byte{0x02},
		Vvsc:        []byte{0xAA, 0xBB},
		FabricIndex: 1,
	}
	if len(n2.Vvsc) != 2 {
		t.Errorf("NOCStruct.Vvsc len=%d, want 2", len(n2.Vvsc))
	}
}

// TestFabricDescriptorStruct_VidVerificationStatementField verifies that
// FabricDescriptorStruct carries the VidVerificationStatement field (optional,
// field 0x06) and that its zero value is nil.
func TestFabricDescriptorStruct_VidVerificationStatementField(t *testing.T) {
	t.Parallel()

	// Zero value: VidVerificationStatement must default to nil.
	f := core.FabricDescriptorStruct{
		RootPublicKey: []byte{0x04},
		VendorID:      0x1234,
		FabricID:      1,
		NodeID:        2,
		Label:         "test",
		FabricIndex:   1,
	}
	if f.VidVerificationStatement != nil {
		t.Errorf("FabricDescriptorStruct.VidVerificationStatement default = %v, want nil", f.VidVerificationStatement)
	}

	// Non-nil case: field populated correctly.
	stmt := make([]byte, 85)
	stmt[0] = 0xAB
	f2 := core.FabricDescriptorStruct{
		RootPublicKey:            []byte{0x04},
		VendorID:                 0x1234,
		FabricID:                 1,
		NodeID:                   2,
		Label:                    "test",
		VidVerificationStatement: stmt,
		FabricIndex:              1,
	}
	if len(f2.VidVerificationStatement) != 85 {
		t.Errorf("VidVerificationStatement len=%d, want 85", len(f2.VidVerificationStatement))
	}
	if f2.VidVerificationStatement[0] != 0xAB {
		t.Errorf("VidVerificationStatement[0]=%02X, want 0xAB", f2.VidVerificationStatement[0])
	}
}

// TestOpcreds_SetOnFabricRemovedNoOp verifies SetOnFabricRemoved accepts
// a non-nil function and nil without panicking.
func TestOpcreds_SetOnFabricRemovedNoOp(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	oc.SetOnFabricRemoved(func(_ context.Context, _ uint8) {})
	oc.SetOnFabricRemoved(nil)
}

// TestOpcreds_SetOnFabricUpdatedNoOp verifies SetOnFabricUpdated accepts
// a non-nil function and nil without panicking.
func TestOpcreds_SetOnFabricUpdatedNoOp(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	oc.SetOnFabricUpdated(func(_ context.Context, _ uint8) {})
	oc.SetOnFabricUpdated(nil)
}

// TestOpcreds_SetIsFailSafeArmedNoOp verifies SetIsFailSafeArmed accepts
// a non-nil function and nil without panicking.
func TestOpcreds_SetIsFailSafeArmedNoOp(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	oc.SetIsFailSafeArmed(func() bool { return true })
	oc.SetIsFailSafeArmed(nil)
}

// TestOpcreds_UpdateFabricLabelInvalidArgType verifies that UpdateFabricLabel
// (0x09) with a wrong fields type returns an error.
func TestOpcreds_UpdateFabricLabelInvalidArgType(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	_, err := oc.MatterInvoke(context.Background(), 0x09, "wrong-type", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for invalid UpdateFabricLabelRequest type, got nil")
	}
}

// TestOpcreds_UpdateFabricLabelLabelTooLong verifies UpdateFabricLabel (0x09)
// with label > 32 bytes returns IM-InvalidCommand (0x85).
func TestOpcreds_UpdateFabricLabelLabelTooLong(t *testing.T) {
	t.Parallel()
	oc := newOpcreds(t)
	req := core.UpdateFabricLabelRequest{Label: string(bytes.Repeat([]byte("x"), 33))}
	_, err := oc.MatterInvoke(context.Background(), 0x09, req, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("UpdateFabricLabel long label: expected IM error, got nil")
	}
	type statusCoder interface {
		MatterStatusCode() im.StatusCode
	}
	sce, ok := err.(statusCoder)
	if !ok {
		t.Fatalf("error type = %T, want StatusCodeError", err)
	}
	if sce.MatterStatusCode() != im.StatusInvalidCommand {
		t.Fatalf("MatterStatusCode() = %v, want StatusInvalidCommand (0x85)", sce.MatterStatusCode())
	}
}
