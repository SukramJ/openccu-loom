// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeLockSource satisfies StateSource for tests.
type fakeLockSource struct{}

func (f *fakeLockSource) IsJammed() bool                    { return false }
func (f *fakeLockSource) IsLocked() (locked, observed bool) { return true, true }
func (f *fakeLockSource) LockInvoke(_ context.Context, _ uint32, _ hmenum.CommandPriority) error {
	return nil
}

// TestParity_DoorLock_LockType_SupportedOperatingModes verifies that
// LockType is DeadBolt (0) and SupportedOperatingModes is 0xFFF6.
// 0xFFF6 = alwaysSet (bits 0-10, 0x07FF) | vacation | privacy | passage
// with Normal (bit 0) and NoRemoteLockUnlock (bit 3) clear = supported.
// Mirrors matter.js DoorLockServer.ts:69
// ({vacation:true,privacy:true,passage:true,alwaysSet:2047}).
func TestParity_DoorLock_LockType_SupportedOperatingModes(t *testing.T) {
	t.Parallel()
	srv := lock.NewDoorLockServer(lock.DoorLockConfig{Source: &fakeLockSource{}})

	lt, ok := srv.MatterRead(wire.DoorLockAttrLockType)
	if !ok {
		t.Fatal("LockType: ok=false")
	}
	if got := lt.(uint8); got != 0 {
		t.Errorf("LockType = %d, want 0 (DeadBolt)", got)
	}

	som, ok := srv.MatterRead(wire.DoorLockAttrSupportedOperatingModes)
	if !ok {
		t.Fatal("SupportedOperatingModes: ok=false")
	}
	// 0xFFF6: matter.js DoorLockServer.ts:69 initializes
	// {vacation:true,privacy:true,passage:true,alwaysSet:2047}.
	const wantSOM uint16 = 0xFFF6
	if got := som.(uint16); got != wantSOM {
		t.Errorf("SupportedOperatingModes = 0x%04X, want 0x%04X", got, wantSOM)
	}
}
