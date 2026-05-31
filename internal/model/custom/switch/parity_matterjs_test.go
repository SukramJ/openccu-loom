// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"encoding/json"
	"testing"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestParityMatterJS_SwitchClusterRevisions pins every cluster revision
// implemented by the switch package against matter.js HEAD so a stale
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

// TestParityMatterJS_SwitchOnOffNoSpuriousOptions asserts that
// Switch.MatterAttributes() does NOT advertise OnOff.Options (0x000F).
// Options is a historical Zigbee-Cluster-Library attribute that Matter
// dropped: matter.js HEAD on-off.element.ts and chip HEAD's
// zzz_generated/.../OnOff/AttributeIds.h both omit it from the OnOff
// cluster. Re-introducing it would be a SPURIOUS-ATTR drift that
// Apple's strict schema check on iOS 18.4+ can flag.
func TestParityMatterJS_SwitchOnOffNoSpuriousOptions(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	s := &Switch{}
	for _, id := range s.MatterAttributes() {
		if id == spuriousOptionsID {
			t.Errorf("Switch MatterAttributes() advertises spurious OnOff.Options (0x000F)")
		}
	}
	if _, ok := s.MatterRead(spuriousOptionsID); ok {
		t.Errorf("Switch MatterRead(0x000F) must return ok=false — attribute not in spec")
	}
}

func TestParityMatterJS_SwitchClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterOnOff, "OnOff", matterOnOffClusterRevision},
	}

	for _, c := range cases {
		js, ok := clusterByID(schema, c.id)
		if !ok {
			t.Errorf("matter.js schema has no cluster 0x%04X (%s)", c.id, c.name)

			continue
		}

		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()

			if c.codeRevision != js.Revision {
				t.Errorf("code revision %d != matter.js %d for %s (0x%04X)",
					c.codeRevision, js.Revision, js.Name, js.ID)
			}
		})
	}
}
