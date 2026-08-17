// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

type fakeACLLister struct {
	entries []store.ACLEntry
	err     error
}

func (f fakeACLLister) ListACL(_ context.Context, fabricIndex uint8) ([]store.ACLEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.ACLEntry
	for _, e := range f.entries {
		if e.FabricIndex == fabricIndex {
			out = append(out, e)
		}
	}
	return out, nil
}

// caseEntry is a CASE ACL entry on fabric f granting priv over the given
// targets (nil targets = all) without restricting subjects (wildcard).
func caseEntry(f uint8, priv store.Privilege, targets []store.ACLTarget) store.ACLEntry {
	return store.ACLEntry{FabricIndex: f, Privilege: priv, AuthMode: store.AuthModeCASE, Targets: targets}
}

// caseEntryWithSubjects is caseEntry plus a non-wildcard Subjects list.
// A request whose (NodeID, CATs) tuple does not satisfy at least one
// listed subject must be denied per Matter §9.10.5.6 and chip
// src/access/AccessControl.cpp:463-509.
func caseEntryWithSubjects(f uint8, priv store.Privilege, subjects []uint64, targets []store.ACLTarget) store.ACLEntry {
	return store.ACLEntry{
		FabricIndex: f,
		Privilege:   priv,
		AuthMode:    store.AuthModeCASE,
		Subjects:    subjects,
		Targets:     targets,
	}
}

const (
	privView    uint8 = 1
	privOperate uint8 = 3
	privManage  uint8 = 4
)

func TestTopologyDispatcher_CheckACL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const ep uint16 = 1
	const onOff uint32 = 0x0006
	const otherCluster uint32 = 0x0008
	const peerA uint64 = 0x00000000_0001B669 // commissioner's operational NodeID
	const peerB uint64 = 0x00000000_0002CCCC // foreign resident on same fabric

	// CAT subject helpers mirror chip src/lib/core/CASEAuthTag.h packing:
	// nodeID = 0xFFFF'FFFD'0000'0000 | (identifier<<16 | version).
	const catSubjectAdminV1 uint64 = 0xFFFFFFFD_0001_0001 // identifier=0x0001, version=0x0001
	const catSubjectAdminV2 uint64 = 0xFFFFFFFD_0001_0002 // identifier=0x0001, version=0x0002
	const catSubjectOpsV1 uint64 = 0xFFFFFFFD_0042_0001   // identifier=0x0042, version=0x0001
	// And the matching peer-CAT entries: 32-bit packed identifier+version.
	const catNocAdminV1 uint32 = 0x0001_0001
	const catNocAdminV2 uint32 = 0x0001_0002
	const catNocOpsV1 uint32 = 0x0042_0001

	tests := []struct {
		name       string
		lister     ACLLister
		fabric     uint8
		subjNodeID uint64
		subjCATs   []uint32
		cluster    uint32
		required   uint8
		want       im.StatusCode
	}{
		// A dispatcher with no ACL source cannot tell an authorised
		// controller from any other node that completed CASE, so it must
		// deny. Granting instead made the whole gate hang off one wiring
		// line in the composition root whose removal nothing observed.
		{"nil lister denies operational access", nil, 1, peerA, nil, onOff, privManage, im.StatusUnsupportedAccess},
		{"nil lister denies even a view read", nil, 1, peerA, nil, onOff, privView, im.StatusUnsupportedAccess},
		// Commissioning runs over PASE and is decided before the source is
		// consulted, so a bridge that has never been commissioned can still
		// be commissioned with no ACL source wired.
		{"nil lister still admits PASE commissioning", nil, 0, 0, nil, onOff, privManage, im.StatusSuccess},
		// The deliberate opt-out: a setup that runs without stored entries
		// says so, and then behaves as an all-granting fabric.
		{"unenforced lister grants", UnenforcedACL{}, 1, peerA, nil, onOff, privManage, im.StatusSuccess},
		{"PASE fabric 0 bypass", fakeACLLister{}, 0, 0, nil, onOff, privManage, im.StatusSuccess},
		{
			"administer wildcard subjects grants operate",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeAdminister, nil)}},
			1, peerA, nil, onOff, privOperate, im.StatusSuccess,
		},
		{
			"view wildcard subjects denies operate",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeView, nil)}},
			1, peerA, nil, onOff, privOperate, im.StatusUnsupportedAccess,
		},
		{
			"view wildcard subjects grants view",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeView, nil)}},
			1, peerA, nil, onOff, privView, im.StatusSuccess,
		},
		{
			"cluster-scoped operate matches target cluster",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, []store.ACLTarget{{Cluster: new(onOff)}})}},
			1, peerA, nil, onOff, privOperate, im.StatusSuccess,
		},
		{
			"cluster-scoped operate denies other cluster",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeOperate, []store.ACLTarget{{Cluster: new(onOff)}})}},
			1, peerA, nil, otherCluster, privOperate, im.StatusUnsupportedAccess,
		},
		{
			"foreign fabric with no entry is denied",
			fakeACLLister{entries: []store.ACLEntry{caseEntry(1, store.PrivilegeAdminister, nil)}},
			2, peerA, nil, onOff, privView, im.StatusUnsupportedAccess,
		},
		{
			"non-CASE (group) entry does not grant CASE access",
			fakeACLLister{entries: []store.ACLEntry{{FabricIndex: 1, Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeGroup}}},
			1, peerA, nil, onOff, privView, im.StatusUnsupportedAccess,
		},
		{
			"store error fails closed",
			fakeACLLister{err: errors.New("db down")},
			1, peerA, nil, onOff, privView, im.StatusUnsupportedAccess,
		},

		// Subject matching (Matter §9.10.5.6 + chip AccessControl.cpp:463-509).
		{
			"explicit subject node id matches",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{peerA}, nil)}},
			1, peerA, nil, onOff, privOperate, im.StatusSuccess,
		},
		{
			"explicit subject node id mismatch denies",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{peerA}, nil)}},
			1, peerB, nil, onOff, privOperate, im.StatusUnsupportedAccess,
		},
		{
			"multi-subject list grants on any match",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeOperate, []uint64{peerA, peerB}, nil)}},
			1, peerB, nil, onOff, privOperate, im.StatusSuccess,
		},
		{
			"explicit subject list with no match denies even with wildcard target",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{peerA}, nil)}},
			1, peerB, nil, otherCluster, privView, im.StatusUnsupportedAccess,
		},

		// CAT subject matching (chip CASEAuthTag.h:174-190).
		{
			"CAT subject grants when peer holds same identifier+version",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{catSubjectAdminV1}, nil)}},
			1, peerA,
			[]uint32{catNocAdminV1},
			onOff, privManage, im.StatusSuccess,
		},
		{
			"CAT subject grants when peer holds higher version than entry",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{catSubjectAdminV1}, nil)}},
			1, peerA,
			[]uint32{catNocAdminV2},
			onOff, privManage, im.StatusSuccess,
		},
		{
			"CAT subject denies when peer holds lower version than entry",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{catSubjectAdminV2}, nil)}},
			1, peerA,
			[]uint32{catNocAdminV1},
			onOff, privView, im.StatusUnsupportedAccess,
		},
		{
			"CAT subject denies when identifier differs",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{catSubjectAdminV1}, nil)}},
			1, peerA,
			[]uint32{catNocOpsV1},
			onOff, privView, im.StatusUnsupportedAccess,
		},
		{
			"CAT subject denies when peer has no CATs",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{catSubjectAdminV1}, nil)}},
			1, peerA, nil, onOff, privView, im.StatusUnsupportedAccess,
		},
		{
			"mixed subjects: CAT match wins even when node id mismatches",
			fakeACLLister{entries: []store.ACLEntry{caseEntryWithSubjects(1, store.PrivilegeAdminister, []uint64{peerA, catSubjectOpsV1}, nil)}},
			1, peerB,
			[]uint32{catNocOpsV1},
			onOff, privOperate, im.StatusSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &TopologyDispatcher{acl: tt.lister}
			got := d.CheckACL(ctx, tt.fabric, tt.subjNodeID, tt.subjCATs, ep, tt.cluster, tt.required)
			if got != tt.want {
				t.Fatalf("CheckACL = 0x%02X, want 0x%02X", got, tt.want)
			}
		})
	}
}

// TopologyDispatcher must satisfy im.ACLChecker so the IM read/write/invoke
// gates engage in production.
func TestTopologyDispatcher_ImplementsACLChecker(t *testing.T) {
	t.Parallel()
	var _ im.ACLChecker = (*TopologyDispatcher)(nil)
}

// TestEndpointHasDeviceType verifies the device-type resolver behind
// DeviceType-restricted ACL targets: the root endpoint reports RootNode,
// the aggregator endpoint (ID 1) reports AggregatorEndpoint, and a
// bridged endpoint reports both its own primary device type and the
// universal BridgedNode tag. A nil endpoint (unresolved) never matches.
// Mirrors chip AccessControl.h:53 DeviceTypeResolver /
// ProviderDeviceTypeResolver.h:34.
func TestEndpointHasDeviceType(t *testing.T) {
	t.Parallel()
	root := &Endpoint{ID: 0}
	agg := &Endpoint{ID: 1}
	bridged := &Endpoint{ID: 5, DeviceType: 0x0100} // OnOffLight

	cases := []struct {
		name       string
		ep         *Endpoint
		deviceType uint32
		want       bool
	}{
		{"root matches RootNode", root, deviceTypeRootNode, true},
		{"root denies AggregatorEndpoint", root, deviceTypeAggregator, false},
		{"aggregator matches AggregatorEndpoint", agg, deviceTypeAggregator, true},
		{"aggregator denies RootNode", agg, deviceTypeRootNode, false},
		{"bridged matches own primary type", bridged, 0x0100, true},
		{"bridged matches BridgedNode", bridged, matterDeviceTypeBridgedNode, true},
		{"bridged denies an unrelated type", bridged, 0x0102, false},
		{"nil endpoint denies", nil, deviceTypeRootNode, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := endpointHasDeviceType(tc.ep, tc.deviceType); got != tc.want {
				t.Errorf("endpointHasDeviceType(ep, 0x%04X) = %v, want %v", tc.deviceType, got, tc.want)
			}
		})
	}
}

// TestAclTargetMatches_DeviceTypeOnly verifies that an ACLTarget whose
// only restriction is DeviceType (chip AccessControl.cpp:529-530
// `IsDeviceTypeOnEndpoint(target.deviceType, requestPath.endpoint)`)
// matches an endpoint hosting that device type — either as its primary
// type or via the universal BridgedNode tag — and denies both a
// non-hosted type and an unresolved (nil) endpoint.
func TestAclTargetMatches_DeviceTypeOnly(t *testing.T) {
	t.Parallel()
	const hostedType uint32 = 0x0100 // OnOffLight — the fixture endpoint's primary type
	const otherType uint32 = 0x0102  // not hosted by the fixture endpoint
	const ep uint16 = 5
	const anyCluster uint32 = 0x0006

	bridged := &Endpoint{ID: ep, DeviceType: 0x0100}

	cases := []struct {
		name   string
		ep     *Endpoint
		target store.ACLTarget
		want   bool
	}{
		{"matches bridged primary device type", bridged, store.ACLTarget{DeviceType: new(hostedType)}, true},
		{"matches via BridgedNode", bridged, store.ACLTarget{DeviceType: new(matterDeviceTypeBridgedNode)}, true},
		{"denies a non-hosted device type", bridged, store.ACLTarget{DeviceType: new(otherType)}, false},
		{"nil endpoint denies", nil, store.ACLTarget{DeviceType: new(hostedType)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := aclTargetMatches([]store.ACLTarget{tc.target}, tc.ep, ep, anyCluster)
			if got != tc.want {
				t.Errorf("aclTargetMatches(...) = %v, want %v", got, tc.want)
			}
		})
	}
}
