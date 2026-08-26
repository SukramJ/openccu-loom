// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint

import (
	"context"
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// stubACLStore is a minimal [mattercore.ACLStoreFacade] used only to
// construct a real AccessControl cluster server. MinWritePrivilege is a
// pure (attrID) → privilege switch that never touches the backing store,
// so the facade need not hold any data.
type stubACLStore struct{}

func (stubACLStore) ListACL(_ context.Context, _ uint8) ([]store.ACLEntry, error) { return nil, nil }

func (stubACLStore) ReplaceACL(_ context.Context, _ uint8, _ []store.ACLEntry) error { return nil }

var _ mattercore.ACLStoreFacade = stubACLStore{}

// recordingACLStore is a [mattercore.ACLStoreFacade] that counts
// ReplaceACL calls, so a test can assert that a denied write never
// reached the persistent ACL table.
type recordingACLStore struct {
	replaced int
}

func (r *recordingACLStore) ListACL(_ context.Context, _ uint8) ([]store.ACLEntry, error) {
	return nil, nil
}

func (r *recordingACLStore) ReplaceACL(_ context.Context, _ uint8, _ []store.ACLEntry) error {
	r.replaced++
	return nil
}

var _ mattercore.ACLStoreFacade = (*recordingACLStore)(nil)

// stubOpCredsStore is a minimal [mattercore.StoreFacade] used only to
// construct a real OperationalCredentials cluster server. Same rationale
// as [stubACLStore]: MinInvokePrivilege dispatches on cmdID alone.
type stubOpCredsStore struct{}

func (stubOpCredsStore) ListFabrics(_ context.Context) ([]store.FabricRecord, error) {
	return nil, nil
}

func (stubOpCredsStore) GetFabric(_ context.Context, _ uint8) (store.FabricRecord, error) {
	return store.FabricRecord{}, nil
}

func (stubOpCredsStore) AddFabric(_ context.Context, _ store.FabricRecord) (uint8, error) {
	return 0, nil
}

func (stubOpCredsStore) UpdateFabricLabel(_ context.Context, _ uint8, _ string) error { return nil }

func (stubOpCredsStore) UpdateFabricNodeID(_ context.Context, _ uint8, _ uint64) error { return nil }

func (stubOpCredsStore) RemoveFabric(_ context.Context, _ uint8) error { return nil }

func (stubOpCredsStore) UpsertIdentity(_ context.Context, _ store.IdentityRecord) error { return nil }

func (stubOpCredsStore) GetIdentity(_ context.Context, _ uint8) (store.IdentityRecord, error) {
	return store.IdentityRecord{}, nil
}

func (stubOpCredsStore) ReplaceACL(_ context.Context, _ uint8, _ []store.ACLEntry) error {
	return nil
}

func (stubOpCredsStore) UpsertGroupKeySet(_ context.Context, _ store.GroupKeySet) error { return nil }

func (stubOpCredsStore) RemoveGroupKeysByFabric(_ context.Context, _ uint8) error { return nil }

var _ mattercore.StoreFacade = stubOpCredsStore{}

// plainClusterID is an arbitrary cluster ID distinct from AccessControl
// (0x001F), OperationalCredentials (0x003E) and GeneralCommissioning
// (0x0030). It stands in for an ordinary cluster that implements neither
// [interfaces.MatterClusterAttributeWritePrivilege] nor
// [interfaces.MatterClusterCommandInvokePrivilege], so the dispatcher
// falls back to the Matter §9.10.4.4 default (Operate, 3) for every
// attribute/command on it.
const plainClusterID uint32 = 0x0201

// privilegeTestTopology builds a root endpoint (ID 0) mounting real
// AccessControl / OperationalCredentials / GeneralCommissioning cluster
// servers — the three root clusters whose MinWritePrivilege /
// MinInvokePrivilege implementations back the C1 write/invoke ACL fix —
// plus one plain [fakeServerFull] (from dispatcher_test.go) representing
// a cluster with no elevated-privilege requirements at all.
func privilegeTestTopology(t *testing.T) *Topology {
	t.Helper()
	acl, err := mattercore.NewAccessControl(stubACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	opc, err := mattercore.NewOperationalCredentials(stubOpCredsStore{}, mattercore.OpcredsConfig{})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}
	gc, err := mattercore.NewGeneralCommissioning(mattercore.GeneralCommissioningConfig{})
	if err != nil {
		t.Fatalf("NewGeneralCommissioning: %v", err)
	}
	plain := &fakeServerFull{id: plainClusterID}

	return &Topology{
		Endpoints: []*Endpoint{
			rootEndpointWith(acl, opc, gc, plain),
			{ID: 1, DeviceType: 0x000E}, // aggregator
		},
		NodeLabel: "test", VendorID: 0xFFF1, ProductID: 0x8000,
	}
}

// =============================================================================
// MinWritePrivilege / MinInvokePrivilege value tests
// =============================================================================

// TestTopologyDispatcher_MinWritePrivilege locks the per-attribute write
// privilege the dispatcher reports for the mounted root clusters, plus the
// Operate-default fallback for unmounted endpoints/clusters and
// non-elevated attributes. Mirrors Matter §9.10.5.3 (AccessControl.ACL /
// Extension → Administer) and matter.js
// packages/model/src/standard/elements/access-control.element.ts:28,32 +
// general-commissioning.element.ts:26.
func TestTopologyDispatcher_MinWritePrivilege(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))

	const (
		accessControlID   uint32 = 0x001F
		generalCommID     uint32 = 0x0030
		accessControlACL  uint32 = 0x0000
		accessControlExt  uint32 = 0x0001
		accessControlCap  uint32 = 0x0002 // SubjectsPerAccessControlEntry — no write-privilege override
		gcBreadcrumb      uint32 = 0x0000
		gcCommissioningIn uint32 = 0x0001 // BasicCommissioningInfo — no write-privilege override
	)

	tests := []struct {
		name      string
		endpoint  uint16
		cluster   uint32
		attribute uint32
		want      uint8
	}{
		{"AccessControl.ACL requires Administer", 0, accessControlID, accessControlACL, 5},
		{"AccessControl.Extension requires Administer", 0, accessControlID, accessControlExt, 5},
		{"AccessControl attribute without override defaults to Operate", 0, accessControlID, accessControlCap, 3},
		// GeneralCommissioning.Breadcrumb is itself Administer-gated
		// (Matter §11.10, access "RW VA") — not the Operate-level control
		// case one might expect from a "commissioning" attribute.
		{"GeneralCommissioning.Breadcrumb requires Administer", 0, generalCommID, gcBreadcrumb, 5},
		{"GeneralCommissioning attribute without override defaults to Operate", 0, generalCommID, gcCommissioningIn, 3},
		{"plain cluster attribute defaults to Operate", 0, plainClusterID, 0x0000, 3},
		{"unmounted cluster on a real endpoint defaults to Operate", 0, 0x9999, 0x0000, 3},
		{"unmounted endpoint defaults to Operate", 42, accessControlID, accessControlACL, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := d.MinWritePrivilege(tt.endpoint, tt.cluster, tt.attribute)
			if got != tt.want {
				t.Errorf("MinWritePrivilege(ep=%d, cluster=0x%04X, attr=0x%04X) = %d, want %d",
					tt.endpoint, tt.cluster, tt.attribute, got, tt.want)
			}
		})
	}
}

// TestTopologyDispatcher_MinInvokePrivilege locks the per-command invoke
// privilege the dispatcher reports for the mounted root clusters, plus the
// Operate-default fallback. Mirrors Matter §11.18 (OperationalCredentials
// commands → Administer) + §11.10 (GeneralCommissioning.ArmFailSafe /
// SetRegulatoryConfig / CommissioningComplete → Administer) and the
// matching matter.js element files.
func TestTopologyDispatcher_MinInvokePrivilege(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))

	const (
		opCredsID          uint32 = 0x003E
		generalCommID      uint32 = 0x0030
		opCredsRemoveFab   uint32 = 0x0A
		opCredsUnmappedCmd uint32 = 0x99 // no command uses this id — exercises the default branch
		gcArmFailSafe      uint32 = 0x00
		gcUnmappedCmd      uint32 = 0x99
	)

	tests := []struct {
		name     string
		endpoint uint16
		cluster  uint32
		command  uint32
		want     uint8
	}{
		{"OperationalCredentials.RemoveFabric requires Administer", 0, opCredsID, opCredsRemoveFab, 5},
		{"OperationalCredentials unmapped command defaults to Operate", 0, opCredsID, opCredsUnmappedCmd, 3},
		{"GeneralCommissioning.ArmFailSafe requires Administer", 0, generalCommID, gcArmFailSafe, 5},
		{"GeneralCommissioning unmapped command defaults to Operate", 0, generalCommID, gcUnmappedCmd, 3},
		{"plain cluster command defaults to Operate", 0, plainClusterID, 0x00, 3},
		{"unmounted cluster on a real endpoint defaults to Operate", 0, 0x9999, 0x00, 3},
		{"unmounted endpoint defaults to Operate", 42, opCredsID, opCredsRemoveFab, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := d.MinInvokePrivilege(tt.endpoint, tt.cluster, tt.command)
			if got != tt.want {
				t.Errorf("MinInvokePrivilege(ep=%d, cluster=0x%04X, cmd=0x%02X) = %d, want %d",
					tt.endpoint, tt.cluster, tt.command, got, tt.want)
			}
		})
	}
}

// =============================================================================
// im.HandleWriteRequest / im.HandleInvokeRequest enforcement
// =============================================================================

// privilegeEnforcementCtx builds a context carrying a CASE fabric filter
// (fabricIndex=1, the fabric fakeACLLister entries are keyed against in
// this file) plus an arbitrary subject. The ACL entries used throughout
// this file grant a wildcard Subjects list (via [caseEntry]), so the
// subject's exact identity does not need to match anything — only the
// entry's Privilege is under test.
func privilegeEnforcementCtx() context.Context {
	const anySubject uint64 = 0x00000000_0001B669
	ctx := im.WithFabricFilter(context.Background(), false, 1)
	return im.WithSubject(ctx, anySubject, nil)
}

// TestHandleWriteRequest_EnforcesElevatedWritePrivilege verifies the C1
// fix's central claim: a WriteRequest against AccessControl.ACL from a
// fabric whose only ACL entry grants Operate is rejected with
// StatusUnsupportedAccess — the write dispatcher no longer applies a flat
// Operate gate. Mirrors matter.js
// packages/node/src/node/server/OnlineServerInteraction.ts
// FabricAccessControl.forRequest and chip src/app/WriteHandler.cpp:780.
func TestHandleWriteRequest_EnforcesElevatedWritePrivilege(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, nil)}})

	req := im.WriteRequest{
		Writes: []im.AttributeWrite{
			{Path: concreteAttrPath(0, 0x001F, 0x0000), Value: im.AttributeValue{Value: uint8(0)}},
		},
	}
	resp := im.HandleWriteRequest(privilegeEnforcementCtx(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	if got := resp.Responses[0].Status.Status; got != im.StatusUnsupportedAccess {
		t.Errorf("AccessControl.ACL write from an Operate-only fabric: status = 0x%02X, want StatusUnsupportedAccess (0x%02X)",
			got, im.StatusUnsupportedAccess)
	}
}

// TestHandleInvokeRequest_EnforcesElevatedInvokePrivilege is the invoke
// counterpart: OperationalCredentials.RemoveFabric from an Operate-only
// fabric must be rejected with StatusUnsupportedAccess before the command
// ever reaches the cluster server. Mirrors chip
// src/app/CommandHandler.cpp "Execute the ACL Access Granting Algorithm
// before existence checks".
func TestHandleInvokeRequest_EnforcesElevatedInvokePrivilege(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, nil)}})

	req := im.InvokeRequest{
		Invokes: []im.CommandInvocation{
			{Path: concreteCmdPath(0, 0x003E, 0x0A)},
		},
	}
	resp := im.HandleInvokeRequest(privilegeEnforcementCtx(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	entry := resp.Responses[0]
	if !entry.IsStatus || entry.Status.Status != im.StatusUnsupportedAccess {
		t.Errorf("OperationalCredentials.RemoveFabric invoke from an Operate-only fabric: isStatus=%v status = 0x%02X, want StatusUnsupportedAccess (0x%02X)",
			entry.IsStatus, entry.Status.Status, im.StatusUnsupportedAccess)
	}
}

// TestHandleWriteRequest_AdministerPrivilegeNotBlockedByGate verifies that
// an ACL entry granting Administer clears the elevated-write gate — the
// gate is privilege-scoped, not a permanent deny. The downstream
// cluster-specific write outcome (success or a decode/validation error) is
// out of scope; only the ACL gate itself is under test here.
func TestHandleWriteRequest_AdministerPrivilegeNotBlockedByGate(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeAdminister, nil)}})

	req := im.WriteRequest{
		Writes: []im.AttributeWrite{
			{Path: concreteAttrPath(0, 0x001F, 0x0000), Value: im.AttributeValue{Value: uint8(0)}},
		},
	}
	resp := im.HandleWriteRequest(privilegeEnforcementCtx(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	if got := resp.Responses[0].Status.Status; got == im.StatusUnsupportedAccess {
		t.Errorf("AccessControl.ACL write from an Administer fabric must clear the ACL gate; got StatusUnsupportedAccess")
	}
}

// TestHandleInvokeRequest_AdministerPrivilegeNotBlockedByGate is the
// invoke counterpart of
// [TestHandleWriteRequest_AdministerPrivilegeNotBlockedByGate].
func TestHandleInvokeRequest_AdministerPrivilegeNotBlockedByGate(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeAdminister, nil)}})

	req := im.InvokeRequest{
		Invokes: []im.CommandInvocation{
			{Path: concreteCmdPath(0, 0x003E, 0x0A)},
		},
	}
	resp := im.HandleInvokeRequest(privilegeEnforcementCtx(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	entry := resp.Responses[0]
	if entry.IsStatus && entry.Status.Status == im.StatusUnsupportedAccess {
		t.Errorf("OperationalCredentials.RemoveFabric invoke from an Administer fabric must clear the ACL gate; got StatusUnsupportedAccess")
	}
}

// TestHandleWriteRequest_OperateAttributeNotBlockedByPrivilegeGate is the
// control case: an Operate-privilege fabric writing a plain,
// non-elevated attribute must sail through the gate unblocked, proving
// the per-element privilege check does not over-reject ordinary writes.
// The target is a writable Thermostat attribute (OccupiedHeatingSetpoint
// 0x0012, access "RW VO") so the outcome isolates the privilege gate from
// the schema read-only-write gate (schema.AttributeWritable).
func TestHandleWriteRequest_OperateAttributeNotBlockedByPrivilegeGate(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(privilegeTestTopology(t))
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, nil)}})

	req := im.WriteRequest{
		Writes: []im.AttributeWrite{
			{Path: concreteAttrPath(0, plainClusterID, 0x0012), Value: im.AttributeValue{Value: uint8(1)}},
		},
	}
	resp := im.HandleWriteRequest(privilegeEnforcementCtx(), d, req)
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	if got := resp.Responses[0].Status.Status; got != im.StatusSuccess {
		t.Errorf("Operate-fabric write to a non-elevated attribute: status = 0x%02X, want StatusSuccess (the fake cluster's write always succeeds)", got)
	}
}

// TestHandleWriteRequest_RejectsWildcardAttributePath proves that a write
// whose path omits the attribute id never reaches a cluster server. The
// requested path carries no attribute, so no per-attribute write privilege
// can be resolved before dispatch; an Operate-only subject would otherwise
// pass a flat Operate gate and then have the value fanned out over every
// attribute the cluster exposes — including AccessControl.ACL, which is
// Administer-gated. Matter reserves wildcard paths for Read/Subscribe;
// mirrors matter.js
// packages/protocol/src/action/server/AttributeWriteResponse.ts:278-283
// ("Wildcard path write must specify a clusterId and attributeId") and
// :308-311 ("Wildcard path write must specify an attributeId").
func TestHandleWriteRequest_RejectsWildcardAttributePath(t *testing.T) {
	t.Parallel()
	aclStore := &recordingACLStore{}
	acl, err := mattercore.NewAccessControl(aclStore)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	topo := &Topology{
		Endpoints: []*Endpoint{
			rootEndpointWith(acl),
			{ID: 1, DeviceType: 0x000E},
		},
		NodeLabel: "test", VendorID: 0xFFF1, ProductID: 0x8000,
	}
	d := NewTopologyDispatcher(topo)
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, nil)}})

	// Wildcard attribute: endpoint + cluster named, attribute omitted.
	wildcardAttr := im.ConcreteAttributePath{
		Endpoint: 0, HasEndpoint: true,
		Cluster: 0x001F, HasCluster: true,
	}
	entries := []mattercore.AccessControlEntryStruct{
		{Privilege: 5, AuthMode: 2, Subjects: []uint64{0x0000000000010001}},
	}
	req := im.WriteRequest{
		Writes: []im.AttributeWrite{
			{Path: wildcardAttr, Value: im.AttributeValue{Value: entries}},
		},
	}
	resp := im.HandleWriteRequest(privilegeEnforcementCtx(), d, req)
	if aclStore.replaced != 0 {
		t.Errorf("wildcard-attribute write from an Operate-only subject rewrote the ACL table (%d ReplaceACL calls); the Administer gate was bypassed", aclStore.replaced)
	}
	if len(resp.Responses) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp.Responses))
	}
	if got := resp.Responses[0].Status.Status; got != im.StatusInvalidAction {
		t.Errorf("wildcard-attribute write: status = 0x%02X, want StatusInvalidAction (0x%02X)", got, im.StatusInvalidAction)
	}
}

// TestHandleWriteRequest_WildcardEndpointAuthorizesEveryResolvedEndpoint
// proves that a wildcard-endpoint write is authorized where it resolves,
// not against the un-expanded request path. The subject's only ACE targets
// endpoint 0, so the write must land on endpoint 0 and be silently skipped
// on the bridged endpoint — a wildcard path discloses only authorized
// locations. Mirrors matter.js
// packages/protocol/src/action/server/AttributeWriteResponse.ts:324-343
// (#writeAttributeForWildcard authorizes each resolved attribute at its own
// location and returns without a status on denial).
func TestHandleWriteRequest_WildcardEndpointAuthorizesEveryResolvedEndpoint(t *testing.T) {
	t.Parallel()
	rootSrv := &recordingServer{id: plainClusterID}
	bridgedSrv := &recordingServer{id: plainClusterID}
	topo := &Topology{
		Endpoints: []*Endpoint{
			rootEndpointWith(rootSrv),
			{ID: 1, DeviceType: 0x000E},
			{ID: 3, Source: recordingSource{srv: bridgedSrv}},
		},
		NodeLabel: "test", VendorID: 0xFFF1, ProductID: 0x8000,
	}
	d := NewTopologyDispatcher(topo)
	onlyRoot := uint16(0)
	d.SetACLLister(fakeACLLister{entries: []store.ACLEntry{
		caseEntry(1, store.PrivilegeOperate, []store.ACLTarget{{Endpoint: &onlyRoot}}),
	}})

	// Wildcard endpoint, concrete cluster + attribute (Thermostat
	// OccupiedHeatingSetpoint, access "RW VO").
	wildcardEndpoint := im.ConcreteAttributePath{
		Cluster: plainClusterID, HasCluster: true,
		Attribute: 0x0012, HasAttribute: true,
	}
	req := im.WriteRequest{
		Writes: []im.AttributeWrite{
			{Path: wildcardEndpoint, Value: im.AttributeValue{Value: int16(2000)}},
		},
	}
	resp := im.HandleWriteRequest(privilegeEnforcementCtx(), d, req)

	if len(bridgedSrv.writeCalls) != 0 {
		t.Errorf("wildcard-endpoint write reached endpoint 3 (calls=%v), which the subject's ACE targets do not cover", bridgedSrv.writeCalls)
	}
	if len(rootSrv.writeCalls) != 1 {
		t.Errorf("wildcard-endpoint write on the authorized endpoint 0: calls=%v, want exactly one", rootSrv.writeCalls)
	}
	for _, r := range resp.Responses {
		if r.Path.Endpoint != 0 {
			t.Errorf("wildcard-endpoint write disclosed a status for the unauthorized endpoint %d", r.Path.Endpoint)
		}
	}
}
