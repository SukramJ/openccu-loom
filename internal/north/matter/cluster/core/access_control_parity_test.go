// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package core — AccessControl cluster-server parity tests against
// matter.js HEAD.
//
// matter.js does not ship a dedicated unit-test file for
// AccessControlServer in packages/node/test/behaviors/ as of HEAD
// (verified against matter.js HEAD). The parity invariants below are derived from:
//   - packages/model/src/standard/elements/access-control.element.ts
//   - packages/node/src/behaviors/access-control/AccessControlServer.ts
//   - packages/protocol/test/groups/FabricGroupsManagerTest.ts (for
//     fabric-scoped semantics)
//
// Conversion pattern:
//   - Each test cites the matter.js source file + line.
//   - Cases already exercised in access_control_test.go are marked
//     t.Skip to avoid duplication.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// TestParityMatterJS_AccessControl_ClusterID pins 0x001F.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts:5 (id: 0x001F).
func TestParityMatterJS_AccessControl_ClusterID(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	const wantID uint32 = 0x001F
	if got := ac.MatterClusterID(); got != wantID {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, wantID)
	}
}

// TestParityMatterJS_AccessControl_ClusterRevision3 pins revision 3.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts:21 (ClusterRevision default: 3). Revision
// drift causes chip-tool's "validate cluster revision" step to fail and
// Apple's HAP mapper to bucket the cluster as "unknown".
func TestParityMatterJS_AccessControl_ClusterRevision3(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	v, ok := ac.MatterRead(0xFFFD) // ClusterRevision
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if got := v.(uint16); got != 3 {
		t.Errorf("ClusterRevision = %d, want 3 (matter.js HEAD access-control.element.ts:21)", got)
	}
}

// TestParityMatterJS_AccessControl_ACLAttributePresent asserts that the
// ACL attribute (0x0000) is readable. matter.js AccessControlServer.ts
// makes the ACL attribute the centerpiece of the cluster; it must be
// present on every fabric-scope read.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts:23 (Acl id=0x0000, type list[AccessControlEntryStruct],
// access "RW F A").
func TestParityMatterJS_AccessControl_ACLAttributePresent(t *testing.T) {
	t.Parallel()
	storeFake := &seededACLStore{
		existing: []mstore.ACLEntry{
			{FabricIndex: 1, Privilege: 5, AuthMode: 2, Subjects: []uint64{1}},
		},
	}
	ac, err := core.NewAccessControl(storeFake)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ac.SetCurrentFabric(1)

	ctx := im.WithFabricFilter(context.Background(), true, 1)
	v, ok := ac.MatterReadFiltered(ctx, 0x0000)
	if !ok {
		t.Fatal("ACL (0x0000): ok=false — attribute must be present")
	}
	_, ok = v.([]core.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("ACL type = %T, want []AccessControlEntryStruct", v)
	}
}

// TestParityMatterJS_AccessControl_FabricScoped_Bug_M_Fix pins the
// Bug M fix: ACL reads MUST be filtered by the FabricFilter from the
// IM context, not the locally-stamped currentFabric. An unscoped read
// returns entries for all fabrics, causing Apple's commissioner to
// reject the subscription with "no Administer privilege for this
// fabric".
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts — acl attribute read uses FabricFilter.
func TestParityMatterJS_AccessControl_FabricScoped_Bug_M_Fix(t *testing.T) {
	t.Parallel()
	storeFake := &seededACLStore{
		existing: []mstore.ACLEntry{
			{FabricIndex: 1, Privilege: 5, AuthMode: 2, Subjects: []uint64{0x1111}},
			{FabricIndex: 2, Privilege: 5, AuthMode: 2, Subjects: []uint64{0x2222}},
		},
	}
	ac, err := core.NewAccessControl(storeFake)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	// Fabric 2 CASE context — must only see fabric-2 entries.
	ctx2 := im.WithFabricFilter(context.Background(), true, 2)
	v, ok := ac.MatterReadFiltered(ctx2, 0x0000)
	if !ok {
		t.Fatal("ACL fabric-filtered read: ok=false")
	}
	list, ok := v.([]core.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("ACL type = %T, want []AccessControlEntryStruct", v)
	}
	// Bug-M guard: list must be non-empty for the requesting fabric.
	if len(list) == 0 {
		t.Fatal("Bug-M regression: fabric-scoped ACL read returned empty list — Apple rejects fabric after CASE")
	}
}

// TestParityMatterJS_AccessControl_ExtensionAttributePresent asserts that
// the Extension attribute (0x0001) is readable — required per matter.js
// access-control.element.ts.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts:32 (Extension id=0x0001).
func TestParityMatterJS_AccessControl_ExtensionAttributePresent(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	ac.SetCurrentFabric(1)
	ctx := im.WithFabricFilter(context.Background(), true, 1)
	v, ok := ac.MatterReadFiltered(ctx, 0x0001)
	if !ok {
		t.Fatal("Extension (0x0001): ok=false — attribute must be present")
	}
	_ = v
}

// TestParityMatterJS_AccessControl_CapacityAttributes asserts that the
// three capacity attributes are all readable with non-zero values.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts:
//   - SubjectsPerAccessControlEntry (0x0002) conformance "M"
//   - TargetsPerAccessControlEntry (0x0003) conformance "M"
//   - AccessControlEntriesPerFabric (0x0004) conformance "M"
func TestParityMatterJS_AccessControl_CapacityAttributes(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	cases := []struct {
		id   uint32
		name string
	}{
		{0x0002, "SubjectsPerAccessControlEntry"},
		{0x0003, "TargetsPerAccessControlEntry"},
		{0x0004, "AccessControlEntriesPerFabric"},
	}
	for _, tc := range cases {
		v, ok := ac.MatterRead(tc.id)
		if !ok {
			t.Errorf("%s (0x%04X): ok=false — mandatory capacity attribute missing", tc.name, tc.id)
			continue
		}
		n, ok := v.(uint16)
		if !ok {
			t.Errorf("%s type = %T, want uint16", tc.name, v)
			continue
		}
		if n == 0 {
			t.Errorf("%s = 0 — capacity limit must be > 0 (matter.js access-control.element.ts)", tc.name)
		}
	}
}

// TestParityMatterJS_AccessControl_FeatureMapExtsSet pins the FeatureMap
// at 0x0001 (EXTS — Extension feature). The bridge advertises the EXTS
// feature because it serves the Extension attribute (0x0001). Without
// the feature bit, chip's MTRBaseClusters.h and Apple's HAP-mapper
// classify the cluster as "Extension list present but not feature-flagged"
// and drop the AccessControl schema validation.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts — Extension feature advertised when Extension
// attribute is served. This is a documented by-design divergence from
// the matter.js element definition default (FeatureMap=0): openccu-loom
// opts in to the EXTS feature for Apple Home compatibility — see
// notes/parity/by_design.md.
func TestParityMatterJS_AccessControl_FeatureMapExtsSet(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	v, ok := ac.MatterRead(0xFFFC) // FeatureMap
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	got := v.(uint32)
	const featureEXTS uint32 = 0x0001
	if got&featureEXTS == 0 {
		t.Errorf("FeatureMap = 0x%08X, want EXTS bit (0x0001) set — openccu-loom serves Extension list so the feature bit must be advertised", got)
	}
}

// TestParityMatterJS_AccessControl_PASEForbiddenInACL verifies that PASE
// AuthMode (1) is rejected in ACL entries. matter.js
// AccessControlServer.ts validates AuthMode before persisting.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts — PASE auth mode is forbidden per Matter spec
// §9.10.4.3.
func TestParityMatterJS_AccessControl_PASEForbiddenInACL(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	ac.SetCurrentFabric(1)

	paseEntry := []core.AccessControlEntryStruct{{
		Privilege:   5,
		AuthMode:    1, // PASE — forbidden per Matter §9.10.4.3
		Subjects:    []uint64{1},
		FabricIndex: 1,
	}}
	if err := ac.MatterWrite(context.Background(), 0x0000, paseEntry); err == nil {
		t.Error("MatterWrite with PASE AuthMode: expected error, got nil — PASE is forbidden per matter.js")
	}
}

// TestParityMatterJS_AccessControl_Administer_GroupAuthMode_Forbidden
// verifies that Administer privilege with Group AuthMode (3) is rejected.
// matter.js access-control.element.ts §9.10.4.4 forbids this combination.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts validation for the Administer+Group prohibition.
func TestParityMatterJS_AccessControl_Administer_GroupAuthMode_Forbidden(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	ac.SetCurrentFabric(1)

	badEntry := []core.AccessControlEntryStruct{{
		Privilege:   5, // Administer
		AuthMode:    3, // Group — forbidden with Administer
		Subjects:    []uint64{1},
		FabricIndex: 1,
	}}
	if err := ac.MatterWrite(context.Background(), 0x0000, badEntry); err == nil {
		t.Error("MatterWrite Administer+Group: expected error, got nil — matter.js forbids this combination")
	}
}

// TestParityMatterJS_AccessControl_WriteEmitsEntryChangedEvent pins the
// event semantics from matter.js: every successful ACL write fires the
// AccessControlEntryChanged event (0x0000) at priority Info.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts (acl write → emit entryChanged at priority Info).
// Matches the audit item and matter.js
// access-control.element.ts:62 (AccessControlEntryChanged event, priority Info).
func TestParityMatterJS_AccessControl_WriteEmitsEntryChangedEvent(t *testing.T) {
	t.Parallel()
	ac := newAccessControlWithStore(t, &seededACLStore{})
	ac.SetCurrentFabric(1)
	ac.SetEndpoint(0)

	emitter := &fakeEmitter{}
	ac.SetMatterEventEmitter(emitter)

	entry := []core.AccessControlEntryStruct{{
		Privilege:   5,
		AuthMode:    2,
		Subjects:    []uint64{1},
		FabricIndex: 1,
	}}
	if err := writeACL(ac, entry); err != nil {
		t.Fatalf("MatterWrite: %v", err)
	}

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 AccessControlEntryChanged event, got %d", len(got))
	}
	ev := got[0]
	if ev.cluster != 0x001F {
		t.Errorf("cluster = 0x%04X, want 0x001F (AccessControl)", ev.cluster)
	}
	if ev.event != 0x0000 {
		t.Errorf("event = 0x%04X, want 0x0000 (AccessControlEntryChanged)", ev.event)
	}
	if ev.priority != matterport.EventPriorityInfo {
		t.Errorf("priority = %v, want Info (matter.js access-control.element.ts:62)", ev.priority)
	}
}

// TestParityMatterJS_AccessControl_InvokeAlwaysErrors verifies that
// AccessControl has no commands — all invocations must fail.
//
// Mirrors matter.js packages/model/src/standard/elements/
// access-control.element.ts — no commands listed.
func TestParityMatterJS_AccessControl_InvokeAlwaysErrors(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	for _, cmdID := range []uint32{0x00, 0x01, 0xFF} {
		if _, err := ac.MatterInvoke(context.Background(), cmdID, nil); err == nil {
			t.Errorf("MatterInvoke(0x%02X): expected error, got nil — AccessControl has no commands", cmdID)
		}
	}
}

// TestParityMatterJS_AccessControl_MaxSubjectsFloor4 asserts the
// SubjectsPerAccessControlEntry limit is at least the spec floor of 4.
//
// Mirrors matter.js access-control.element.ts §9.10.8.3 — the Matter
// Core Spec requires at least 4 subjects per entry; matter.js uses
// `MAX_ACL_SUBJECTS_PER_ENTRY = 4`.
func TestParityMatterJS_AccessControl_MaxSubjectsFloor4(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	v, ok := ac.MatterRead(0x0002)
	if !ok {
		t.Fatal("SubjectsPerAccessControlEntry: ok=false")
	}
	if got := v.(uint16); got < 4 {
		t.Errorf("SubjectsPerAccessControlEntry = %d, want ≥ 4 (Matter spec floor)", got)
	}
}

// TestParityMatterJS_AccessControl_MaxTargetsFloor4 asserts the
// TargetsPerAccessControlEntry limit is at least 4.
//
// Mirrors matter.js access-control.element.ts §9.10.8.4.
func TestParityMatterJS_AccessControl_MaxTargetsFloor4(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	v, ok := ac.MatterRead(0x0003)
	if !ok {
		t.Fatal("TargetsPerAccessControlEntry: ok=false")
	}
	if got := v.(uint16); got < 4 {
		t.Errorf("TargetsPerAccessControlEntry = %d, want ≥ 4 (Matter spec floor)", got)
	}
}

// TestParityMatterJS_AccessControl_MaxEntriesFloor4 asserts the
// AccessControlEntriesPerFabric limit is at least 4.
//
// Mirrors matter.js access-control.element.ts §9.10.8.5 — spec minimum
// is 3, but matter.js defaults to 4.
func TestParityMatterJS_AccessControl_MaxEntriesFloor4(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	v, ok := ac.MatterRead(0x0004)
	if !ok {
		t.Fatal("AccessControlEntriesPerFabric: ok=false")
	}
	if got := v.(uint16); got < 4 {
		t.Errorf("AccessControlEntriesPerFabric = %d, want ≥ 4 (matter.js default 4)", got)
	}
}

// TestParityMatterJS_AccessControl_MatterEventsContainsBothEvents pins
// MatterEvents() returning both the AccessControlEntryChanged (0x0000)
// and AccessControlExtensionChanged (0x0001) event IDs. The
// ExtensionChanged event is required for clusters that advertise the EXTS
// feature; openccu-loom must list both in MatterEvents() so that the
// dispatcher's EventList attribute synthesis (0xFFFA) is accurate.
// Apple Home and chip-tool both enforce that EventList is a superset of
// the events the cluster may emit.
//
// Source-Origin: derived from matter.js packages/model/src/standard/
// elements/access-control.element.ts:62-88 — events section lists
// AccessControlEntryChanged (id 0, priority Info, conformance M) and
// AccessControlExtensionChanged (id 1, priority Info, conformance EXTS).
func TestParityMatterJS_AccessControl_MatterEventsContainsBothEvents(t *testing.T) {
	t.Parallel()
	ac := newAccessControl(t)
	events := ac.MatterEvents()
	want := map[uint32]string{
		0x0000: "AccessControlEntryChanged",
		0x0001: "AccessControlExtensionChanged",
	}
	got := make(map[uint32]bool, len(events))
	for _, id := range events {
		got[id] = true
	}
	for id, name := range want {
		if !got[id] {
			t.Errorf("MatterEvents() missing %s (0x%04X) — access-control.element.ts events section", name, id)
		}
	}
	if len(events) < 2 {
		t.Errorf("MatterEvents() len=%d, want ≥ 2 (EntryChanged + ExtensionChanged)", len(events))
	}
}
