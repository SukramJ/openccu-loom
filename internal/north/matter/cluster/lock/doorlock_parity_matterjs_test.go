// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock_test

import (
	"context"
	"encoding/json"
	"testing"

	doorlockcluster "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSource is a minimal StateSource for tests.
type stubSource struct {
	jammed   bool
	locked   bool
	observed bool
	invoked  []uint32
}

func (s *stubSource) IsJammed() bool                    { return s.jammed }
func (s *stubSource) IsLocked() (locked, observed bool) { return s.locked, s.observed }
func (s *stubSource) LockInvoke(_ context.Context, cmdID uint32, _ hmenum.CommandPriority) error {
	s.invoked = append(s.invoked, cmdID)
	return nil
}

type matterClusterEntry struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchema struct {
	Clusters []matterClusterEntry `json:"clusters"`
}

func loadSchema(t *testing.T) *matterSchema {
	t.Helper()
	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal matter-schema-snapshot.json: %v", err)
	}
	return &s
}

func clusterByID(s *matterSchema, id uint32) (matterClusterEntry, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}
	return matterClusterEntry{}, false
}

// TestParityMatterJS_DoorLockClusterRevision pins DoorLockServer's cluster
// revision against matter.js HEAD so a stale revision is caught before review.
func TestParityMatterJS_DoorLockClusterRevision(t *testing.T) {
	t.Parallel()

	schema := loadSchema(t)
	js, ok := clusterByID(schema, wire.DoorLockClusterID)
	if !ok {
		t.Fatalf("matter.js schema has no cluster 0x%04X (DoorLock)", wire.DoorLockClusterID)
	}

	srv := doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{
		Source: &stubSource{observed: true},
	})

	_, revOK := srv.MatterRead(0xFFFD) // ClusterRevision
	if !revOK {
		t.Fatal("DoorLockServer.MatterRead(ClusterRevision) returned ok=false")
	}

	revAny, _ := srv.MatterRead(0xFFFD)
	rev, _ := revAny.(uint16)
	if rev != js.Revision {
		t.Errorf("DoorLockServer cluster revision %d != matter.js %d for DoorLock (0x%04X)",
			rev, js.Revision, wire.DoorLockClusterID)
	}
}

// TestParityMatterJS_DoorLockMandatoryAttributes verifies that
// DoorLockServer.MatterAttributes() covers all five mandatory attributes and
// that MatterRead returns a valid value for each.
func TestParityMatterJS_DoorLockMandatoryAttributes(t *testing.T) {
	t.Parallel()

	srv := doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{
		Source: &stubSource{locked: true, observed: true},
	})

	attrs := make(map[uint32]bool)
	for _, id := range srv.MatterAttributes() {
		attrs[id] = true
	}

	mandatory := []struct {
		id   uint32
		name string
	}{
		{wire.DoorLockAttrLockState, "LockState (0x0000)"},
		{wire.DoorLockAttrLockType, "LockType (0x0001)"},
		{wire.DoorLockAttrActuatorEnabled, "ActuatorEnabled (0x0002)"},
		{wire.DoorLockAttrOperatingMode, "OperatingMode (0x0025)"},
		{wire.DoorLockAttrSupportedOperatingModes, "SupportedOperatingModes (0x0026)"},
	}

	for _, m := range mandatory {
		if !attrs[m.id] {
			t.Errorf("DoorLockServer.MatterAttributes() missing %s", m.name)
		}
		_, ok := srv.MatterRead(m.id)
		if !ok {
			t.Errorf("DoorLockServer.MatterRead(0x%04X) returned ok=false for %s", m.id, m.name)
		}
	}
}

// TestParityMatterJS_DoorLockLockStateMapping verifies the three lock state
// transitions: jammed → NotFullyLocked (0), locked → Locked (1),
// unlocked → Unlocked (2), unobserved → nil.
func TestParityMatterJS_DoorLockLockStateMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		src     stubSource
		wantNil bool
		wantVal uint8
	}{
		{
			name:    "jammed → NotFullyLocked",
			src:     stubSource{jammed: true, locked: true, observed: true},
			wantVal: 0,
		},
		{
			name:    "locked → Locked",
			src:     stubSource{locked: true, observed: true},
			wantVal: 1,
		},
		{
			name:    "unlocked → Unlocked",
			src:     stubSource{locked: false, observed: true},
			wantVal: 2,
		},
		{
			name:    "unobserved → nil",
			src:     stubSource{observed: false},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := tc.src
			srv := doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{Source: &src})
			val, ok := srv.MatterRead(wire.DoorLockAttrLockState)
			if !ok {
				t.Fatal("MatterRead returned ok=false")
			}
			if tc.wantNil {
				if val != nil {
					t.Errorf("want nil, got %v", val)
				}
				return
			}
			got, _ := val.(uint8)
			if got != tc.wantVal {
				t.Errorf("LockState = %d, want %d", got, tc.wantVal)
			}
		})
	}
}

// TestParityMatterJS_DoorLockCommandDispatch verifies that MatterInvoke
// routes LockDoor / UnlockDoor / UnboltDoor to the source and bumps
// DataVersion.
func TestParityMatterJS_DoorLockCommandDispatch(t *testing.T) {
	t.Parallel()

	cmds := []uint32{
		wire.DoorLockCmdLockDoor,
		wire.DoorLockCmdUnlockDoor,
		wire.DoorLockCmdUnboltDoor,
	}

	for _, cmdID := range cmds {
		t.Run(t.Name(), func(t *testing.T) {
			t.Parallel()
			src := &stubSource{observed: true}
			srv := doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{Source: src})
			dvBefore := srv.MatterDataVersion()

			_, err := srv.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("MatterInvoke(0x%02X) error: %v", cmdID, err)
			}
			if len(src.invoked) == 0 || src.invoked[0] != cmdID {
				t.Errorf("LockInvoke not called with cmdID 0x%02X; got %v", cmdID, src.invoked)
			}
			if srv.MatterDataVersion() == dvBefore {
				t.Error("DataVersion not bumped after successful MatterInvoke")
			}
		})
	}
}

// TestParityMatterJS_DoorLockAcceptedCommands verifies that
// MatterAcceptedCommands advertises LockDoor / UnlockDoor / UnboltDoor.
func TestParityMatterJS_DoorLockAcceptedCommands(t *testing.T) {
	t.Parallel()

	srv := doorlockcluster.NewDoorLockServer(doorlockcluster.DoorLockConfig{
		Source: &stubSource{},
	})
	accepted := make(map[uint32]bool)
	for _, id := range srv.MatterAcceptedCommands() {
		accepted[id] = true
	}

	required := []struct {
		id   uint32
		name string
	}{
		{wire.DoorLockCmdLockDoor, "LockDoor (0x00)"},
		{wire.DoorLockCmdUnlockDoor, "UnlockDoor (0x01)"},
		{wire.DoorLockCmdUnboltDoor, "UnboltDoor (0x27)"},
	}
	for _, r := range required {
		if !accepted[r.id] {
			t.Errorf("MatterAcceptedCommands missing %s", r.name)
		}
	}
}
