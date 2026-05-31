// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestParityMatterJS_LockClusterRevisions pins every cluster revision
// implemented by the lock package against matter.js HEAD so a stale
// revision does not pass review unnoticed.

type matterClusterEntry struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchema struct {
	Clusters []matterClusterEntry `json:"clusters"`
}

func loadMatterSchemaT(t *testing.T) *matterSchema {
	t.Helper()

	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
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

// TestParityMatterJS_DoorLockMandatoryAttributes asserts that the
// DoorLockServer returned by Lock.MatterClusterServers() covers
// OperatingMode (0x0025) and SupportedOperatingModes (0x0026),
// both spec-mandatory.
func TestParityMatterJS_DoorLockMandatoryAttributes(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	servers := r.lock.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatal("MatterClusterServers returned empty slice")
	}
	lister, ok := servers[0].(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("DoorLockServer does not implement MatterClusterAttributeLister")
	}
	attrs := make(map[uint32]bool)
	for _, id := range lister.MatterAttributes() {
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
			t.Errorf("DoorLock MatterAttributes() missing mandatory %s", m.name)
		}
		_, ok := servers[0].MatterRead(m.id)
		if !ok {
			t.Errorf("DoorLock MatterRead(0x%04X) returned ok=false for mandatory %s", m.id, m.name)
		}
	}
}

func TestParityMatterJS_LockClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	servers := r.lock.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatal("MatterClusterServers returned empty slice")
	}

	clusterID := servers[0].MatterClusterID()
	revAny, revOK := servers[0].MatterRead(0xFFFD)
	if !revOK {
		t.Fatal("DoorLockServer.MatterRead(ClusterRevision) returned ok=false")
	}
	rev, _ := revAny.(uint16)

	js, ok := clusterByID(schema, clusterID)
	if !ok {
		t.Errorf("matter.js schema has no cluster 0x%04X (DoorLock)", clusterID)
		return
	}

	t.Run(js.Name, func(t *testing.T) {
		t.Parallel()

		if rev != js.Revision {
			t.Errorf("code revision %d != matter.js %d for %s (0x%04X)",
				rev, js.Revision, js.Name, clusterID)
		}
	})
}
