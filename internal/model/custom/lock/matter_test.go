// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/wire"
	"github.com/SukramJ/go-fabric/contract"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// requesterCtx is the context of a lock command from a commissioned
// controller: go-fabric's DoorLock server refuses a lock command whose
// session has no fabric (a PASE session before AddNOC) with
// UNSUPPORTED_ACCESS before the lock is asked, as matter.js does
// (DoorLockServer.ts:1017-1022), so every invoke here names fabric 1.
func requesterCtx() context.Context {
	return im.WithFabricFilter(context.Background(), false, 1)
}

// matterServers returns the cluster servers of the device the lock
// projects.
func matterServers(l *Lock) []contract.ClusterServer {
	d, err := l.MatterDevice(fabricdevice.Options{StableKey: fabricdevice.StringKey(l.Address), Name: "lock"})
	if err != nil || d == nil {
		panic(fmt.Sprintf("MatterDevice = (%v, %v), want a device", d, err))
	}
	return d.Servers()
}

// srv returns the DoorLock server of the device the lock projects.
func srv(l *Lock) interface {
	MatterClusterID() uint32
	MatterRead(uint32) (any, bool)
	MatterInvoke(context.Context, uint32, any) (any, error)
	MatterReportable() []uint32
} {
	servers := matterServers(l)
	if len(servers) == 0 {
		panic("the lock's device carries no server")
	}
	type fullServer interface {
		MatterClusterID() uint32
		MatterRead(uint32) (any, bool)
		MatterInvoke(context.Context, uint32, any) (any, error)
		MatterReportable() []uint32
	}
	return servers[0].(fullServer)
}

// TestMatterDeviceTypeIsDoorLock locks the DoorLock (0x000A) device
// type advertisement.
func TestMatterDeviceTypeIsDoorLock(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if got := r.lock.MatterDeviceType(); got != 0x000A {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x000A", got)
	}
}

// TestMatterDeviceServesDoorLock confirms the single-cluster projection
// serves DoorLock (0x0101).
func TestMatterDeviceServesDoorLock(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	servers := matterServers(r.lock)
	if len(servers) != 1 {
		t.Fatalf("expected 1 cluster server, got %d", len(servers))
	}
	if id := servers[0].MatterClusterID(); id != 0x0101 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0101", id)
	}
}

// TestLockStateLockedMaps maps StateLocked to Matter LockState=1.
func TestLockStateLockedMaps(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	fireLockEnum(t, r.stateDP, string(StateLocked))
	v, ok := srv(r.lock).MatterRead(0x0000)
	if !ok || v.(uint8) != 1 {
		t.Fatalf("LockState = (%v, %v), want (1, true)", v, ok)
	}
}

// TestLockStateUnlockedMaps maps StateUnlocked to Matter LockState=2.
func TestLockStateUnlockedMaps(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	v, ok := srv(r.lock).MatterRead(0x0000)
	if !ok || v.(uint8) != 2 {
		t.Fatalf("LockState = (%v, %v), want (2, true)", v, ok)
	}
}

// TestLockStateJammedOverridesLocked is the central regression guard:
// a jammed lock reports NotFullyLocked regardless of the underlying
// state. Matter spec semantics for "physical mechanism cannot complete
// the operation".
func TestLockStateJammedOverridesLocked(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	fireLockEnum(t, r.stateDP, string(StateLocked))
	r.jammedDP.OnEvent(true)
	v, ok := srv(r.lock).MatterRead(0x0000)
	if !ok || v.(uint8) != 0 {
		t.Fatalf("LockState (jammed) = (%v, %v), want (0=NotFullyLocked, true)", v, ok)
	}
}

// TestLockStateUnobserved confirms stale-data surfaces as (nil, true) —
// attribute is supported but value is transiently null (Apple Home tolerates
// null; (nil, false) would signal UnsupportedAttribute and abort the HAP build).
func TestLockStateUnobserved(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	v, ok := srv(r.lock).MatterRead(0x0000)
	if !ok || v != nil {
		t.Fatalf("LockState on unobserved = (%v, %v), want (nil, true)", v, ok)
	}
}

// TestLockTypeIsDeadBolt locks the static LockType (0=DeadBolt).
func TestLockTypeIsDeadBolt(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	v, ok := srv(r.lock).MatterRead(0x0001)
	if !ok || v.(uint8) != 0 {
		t.Fatalf("LockType = (%v, %v), want (0=DeadBolt, true)", v, ok)
	}
}

// TestLockDoorCommand routes through Lock.Lock.
func TestLockDoorCommand(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{})
	if _, err := srv(r.lock).MatterInvoke(requesterCtx(), wire.DoorLockCmdLockDoor, nil); err != nil {
		t.Fatalf("LockDoor err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("LockDoor did not reach the wire")
	}
}

// TestUnlockDoorCommand routes through Lock.Unlock.
func TestUnlockDoorCommand(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{})
	if _, err := srv(r.lock).MatterInvoke(requesterCtx(), wire.DoorLockCmdUnlockDoor, nil); err != nil {
		t.Fatalf("UnlockDoor err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("UnlockDoor did not reach the wire")
	}
}

// TestUnboltDoorRequiresSupportsOpen confirms the latch-release
// command is gated by [LockCapabilities.SupportsOpen]: the lock refuses
// it with ErrNotSupported, which go-fabric's server answers as the IM
// status FAILURE (the error carries no status of its own), as matter.js
// answers a lock operation whose handler throws (DoorLockServer.ts:1033-1038).
func TestUnboltDoorRequiresSupportsOpen(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: false})
	_, err := srv(r.lock).MatterInvoke(requesterCtx(), wire.DoorLockCmdUnboltDoor, nil)
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusFailure {
		t.Fatalf("UnboltDoor without SupportsOpen err = %v, want IM status FAILURE", err)
	}
	if len(w.calls) != 0 {
		t.Fatalf("UnboltDoor without SupportsOpen reached the wire: %v", w.calls)
	}
}

// TestUnboltDoorWhenSupported routes through Lock.Open.
func TestUnboltDoorWhenSupported(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
	if _, err := srv(r.lock).MatterInvoke(requesterCtx(), wire.DoorLockCmdUnboltDoor, nil); err != nil {
		t.Fatalf("UnboltDoor err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("UnboltDoor did not reach the wire")
	}
}

// TestUnknownLockCommand surfaces an error for an unknown command ID.
func TestUnknownLockCommand(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	_, err := srv(r.lock).MatterInvoke(requesterCtx(), 0x99, nil)
	if err == nil {
		t.Fatal("expected error for unknown command ID 0x99")
	}
}

// TestDoorLockFeatureMapAdvertisesUnbolt confirms the Unbolt bit (0x10)
// is set in the FeatureMap after the Matter 1.4 promotion.
func TestDoorLockFeatureMapAdvertisesUnbolt(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	v, ok := srv(r.lock).MatterRead(0xFFFC)
	if !ok {
		t.Fatalf("FeatureMap read returned (nil, false)")
	}
	got := v.(uint32)
	// UBOLT is feature bit 12 per matter.js door-lock-cluster.element.ts
	// (constraint "12"); bit 4 is WeekDayAccessSchedules.
	const unboltBit uint32 = 1 << 12
	if got&unboltBit == 0 {
		t.Fatalf("FeatureMap = 0x%08X, Unbolt bit (0x%08X) not set", got, unboltBit)
	}
}

// TestDoorLockClusterRevision locks the ClusterRevision.
func TestDoorLockClusterRevision(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	v, ok := srv(r.lock).MatterRead(0xFFFD)
	if !ok || v.(uint16) == 0 {
		t.Fatalf("ClusterRevision = (%v, %v), want (non-zero, true)", v, ok)
	}
}

// TestLockReportable locks the LockState as the only reportable
// attribute on the projection.
func TestLockReportable(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	rep := srv(r.lock).MatterReportable()
	if len(rep) != 1 || rep[0] != 0x0000 {
		t.Fatalf("Reportable = %v, want [0x0000]", rep)
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---

// TestLockOnMatterValueChangedFiresOnConfirmedStateChange verifies that a
// CCU-confirmed LOCK_STATE change (e.g. operated at the door, not through
// Apple) reaches a registered OnMatterValueChanged callback.
func TestLockOnMatterValueChangedFiresOnConfirmedStateChange(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	var count int
	_ = r.lock.OnMatterValueChanged(func() { count++ })
	fireLockEnum(t, r.stateDP, string(StateLocked))
	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestLockOnMatterValueChangedUnsubscribeStopsCallback verifies that the
// returned closure detaches every wired DP so a further confirmed change
// does not fire the callback again.
func TestLockOnMatterValueChangedUnsubscribeStopsCallback(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	var count int
	unsub := r.lock.OnMatterValueChanged(func() { count++ })
	fireLockEnum(t, r.stateDP, string(StateLocked))
	unsub()
	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestLockOnMatterValueChangedNilSafe verifies nil-receiver and
// nil-callback safety.
func TestLockOnMatterValueChangedNilSafe(t *testing.T) {
	var l *Lock
	unsub := l.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Lock: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic

	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	unsub2 := r.lock.OnMatterValueChanged(nil)
	if unsub2 == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	fireLockEnum(t, r.stateDP, string(StateLocked)) // must not panic with no subscriber
}
