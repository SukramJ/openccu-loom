// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"fmt"

	doorlockcluster "github.com/SukramJ/go-fabric/cluster/lock"
	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Lock participates in the Matter source surface
// (ADR 0012) as a DoorLock (0x000A) endpoint, satisfies the StateSource
// interface consumed by [doorlockcluster.DoorLockServer], and exposes the
// per-cluster DataVersion so the bridge can track physical state changes.
var (
	_ interfaces.MatterEndpointSource     = (*Lock)(nil)
	_ doorlockcluster.StateSource         = (*Lock)(nil)
	_ interfaces.MatterClusterDataVersion = (*Lock)(nil)
	_ interfaces.MatterChangeNotifier     = (*Lock)(nil)
)

// matterDeviceTypeDoorLock is the Matter Device Type ID for Door Lock.
const matterDeviceTypeDoorLock uint16 = 0x000A

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (l *Lock) MatterDeviceType() uint16 { return matterDeviceTypeDoorLock }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]. A Lock
// reads its DoorLock.LockState from one of several kind-specific wire DPs
// (IP LOCK_STATE + DIRECTION, RF/Button boolean STATE, plus the jam flag);
// fan every one into the callback so a lock/unlock or jam that happened at
// the device — not through Apple — dirty-marks the DoorLock cluster and
// reaches Apple's Subscribe. Each DP's OnMatterValueChanged guards a nil
// receiver, so the kind's absent DPs contribute a no-op unsubscribe.
func (l *Lock) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.stateDp.OnMatterValueChanged(cb),
		l.directionDp.OnMatterValueChanged(cb),
		l.jammedDp.OnMatterValueChanged(cb),
		l.boolStateDp.OnMatterValueChanged(cb),
	)
}

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Returns a [doorlockcluster.DoorLockServer] backed by this Lock so
// the Matter bridge attaches the DoorLock cluster (0x0101) to the
// Lock (0x000A) endpoint. The Lock's persistent DataVersionTracker is
// threaded into the server so bumps from physical operation survive
// server reconstruction across [MatterClusterServers] calls.
func (l *Lock) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{
		doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{
			Source:      l,
			DataVersion: &l.dataVersion,
		}),
	}
}

// LockInvoke implements [doorlockcluster.StateSource]. Dispatches
// the Matter command ID to the corresponding HM operation at
// [matterDispatchPriority] — the cluster contract carries no priority,
// so the urgency of a bridged lock operation is decided here.
func (l *Lock) LockInvoke(ctx context.Context, cmdID uint32) error {
	switch cmdID {
	case wire.DoorLockCmdLockDoor:
		return l.Lock(ctx, matterDispatchPriority)
	case wire.DoorLockCmdUnlockDoor:
		return l.Unlock(ctx, matterDispatchPriority)
	case wire.DoorLockCmdUnboltDoor:
		return l.Open(ctx, matterDispatchPriority)
	default:
		return fmt.Errorf("lock: unknown Matter command 0x%02X", cmdID)
	}
}
