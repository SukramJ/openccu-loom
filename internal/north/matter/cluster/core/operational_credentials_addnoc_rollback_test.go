// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// replaceACLFailStore wraps fakeStore and injects a failure on ReplaceACL.
// Used to simulate the AddNOC failure path after AddFabric + UpsertIdentity +
// UpsertGroupKeySet have already committed but ACL installation fails.
type replaceACLFailStore struct {
	*fakeStore
	replaceACLErr error
}

func (s *replaceACLFailStore) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return s.replaceACLErr
}

// upsertIdentityFailStore wraps fakeStore and injects a failure on UpsertIdentity.
// Simulates the AddNOC failure path where AddFabric has committed but UpsertIdentity
// fails, verifying that the canonical revertAddNOC helper cleans up the fabric row.
type upsertIdentityFailStore struct {
	*fakeStore
	upsertIdentityErr error
}

func (s *upsertIdentityFailStore) UpsertIdentity(_ context.Context, _ mstore.IdentityRecord) error {
	return s.upsertIdentityErr
}

// TestAddNOC_ReplaceACLFailure_CleansUpGroupKeys verifies that when AddNOC
// fails during ReplaceACL, all group-key-set rows written for the pending
// fabric (specifically the IPK epoch-key in KeySetID=0) are removed.
//
// Without the explicit RemoveGroupKeysByFabric call in the rollback path,
// the group-key rows for the failed fabric index persist in non-SQL store
// implementations (those that have no FK CASCADE), polluting the group-key
// table with orphaned key material tied to a fabric that never completed
// commissioning.
func TestAddNOC_ReplaceACLFailure_CleansUpGroupKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	inner := newFakeStore()
	wrapped := &replaceACLFailStore{
		fakeStore:     inner,
		replaceACLErr: errors.New("injected ReplaceACL failure"),
	}

	oc, err := core.NewOperationalCredentials(wrapped, core.OpcredsConfig{
		SupportedFabrics: 5,
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	oc.SetIsFailSafeArmed(func() bool { return true })

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey root: %v", err)
	}
	rootRaw := buildCoreSignedCert(t, rootPriv, true, rootPriv)

	// Step 1: AddTrustedRootCertificate — sets pendingTrustRoot.
	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Step 2: CSRRequest — generates the pending key pair. The NOC must be
	// minted over that real pending key (not a throwaway one) so AddNOC
	// clears public-key validation and actually reaches the injected
	// ReplaceACL failure this test targets.
	pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
	nocRaw := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

	// Step 3: AddNOC — this will succeed through AddFabric + UpsertIdentity +
	// UpsertGroupKeySet (IPK) but fail at ReplaceACL.
	ipk := make([]byte, 16)
	for i := range ipk {
		ipk[i] = byte(0xAA)
	}
	resp, err := oc.MatterInvoke(ctx, 0x06,
		core.AddNOCRequest{
			NOCValue:         nocRaw,
			IPKValue:         ipk,
			CaseAdminSubject: 0x0001_0203_0405_0607,
			AdminVendorID:    0x1234,
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC: unexpected IM error: %v", err)
	}
	nocResp, ok := resp.(core.NOCResponse)
	if !ok {
		t.Fatalf("AddNOC response type = %T, want NOCResponse", resp)
	}
	if nocResp.StatusCode == core.NOCStatusOK {
		t.Fatal("AddNOC: expected failure (ReplaceACL injected error), got OK")
	}

	// The fabric row added by AddFabric must have been rolled back.
	fabrics, err := inner.ListFabrics(ctx)
	if err != nil {
		t.Fatalf("ListFabrics: %v", err)
	}
	if len(fabrics) != 0 {
		t.Errorf("fabric table after AddNOC failure: len=%d, want 0 (rollback expected)", len(fabrics))
	}

	// All group-key rows for every fabric index must be gone.
	// The failed fabric index would have been 1 (first allocation); check
	// that no key sets remain under any index in the 1..10 range.
	for fi := uint8(1); fi <= 10; fi++ {
		keys, err := inner.ListGroupKeySets(ctx, fi)
		if err != nil {
			t.Fatalf("ListGroupKeySets(fi=%d): %v", fi, err)
		}
		if len(keys) != 0 {
			t.Errorf("group keys for fabricIndex=%d after rollback: want 0, got %d (IPK epoch-key not cleaned up)",
				fi, len(keys))
		}
	}
}

// TestAddNOC_UpsertIdentityFailure_RevertsViaCanonicalHelper verifies that
// when AddNOC fails during UpsertIdentity the canonical revertAddNOC helper
// removes the fabric row that AddFabric already committed. The rollback path
// for every failure after AddFabric must use the same helper so new store
// tables added in a future iteration are also cleaned up in one place.
func TestAddNOC_UpsertIdentityFailure_RevertsViaCanonicalHelper(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	inner := newFakeStore()
	wrapped := &upsertIdentityFailStore{
		fakeStore:         inner,
		upsertIdentityErr: errors.New("injected UpsertIdentity failure"),
	}

	oc, err := core.NewOperationalCredentials(wrapped, core.OpcredsConfig{
		SupportedFabrics: 5,
	})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	oc.SetIsFailSafeArmed(func() bool { return true })

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey root: %v", err)
	}
	rootRaw := buildCoreSignedCert(t, rootPriv, true, rootPriv)

	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Mint the NOC over the real pending key so AddNOC clears public-key
	// validation and actually reaches the injected UpsertIdentity failure
	// this test targets.
	pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
	nocRaw := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

	ipk := make([]byte, 16)
	resp, err := oc.MatterInvoke(ctx, 0x06,
		core.AddNOCRequest{
			NOCValue:         nocRaw,
			IPKValue:         ipk,
			CaseAdminSubject: 0x0001_0203_0405_0607,
			AdminVendorID:    0x1234,
		},
		hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC: unexpected IM error: %v", err)
	}
	nocResp, ok := resp.(core.NOCResponse)
	if !ok {
		t.Fatalf("AddNOC response type = %T, want NOCResponse", resp)
	}
	if nocResp.StatusCode == core.NOCStatusOK {
		t.Fatal("AddNOC: expected failure (UpsertIdentity injected error), got OK")
	}

	// The fabric row added by AddFabric must have been rolled back via
	// revertAddNOC.
	fabrics, err := inner.ListFabrics(ctx)
	if err != nil {
		t.Fatalf("ListFabrics: %v", err)
	}
	if len(fabrics) != 0 {
		t.Errorf("fabric table after UpsertIdentity failure: len=%d, want 0 (canonical rollback expected)", len(fabrics))
	}
}

// TestAddNOC_InvalidCaseAdminSubject_PersistsNothing pins that the
// CaseAdminSubject range check runs before the fabric, the identity
// (including its private key) and the IPK key set are written. A subject
// the bridge refuses must not leave a half-installed fabric behind: a
// retry inside the same fail-safe window would hit fabricAlreadyInstalled
// and answer FabricConflict instead of re-running cleanly.
//
// The Group node ID case additionally pins the operational-node upper
// bound: chip src/lib/core/NodeId.h:59 kMaxOperationalNodeId is
// 0xFFFF_FFEF_FFFF_FFFF, so every reserved subrange above it must be
// refused rather than persisted as an unmatchable Administer ACL.
func TestAddNOC_InvalidCaseAdminSubject_PersistsNothing(t *testing.T) {
	t.Parallel()

	for name, subject := range map[string]uint64{
		"undefined":       0,
		"group":           0xFFFF_FFFF_FFFF_FF01,
		"temporary-local": 0xFFFF_FFFE_0000_0001,
		// A CAT whose low 16 bits (the version) are zero. The ACL
		// validator refuses it, so accepting it here would persist an
		// Administer entry no controller can ever rewrite — every
		// round-trip of the ACL attribute rejects the same subject.
		"cat-version-zero": 0xFFFF_FFFD_0001_0000,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := newFakeStore()
			oc, err := core.NewOperationalCredentials(store, core.OpcredsConfig{SupportedFabrics: 5})
			if err != nil {
				t.Fatalf("NewOperationalCredentials: %v", err)
			}
			oc.SetIsFailSafeArmed(func() bool { return true })

			rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatalf("GenerateKey root: %v", err)
			}
			rootRaw := buildCoreSignedCert(t, rootPriv, true, rootPriv)
			if _, err := oc.MatterInvoke(ctx, 0x0B,
				core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
				hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("AddTrustedRootCertificate: %v", err)
			}
			pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
			nocRaw := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

			resp, err := oc.MatterInvoke(ctx, 0x06,
				core.AddNOCRequest{
					NOCValue:         nocRaw,
					IPKValue:         make([]byte, 16),
					CaseAdminSubject: subject,
					AdminVendorID:    0x1234,
				},
				hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("AddNOC: unexpected IM error: %v", err)
			}
			nocResp, ok := resp.(core.NOCResponse)
			if !ok {
				t.Fatalf("AddNOC response type = %T, want NOCResponse", resp)
			}
			if nocResp.StatusCode != core.NOCStatusInvalidAdminSubject {
				t.Fatalf("AddNOC StatusCode = %v, want InvalidAdminSubject", nocResp.StatusCode)
			}

			fabrics, err := store.ListFabrics(ctx)
			if err != nil {
				t.Fatalf("ListFabrics: %v", err)
			}
			if len(fabrics) != 0 {
				t.Errorf("fabric table after rejected CaseAdminSubject: len=%d, want 0", len(fabrics))
			}
			for fi := uint8(1); fi <= 5; fi++ {
				keys, err := store.ListGroupKeySets(ctx, fi)
				if err != nil {
					t.Fatalf("ListGroupKeySets(fi=%d): %v", fi, err)
				}
				if len(keys) != 0 {
					t.Errorf("group keys for fabricIndex=%d: want 0, got %d", fi, len(keys))
				}
			}
		})
	}
}

// TestAddNOC_VersionedCASEAuthTagSubjectIsAccepted is the positive half of
// the CaseAdminSubject guard: a CAT with a non-zero version is a legitimate
// administrator subject and must commission normally. It also pins that the
// default ACL entry AddNOC installs carries a subject the AccessControl
// cluster itself considers valid, so a controller can read the list back and
// write it again.
func TestAddNOC_VersionedCASEAuthTagSubjectIsAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const catSubject uint64 = 0xFFFF_FFFD_0001_0001 // CAT id 0x0001, version 1

	store := newFakeStore()
	oc, err := core.NewOperationalCredentials(store, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey root: %v", err)
	}
	rootRaw := buildCoreSignedCert(t, rootPriv, true, rootPriv)
	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}
	pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
	nocRaw := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: catSubject,
		AdminVendorID:    0x1234,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("AddNOC: unexpected IM error: %v", err)
	}
	nocResp, ok := resp.(core.NOCResponse)
	if !ok {
		t.Fatalf("AddNOC response type = %T, want NOCResponse", resp)
	}
	if nocResp.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %v (%s), want OK for a versioned CAT subject", nocResp.StatusCode, nocResp.DebugText)
	}

	entries, err := store.ListACL(ctx, nocResp.FabricIndex)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(entries) != 1 || len(entries[0].Subjects) != 1 || entries[0].Subjects[0] != catSubject {
		t.Fatalf("default ACL = %+v, want a single Administer entry for the CAT subject", entries)
	}

	// The persisted subject must survive a controller round-trip through
	// the ACL attribute — the write path is the same validator the
	// AddNOC guard now uses.
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	wire := make([]core.AccessControlEntryStruct, 0, len(entries))
	for _, e := range entries {
		wire = append(wire, core.AccessControlEntryStruct{
			Privilege:   uint8(e.Privilege),
			AuthMode:    uint8(e.AuthMode),
			Subjects:    e.Subjects,
			FabricIndex: e.FabricIndex,
		})
	}
	if err := ac.MatterWrite(ctx, 0x0000, wire, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ACL write-back of the AddNOC default entry: %v", err)
	}
}
