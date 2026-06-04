// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestParityMatterJS_LightClusterRevisions pins every cluster revision
// implemented by the light package against matter.js HEAD so a stale
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

// TestParityMatterJS_LightOnOffNoSpuriousOptions locks the absence of
// OnOff.Options (0x000F) on the lightOnOffServer projection. Options is a
// Zigbee-Cluster-Library holdover the Matter spec dropped from the OnOff
// cluster — matter.js HEAD on-off.element.ts and chip HEAD's
// zzz_generated/.../OnOff/AttributeIds.h both omit it. The LevelControl
// cluster (0x0008) retains its own Options attribute (also 0x000F) per
// matter.js level-control.element.ts; that one stays.
func TestParityMatterJS_LightOnOffNoSpuriousOptions(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	var srv lightOnOffServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(lightOnOffServer); ok {
			srv = v
			break
		}
	}
	for _, id := range srv.MatterAttributes() {
		if id == spuriousOptionsID {
			t.Errorf("lightOnOffServer MatterAttributes() advertises spurious OnOff.Options (0x000F)")
		}
	}
	if _, ok := srv.MatterRead(spuriousOptionsID); ok {
		t.Errorf("lightOnOffServer MatterRead(0x000F) must return ok=false — OnOff has no Options attribute")
	}
}

// TestParityMatterJS_LevelControlMandatoryAttributes asserts lightLevelServer
// exposes LevelControl.Options (0x000F) and OnLevel (0x0011) per
// matter.js level-control.element.ts. Both are spec-mandatory and must
// be served on every dimmable-light endpoint.
func TestParityMatterJS_LevelControlMandatoryAttributes(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	var srv lightLevelServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(lightLevelServer); ok {
			srv = v
			break
		}
	}
	attrs := make(map[uint32]bool)
	for _, id := range srv.MatterAttributes() {
		attrs[id] = true
	}
	for _, m := range []struct {
		id   uint32
		name string
	}{
		{matterAttrLevelOptions, "Options (0x000F)"},
		{matterAttrLevelOnLevel, "OnLevel (0x0011)"},
	} {
		if !attrs[m.id] {
			t.Errorf("lightLevelServer MatterAttributes() missing mandatory %s", m.name)
		}
		_, ok := srv.MatterRead(m.id)
		if !ok {
			t.Errorf("lightLevelServer MatterRead(0x%04X) returned ok=false for %s", m.id, m.name)
		}
	}
}

// TestParityMatterJS_GroupsMandatoryAttributes asserts that both dimmable
// and non-dimmable lights include the Groups cluster (0x0004) stub with
// NameSupport (0x0000) per matter.js packages/node/src/devices/on-off-light.ts.
func TestParityMatterJS_GroupsMandatoryAttributes(t *testing.T) {
	t.Parallel()
	for _, dimmable := range []bool{true, false} {
		t.Run(map[bool]string{true: "dimmable", false: "non-dimmable"}[dimmable], func(t *testing.T) {
			t.Parallel()
			l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: dimmable})
			var found bool
			for _, s := range l.MatterClusterServers() {
				if s.MatterClusterID() != uint32(0x0004) {
					continue
				}
				found = true
				lister, ok := s.(interfaces.MatterClusterAttributeLister)
				if !ok {
					t.Errorf("groupsServer does not implement MatterClusterAttributeLister")
					continue
				}
				attrs := make(map[uint32]bool)
				for _, id := range lister.MatterAttributes() {
					attrs[id] = true
				}
				if !attrs[uint32(0x0000)] {
					t.Errorf("groupsServer MatterAttributes() missing NameSupport (0x0000)")
				}
				_, ok2 := s.MatterRead(uint32(0x0000))
				if !ok2 {
					t.Errorf("groupsServer MatterRead(NameSupport) returned ok=false")
				}
			}
			if !found {
				t.Errorf("light (dimmable=%v) has no Groups cluster server (0x0004)", dimmable)
			}
		})
	}
}

// TestParityMatterJS_LevelControlAcceptedCommands verifies that
// lightLevelServer enumerates all eight LevelControl commands in
// MatterAcceptedCommands so AcceptedCommandList (0xFFF9) is populated
// correctly during commissioning.
func TestParityMatterJS_LevelControlAcceptedCommands(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	var srv lightLevelServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(lightLevelServer); ok {
			srv = v
			break
		}
	}
	lister, ok := any(srv).(interface{ MatterAcceptedCommands() []uint32 })
	if !ok {
		t.Fatal("lightLevelServer does not implement MatterClusterCommandLister")
	}
	want := map[uint32]string{
		0x00: "MoveToLevel",
		0x01: "Move",
		0x02: "Step",
		0x03: "Stop",
		0x04: "MoveToLevelWithOnOff",
		0x05: "MoveWithOnOff",
		0x06: "StepWithOnOff",
		0x07: "StopWithOnOff",
	}
	got := make(map[uint32]bool)
	for _, id := range lister.MatterAcceptedCommands() {
		got[id] = true
	}
	for id, name := range want {
		if !got[id] {
			t.Errorf("LevelControl MatterAcceptedCommands() missing %s (0x%02X)", name, id)
		}
	}
}

// TestParityMatterJS_OnOffAcceptedCommands verifies that lightOnOffServer
// enumerates Off (0x00), On (0x01), Toggle (0x02) in MatterAcceptedCommands.
func TestParityMatterJS_OnOffAcceptedCommands(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	var srv lightOnOffServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(lightOnOffServer); ok {
			srv = v
			break
		}
	}
	lister, ok := any(srv).(interface{ MatterAcceptedCommands() []uint32 })
	if !ok {
		t.Fatal("lightOnOffServer does not implement MatterClusterCommandLister")
	}
	want := map[uint32]string{
		0x00: "Off",
		0x01: "On",
		0x02: "Toggle",
	}
	got := make(map[uint32]bool)
	for _, id := range lister.MatterAcceptedCommands() {
		got[id] = true
	}
	for id, name := range want {
		if !got[id] {
			t.Errorf("OnOff MatterAcceptedCommands() missing %s (0x%02X)", name, id)
		}
	}
}

func TestParityMatterJS_LightClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterOnOff, "OnOff", matterOnOffClusterRevision},
		{matterClusterLevelControl, "LevelControl", matterLevelControlClusterRevision},
		{uint32(0x0004), "Groups", uint16(4)},
		// D-50: ColorControl is mounted on dimmable+colored lights via
		// `matter_color.go::{ctColorServer, hsColorServer, rgbwColorServer}`.
		// matter.js HEAD `color-control.element.ts` carries revision 9.
		{matterClusterColorControl, "ColorControl", matterColorControlClusterRevision},
		// D-51: ScenesManagement is the shared stub from
		// `cluster/wire/scenes_management.go` (revision 1, matter.js
		// HEAD `scenes-management.element.ts`).
		{uint32(0x0062), "ScenesManagement", uint16(1)},
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
