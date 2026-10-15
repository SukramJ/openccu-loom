// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

// Tests for the two operator-facing views of the Matter session table:
// the occupancy the REST diagnostics surface reports, and the debug
// record the CASE resume path leaves behind.

import (
	"testing"

	"github.com/SukramJ/go-fabric/secure/operational"
	"github.com/SukramJ/go-fabric/secure/sigma"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// TestMatterSessionLister_ReportsOccupancyFromTheLiveManager pins that
// the occupancy the diagnostics endpoint serves is read from the same
// manager that hands out session ids — not recomputed from the session
// list, which cannot see a staked id at all.
//
// A handshake that announced a session id in Sigma2 and never completed
// holds its slot for twenty minutes. That is the shape that drains the
// 16-bit space, and it is invisible in every other view.
func TestMatterSessionLister_ReportsOccupancyFromTheLiveManager(t *testing.T) {
	mgr := buildTestOperationalManager(t)
	lister := matterSessionLister{op: mgr}

	staked, err := mgr.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID: %v", err)
	}
	var keys sigma.SessionKeys
	for i := range keys.I2RKey {
		keys.I2RKey[i] = byte(i + 1)
		keys.R2IKey[i] = byte(i + 17)
	}
	if _, err := mgr.OpenFromSigmaWithID(staked, 1, 0xB0B, 0xA11CE, 0x1111, nil, keys); err != nil {
		t.Fatalf("OpenFromSigmaWithID: %v", err)
	}
	if _, err := mgr.AllocateID(); err != nil {
		t.Fatalf("AllocateID (abandoned handshake): %v", err)
	}

	occ := lister.MatterSessionOccupancy()
	if occ.Live != 1 {
		t.Errorf("live = %d, want 1", occ.Live)
	}
	if occ.Reserved != 1 {
		t.Errorf("reserved = %d, want 1 — the abandoned handshake still owns its id", occ.Reserved)
	}
	if occ.Capacity != operational.SessionIDSpace {
		t.Errorf("capacity = %d, want %d", occ.Capacity, operational.SessionIDSpace)
	}
	if occ.Free != operational.SessionIDSpace-2 {
		t.Errorf("free = %d, want %d", occ.Free, operational.SessionIDSpace-2)
	}
	if len(lister.MatterSessions()) != 1 {
		t.Errorf("the session list must still report only the established session")
	}
}

// TestMatterSessionLister_WithoutManagerReportsNothing pins the
// bridge-disabled path: the adapter is built before the bridge exists in
// some configurations, and a zero occupancy must not read as a full
// table.
func TestMatterSessionLister_WithoutManagerReportsNothing(t *testing.T) {
	t.Parallel()
	occ := matterSessionLister{}.MatterSessionOccupancy()
	if occ != (handlers.MatterSessionOccupancy{}) {
		t.Errorf("occupancy without a manager = %+v, want the zero value", occ)
	}
}
