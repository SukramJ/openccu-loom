// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// srv returns the DoorLockServer mounted by the lock's MatterClusterServers.
func srv(l *Lock) interface {
	MatterClusterID() uint32
	MatterRead(uint32) (any, bool)
	MatterInvoke(context.Context, uint32, any, hmenum.CommandPriority) (any, error)
	MatterReportable() []uint32
} {
	servers := l.MatterClusterServers()
	if len(servers) == 0 {
		panic("MatterClusterServers returned empty slice")
	}
	type fullServer interface {
		MatterClusterID() uint32
		MatterRead(uint32) (any, bool)
		MatterInvoke(context.Context, uint32, any, hmenum.CommandPriority) (any, error)
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

// TestMatterClusterServersExposesDoorLock confirms the single-cluster
// projection exposes DoorLock (0x0101).
func TestMatterClusterServersExposesDoorLock(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	servers := r.lock.MatterClusterServers()
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
	if _, err := srv(r.lock).MatterInvoke(context.Background(), wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
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
	if _, err := srv(r.lock).MatterInvoke(context.Background(), wire.DoorLockCmdUnlockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("UnlockDoor err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("UnlockDoor did not reach the wire")
	}
}

// TestUnboltDoorRequiresSupportsOpen confirms the latch-release
// command is gated by [LockCapabilities.SupportsOpen].
func TestUnboltDoorRequiresSupportsOpen(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: false})
	_, err := srv(r.lock).MatterInvoke(context.Background(), wire.DoorLockCmdUnboltDoor, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("UnboltDoor without SupportsOpen err = %v, want ErrNotSupported", err)
	}
}

// TestUnboltDoorWhenSupported routes through Lock.Open.
func TestUnboltDoorWhenSupported(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
	if _, err := srv(r.lock).MatterInvoke(context.Background(), wire.DoorLockCmdUnboltDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("UnboltDoor err: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("UnboltDoor did not reach the wire")
	}
}

// TestUnknownLockCommand surfaces an error for an unknown command ID.
func TestUnknownLockCommand(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	_, err := srv(r.lock).MatterInvoke(context.Background(), 0x99, nil, hmenum.CommandPriorityHigh)
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
