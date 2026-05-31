// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// combinedSrv holds a single DoorLockServer instance for both invoke and
// DataVersion checks. MatterClusterServers() constructs a new server on each
// call, so tests must pin one instance to observe the bump.
type combinedSrv struct {
	interfaces.MatterClusterServer
	interfaces.MatterClusterDataVersion
}

func getCombinedSrv(l *Lock) combinedSrv {
	servers := l.MatterClusterServers()
	s := servers[0]
	dv := s.(interfaces.MatterClusterDataVersion)
	return combinedSrv{s, dv}
}

// TestParityMatterJS_LockDataVersionBumpsOnInvokeLock verifies that a
// successful LockDoor invoke increments the cluster's DataVersion.
func TestParityMatterJS_LockDataVersionBumpsOnInvokeLock(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	c := getCombinedSrv(r.lock)
	before := c.MatterDataVersion()

	if _, err := c.MatterInvoke(context.Background(), wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(LockDoor): %v", err)
	}
	if after := c.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after LockDoor: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LockDataVersionBumpsOnInvokeUnlock verifies that a
// successful UnlockDoor invoke increments DataVersion.
func TestParityMatterJS_LockDataVersionBumpsOnInvokeUnlock(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	c := getCombinedSrv(r.lock)
	before := c.MatterDataVersion()

	if _, err := c.MatterInvoke(context.Background(), wire.DoorLockCmdUnlockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(UnlockDoor): %v", err)
	}
	if after := c.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after UnlockDoor: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LockDataVersionBumpsOnInvokeUnbolt verifies that a
// successful UnboltDoor invoke increments DataVersion.
func TestParityMatterJS_LockDataVersionBumpsOnInvokeUnbolt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	c := getCombinedSrv(r.lock)
	before := c.MatterDataVersion()

	if _, err := c.MatterInvoke(context.Background(), wire.DoorLockCmdUnboltDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(UnboltDoor): %v", err)
	}
	if after := c.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after UnboltDoor: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LockDataVersionMonotonicallyRises verifies that
// consecutive successful invokes each increment the counter strictly.
func TestParityMatterJS_LockDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	c := getCombinedSrv(r.lock)

	cmds := []uint32{wire.DoorLockCmdLockDoor, wire.DoorLockCmdUnlockDoor, wire.DoorLockCmdLockDoor}
	for i, cmd := range cmds {
		prev := c.MatterDataVersion()
		if _, err := c.MatterInvoke(context.Background(), cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("cmd %d: %v", i, err)
		}
		if next := c.MatterDataVersion(); next <= prev {
			t.Fatalf("cmd %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_LockDataVersionStableOnRead verifies that
// MatterRead does not alter DataVersion.
func TestParityMatterJS_LockDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	c := getCombinedSrv(r.lock)
	before := c.MatterDataVersion()

	c.MatterRead(wire.DoorLockAttrLockState)
	c.MatterRead(wire.DoorLockAttrLockType)
	c.MatterRead(0xFFFD)

	if after := c.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LockDataVersionStableOnFailedInvoke verifies that
// an invoke with an unknown command ID does not increment DataVersion.
func TestParityMatterJS_LockDataVersionStableOnFailedInvoke(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	c := getCombinedSrv(r.lock)
	before := c.MatterDataVersion()

	_, _ = c.MatterInvoke(context.Background(), 0x99, nil, hmenum.CommandPriorityHigh)

	if after := c.MatterDataVersion(); after != before {
		t.Fatalf("failed invoke bumped DataVersion: before=%d after=%d", before, after)
	}
}
