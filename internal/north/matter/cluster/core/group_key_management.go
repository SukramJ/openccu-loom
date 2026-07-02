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

// GroupKeyManagement implements the Matter GroupKeyManagement cluster
// (0x003F) per Matter Core Specification 1.5.1 §11.2.10. Mandatory on
// the Root endpoint; fabric-scoped attributes (GroupKeyMap,
// GroupTable) are filtered to the requesting fabric by the IM layer
// before the cluster sees them.
//
// The cluster is a thin facade over [store.Store] — every read goes
// through the store, every write commits to it.
type GroupKeyManagement struct {
	store GroupStoreFacade

	mu                    sync.RWMutex
	currentFabric         uint8
	maxGroupsPerFabric    uint16
	maxGroupKeysPerFabric uint16

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped at construction (non-zero sentinel) and after every
	// successful GroupKeyMap / GroupKeySet mutation so DataVersionFilter
	// evaluation works correctly. Satisfies
	// [interfaces.MatterClusterDataVersion]. Mirrors matter.js behavior
	// layer auto-tracking and chip's ember dirty-marking in
	// src/app/clusters/group-key-mgmt-server/.
	dataVersion cluster.DataVersionTracker
}

// GroupStoreFacade is the subset of [store.Store] this cluster uses.
type GroupStoreFacade interface {
	UpsertGroupKeySet(ctx context.Context, rec store.GroupKeySet) error
	GetGroupKeySet(ctx context.Context, fabricIndex uint8, groupKeySetID uint16) (store.GroupKeySet, error)
	ListGroupKeySets(ctx context.Context, fabricIndex uint8) ([]store.GroupKeySet, error)
	RemoveGroupKeySet(ctx context.Context, fabricIndex uint8, groupKeySetID uint16) error
	SetGroupKeyMapping(ctx context.Context, m store.GroupKeyMapping) error
	RemoveGroupKeyMapping(ctx context.Context, fabricIndex uint8, groupID uint16) error
	ListGroupKeyMappings(ctx context.Context, fabricIndex uint8) ([]store.GroupKeyMapping, error)
}

// Cluster ID + revision per Matter §11.2.10.
const (
	groupKeyMgmtClusterID       uint32 = 0x003F
	groupKeyMgmtClusterRevision uint16 = 2 // 1.5.1 baseline

	groupKeyMgmtAttrGroupKeyMap           uint32 = 0x0000
	groupKeyMgmtAttrGroupTable            uint32 = 0x0001
	groupKeyMgmtAttrMaxGroupsPerFabric    uint32 = 0x0002
	groupKeyMgmtAttrMaxGroupKeysPerFabric uint32 = 0x0003

	groupKeyMgmtCmdKeySetWrite              uint32 = 0x00
	groupKeyMgmtCmdKeySetRead               uint32 = 0x01
	groupKeyMgmtCmdKeySetReadResponse       uint32 = 0x02
	groupKeyMgmtCmdKeySetRemove             uint32 = 0x03
	groupKeyMgmtCmdKeySetReadAllIndices     uint32 = 0x04
	groupKeyMgmtCmdKeySetReadAllIndicesResp uint32 = 0x05
)

// Errors.
var (
	errGroupKeyMgmtInvalidArg = errors.New("matter: GroupKeyManagement invalid argument")
	errGroupKeyMgmtNotFound   = errors.New("matter: GroupKeyManagement key set not found")
)

// GroupKeyMgmtConfig drives [NewGroupKeyManagement]. Defaults mirror
// matter.js HEAD GroupKeyManagementServer.ts:531-532
// (maxGroupKeysPerFabric = 20, maxGroupsPerFabric = 21). Spec floors
// per Matter §11.2.4 are maxGroupKeysPerFabric ≥ 3 and
// maxGroupsPerFabric ≥ 4.
type GroupKeyMgmtConfig struct {
	MaxGroupsPerFabric    uint16
	MaxGroupKeysPerFabric uint16
}

// matter.js defaults — see GroupKeyManagementServer.ts:531-532.
const (
	defaultMaxGroupsPerFabric    uint16 = 21
	defaultMaxGroupKeysPerFabric uint16 = 20
)

// NewGroupKeyManagement constructs the cluster.
func NewGroupKeyManagement(s GroupStoreFacade, cfg GroupKeyMgmtConfig) (*GroupKeyManagement, error) {
	if s == nil {
		return nil, errors.New("matter: GroupKeyManagement store is required")
	}
	if cfg.MaxGroupsPerFabric == 0 {
		cfg.MaxGroupsPerFabric = defaultMaxGroupsPerFabric
	}
	if cfg.MaxGroupKeysPerFabric == 0 {
		cfg.MaxGroupKeysPerFabric = defaultMaxGroupKeysPerFabric
	}
	g := &GroupKeyManagement{
		store:                 s,
		maxGroupsPerFabric:    cfg.MaxGroupsPerFabric,
		maxGroupKeysPerFabric: cfg.MaxGroupKeysPerFabric,
	}
	// Seed DataVersion at a non-zero sentinel on construction so
	// DataVersionFilter=0 does not produce a false-positive cache hit on
	// the first read.
	g.dataVersion.Bump()
	return g, nil
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer                  = (*GroupKeyManagement)(nil)
	_ interfaces.MatterClusterCommandLister           = (*GroupKeyManagement)(nil)
	_ interfaces.MatterClusterDataVersion             = (*GroupKeyManagement)(nil)
	_ interfaces.MatterClusterCommandInvokePrivilege  = (*GroupKeyManagement)(nil)
	_ interfaces.MatterClusterAttributeWritePrivilege = (*GroupKeyManagement)(nil)
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the per-cluster monotonic counter seeded at construction and
// bumped on every GroupKeyMap / GroupKeySet mutation. Mirrors matter.js
// behavior layer auto-tracking and chip's ember dirty-marking in
// src/app/clusters/group-key-mgmt-server/.
func (g *GroupKeyManagement) MatterDataVersion() uint32 {
	return g.dataVersion.Current()
}

// MatterClusterID implements [interfaces.MatterClusterServer].
func (g *GroupKeyManagement) MatterClusterID() uint32 { return groupKeyMgmtClusterID }

// MinInvokePrivilege implements [interfaces.MatterClusterCommandInvokePrivilege].
// Every GroupKeyManagement command requires Administer (5) per Matter
// §11.2.10 (access "F A"). Mirrors matter.js
// packages/model/src/standard/elements/group-key-management.element.ts:48,54,65,71.
func (g *GroupKeyManagement) MinInvokePrivilege(cmdID uint32) uint8 {
	switch cmdID {
	case groupKeyMgmtCmdKeySetWrite, groupKeyMgmtCmdKeySetRead, groupKeyMgmtCmdKeySetRemove, groupKeyMgmtCmdKeySetReadAllIndices:
		return 5 // Administer
	default:
		return 3 // Operate — standard default
	}
}

// MinWritePrivilege implements [interfaces.MatterClusterAttributeWritePrivilege].
// GroupKeyMap (0x0000) requires Manage (4) per Matter §11.2.10 (access
// "RW F VM"). Mirrors matter.js packages/model/src/standard/elements/
// group-key-management.element.ts:28.
func (g *GroupKeyManagement) MinWritePrivilege(attrID uint32) uint8 {
	switch attrID {
	case groupKeyMgmtAttrGroupKeyMap:
		return 4 // Manage
	default:
		return 3 // Operate — standard default
	}
}

// GroupKeyMapStruct mirrors Matter §11.2.10.4.1.
type GroupKeyMapStruct struct {
	GroupID       uint16
	GroupKeySetID uint16
	FabricIndex   uint8
}

// GroupInfoMapStruct mirrors Matter §11.2.10.4.2.
type GroupInfoMapStruct struct {
	GroupID     uint16
	Endpoints   []uint16
	GroupName   string
	FabricIndex uint8
}

// GroupKeySetStruct mirrors Matter §11.2.10.4.3.
type GroupKeySetStruct struct {
	GroupKeySetID          uint16
	GroupKeySecurityPolicy uint8
	EpochKey0              []byte // nullable
	EpochStartTime0        uint64
	EpochKey1              []byte
	EpochStartTime1        uint64
	EpochKey2              []byte
	EpochStartTime2        uint64
}

// MatterRead implements [interfaces.MatterClusterServer].
func (g *GroupKeyManagement) MatterRead(attrID uint32) (any, bool) {
	return g.matterReadWithCtx(context.Background(), attrID)
}

// MatterReadFiltered is the fabric-scoped read path for GroupKeyMap.
// Derives the fabric from [im.FabricFilterFromContext] so CASE sessions
// see only their own key mappings. Mirrors matter.js
// GroupKeyManagementServer.ts:103-115 — GroupKeyMap attribute read uses
// context.session.associatedFabric as the filter.
func (g *GroupKeyManagement) MatterReadFiltered(ctx context.Context, attrID uint32) (any, bool) {
	return g.matterReadWithCtx(ctx, attrID)
}

func (g *GroupKeyManagement) matterReadWithCtx(ctx context.Context, attrID uint32) (any, bool) {
	switch attrID {
	case groupKeyMgmtAttrGroupKeyMap:
		// Derive fabric from IM context. Falls back to g.currentFabric for
		// test harnesses that do not stamp the context.
		_, fabric := im.FabricFilterFromContext(ctx)
		if fabric == 0 {
			g.mu.RLock()
			fabric = g.currentFabric
			g.mu.RUnlock()
		}
		mappings, err := g.store.ListGroupKeyMappings(ctx, fabric)
		if err != nil {
			return nil, false
		}
		out := make([]GroupKeyMapStruct, 0, len(mappings))
		for _, m := range mappings {
			out = append(out, GroupKeyMapStruct{
				GroupID:       m.GroupID,
				GroupKeySetID: m.GroupKeySetID,
				FabricIndex:   m.FabricIndex,
			})
		}
		return out, true
	case groupKeyMgmtAttrGroupTable:
		// Endpoints + GroupName persistence is not yet wired —
		// returning empty list is spec-compliant when no groups have
		// been added. Stufe 7 (subscription state machine) ties this
		// into the Groups cluster; v1.1 ships with empty group table.
		return []GroupInfoMapStruct{}, true
	case groupKeyMgmtAttrMaxGroupsPerFabric:
		g.mu.RLock()
		v := g.maxGroupsPerFabric
		g.mu.RUnlock()
		return v, true
	case groupKeyMgmtAttrMaxGroupKeysPerFabric:
		g.mu.RLock()
		v := g.maxGroupKeysPerFabric
		g.mu.RUnlock()
		return v, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return groupKeyMgmtClusterRevision, true
	// Global attributes 0xFFF8–0xFFFB: mirrors matter.js ClusterServer
	// auto-populated globalAttributes
	// (packages/node/src/behaviors/ClusterBehavior.ts) and chip
	// endpoint_config.h cluster metadata tables.
	case cluster.AttrGlobalGeneratedCommandList:
		// GKM generated commands: KeySetReadResponse (0x02),
		// KeySetReadAllIndicesResp (0x05).
		return []uint32{
			groupKeyMgmtCmdKeySetReadResponse,       // 0x02
			groupKeyMgmtCmdKeySetReadAllIndicesResp, // 0x05
		}, true
	case cluster.AttrGlobalAcceptedCommandList:
		// GKM accepted commands per Matter §11.2.10.
		return []uint32{
			groupKeyMgmtCmdKeySetWrite,          // 0x00
			groupKeyMgmtCmdKeySetRead,           // 0x01
			groupKeyMgmtCmdKeySetRemove,         // 0x03
			groupKeyMgmtCmdKeySetReadAllIndices, // 0x04
		}, true
	case cluster.AttrGlobalEventList:
		// GKM has no events per matter.js group-key-management.element.ts.
		return []uint32{}, true
	case cluster.AttrGlobalAttributeList:
		// Full attribute list per Matter §11.2.10 + global attrs.
		return []uint32{
			groupKeyMgmtAttrGroupKeyMap,            // 0x0000
			groupKeyMgmtAttrGroupTable,             // 0x0001
			groupKeyMgmtAttrMaxGroupsPerFabric,     // 0x0002
			groupKeyMgmtAttrMaxGroupKeysPerFabric,  // 0x0003
			cluster.AttrGlobalFeatureMap,           // 0xFFFC
			cluster.AttrGlobalClusterRevision,      // 0xFFFD
			cluster.AttrGlobalGeneratedCommandList, // 0xFFF8
			cluster.AttrGlobalAcceptedCommandList,  // 0xFFF9
			cluster.AttrGlobalEventList,            // 0xFFFA
			cluster.AttrGlobalAttributeList,        // 0xFFFB
		}, true
	}
	return nil, false
}

// MatterWrite handles GroupKeyMap as a writable attribute (Matter
// §11.2.10.4.1). The IM layer pre-filters the list to the requesting
// fabric.
func (g *GroupKeyManagement) MatterWrite(ctx context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	if attrID != groupKeyMgmtAttrGroupKeyMap {
		return fmt.Errorf("matter: GroupKeyManagement attribute 0x%04X is read-only", attrID)
	}
	list, ok := value.([]GroupKeyMapStruct)
	if !ok {
		return fmt.Errorf("%w: GroupKeyMap write expected []GroupKeyMapStruct, got %T", errGroupKeyMgmtInvalidArg, value)
	}
	// Resolve fabric from the IM dispatcher's filter context:
	// g.currentFabric is set by SetCurrentFabric which has no production
	// caller, so every inbound write would see fabric=0 and reject as
	// cross-fabric. FabricFilterFromContext returns the session's fabric
	// on CASE (after AddNOC) — the correct value for a fabric-scoped write.
	_, fabric := im.FabricFilterFromContext(ctx)
	if fabric == 0 {
		// Fall back to the legacy SetCurrentFabric path so test
		// harnesses that haven't migrated to FabricFilterFromContext
		// still work.
		g.mu.RLock()
		fabric = g.currentFabric
		g.mu.RUnlock()
	}

	// Matter spec semantics: writing GroupKeyMap is a *replace* per
	// fabric — every entry is set. We naively apply each mapping;
	// removals require explicit absence (the IM layer expands).
	for _, m := range list {
		if m.FabricIndex != fabric {
			return fmt.Errorf("%w: cross-fabric write rejected", errGroupKeyMgmtInvalidArg)
		}
		if err := g.store.SetGroupKeyMapping(ctx, store.GroupKeyMapping{
			FabricIndex:   fabric,
			GroupID:       m.GroupID,
			GroupKeySetID: m.GroupKeySetID,
		}); err != nil {
			return fmt.Errorf("matter: GroupKeyMap write: %w", err)
		}
	}
	// Bump DataVersion after a successful GroupKeyMap mutation so
	// DataVersionFilter evaluation correctly detects the cluster changed.
	g.dataVersion.Bump()
	return nil
}

// KeySetWriteRequest mirrors Matter §11.2.10.6.1.
type KeySetWriteRequest struct {
	GroupKeySet GroupKeySetStruct
}

// KeySetReadRequest mirrors Matter §11.2.10.6.2.
type KeySetReadRequest struct {
	GroupKeySetID uint16
}

// KeySetReadResponse mirrors Matter §11.2.10.6.3.
type KeySetReadResponse struct {
	GroupKeySet GroupKeySetStruct
}

// KeySetRemoveRequest mirrors Matter §11.2.10.6.4.
type KeySetRemoveRequest struct {
	GroupKeySetID uint16
}

// KeySetReadAllIndicesResponse mirrors Matter §11.2.10.6.5.
type KeySetReadAllIndicesResponse struct {
	GroupKeySetIDs []uint16
}

// MatterInvoke implements [interfaces.MatterClusterServer].
func (g *GroupKeyManagement) MatterInvoke(ctx context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	// Resolve fabric from IM dispatcher context: SetCurrentFabric has no
	// production caller, so every KeySetWrite / KeySetRead / KeySetRemove /
	// KeySetReadAllIndices command arriving on a post-AddNOC CASE session
	// previously saw fabric=0 and returned "command without active fabric" —
	// which commissioners retransmit-loop on. FabricFilterFromContext returns
	// the session's fabric (CASE > 0) and falls through to the legacy
	// currentFabric only when no IM context is wired (test paths).
	_, fabric := im.FabricFilterFromContext(ctx)
	if fabric == 0 {
		g.mu.RLock()
		fabric = g.currentFabric
		g.mu.RUnlock()
	}
	if fabric == 0 {
		return nil, errors.New("matter: GroupKeyManagement command without active fabric")
	}

	switch cmdID {
	case groupKeyMgmtCmdKeySetWrite:
		return g.handleKeySetWrite(ctx, fabric, fields)
	case groupKeyMgmtCmdKeySetRead:
		return g.handleKeySetRead(ctx, fabric, fields)
	case groupKeyMgmtCmdKeySetRemove:
		return g.handleKeySetRemove(ctx, fabric, fields)
	case groupKeyMgmtCmdKeySetReadAllIndices:
		return g.handleKeySetReadAllIndices(ctx, fabric)
	}
	return nil, fmt.Errorf("matter: GroupKeyManagement command 0x%02X not supported", cmdID)
}

// MatterReportable lists subscribe-able attributes.
func (g *GroupKeyManagement) MatterReportable() []uint32 {
	return []uint32{groupKeyMgmtAttrGroupKeyMap, groupKeyMgmtAttrGroupTable}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister]
// so wildcard subscribe enumerates the full cluster surface.
//
// Global attributes 0xFFF8–0xFFFB included so Apple's initial subscribe
// sweep can cache GeneratedCommandList, AcceptedCommandList, EventList
// and AttributeList for cluster 0x3F.
func (g *GroupKeyManagement) MatterAttributes() []uint32 {
	return []uint32{
		groupKeyMgmtAttrGroupKeyMap,
		groupKeyMgmtAttrGroupTable,
		groupKeyMgmtAttrMaxGroupsPerFabric,
		groupKeyMgmtAttrMaxGroupKeysPerFabric,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
		cluster.AttrGlobalGeneratedCommandList,
		cluster.AttrGlobalAcceptedCommandList,
		cluster.AttrGlobalEventList,
		cluster.AttrGlobalAttributeList,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke.
// Mirrors matter.js packages/model/src/standard/elements/
// group-key-management.element.ts accepted commands.
func (g *GroupKeyManagement) MatterAcceptedCommands() []uint32 {
	return []uint32{
		groupKeyMgmtCmdKeySetWrite,          // 0x00
		groupKeyMgmtCmdKeySetRead,           // 0x01
		groupKeyMgmtCmdKeySetRemove,         // 0x03
		groupKeyMgmtCmdKeySetReadAllIndices, // 0x04
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
// Lists the response command IDs this server may emit.
// Mirrors matter.js packages/model/src/standard/elements/
// group-key-management.element.ts generated commands.
func (g *GroupKeyManagement) MatterGeneratedCommands() []uint32 {
	return []uint32{
		groupKeyMgmtCmdKeySetReadResponse,       // 0x02
		groupKeyMgmtCmdKeySetReadAllIndicesResp, // 0x05
	}
}

// SetCurrentFabric is called by the IM dispatcher before each
// fabric-scoped invocation.
func (g *GroupKeyManagement) SetCurrentFabric(idx uint8) {
	g.mu.Lock()
	g.currentFabric = idx
	g.mu.Unlock()
}

// max64BitTime mirrors matter.js MAX_64BIT_TIME — when an
// EpochStartTime field carries this sentinel the corresponding slot
// is disabled (see GroupKeyManagementServer.ts:24,298-309).
const max64BitTime uint64 = 0xFFFFFFFFFFFFFFFF

// ipkDefaultEpochStartTime mirrors matter.js
// `IPK_DEFAULT_EPOCH_START_TIME` (the sentinel reserved for the IPK).
// EpochStartTime0 must be strictly greater than this value
// (GroupKeyManagementServer.ts:314-317).
const ipkDefaultEpochStartTime uint64 = 0

func (g *GroupKeyManagement) handleKeySetWrite(ctx context.Context, fabric uint8, fields any) (any, error) {
	req, ok := fields.(KeySetWriteRequest)
	if !ok {
		return nil, fmt.Errorf("%w: KeySetWriteRequest expected, got %T", errGroupKeyMgmtInvalidArg, fields)
	}
	gks := req.GroupKeySet

	// Mirrors matter.js packages/node/src/behaviors/group-key-management/
	// GroupKeyManagementServer.ts:280-352. Five validation classes,
	// every failure is reported as InvalidCommand (sentinel substring
	// "invalid command argument" → StatusInvalidCommand via
	// dispatcher.invokeErrorStatus):
	//
	//   1. MAX_64BIT_TIME on EpochStartTimeN disables the slot —
	//      treat (start, key) as null.
	//   2. EpochKey0 + EpochStartTime0 are mandatory and EpochStartTime0
	//      must exceed IPK_DEFAULT_EPOCH_START_TIME.
	//   3. EpochKey1 ↔ EpochStartTime1 must agree (both set or both
	//      absent), EpochStartTime1 > EpochStartTime0.
	//   4. EpochKey2 ↔ EpochStartTime2 must agree, EpochStartTime2 >
	//      EpochStartTime1, and EpochKey1 must be present whenever
	//      EpochKey2 is present.
	//   5. GroupKeySecurityPolicy must be TrustFirst (the only value
	//      matter.js accepts as of HEAD).
	if gks.EpochStartTime0 == max64BitTime {
		gks.EpochStartTime0 = 0
		gks.EpochKey0 = nil
	}
	if gks.EpochStartTime1 == max64BitTime {
		gks.EpochStartTime1 = 0
		gks.EpochKey1 = nil
	}
	if gks.EpochStartTime2 == max64BitTime {
		gks.EpochStartTime2 = 0
		gks.EpochKey2 = nil
	}

	// EpochKey length constraint: each non-nil key must be exactly 16 bytes.
	// Mirrors chip src/app/clusters/group-key-mgmt-server/
	// GroupKeyManagementCluster.cpp: VerifyOrExit(epochKey.size() == 16,
	// err = CHIP_ERROR_INVALID_ARGUMENT) which the dispatcher maps to
	// Status::ConstraintError (substring "constraint" triggers that path).
	const epochKeyLen = 16
	if len(gks.EpochKey0) > 0 && len(gks.EpochKey0) != epochKeyLen {
		return nil, fmt.Errorf("matter: KeySetWrite: constraint error: EpochKey0 must be exactly %d bytes, got %d", epochKeyLen, len(gks.EpochKey0))
	}
	if len(gks.EpochKey1) > 0 && len(gks.EpochKey1) != epochKeyLen {
		return nil, fmt.Errorf("matter: KeySetWrite: constraint error: EpochKey1 must be exactly %d bytes, got %d", epochKeyLen, len(gks.EpochKey1))
	}
	if len(gks.EpochKey2) > 0 && len(gks.EpochKey2) != epochKeyLen {
		return nil, fmt.Errorf("matter: KeySetWrite: constraint error: EpochKey2 must be exactly %d bytes, got %d", epochKeyLen, len(gks.EpochKey2))
	}

	if len(gks.EpochKey0) == 0 || gks.EpochStartTime0 == 0 {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochKey0 and EpochStartTime0 must be set")
	}
	if gks.EpochStartTime0 <= ipkDefaultEpochStartTime {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochStartTime0 must be > IPK default")
	}

	if len(gks.EpochKey1) > 0 && (gks.EpochStartTime1 == 0 || gks.EpochStartTime1 <= gks.EpochStartTime0) {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochStartTime1 must be set and greater than EpochStartTime0")
	}
	if len(gks.EpochKey1) == 0 && gks.EpochStartTime1 != 0 {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochKey1 must be set if EpochStartTime1 is set")
	}

	if len(gks.EpochKey2) > 0 && len(gks.EpochKey1) == 0 {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochKey1 must be set if EpochKey2 is set")
	}
	if len(gks.EpochKey2) > 0 && (gks.EpochStartTime2 == 0 || gks.EpochStartTime1 == 0 || gks.EpochStartTime2 <= gks.EpochStartTime1) {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochStartTime2 must be set and greater than EpochStartTime1")
	}
	if len(gks.EpochKey2) == 0 && gks.EpochStartTime2 != 0 {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: EpochKey2 must be set if EpochStartTime2 is set")
	}

	if store.SecurityPolicy(gks.GroupKeySecurityPolicy) != store.SecurityPolicyTrustFirst {
		return nil, errors.New("matter: KeySetWrite: invalid command argument: GroupKeySecurityPolicy must be TrustFirst")
	}

	rec := store.GroupKeySet{
		FabricIndex:    fabric,
		GroupKeySetID:  gks.GroupKeySetID,
		SecurityPolicy: store.SecurityPolicy(gks.GroupKeySecurityPolicy),
		EpochKey0:      gks.EpochKey0,
		EpochStart0:    gks.EpochStartTime0,
		EpochKey1:      gks.EpochKey1,
		EpochStart1:    gks.EpochStartTime1,
		EpochKey2:      gks.EpochKey2,
		EpochStart2:    gks.EpochStartTime2,
	}
	if err := g.store.UpsertGroupKeySet(ctx, rec); err != nil {
		return nil, fmt.Errorf("matter: KeySetWrite: %w", err)
	}
	// Bump DataVersion after a successful KeySetWrite so DataVersionFilter
	// evaluation correctly detects the cluster changed.
	g.dataVersion.Bump()
	return nil, nil
}

func (g *GroupKeyManagement) handleKeySetRead(ctx context.Context, fabric uint8, fields any) (any, error) {
	req, ok := fields.(KeySetReadRequest)
	if !ok {
		return nil, fmt.Errorf("%w: KeySetReadRequest expected, got %T", errGroupKeyMgmtInvalidArg, fields)
	}
	rec, err := g.store.GetGroupKeySet(ctx, fabric, req.GroupKeySetID)
	if errors.Is(err, store.ErrGroupKeySetNotFound) {
		return nil, fmt.Errorf("%w: id=%d", errGroupKeyMgmtNotFound, req.GroupKeySetID)
	}
	if err != nil {
		return nil, fmt.Errorf("matter: KeySetRead: %w", err)
	}
	return KeySetReadResponse{
		GroupKeySet: GroupKeySetStruct{
			GroupKeySetID:          rec.GroupKeySetID,
			GroupKeySecurityPolicy: uint8(rec.SecurityPolicy),
			// Per Matter §11.2.10.6.3, EpochKey* fields are nulled out
			// in the response (the keys never leave the bridge after
			// initial write). We intentionally omit them.
			EpochStartTime0: rec.EpochStart0,
			EpochStartTime1: rec.EpochStart1,
			EpochStartTime2: rec.EpochStart2,
		},
	}, nil
}

func (g *GroupKeyManagement) handleKeySetRemove(ctx context.Context, fabric uint8, fields any) (any, error) {
	req, ok := fields.(KeySetRemoveRequest)
	if !ok {
		return nil, fmt.Errorf("%w: KeySetRemoveRequest expected, got %T", errGroupKeyMgmtInvalidArg, fields)
	}
	// Mirrors matter.js packages/node/src/behaviors/group-key-management/
	// GroupKeyManagementServer.ts:405-408 — GroupKeySet 0 is the IPK
	// (Identity Protection Key) that the bridge needs for CASE; the
	// spec forbids deleting it via KeySetRemove. matter.js throws a
	// StatusResponseError with InvalidCommand; we surface the same
	// status by emitting a sentinel substring picked up by
	// `invokeErrorStatus` in dispatcher.go.
	if req.GroupKeySetID == 0 {
		return nil, errors.New("matter: KeySetRemove: invalid command argument: GroupKeySet 0 (IPK) cannot be removed")
	}
	if err := g.store.RemoveGroupKeySet(ctx, fabric, req.GroupKeySetID); err != nil {
		return nil, fmt.Errorf("matter: KeySetRemove: %w", err)
	}
	// Bump DataVersion after a successful KeySetRemove.
	g.dataVersion.Bump()
	return nil, nil
}

func (g *GroupKeyManagement) handleKeySetReadAllIndices(ctx context.Context, fabric uint8) (any, error) {
	sets, err := g.store.ListGroupKeySets(ctx, fabric)
	if err != nil {
		return nil, fmt.Errorf("matter: KeySetReadAllIndices: %w", err)
	}
	ids := make([]uint16, 0, len(sets))
	for _, s := range sets {
		ids = append(ids, s.GroupKeySetID)
	}
	return KeySetReadAllIndicesResponse{GroupKeySetIDs: ids}, nil
}
