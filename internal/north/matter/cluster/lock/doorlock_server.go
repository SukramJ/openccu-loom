// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package lock contains the standalone DoorLock cluster server for the
// Matter bridge. The server wraps a [StateSource] that the rich model
// layer (internal/model/custom/lock) satisfies, keeping cluster logic
// separate from the domain model.
package lock

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DoorLock cluster revision 10 per matter.js HEAD (@matter/model 0.16.11).
const doorLockClusterRevision uint16 = 10

// DoorLock FeatureMap: only the Unbolting bit is advertised. UBOLT is
// feature bit 12 per matter.js door-lock-cluster.element.ts (UBOLT
// constraint "12"); bit 4 is WeekDayAccessSchedules.
const doorLockFeatureUnbolt uint32 = 1 << 12

// LockState enum per Matter §5.2.6.2.
const (
	lockStateNotFullyLocked uint8 = 0
	lockStateLocked         uint8 = 1
	lockStateUnlocked       uint8 = 2
)

// LockType enum: 0 = DeadBolt.
const lockTypeDeadBolt uint8 = 0

// LockOperationTypeEnum values per Matter §5.2.6.14. Mirrors matter.js
// door-lock-cluster.element.ts:704-709 (Lock 0x0, Unlock 0x1,
// Unlatch 0x4).
const (
	lockOperationTypeLock    uint8 = 0
	lockOperationTypeUnlock  uint8 = 1
	lockOperationTypeUnlatch uint8 = 4
)

// operationSourceRemote is the OperationSourceEnum value for a
// controller-driven operation per Matter §5.2.6.15. Mirrors matter.js
// door-lock-cluster.element.ts:739 (Remote 0x7, the only
// conformance-M source).
const operationSourceRemote uint8 = 7

// LockOperationEvent is the cluster-native payload for the DoorLock
// LockOperation event (id 0x02, priority critical) per Matter §5.2.10.3.
// Field set mirrors matter.js door-lock-cluster.element.ts:181-195;
// UserIndex, FabricIndex and SourceNode are nullable (nil pointer →
// TLV null). The Credentials field ([USR]-gated) is omitted entirely —
// this projection advertises no USR feature.
//
//nolint:revive // LockOperationEvent mirrors the Matter event name verbatim.
type LockOperationEvent struct {
	LockOperationType uint8
	OperationSource   uint8
	UserIndex         *uint16
	FabricIndex       *uint8
	SourceNode        *uint64
}

var (
	errUnknownAttribute = errors.New("doorlock: unknown attribute")
	errUnknownCommand   = errors.New("doorlock: unknown command")
)

// StateSource is the read-side interface a model-layer lock DP must
// satisfy so DoorLockServer can project its Matter attribute surface.
//
// IsJammed returns true when ERROR_JAMMED is asserted.
// IsLocked returns (locked=true, observed=true) when the lock state is known
// and the lock is in the locked position.
// LockInvoke dispatches LockDoor / UnlockDoor / UnboltDoor commands to the
// CCU. cmdID is one of the [wire.DoorLockCmd*] constants.
type StateSource interface {
	IsJammed() bool
	IsLocked() (locked, observed bool)
	LockInvoke(ctx context.Context, cmdID uint32, priority hmenum.CommandPriority) error
}

// DoorLockConfig holds the construction parameters for [DoorLockServer].
type DoorLockConfig struct {
	// Source provides live lock state and command dispatch.
	Source StateSource
	// DataVersion is an optional pointer to a [cluster.DataVersionTracker]
	// owned by the model layer. When non-nil, the server uses the caller's
	// tracker for [MatterDataVersion] and [MatterInvoke] bumps so the
	// counter survives server reconstruction across [MatterClusterServers]
	// calls. When nil, an embedded tracker is used (prior behaviour).
	DataVersion *cluster.DataVersionTracker
}

// DoorLockServer implements [interfaces.MatterClusterServer] for the
// DoorLock cluster (0x0101). It wraps a [StateSource] from the model
// layer and projects the mandatory attribute surface Apple Home and other
// controllers expect on any Lock (0x000A) device type endpoint.
//
// LockType is fixed to DeadBolt (0); ActuatorEnabled is always true;
// OperatingMode is fixed to Normal (0); SupportedOperatingModes is 0xFFF6
// — all alwaysSet bits (2047 = bits 0-10) set plus bits for vacation,
// privacy, passage; Normal (bit 0) and NoRemoteLockUnlock (bit 3) are
// both clear = supported. Mirrors matter.js DoorLockServer.ts:69
// ({vacation:true,privacy:true,passage:true,alwaysSet:2047}) → 0xFFF6.
type DoorLockServer struct {
	embedded cluster.DataVersionTracker // used when cfg.DataVersion is nil
	ext      *cluster.DataVersionTracker
	src      StateSource

	mu       sync.Mutex
	emitter  interfaces.MatterEventEmitter
	endpoint uint16
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer          = (*DoorLockServer)(nil)
	_ interfaces.MatterClusterDataVersion     = (*DoorLockServer)(nil)
	_ interfaces.MatterClusterAttributeLister = (*DoorLockServer)(nil)
	_ interfaces.MatterClusterCommandLister   = (*DoorLockServer)(nil)
	_ interfaces.MatterClusterEventLister     = (*DoorLockServer)(nil)
	_ interfaces.MatterEventReceiver          = (*DoorLockServer)(nil)
)

// NewDoorLockServer constructs a DoorLockServer with LockType=DeadBolt.
func NewDoorLockServer(cfg DoorLockConfig) *DoorLockServer {
	return &DoorLockServer{src: cfg.Source, ext: cfg.DataVersion}
}

// tracker returns the active DataVersionTracker — the caller-supplied
// external tracker when available, the embedded one otherwise.
func (s *DoorLockServer) tracker() *cluster.DataVersionTracker {
	if s.ext != nil {
		return s.ext
	}
	return &s.embedded
}

// MatterClusterID returns 0x0101 (DoorLock).
func (*DoorLockServer) MatterClusterID() uint32 { return wire.DoorLockClusterID }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *DoorLockServer) MatterDataVersion() uint32 { return s.tracker().Current() }

// MatterRead implements [interfaces.MatterClusterServer].
func (s *DoorLockServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case wire.DoorLockAttrLockState:
		if s.src.IsJammed() {
			return lockStateNotFullyLocked, true
		}
		locked, observed := s.src.IsLocked()
		if !observed {
			return nil, true
		}
		if locked {
			return lockStateLocked, true
		}
		return lockStateUnlocked, true
	case wire.DoorLockAttrLockType:
		return lockTypeDeadBolt, true
	case wire.DoorLockAttrActuatorEnabled:
		return true, true
	case wire.DoorLockAttrOperatingMode:
		return uint8(0), true
	case wire.DoorLockAttrSupportedOperatingModes:
		// 0xFFF6: alwaysSet bits (0x07FF, bits 0-10) | vacation (bit 1) |
		// privacy (bit 2) | passage (bit 4) — Normal (bit 0) and
		// NoRemoteLockUnlock (bit 3) clear = supported.
		// Mirrors matter.js DoorLockServer.ts:69.
		return uint16(0xFFF6), true
	case cluster.AttrGlobalFeatureMap:
		return doorLockFeatureUnbolt, true
	case cluster.AttrGlobalClusterRevision:
		return doorLockClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite implements [interfaces.MatterClusterServer]. DoorLock has
// no writable attributes in this projection; all state changes go through
// commands.
func (*DoorLockServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errUnknownAttribute, attrID)
}

// MatterInvoke implements [interfaces.MatterClusterServer]. Dispatches
// LockDoor / UnlockDoor / UnboltDoor to the underlying source and fires
// the LockOperation event on success.
func (s *DoorLockServer) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	switch cmdID {
	case wire.DoorLockCmdLockDoor, wire.DoorLockCmdUnlockDoor, wire.DoorLockCmdUnboltDoor:
		if err := s.src.LockInvoke(ctx, cmdID, priority); err != nil {
			return nil, err
		}
		s.tracker().Bump()
		s.emitLockOperation(ctx, cmdID)
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errUnknownCommand, cmdID)
	}
}

// emitLockOperation fires the Matter §5.2.10.3 LockOperation event
// after a successful remote lock/unlock/unbolt. Mirrors matter.js
// DoorLockServer.ts:119-143 (each command handler ends in
// #emitLockOperation) and :911-939 (payload assembly:
// OperationSource=Remote, UserIndex/Credentials null without PIN
// credentials, FabricIndex + SourceNode from the invoking session).
// A PASE session carries fabric index 0 and no subject node — both
// encode as TLV null, matching matter.js's null fallbacks. No-op until
// the bridge wires an emitter via [SetMatterEventEmitter].
func (s *DoorLockServer) emitLockOperation(ctx context.Context, cmdID uint32) {
	s.mu.Lock()
	emitter := s.emitter
	endpoint := s.endpoint
	s.mu.Unlock()
	if emitter == nil {
		return
	}
	var opType uint8
	switch cmdID {
	case wire.DoorLockCmdLockDoor:
		opType = lockOperationTypeLock
	case wire.DoorLockCmdUnlockDoor:
		opType = lockOperationTypeUnlock
	case wire.DoorLockCmdUnboltDoor:
		// UnboltDoor reports Unlatch, not Unlock — matter.js
		// DoorLockServer.ts:140-142.
		opType = lockOperationTypeUnlatch
	}
	ev := LockOperationEvent{
		LockOperationType: opType,
		OperationSource:   operationSourceRemote,
	}
	if _, fabricIndex := im.FabricFilterFromContext(ctx); fabricIndex != 0 {
		ev.FabricIndex = &fabricIndex
	}
	if nodeID, _ := im.SubjectFromContext(ctx); nodeID != 0 {
		ev.SourceNode = &nodeID
	}
	emitter.MatterEmitEvent(endpoint, wire.DoorLockClusterID,
		wire.DoorLockEventLockOperation, ev,
		interfaces.MatterEventPriorityCritical)
}

// SetMatterEventEmitter implements [interfaces.MatterEventReceiver].
// Called by the bridge during topology assembly so [MatterInvoke] can
// fire the LockOperation event without holding a bridge reference.
// Idempotent — re-wiring during topology rebuild replaces the emitter.
func (s *DoorLockServer) SetMatterEventEmitter(emitter interfaces.MatterEventEmitter) {
	s.mu.Lock()
	s.emitter = emitter
	s.mu.Unlock()
}

// SetEndpoint stamps the endpoint id this DoorLock server is mounted
// on. Matter events carry the (endpoint, cluster, event) triple; the
// endpoint is captured at assembly time because the server is built by
// the model layer before the bridge assigns endpoint ids.
func (s *DoorLockServer) SetEndpoint(endpoint uint16) {
	s.mu.Lock()
	s.endpoint = endpoint
	s.mu.Unlock()
}

// MatterReportable returns the attribute IDs that change on wire events.
func (*DoorLockServer) MatterReportable() []uint32 {
	return []uint32{wire.DoorLockAttrLockState}
}

// MatterAttributes implements [interfaces.MatterClusterAttributeLister].
func (*DoorLockServer) MatterAttributes() []uint32 {
	return []uint32{
		wire.DoorLockAttrLockState,
		wire.DoorLockAttrLockType,
		wire.DoorLockAttrActuatorEnabled,
		wire.DoorLockAttrOperatingMode,
		wire.DoorLockAttrSupportedOperatingModes,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
func (*DoorLockServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		wire.DoorLockCmdLockDoor,
		wire.DoorLockCmdUnlockDoor,
		wire.DoorLockCmdUnboltDoor,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
// DoorLock commands produce status-only InvokeResponses; no generated command
// IDs are advertised.
func (*DoorLockServer) MatterGeneratedCommands() []uint32 { return nil }

// MatterEvents implements [interfaces.MatterClusterEventLister]. The
// three conformance-M DoorLock events are advertised (matter.js
// door-lock-cluster.element.ts:172/181/198); DoorStateChange (DPS) and
// LockUserChange (USR) are feature-gated and absent. The server emits
// LockOperation on successful remote operations; DoorLockAlarm and
// LockOperationError have no emission path without PIN-credential
// support, matching matter.js where they fire only from the wrong-code
// path (DoorLockServer.ts:889 / :941).
func (*DoorLockServer) MatterEvents() []uint32 {
	return []uint32{
		wire.DoorLockEventDoorLockAlarm,
		wire.DoorLockEventLockOperation,
		wire.DoorLockEventLockOperationError,
	}
}
