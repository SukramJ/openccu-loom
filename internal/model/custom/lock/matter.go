// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"fmt"

	doorlockdef "github.com/SukramJ/go-fabric/cluster/spec/doorlock"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Lock participates in the Matter source surface
// (ADR 0012) as a DoorLock (0x000A) device, is the StateSource the
// DoorLock declaration names, and notifies the bridge of physical state
// changes, which advance the cluster's DataVersion there.
var (
	_ interfaces.MatterDeviceSource   = (*Lock)(nil)
	_ doorlockdef.StateSource         = (*Lock)(nil)
	_ interfaces.MatterChangeNotifier = (*Lock)(nil)
)

// matterDeviceTypeDoorLock is the Matter Device Type ID for Door Lock.
const matterDeviceTypeDoorLock = uint16(fabricdevice.DoorLockID)

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

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (l *Lock) MatterDeviceType() uint16 { return matterDeviceTypeDoorLock }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]. A Lock
// reads its DoorLock.LockState from one of several kind-specific wire DPs
// (IP LOCK_STATE + DIRECTION, RF/Button boolean STATE, plus the jam flag —
// ERROR_JAMMED, or the RF lock's ERROR, see [Lock.IsJammed]); fan every one
// into the callback so a lock/unlock or jam that happened at the device —
// not through Apple — dirty-marks the DoorLock cluster, advances its
// DataVersion in the bridge and reaches Apple's Subscribe. Each DP's
// OnMatterValueChanged guards a nil receiver, so the kind's absent DPs
// contribute a no-op unsubscribe.
func (l *Lock) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.stateDp.OnMatterValueChanged(cb),
		l.directionDp.OnMatterValueChanged(cb),
		l.jammedDp.OnMatterValueChanged(cb),
		l.boolStateDp.OnMatterValueChanged(cb),
		l.rfErrorDp.OnMatterValueChanged(cb),
	)
}

// MatterDevice implements [interfaces.MatterDeviceSource]: a DoorLock
// with the DoorLock cluster (0x0101) declared on this Lock as its
// StateSource, built once per lock. The server reads LockState from the
// lock at every read and hands it the three commands; Unbolting is
// selected because every lock kind this profile drives can open the
// door, and an empty selection is none (matter.js's).
func (l *Lock) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return l.matterDevice.Device(func() (*fabricdevice.Device, error) {
		return generic.NewMatterDevice(l, matterDeviceTypeDoorLock, opts,
			doorlockdef.Declare{StateSource: l, Features: doorlockdef.FeatureUnbolting})
	})
}

// LockInvoke implements [doorlockdef.StateSource]. Dispatches
// the Matter command ID to the corresponding HM operation at
// [matterDispatchPriority] — the cluster contract carries no priority,
// so the urgency of a bridged lock operation is decided here.
func (l *Lock) LockInvoke(ctx context.Context, cmdID uint32) error {
	switch cmdID {
	case doorlockdef.CmdLockDoor:
		return l.Lock(ctx, matterDispatchPriority)
	case doorlockdef.CmdUnlockDoor:
		return l.Unlock(ctx, matterDispatchPriority)
	case doorlockdef.CmdUnboltDoor:
		return l.Open(ctx, matterDispatchPriority)
	default:
		return fmt.Errorf("lock: unknown Matter command 0x%02X", cmdID)
	}
}
