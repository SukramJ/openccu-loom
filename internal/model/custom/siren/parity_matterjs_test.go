// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"encoding/json"
	"testing"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestParityMatterJS_SirenClusterRevisions pins every cluster revision
// implemented by the siren package against matter.js HEAD so a stale
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

// TestParityMatterJS_SmokeCOMandatoryAttributes asserts that
// smokeCOServer.MatterAttributes() covers every mandatory attribute per
// matter.js HEAD (packages/model/src/standard/elements/
// smoke-co-alarm.element.ts), including HardwareFaultAlert (0x0006) and
// EndOfServiceAlert (0x0007).
func TestParityMatterJS_SmokeCOMandatoryAttributes(t *testing.T) {
	t.Parallel()
	srv := smokeCOServer{s: &SmokeSiren{}}
	attrs := make(map[uint32]bool, len(srv.MatterAttributes()))
	for _, id := range srv.MatterAttributes() {
		attrs[id] = true
	}
	mandatory := []struct {
		id   uint32
		name string
	}{
		{matterAttrSmokeExpressedState, "ExpressedState (0x0000)"},
		{matterAttrSmokeState, "SmokeState (0x0001)"},
		{matterAttrCOState, "COState (0x0002)"},
		{matterAttrBatteryAlert, "BatteryAlert (0x0003)"},
		{matterAttrHardwareFaultAlert, "HardwareFaultAlert (0x0006)"},
		{matterAttrEndOfServiceAlert, "EndOfServiceAlert (0x0007)"},
		{matterAttrTestInProgress, "TestInProgress (0x0008)"},
	}
	for _, m := range mandatory {
		if !attrs[m.id] {
			t.Errorf("SmokeCOAlarm MatterAttributes() missing mandatory %s", m.name)
		}
		_, ok := srv.MatterRead(m.id)
		if !ok {
			t.Errorf("SmokeCOAlarm MatterRead(0x%04X) returned ok=false for mandatory %s", m.id, m.name)
		}
	}
}

// TestParityMatterJS_SirenOnOffNoSpuriousOptions locks the absence of
// OnOff.Options (0x000F) on the sirenOnOffServer projection. Options is
// a Zigbee-Cluster-Library holdover the Matter spec dropped from OnOff;
// matter.js HEAD on-off.element.ts and chip HEAD both omit it.
func TestParityMatterJS_SirenOnOffNoSpuriousOptions(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	srv := sirenOnOffServer{s: &Siren{}}
	for _, id := range srv.MatterAttributes() {
		if id == spuriousOptionsID {
			t.Errorf("sirenOnOff MatterAttributes() advertises spurious OnOff.Options (0x000F)")
		}
	}
	if _, ok := srv.MatterRead(spuriousOptionsID); ok {
		t.Errorf("sirenOnOff MatterRead(0x000F) must return ok=false — Options is not in OnOff schema")
	}
}

func TestParityMatterJS_SirenClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterOnOff, "OnOff", matterOnOffClusterRevision},
		{matterClusterBooleanState, "BooleanState", matterBooleanStateClusterRevision},
		{matterClusterSmokeCOAlarm, "SmokeCoAlarm", matterSmokeCOAlarmClusterRevision},
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
