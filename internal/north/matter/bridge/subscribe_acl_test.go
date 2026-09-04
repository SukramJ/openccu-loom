// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box tests for Bridge.readAuthorizedResults (subscribe.go) — the
// subscription-path ACL gate that mirrors HandleReadRequest's per-result
// authorization on top of dispatcher.Read. Lives in package bridge to call
// the unexported method directly.
//
// Cases 1-3 mount a real cluster/core.AccessControl (0x001F) cluster
// server on the root endpoint of a real endpoint.TopologyDispatcher (via
// Bridge.AttachRootClusters + Bridge.AttachACLLister + Bridge.Reassemble)
// so the test exercises the exact MinReadPrivilege / CheckACL wiring
// production uses, following the pattern in
// internal/north/matter/endpoint/dispatcher_acl_test.go. Case 4 needs a
// dispatcher that does NOT implement im.ACLChecker; TopologyDispatcher
// always does, so a minimal fake im.Dispatcher stands in for that one case.

import (
	"context"
	"testing"

	core "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// aclStoreFake backs both the AccessControl cluster server's
// ACLStoreFacade and the TopologyDispatcher's ACLLister with the same
// in-memory entry set, mirroring how a single matter/store.Store
// instance serves both roles in production.
type aclStoreFake struct {
	entries []store.ACLEntry
}

func (f *aclStoreFake) ListACL(_ context.Context, fabricIndex uint8) ([]store.ACLEntry, error) {
	var out []store.ACLEntry
	for _, e := range f.entries {
		if e.FabricIndex == fabricIndex {
			out = append(out, e)
		}
	}
	return out, nil
}

// ReplaceACL satisfies core.ACLStoreFacade; readAuthorizedResults only
// ever reads, so this is never invoked by the tests below.
func (f *aclStoreFake) ReplaceACL(_ context.Context, _ uint8, _ []store.ACLEntry) error {
	return nil
}

// accessControlACLPath is the AccessControl.ACL attribute (endpoint 0,
// cluster 0x001F, attribute 0x0000) — the fabric-sensitive path
// readAuthorizedResults exists to protect (Matter §9.10.5.3: ACL/Extension
// require Administer).
func accessControlACLPath() im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint: 0, HasEndpoint: true,
		Cluster: 0x001F, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
}

// newACLTestBridge builds a started Bridge with a real AccessControl
// cluster server mounted on the root endpoint and fake wired as both the
// cluster's store and the dispatcher's ACLLister, then reassembles so the
// live TopologyDispatcher picks up both.
func newACLTestBridge(t *testing.T, fake *aclStoreFake) *Bridge {
	t.Helper()
	b := newStartedBridge(t)
	ac, err := core.NewAccessControl(fake)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	b.AttachRootClusters([]matterport.ClusterServer{ac})
	b.AttachACLLister(fake)
	if err := b.Reassemble(context.Background()); err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	return b
}

// TestReadAuthorizedResults_UnauthorizedAttributeDropped verifies that a
// CASE subject holding only View on fabric 1 (no elevated grant at all)
// gets nothing back for AccessControl.ACL, which requires Administer.
func TestReadAuthorizedResults_UnauthorizedAttributeDropped(t *testing.T) {
	t.Parallel()
	fake := &aclStoreFake{entries: []store.ACLEntry{
		{FabricIndex: 1, Privilege: store.PrivilegeView, AuthMode: store.AuthModeCASE},
	}}
	b := newACLTestBridge(t, fake)

	ctx := im.WithFabricFilter(context.Background(), true, 1)
	ctx = im.WithSubject(ctx, 0x0000000000001111, nil)

	got := b.readAuthorizedResults(ctx, b.Dispatcher(), accessControlACLPath())
	if len(got) != 0 {
		t.Fatalf("readAuthorizedResults: want 0 results (View is insufficient for AccessControl.ACL), got %d: %+v", len(got), got)
	}
}

// TestReadAuthorizedResults_AuthorizedAttributeKept verifies that a CASE
// subject holding an Administer grant on fabric 1 gets the
// AccessControl.ACL result back unfiltered.
func TestReadAuthorizedResults_AuthorizedAttributeKept(t *testing.T) {
	t.Parallel()
	fake := &aclStoreFake{entries: []store.ACLEntry{
		{FabricIndex: 1, Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE},
	}}
	b := newACLTestBridge(t, fake)

	ctx := im.WithFabricFilter(context.Background(), true, 1)
	ctx = im.WithSubject(ctx, 0x0000000000001111, nil)

	got := b.readAuthorizedResults(ctx, b.Dispatcher(), accessControlACLPath())
	if len(got) != 1 {
		t.Fatalf("readAuthorizedResults: want 1 result (Administer grants AccessControl.ACL), got %d: %+v", len(got), got)
	}
	want := accessControlACLPath()
	if got[0].Path != want {
		t.Errorf("readAuthorizedResults: path = %+v, want %+v", got[0].Path, want)
	}
	if got[0].Status != im.StatusSuccess {
		t.Errorf("readAuthorizedResults: status = %v, want StatusSuccess", got[0].Status)
	}
}

// TestReadAuthorizedResults_PASEBypassesACL verifies that fabricIndex==0
// (PASE / pre-commissioning) returns the read result unchanged even when
// the ACL lister would deny every fabric-1 subject — ACL enforcement does
// not apply before AddNOC.
func TestReadAuthorizedResults_PASEBypassesACL(t *testing.T) {
	t.Parallel()
	// A restrictive lister: fabric 1 holds no grant at all, and the
	// request below carries fabricIndex 0 (PASE) so this must never be
	// consulted for the drop/keep decision.
	fake := &aclStoreFake{}
	b := newACLTestBridge(t, fake)

	ctx := im.WithFabricFilter(context.Background(), false, 0)
	ctx = im.WithSubject(ctx, 0, nil)

	got := b.readAuthorizedResults(ctx, b.Dispatcher(), accessControlACLPath())
	if len(got) != 1 {
		t.Fatalf("readAuthorizedResults: want 1 result under PASE bypass, got %d: %+v", len(got), got)
	}
	if got[0].Status != im.StatusSuccess {
		t.Errorf("readAuthorizedResults: status = %v, want StatusSuccess", got[0].Status)
	}
}

// fakeReadOnlyDispatcher implements im.Dispatcher but deliberately does
// NOT implement im.ACLChecker, exercising the passthrough branch of
// readAuthorizedResults for a dispatcher that never wired ACL
// enforcement. TopologyDispatcher (the production dispatcher) always
// implements im.ACLChecker, so this case cannot be reached with the real
// type — a minimal fake stands in.
type fakeReadOnlyDispatcher struct {
	results []im.ReadResult
}

func (f *fakeReadOnlyDispatcher) Read(_ context.Context, _ im.ConcreteAttributePath) []im.ReadResult {
	return f.results
}

func (f *fakeReadOnlyDispatcher) Write(_ context.Context, _ im.ConcreteAttributePath, _ im.AttributeValue) []im.WriteResult {
	return nil
}

func (f *fakeReadOnlyDispatcher) Invoke(_ context.Context, _ im.ConcreteCommandPath, _ any) im.InvokeResult {
	return im.InvokeResult{}
}

var _ im.Dispatcher = (*fakeReadOnlyDispatcher)(nil)

// TestReadAuthorizedResults_NoACLChecker_Passthrough verifies that a
// dispatcher without an im.ACLChecker returns every result unchanged,
// even for a CASE fabric and a fabric-sensitive path — matching the
// documented "dispatcher without ACLChecker" fallback.
func TestReadAuthorizedResults_NoACLChecker_Passthrough(t *testing.T) {
	t.Parallel()
	path := accessControlACLPath()
	fd := &fakeReadOnlyDispatcher{results: []im.ReadResult{{Path: path, Status: im.StatusSuccess}}}

	ctx := im.WithFabricFilter(context.Background(), true, 1)
	ctx = im.WithSubject(ctx, 0x0000000000001111, nil)

	b := &Bridge{}
	got := b.readAuthorizedResults(ctx, fd, path)
	if len(got) != 1 {
		t.Fatalf("readAuthorizedResults: want 1 result (no ACLChecker => passthrough), got %d: %+v", len(got), got)
	}
	if got[0] != fd.results[0] {
		t.Errorf("readAuthorizedResults: result = %+v, want unchanged %+v", got[0], fd.results[0])
	}
}
