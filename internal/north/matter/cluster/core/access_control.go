// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// AccessControl implements the Matter Access Control Cluster
// (0x001F) per Matter Core Specification 1.5.1 §9.10. Mandatory on
// the Root endpoint; the cluster's `acl` attribute is the controller's
// authoritative source for what each subject may read / write / invoke
// on every cluster of every endpoint.
//
// openccu-loom exposes the ACL list READ-ONLY in v1.1 — every entry is
// inserted by the bridge itself (the AddNOC handler installs the
// default Administer entry per §11.18.6.8.1 immediately after the
// fabric is persisted). Apple Home reads ACL right after CASE; an
// empty / missing cluster surfaces as ACCESS_DENIED on every
// follow-up read and Apple's pairing UI tears the new fabric down via
// RemoveFabric. Implementing ACL READ is the minimum surface that
// keeps Apple happy through the post-CASE handshake.
type AccessControl struct {
	store ACLStoreFacade

	mu            sync.RWMutex
	currentFabric uint8

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped after every successful ACL replace so subscribers
	// can detect changes. Satisfies [interfaces.MatterClusterDataVersion].
	dataVersion cluster.DataVersionTracker

	// extensions holds the per-fabric AccessControlExtensionStruct list
	// (attribute 0x0001, conformance EXTS). The list is keyed by fabric
	// index and stored in-memory; the entries are vendor-opaque octstrings
	// (max 128 bytes each) that controllers use to attach metadata to an
	// ACL fabric. Mirroring matter.js AccessControlServer.ts in-memory
	// extension state.
	extensions map[uint8][]AccessControlExtensionEntry

	// Event surface — wired by the bridge during topology assembly via
	// [SetMatterEventEmitter] + [SetEndpoint] so [MatterWrite] can fire
	// the spec-mandated AccessControlEntryChanged event (Matter §9.10.7.1,
	// event id 0x0, priority Info) on every ACL mutation. Mirrors matter.js
	// packages/node/src/behaviors/access-control/AccessControlServer.ts where
	// acl attribute writes trigger the entryChanged event.
	endpoint uint16
	emitter  interfaces.MatterEventEmitter
}

// AccessControlExtensionEntry mirrors Matter §9.10.4.6
// AccessControlExtensionStruct. The Data field is a vendor-opaque
// octet-string (max 128 bytes); FabricIndex is stamped by the cluster
// server from the IM session context on every write.
type AccessControlExtensionEntry struct {
	Data        []byte
	FabricIndex uint8
}

// ACLStoreFacade is the subset of [store.Store] this cluster reads
// and writes. ReplaceACL is the post-CASE write path Apple Home uses
// to install HomePod / AppleTV edge controllers as additional
// Administer subjects after CommissioningComplete (see Matter §9.10
// + the iCloud-Heim post-pairing step Apple's homed runs).
type ACLStoreFacade interface {
	ListACL(ctx context.Context, fabricIndex uint8) ([]store.ACLEntry, error)
	ReplaceACL(ctx context.Context, fabricIndex uint8, entries []store.ACLEntry) error
}

// Cluster ID + revision per Matter §9.10.
const (
	accessControlClusterID       uint32 = 0x001F
	accessControlClusterRevision uint16 = 2 // matter.js HEAD (@matter/model 0.16.11)

	accessControlAttrACL                           uint32 = 0x0000
	accessControlAttrExtension                     uint32 = 0x0001
	accessControlAttrSubjectsPerAccessControl      uint32 = 0x0002
	accessControlAttrTargetsPerAccessControl       uint32 = 0x0003
	accessControlAttrAccessControlEntriesPerFabric uint32 = 0x0004

	// Capacity limits we report. The Matter Core spec floor is 4 per
	// dimension; matter.js uses the same constants. Higher numbers
	// would advertise capacity we do not actually enforce.
	accessControlSubjectsPerEntry      uint16 = 4
	accessControlTargetsPerEntry       uint16 = 4
	accessControlEntriesPerFabricLimit uint16 = 4

	// AuthMode + Privilege values mirror Matter §9.10.4.4 enums and
	// matter.js packages/types/src/clusters/access-control.ts.
	accessControlAuthModePASE        uint8 = 1
	accessControlAuthModeCASE        uint8 = 2 //nolint:unused // listed for symmetry with matter.js enum
	accessControlAuthModeGroup       uint8 = 3
	accessControlPrivilegeAdminister uint8 = 5

	// accessControlEventEntryChanged is the Matter §9.10.7.1 event
	// (id 0x0, priority Info) emitted after every ACL mutation. Mirrors
	// matter.js packages/model/src/standard/elements/access-control.element.ts:62.
	accessControlEventEntryChanged uint32 = 0x0000
	// accessControlEventExtensionChanged is the Matter §9.10.7.2 event
	// (id 0x1, conformance EXTS). chip emits this event per spec §9.10.7
	// and matter.js element.ts:77-88 lists it as conformance "EXTS".
	// Declared here so MatterEvents() returns a complete set for EventList
	// synthesis when EventList suppression is lifted in a future release.
	accessControlEventExtensionChanged uint32 = 0x0001
)

// ChangeType constants for [AccessControlEntryChangedEvent], mirroring
// Matter §9.10.4.2 ChangeTypeEnum and matter.js
// packages/model/src/standard/elements/access-control.element.ts:35-39.
const (
	// AccessControlChangeTypeChanged signals an existing entry was modified.
	AccessControlChangeTypeChanged uint8 = 0
	// AccessControlChangeTypeAdded signals a new entry was inserted.
	AccessControlChangeTypeAdded uint8 = 1
	// AccessControlChangeTypeRemoved signals an entry was deleted.
	AccessControlChangeTypeRemoved uint8 = 2
)

// AccessControlEntryChangedEvent is the payload for event 0x0000 on
// cluster 0x001F. Mirrors Matter §9.10.7.1 and matter.js
// packages/model/src/standard/elements/access-control.element.ts:62-74.
// Priority: Info (MatterEventPriorityInfo).
//
// AdminNodeID and AdminPasscodeID are both nullable (quality X); both
// are nil in v1.1 because the IM layer does not yet track which
// commissioner sent the write. LatestValue is nullable (quality X) and
// set to nil for bulk-replace operations where per-entry diffing is
// ambiguous.
type AccessControlEntryChangedEvent struct {
	AdminNodeID     *uint64                   // nullable; nil = "not tracked"
	AdminPasscodeID *uint16                   // nullable; nil = "not tracked"
	ChangeType      uint8                     // AccessControlChangeType{Changed,Added,Removed}
	LatestValue     *AccessControlEntryStruct // nullable; nil for bulk-replace
	FabricIndex     uint8
}

// AccessControlEntryStruct mirrors Matter §9.10.4.4 AccessControlEntry.
// Field order matches the wire-encoded TLV tags so the default
// attribute writer can emit it via reflection.
type AccessControlEntryStruct struct {
	Privilege   uint8             // 1=View, 2=ProxyView, 3=Operate, 4=Manage, 5=Administer
	AuthMode    uint8             // 1=PASE, 2=CASE, 3=Group
	Subjects    []uint64          // nullable; nil ⇒ matches every subject
	Targets     []ACLTargetStruct // nullable; nil ⇒ matches every cluster/endpoint/device-type
	FabricIndex uint8
}

// ACLTargetStruct mirrors §9.10.4.5.
type ACLTargetStruct struct {
	Cluster    *uint32 // null ⇒ any cluster
	Endpoint   *uint16 // null ⇒ any endpoint
	DeviceType *uint32 // null ⇒ any device type
}

// NewAccessControl constructs the cluster.
func NewAccessControl(s ACLStoreFacade) (*AccessControl, error) {
	if s == nil {
		return nil, errors.New("matter: AccessControl store is required")
	}
	return &AccessControl{store: s}, nil
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer                  = (*AccessControl)(nil)
	_ interfaces.FabricScopedReader                   = (*AccessControl)(nil)
	_ interfaces.MatterEventReceiver                  = (*AccessControl)(nil)
	_ interfaces.MatterClusterDataVersion             = (*AccessControl)(nil)
	_ interfaces.MatterClusterAttributeReadPrivilege  = (*AccessControl)(nil)
	_ interfaces.MatterClusterAttributeWritePrivilege = (*AccessControl)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (a *AccessControl) MatterClusterID() uint32 { return accessControlClusterID }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the current per-cluster monotonic counter bumped on every
// successful ACL replace. Mirrors matter.js AccessControlServer.ts
// DataVersion tracking on ACL attribute mutations.
func (a *AccessControl) MatterDataVersion() uint32 { return a.dataVersion.Current() }

// MinReadPrivilege implements [interfaces.MatterClusterAttributeReadPrivilege].
// ACL (0x0000) and Extension (0x0001) require Administer (5) per Matter
// §9.10.5.3. Mirrors chip
// src/app/clusters/access-control-server/access-control-server.cpp
// AttributeReadAclRequired guard, and matter.js
// packages/model/src/standard/elements/access-control.element.ts
// access: "administer" on acl + extension attributes.
func (*AccessControl) MinReadPrivilege(attrID uint32) uint8 {
	switch attrID {
	case accessControlAttrACL, accessControlAttrExtension:
		return accessControlPrivilegeAdminister // 5
	default:
		return 1 // View — standard default
	}
}

// MinWritePrivilege implements [interfaces.MatterClusterAttributeWritePrivilege].
// ACL (0x0000) and Extension (0x0001) require Administer (5) per Matter
// §9.10.5.3 (access "RW … A"). Mirrors matter.js
// packages/model/src/standard/elements/access-control.element.ts:28,32.
func (*AccessControl) MinWritePrivilege(attrID uint32) uint8 {
	switch attrID {
	case accessControlAttrACL, accessControlAttrExtension:
		return accessControlPrivilegeAdminister // 5
	default:
		return 3 // Operate — standard default
	}
}

// SetCurrentFabric is called by the IM dispatcher before fabric-scoped
// reads so the cluster filters the ACL list to the requesting fabric.
func (a *AccessControl) SetCurrentFabric(idx uint8) {
	a.mu.Lock()
	a.currentFabric = idx
	a.mu.Unlock()
}

// MatterRead implements [interfaces.MatterClusterServer].
func (a *AccessControl) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case accessControlAttrACL:
		ctx := context.Background()
		a.mu.RLock()
		fabric := a.currentFabric
		a.mu.RUnlock()
		entries, err := a.store.ListACL(ctx, fabric)
		if err != nil {
			return nil, false
		}
		out := make([]AccessControlEntryStruct, 0, len(entries))
		for _, e := range entries {
			ace := AccessControlEntryStruct{
				Privilege:   uint8(e.Privilege),
				AuthMode:    uint8(e.AuthMode),
				Subjects:    append([]uint64(nil), e.Subjects...),
				FabricIndex: e.FabricIndex,
			}
			if len(e.Targets) > 0 {
				ace.Targets = make([]ACLTargetStruct, 0, len(e.Targets))
				for _, t := range e.Targets {
					ace.Targets = append(ace.Targets, ACLTargetStruct{
						Cluster:    t.Cluster,
						Endpoint:   t.Endpoint,
						DeviceType: t.DeviceType,
					})
				}
			}
			out = append(out, ace)
		}
		return out, true
	case accessControlAttrExtension:
		a.mu.RLock()
		exts := a.extensions[a.currentFabric]
		a.mu.RUnlock()
		if len(exts) == 0 {
			return []AccessControlExtensionEntry{}, true
		}
		out := make([]AccessControlExtensionEntry, len(exts))
		copy(out, exts)
		return out, true
	case accessControlAttrSubjectsPerAccessControl:
		return accessControlSubjectsPerEntry, true
	case accessControlAttrTargetsPerAccessControl:
		return accessControlTargetsPerEntry, true
	case accessControlAttrAccessControlEntriesPerFabric:
		return accessControlEntriesPerFabricLimit, true
	case cluster.AttrGlobalFeatureMap:
		// FeatureMap = EXTS (Extension, bit 0). chip's MTRBaseClusters.h
		// declares MTRAccessControlFeatureExtension = 0x1 (iOS 18.4)
		// and matter.js's `AccessControlServer.with("Extension")` sets
		// the feature flag whenever the Extension list attribute is
		// served. We serve Extension (`accessControlAttrExtension`
		// returns an empty list above) → advertise the feature.
		// FeatureMap = 0 made Apple's HAP-mapper classify the cluster
		// as schematically inconsistent (Extension list present but
		// not feature-flagged) and drop the entire AccessControl
		// schema validation, so the cluster advertises EXTS.
		return uint32(0x1), true
	case cluster.AttrGlobalClusterRevision:
		return accessControlClusterRevision, true
	}
	return nil, false
}

// MatterReadFiltered implements [interfaces.FabricScopedReader].
// AccessControl.ACL is a fabric-scoped attribute per Matter §9.10.5.3
// — every entry carries a FabricIndex and the wire MUST return only
// entries for the requesting fabric when FabricFiltered=true. Apple
// Home reads ACL on the CASE session immediately after Subscribe-
// Initial; without this filter the read falls back to MatterRead's
// `a.currentFabric` (only ever set by ACL writes — zero on a fresh
// CASE session), ListACL returns `[]`, and Apple interprets the empty
// list as "this subject has no Administer privilege" and tears the
// fabric down via RemoveFabric.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts: every read of `acl` and `extension` consults
// the FabricFilter from the IM context. The non-fabric-scoped attributes
// (SubjectsPerAccessControlEntry, TargetsPerAccessControlEntry,
// AccessControlEntriesPerFabric, FeatureMap, ClusterRevision) fall
// through to MatterRead.
func (a *AccessControl) MatterReadFiltered(ctx context.Context, attrID uint32) (any, bool) {
	if attrID == accessControlAttrExtension {
		_, fabricIndex := im.FabricFilterFromContext(ctx)
		if fabricIndex == 0 {
			return a.MatterRead(attrID) //nolint:contextcheck // MatterRead is the unfiltered cluster-interface read; it takes no ctx by the Matter cluster-server contract
		}
		a.mu.RLock()
		exts := a.extensions[fabricIndex]
		a.mu.RUnlock()
		if len(exts) == 0 {
			return []AccessControlExtensionEntry{}, true
		}
		out := make([]AccessControlExtensionEntry, len(exts))
		copy(out, exts)
		return out, true
	}
	if attrID != accessControlAttrACL {
		return a.MatterRead(attrID) //nolint:contextcheck // MatterRead is the unfiltered cluster-interface read; it takes no ctx by the Matter cluster-server contract
	}
	_, fabricIndex := im.FabricFilterFromContext(ctx)
	if fabricIndex == 0 {
		// PASE (pre-AddNOC) or no FabricFilter set: fall through to
		// MatterRead which uses a.currentFabric (the last write target).
		return a.MatterRead(attrID) //nolint:contextcheck // MatterRead is the unfiltered cluster-interface read; it takes no ctx by the Matter cluster-server contract
	}
	entries, err := a.store.ListACL(ctx, fabricIndex)
	if err != nil {
		return nil, false
	}
	out := make([]AccessControlEntryStruct, 0, len(entries))
	for _, e := range entries {
		ace := AccessControlEntryStruct{
			Privilege:   uint8(e.Privilege),
			AuthMode:    uint8(e.AuthMode),
			Subjects:    append([]uint64(nil), e.Subjects...),
			FabricIndex: e.FabricIndex,
		}
		if len(e.Targets) > 0 {
			ace.Targets = make([]ACLTargetStruct, 0, len(e.Targets))
			for _, t := range e.Targets {
				ace.Targets = append(ace.Targets, ACLTargetStruct{
					Cluster:    t.Cluster,
					Endpoint:   t.Endpoint,
					DeviceType: t.DeviceType,
				})
			}
		}
		out = append(out, ace)
	}
	return out, true
}

// MatterWrite handles writes to the cluster's writable attributes.
// The only writable attribute today is ACL (0x0000) — Apple Home
// rewrites the entire list ~10 ms after CommissioningComplete to
// install HomePod / AppleTV edge controllers as additional Administer
// subjects on the freshly-paired bridge. Without a working write path
// Apple times out after 10 s and tears the fabric down via
// RemoveFabric. Extension (0x0001) is not implemented — matter.js does
// the same and Apple does not write it.
func (a *AccessControl) MatterWrite(ctx context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error { //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	if attrID == accessControlAttrACL {
		entries, ok := value.([]AccessControlEntryStruct)
		if !ok {
			return fmt.Errorf("matter: AccessControl.ACL write: value type %T not []AccessControlEntryStruct", value)
		}
		// Fabric resolution priority (matches MatterReadFiltered):
		//   1. ctx-fabric stamped by bridge/receive.go from the inbound
		//      CASE session — the spec-correct source for every
		//      fabric-scoped write per Matter §9.10.5.3.
		//   2. a.currentFabric set via SetCurrentFabric — legacy hook
		//      retained for tests that pre-date the ctx plumbing.
		//   3. entries[0].FabricIndex when the caller stamps the entry
		//      themselves (rare; mostly clients-as-server in tests).
		//   4. Hard-coded fabric=1 last resort.
		// Without (1) Apple Home's post-CommissioningComplete ACL
		// rewrite lands in the wrong fabric, Apple reads its own fabric
		// on the next Subscribe-Initial, sees the unchanged
		// case_admin_subject ACL, and tears the pair down with the iOS
		// "accessory could not be added" dialog.
		_, ctxFabric := im.FabricFilterFromContext(ctx)
		a.mu.RLock()
		fabric := a.currentFabric
		a.mu.RUnlock()
		if ctxFabric != 0 {
			fabric = ctxFabric
		}
		if fabric == 0 && len(entries) > 0 && entries[0].FabricIndex != 0 {
			fabric = entries[0].FabricIndex
		}
		if fabric == 0 {
			fabric = 1 // last-resort: only ever 1 fabric in v1.1.
		}
		// Validation — Mirrors chip src/access/AccessControl.cpp:680-764
		// Entry::IsValid() and matter.js packages/node/src/behaviors/
		// access-control/AccessControlServer.ts:165-265. Rules:
		//   1. AccessControlEntriesPerFabric (≤ 4 entries on this fabric).
		//   2. SubjectsPerAccessControlEntry (≤ 4 subjects per entry).
		//   3. TargetsPerAccessControlEntry (≤ 4 targets per entry).
		//   4. AuthMode != PASE on every entry — PASE auth is only valid
		//      during commissioning, never in the persisted ACL.
		//   5. Group-AuthMode entries must NOT carry Administer privilege.
		//   6. CASE-AuthMode: each non-zero subject must be a valid CASE
		//      NodeID (operational range) or CASE Auth Tag.
		//      Mirrors chip AccessControl.cpp:735 IsValidCaseNodeId check.
		//   7. Group-AuthMode: each subject must be a valid Group NodeID
		//      (0xFFFF_FFFF_FFFF_FF00 .. 0xFFFF_FFFF_FFFF_FFFF range).
		//      Mirrors chip AccessControl.cpp:735 IsValidGroupNodeId check.
		//   8. Target ClusterId must be ≤ 0xFFFF_FFFF (Matter §7.18.2.4).
		//      Mirrors chip AccessControl.cpp:746 IsValidClusterId.
		//   9. Target EndpointId must be ≤ 0xFFFE (0xFFFF is wildcard/invalid).
		//      Mirrors chip AccessControl.cpp:747 IsValidEndpointId.
		//  10. Target DeviceTypeId must be ≤ 0xFFFF_FFFF.
		//      Mirrors chip AccessControl.cpp:748 IsValidDeviceTypeId.
		//  11. DeviceType and Endpoint on the same Target are mutually exclusive.
		//  12. At least one of (Cluster, Endpoint, DeviceType) must be set on each Target.
		// Limit failures → ResourceExhausted; semantic failures →
		// ConstraintError (both via the dispatcher's `writeErrorStatus`
		// substring matching).
		fabricACLs := 0
		for _, e := range entries {
			if e.FabricIndex == fabric || e.FabricIndex == 0 {
				fabricACLs++
			}
		}
		if fabricACLs > int(accessControlEntriesPerFabricLimit) {
			return fmt.Errorf("matter: AccessControl.ACL write: resource exhausted: AccessControlEntriesPerFabric=%d > limit=%d", fabricACLs, accessControlEntriesPerFabricLimit)
		}
		for i, e := range entries {
			if len(e.Subjects) > int(accessControlSubjectsPerEntry) {
				return fmt.Errorf("matter: AccessControl.ACL[%d] write: resource exhausted: SubjectsPerAccessControlEntry=%d > limit=%d", i, len(e.Subjects), accessControlSubjectsPerEntry)
			}
			if len(e.Targets) > int(accessControlTargetsPerEntry) {
				return fmt.Errorf("matter: AccessControl.ACL[%d] write: resource exhausted: TargetsPerAccessControlEntry=%d > limit=%d", i, len(e.Targets), accessControlTargetsPerEntry)
			}
			if e.AuthMode == accessControlAuthModePASE {
				return fmt.Errorf("matter: AccessControl.ACL[%d] write: constraint error: AuthMode=PASE is forbidden in ACL", i)
			}
			if e.AuthMode == accessControlAuthModeGroup && e.Privilege == accessControlPrivilegeAdminister {
				return fmt.Errorf("matter: AccessControl.ACL[%d] write: constraint error: Group authmode + Administer privilege rejected", i)
			}
			// Subject range validation per AuthMode.
			// Mirrors chip src/access/AccessControl.cpp:735 IsValidCaseNodeId /
			// IsValidGroupNodeId per-subject checks inside Entry::IsValid().
			for j, subj := range e.Subjects {
				if subj == 0 {
					return fmt.Errorf("matter: AccessControl.ACL[%d].Subjects[%d] write: constraint error: subject 0 is the undefined node ID", i, j)
				}
				if e.AuthMode == accessControlAuthModeCASE {
					// CASE subjects: operational node ID or CASE Auth Tag (CAT).
					if !aclIsValidCASESubject(subj) {
						return fmt.Errorf("matter: AccessControl.ACL[%d].Subjects[%d] write: constraint error: CASE subject 0x%016X is not a valid operational node ID or CASE auth tag", i, j, subj)
					}
				}
				if e.AuthMode == accessControlAuthModeGroup {
					// Group subjects: 0xFFFF_FFFF_FFFF_FF00 .. 0xFFFF_FFFF_FFFF_FFFF.
					if !aclIsValidGroupSubject(subj) {
						return fmt.Errorf("matter: AccessControl.ACL[%d].Subjects[%d] write: constraint error: Group subject 0x%016X out of group node ID range", i, j, subj)
					}
				}
			}
			// Target validation: matter.js requires DeviceType and
			// Endpoint mutually exclusive, and at least one of
			// (Cluster, Endpoint, DeviceType) must be present.
			// Additionally validate cluster/endpoint/device-type ID ranges.
			for j, t := range e.Targets {
				if t.DeviceType != nil && t.Endpoint != nil {
					return fmt.Errorf("matter: AccessControl.ACL[%d].Targets[%d] write: constraint error: DeviceType and Endpoint mutually exclusive", i, j)
				}
				if t.Cluster == nil && t.Endpoint == nil && t.DeviceType == nil {
					return fmt.Errorf("matter: AccessControl.ACL[%d].Targets[%d] write: constraint error: at least one of Cluster/Endpoint/DeviceType must be set", i, j)
				}
				// ClusterId and EndpointId range checks.
				// Mirrors chip AccessControl.cpp:746-748 IsValidClusterId /
				// IsValidEndpointId / IsValidDeviceTypeId guards in IsValid().
				if t.Endpoint != nil && *t.Endpoint == 0xFFFF {
					return fmt.Errorf("matter: AccessControl.ACL[%d].Targets[%d] write: constraint error: EndpointId 0xFFFF is reserved", i, j)
				}
			}
		}

		// Per spec §9.10.5 every ACL entry is fabric-scoped — we MUST
		// stamp the caller's fabric on every entry before persisting.
		out := make([]store.ACLEntry, 0, len(entries))
		for i, e := range entries {
			rec := store.ACLEntry{
				FabricIndex: fabric,
				Privilege:   store.Privilege(e.Privilege),
				AuthMode:    store.AuthMode(e.AuthMode),
				Subjects:    append([]uint64(nil), e.Subjects...),
				Position:    uint16(i), //nolint:gosec // i bounded by accessControlEntriesPerFabricLimit; see #20
			}
			if len(e.Targets) > 0 {
				rec.Targets = make([]store.ACLTarget, 0, len(e.Targets))
				for _, t := range e.Targets {
					rec.Targets = append(rec.Targets, store.ACLTarget{
						Cluster:    t.Cluster,
						Endpoint:   t.Endpoint,
						DeviceType: t.DeviceType,
					})
				}
			}
			out = append(out, rec)
		}
		// Snapshot old ACL before the replace so we can classify the
		// change type for the spec-mandated AccessControlEntryChanged event
		// (§9.10.7.1). We read before write; a store error here is
		// non-fatal for the write itself — we fall back to ChangeType=Changed
		// if the snapshot fails.
		oldEntries, _ := a.store.ListACL(ctx, fabric)

		if err := a.store.ReplaceACL(ctx, fabric, out); err != nil {
			return fmt.Errorf("matter: AccessControl.ACL write: %w", err)
		}
		// Bump DataVersion after a successful mutation so DataVersionFilter
		// evaluation correctly detects the cluster changed. Must happen
		// AFTER the store write succeeds per DataVersionTracker contract.
		a.dataVersion.Bump()

		// Emit AccessControlEntryChanged event per Matter §9.10.7.1.
		// Use bulk-classify heuristic (mirrors matter.js
		// packages/node/src/behaviors/access-control/AccessControlServer.ts
		// entryChanged emit on every acl write): one event per write,
		// ChangeType derived from list-length delta, LatestValue=nil
		// (spec quality X — permitted to omit).
		a.mu.RLock()
		emitter := a.emitter
		endpoint := a.endpoint
		a.mu.RUnlock()
		if emitter != nil {
			changeType := AccessControlChangeTypeChanged
			switch {
			case len(out) > len(oldEntries):
				changeType = AccessControlChangeTypeAdded
			case len(out) < len(oldEntries):
				changeType = AccessControlChangeTypeRemoved
			}
			emitter.MatterEmitEvent(
				endpoint,
				accessControlClusterID,
				accessControlEventEntryChanged,
				AccessControlEntryChangedEvent{
					AdminNodeID:     nil,
					AdminPasscodeID: nil,
					ChangeType:      changeType,
					LatestValue:     nil,
					FabricIndex:     fabric,
				},
				interfaces.MatterEventPriorityInfo,
			)
		}
		return nil
	}
	if attrID == accessControlAttrExtension {
		// Extension (0x0001) write path: replace the per-fabric extension
		// list. The list carries vendor-opaque octstrings (max 128 bytes
		// each) that some controllers attach to ACL fabrics.
		// Mirrors matter.js AccessControlServer.ts in-memory extension state.
		entries, ok := value.([]AccessControlExtensionEntry)
		if !ok {
			return fmt.Errorf("matter: AccessControl.Extension write: value type %T not []AccessControlExtensionEntry", value)
		}
		for i, e := range entries {
			if len(e.Data) > 128 {
				return fmt.Errorf("matter: AccessControl.Extension[%d] write: Data length %d exceeds max 128", i, len(e.Data))
			}
		}
		_, ctxFabric := im.FabricFilterFromContext(ctx)
		a.mu.RLock()
		fabric := a.currentFabric
		a.mu.RUnlock()
		if ctxFabric != 0 {
			fabric = ctxFabric
		}
		if fabric == 0 {
			fabric = 1
		}
		stamped := make([]AccessControlExtensionEntry, len(entries))
		for i, e := range entries {
			stamped[i] = AccessControlExtensionEntry{
				Data:        append([]byte(nil), e.Data...),
				FabricIndex: fabric,
			}
		}
		a.mu.Lock()
		if a.extensions == nil {
			a.extensions = make(map[uint8][]AccessControlExtensionEntry)
		}
		a.extensions[fabric] = stamped
		a.mu.Unlock()
		a.dataVersion.Bump()
		return nil
	}
	return fmt.Errorf("matter: AccessControl attribute 0x%04X not writable", attrID)
}

// MatterInvoke — no commands; AccessControl in 1.5.1 is attribute-only.
func (a *AccessControl) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("matter: AccessControl has no command 0x%X", cmdID)
}

// MatterReportable returns the attributes that emit reports on
// change. The ACL list and Extension list are reportable per
// §9.10.4 so subscribers see entries appearing immediately after
// AddNOC; v1.1 ships the static set.
func (a *AccessControl) MatterReportable() []uint32 {
	return []uint32{accessControlAttrACL, accessControlAttrExtension}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard reads expand correctly. Returns the full attribute set
// EXCLUDING the universal globals (FeatureMap, ClusterRevision) —
// the dispatcher merges those automatically.
func (a *AccessControl) MatterAttributes() []uint32 {
	return []uint32{
		accessControlAttrACL,
		accessControlAttrExtension,
		accessControlAttrSubjectsPerAccessControl,
		accessControlAttrTargetsPerAccessControl,
		accessControlAttrAccessControlEntriesPerFabric,
	}
}

// MatterEvents implements [interfaces.MatterClusterEventLister] so the
// dispatcher synthesises the global EventList (0xFFFA) attribute
// correctly for this cluster. Includes AccessControlExtensionChanged
// (0x0001) per matter.js
// packages/model/src/standard/elements/access-control.element.ts:77-88
// and chip's AccessControl cluster server (spec §9.10.7). The event is
// listed here so EventList synthesis is complete if EventList suppression
// is lifted; no emission path is wired because Extensions are not
// implemented in v1.1.
func (a *AccessControl) MatterEvents() []uint32 {
	return []uint32{accessControlEventEntryChanged, accessControlEventExtensionChanged}
}

// SetMatterEventEmitter implements [interfaces.MatterEventReceiver].
// Called by the bridge during topology assembly so [MatterWrite] can
// fire the §9.10.7.1 AccessControlEntryChanged event without the
// cluster holding a direct reference to the bridge. Idempotent.
func (a *AccessControl) SetMatterEventEmitter(emitter interfaces.MatterEventEmitter) {
	a.mu.Lock()
	a.emitter = emitter
	a.mu.Unlock()
}

// SetEndpoint stamps the endpoint id this AccessControl server is
// mounted on. Matter events carry the (endpoint, cluster, event)
// triple so the commissioner can fan them out to the right
// subscription path. The root endpoint is always 0 in standard
// topologies, but the bridge injects the real value here so the
// cluster does not hard-code it.
func (a *AccessControl) SetEndpoint(endpoint uint16) {
	a.mu.Lock()
	a.endpoint = endpoint
	a.mu.Unlock()
}

// aclIsValidCASESubject reports whether id is valid as a CASE-AuthMode ACL subject.
// Valid: operational node ID (0x0001..0xFFFF_FFFF_FFFF_FFEF) or CASE Auth Tag
// (upper 32 bits == 0xFFFF_FFFD). Mirrors chip
// src/access/AccessControl.cpp:735 IsValidCaseNodeId / IsCASEAuthTag guards.
func aclIsValidCASESubject(id uint64) bool {
	// Operational node ID range.
	if id >= 0x0000_0000_0000_0001 && id <= 0xFFFF_FFFF_FFFF_FFEF {
		return true
	}
	// CASE Auth Tag: upper 32 bits == 0xFFFF_FFFD.
	return (id >> 32) == 0xFFFF_FFFD
}

// aclIsValidGroupSubject reports whether id is valid as a Group-AuthMode ACL
// subject. Valid: Group Node ID range 0xFFFF_FFFF_FFFF_FF00 ..
// 0xFFFF_FFFF_FFFF_FFFF. Mirrors chip
// src/access/AccessControl.cpp:735 IsValidGroupNodeId guard.
func aclIsValidGroupSubject(id uint64) bool {
	return id >= 0xFFFF_FFFF_FFFF_FF00
}
