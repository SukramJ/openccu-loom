// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"
	"github.com/SukramJ/go-fabric/cluster/onoff"

	matterparity "github.com/SukramJ/go-fabric/parity"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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
// OnOff.Options (0x000F) on the light's OnOff server. Options is a
// Zigbee-Cluster-Library holdover the Matter spec dropped from the OnOff
// cluster — matter.js HEAD on-off.element.ts and chip HEAD's
// zzz_generated/.../OnOff/AttributeIds.h both omit it. The LevelControl
// cluster (0x0008) retains its own Options attribute (also 0x000F) per
// matter.js level-control.element.ts; that one stays.
func TestParityMatterJS_LightOnOffNoSpuriousOptions(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	for _, id := range srv.MatterAttributes() {
		if id == spuriousOptionsID {
			t.Errorf("light OnOff MatterAttributes() advertises spurious OnOff.Options (0x000F)")
		}
	}
	if _, ok := srv.MatterRead(spuriousOptionsID); ok {
		t.Errorf("light OnOff MatterRead(0x000F) must return ok=false — OnOff has no Options attribute")
	}
}

// TestParityMatterJS_LevelControlMatchesMatterJS pins the LevelControl
// server a dimmable light serves against the matter.js snapshot: the
// FeatureMap is exactly OO|LT with both bits read from the element's own
// feature entries, and the attribute and accepted-command lists are the
// ones that FeatureMap and the served revision make mandatory — every
// attribute whose conformance is M, LT, or "Rev >= v7, O" at revision >= 7,
// and every request command whose conformance is M.
func TestParityMatterJS_LevelControlMatchesMatterJS(t *testing.T) {
	t.Parallel()
	var schema struct {
		Clusters []hmLgtOnOffCluster `json:"clusters"`
	}
	if err := json.Unmarshal(matterparity.SchemaJSON(), &schema); err != nil {
		t.Fatalf("unmarshal matter-schema-snapshot.json: %v", err)
	}
	var el hmLgtOnOffCluster
	found := false
	for _, c := range schema.Clusters {
		if c.ID == levelcontrol.ClusterID {
			el, found = c, true
		}
	}
	if !found {
		t.Fatal("matter.js snapshot carries no LevelControl cluster (0x0008)")
	}

	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)

	bits := map[string]uint32{}
	for _, f := range el.Features {
		bits[f.Name] = f.Bit
	}
	oo, haveOO := bits["OO"]
	lt, haveLT := bits["LT"]
	if !haveOO || !haveLT {
		t.Fatalf("matter.js LevelControl element lacks OO or LT: %v", bits)
	}
	fm, ok := srv.MatterRead(matterAttrFeatureMap)
	if want := uint32(1)<<oo | uint32(1)<<lt; !ok || fm != want {
		t.Fatalf("LevelControl FeatureMap = (%v, %v), want 0x%02X (matter.js OO bit %d | LT bit %d)", fm, ok, want, oo, lt)
	}

	rev, ok := srv.MatterRead(matterAttrClusterRevision)
	if !ok || rev != el.Revision {
		t.Fatalf("LevelControl ClusterRevision = (%v, %v), matter.js says %d", rev, ok, el.Revision)
	}

	wantAttrs := map[uint32]string{}
	for _, a := range el.Attributes {
		switch a.Conformance {
		case "M", "LT":
			wantAttrs[a.ID] = a.Name
		case "Rev >= v7, O":
			if el.Revision >= 7 {
				wantAttrs[a.ID] = a.Name
			}
		}
	}
	hmLgtAssertIDSet(t, "light LevelControl MatterAttributes", srv.MatterAttributes(), wantAttrs)

	wantCmds := map[uint32]string{}
	for _, c := range el.Commands {
		if c.Direction == "request" && c.Conformance == "M" {
			wantCmds[c.ID] = c.Name
		}
	}
	hmLgtAssertIDSet(t, "light LevelControl MatterAcceptedCommands", srv.MatterAcceptedCommands(), wantCmds)

	if got := srv.MatterReportable(); len(got) != 1 || got[0] != levelcontrol.AttrCurrentLevel {
		t.Errorf("light LevelControl MatterReportable = %#x, want [CurrentLevel]", got)
	}
}

// TestParityMatterJS_StackOwnedClustersNotSupplied asserts that neither
// a dimmable nor a non-dimmable light supplies Groups (0x0004) or
// ScenesManagement (0x0062). Both are mandatory on the light device types
// (matter.js packages/node/src/devices/on-off-light.ts) but are stack
// state: the endpoint assembler mounts them from the node's group state
// (go-fabric endpoint/materialize.go mountGroups / mountScenes).
func TestParityMatterJS_StackOwnedClustersNotSupplied(t *testing.T) {
	t.Parallel()
	for _, dimmable := range []bool{true, false} {
		t.Run(map[bool]string{true: "dimmable", false: "non-dimmable"}[dimmable], func(t *testing.T) {
			t.Parallel()
			l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: dimmable})
			for _, s := range l.MatterClusterServers() {
				if id := s.MatterClusterID(); id == 0x0004 || id == 0x0062 {
					t.Errorf("light (dimmable=%v) supplies stack-owned cluster 0x%04X", dimmable, id)
				}
			}
		})
	}
}

// TestParityMatterJS_OnOffAcceptedCommands verifies that the light's OnOff server
// enumerates Off (0x00), On (0x01), Toggle (0x02) in MatterAcceptedCommands.
func TestParityMatterJS_OnOffAcceptedCommands(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	lister := srv
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
		{onoff.ClusterID, "OnOff", onOffClusterRevision(t)},
		{levelcontrol.ClusterID, "LevelControl", levelClusterRevision(t)},
		// D-50: ColorControl is mounted on dimmable+colored lights via
		// `matter_color.go::{ctColorServer, hsColorServer, rgbwColorServer}`.
		// matter.js HEAD `color-control.element.ts` carries revision 9.
		{matterClusterColorControl, "ColorControl", matterColorControlClusterRevision},
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

// onOffClusterRevision reads ClusterRevision from the OnOff server a
// light actually serves, so the revision pin covers the served value.
func onOffClusterRevision(t *testing.T) uint16 {
	t.Helper()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	v, ok := onOffServer(t, l).MatterRead(matterAttrClusterRevision)
	rev, isU16 := v.(uint16)
	if !ok || !isU16 {
		t.Fatalf("light OnOff ClusterRevision = (%v, %v), want a uint16", v, ok)
	}
	return rev
}

// levelClusterRevision reads ClusterRevision from the LevelControl server
// a dimmable light actually serves.
func levelClusterRevision(t *testing.T) uint16 {
	t.Helper()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	v, ok := levelServer(t, l).MatterRead(matterAttrClusterRevision)
	rev, isU16 := v.(uint16)
	if !ok || !isU16 {
		t.Fatalf("light LevelControl ClusterRevision = (%v, %v), want a uint16", v, ok)
	}
	return rev
}
