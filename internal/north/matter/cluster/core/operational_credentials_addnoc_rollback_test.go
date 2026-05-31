// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
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

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	// Step 1: AddTrustedRootCertificate — sets pendingTrustRoot.
	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	// Step 2: CSRRequest — generates the pending key pair.
	csrNonce := make([]byte, 32)
	for i := range csrNonce {
		csrNonce[i] = byte(i + 1)
	}
	if _, err := oc.MatterInvoke(ctx, 0x04,
		core.CSRRequest{CSRNonce: csrNonce},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

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

	rootRaw, nocRaw, _ := buildTestNOCAndRoot(t)

	if _, err := oc.MatterInvoke(ctx, 0x0B,
		core.AddTrustedRootCertificateRequest{RootCACertificate: rootRaw},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	csrNonce := make([]byte, 32)
	if _, err := oc.MatterInvoke(ctx, 0x04,
		core.CSRRequest{CSRNonce: csrNonce},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("CSRRequest: %v", err)
	}

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
