// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"
	"fmt"

	doorlockcluster "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
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
)

// matterDeviceTypeDoorLock is the Matter Device Type ID for Door Lock.
const matterDeviceTypeDoorLock uint16 = 0x000A

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (l *Lock) MatterDeviceType() uint16 { return matterDeviceTypeDoorLock }

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
// the Matter command ID to the corresponding HM operation.
func (l *Lock) LockInvoke(ctx context.Context, cmdID uint32, priority hmenum.CommandPriority) error {
	switch cmdID {
	case wire.DoorLockCmdLockDoor:
		return l.Lock(ctx, priority)
	case wire.DoorLockCmdUnlockDoor:
		return l.Unlock(ctx, priority)
	case wire.DoorLockCmdUnboltDoor:
		return l.Open(ctx, priority)
	default:
		return fmt.Errorf("lock: unknown Matter command 0x%02X", cmdID)
	}
}
