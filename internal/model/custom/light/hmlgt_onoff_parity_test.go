// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/onoff"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// hmLgtOnOffCluster is the matter.js OnOff element as the snapshot
// carries it. Only the fields this guard reads are decoded — and every
// one of them is read from the snapshot, never derived from position:
// the feature bit comes from the element's own `bit`, not from the order
// of the features array.
type hmLgtOnOffCluster struct {
	ID         uint32 `json:"id"`
	Name       string `json:"name"`
	Revision   uint16 `json:"revision"`
	Attributes []struct {
		ID          uint32 `json:"id"`
		Name        string `json:"name"`
		Conformance string `json:"conformance"`
	} `json:"attributes"`
	Commands []struct {
		ID          uint32 `json:"id"`
		Name        string `json:"name"`
		Direction   string `json:"direction"`
		Conformance string `json:"conformance"`
	} `json:"commands"`
	Features []struct {
		Name string `json:"name"`
		Bit  uint32 `json:"bit"`
	} `json:"features"`
}

// hmLgtLoadOnOffElement returns the OnOff cluster (0x0006) from the
// embedded matter.js schema snapshot.
func hmLgtLoadOnOffElement(t *testing.T) hmLgtOnOffCluster {
	t.Helper()
	var s struct {
		Clusters []hmLgtOnOffCluster `json:"clusters"`
	}
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal matter-schema-snapshot.json: %v", err)
	}
	for _, c := range s.Clusters {
		if c.ID == 0x0006 {
			return c
		}
	}
	t.Fatal("matter.js snapshot carries no OnOff cluster (0x0006)")

	return hmLgtOnOffCluster{}
}

// The light endpoints advertise the OnOff cluster with the LT feature
// bit set, which fixes what AcceptedCommandList and the attribute list
// must contain: every OnOff command whose conformance is M, LT or
// !OFFONLY, and every attribute whose conformance is M or LT. Pinning
// the ids against matter.js — rather than against a second hand-written
// list — is what makes a transcription slip in the constant block
// visible instead of shipping as a silent pair-abort.
func TestHmLgtLightOnOffMatchesMatterJS(t *testing.T) {
	t.Parallel()

	el := hmLgtLoadOnOffElement(t)
	s := lightOnOffServer{l: &Light{}}

	// The FeatureMap the endpoint advertises decides which conformance
	// clauses apply, so read it first.
	fm, ok := s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Fatal("the light OnOff server does not answer FeatureMap (0xFFFC)")
	}
	featureMap, isU32 := fm.(uint32)
	if !isU32 {
		t.Fatalf("FeatureMap read %T, want uint32", fm)
	}
	var ltBit uint32
	var haveLT bool
	for _, f := range el.Features {
		if f.Name == "LT" {
			ltBit, haveLT = f.Bit, true
		}
	}
	if !haveLT {
		t.Fatal("matter.js OnOff element declares no LT feature")
	}
	if featureMap != 1<<ltBit {
		t.Fatalf("light OnOff FeatureMap = 0x%02X, want 0x%02X (matter.js LT bit %d, no other feature advertised)",
			featureMap, uint32(1)<<ltBit, ltBit)
	}

	wantCmds := map[uint32]string{}
	for _, c := range el.Commands {
		if c.Direction != "request" {
			continue
		}
		switch c.Conformance {
		case "M", "LT", "!OFFONLY":
			wantCmds[c.ID] = c.Name
		}
	}
	hmLgtAssertIDSet(t, "lightOnOffServer.MatterAcceptedCommands", s.MatterAcceptedCommands(), wantCmds)

	wantAttrs := map[uint32]string{}
	for _, a := range el.Attributes {
		switch a.Conformance {
		case "M", "LT":
			wantAttrs[a.ID] = a.Name
		}
	}
	hmLgtAssertIDSet(t, "lightOnOffServer.MatterAttributes", s.MatterAttributes(), wantAttrs)

	if onoff.Revision() != el.Revision {
		t.Errorf("OnOff cluster revision %d, matter.js says %d", onoff.Revision(), el.Revision)
	}
}

// hmLgtAssertIDSet compares a projected id list against the ids matter.js
// declares, naming both the missing and the spurious entries.
func hmLgtAssertIDSet(t *testing.T, what string, got []uint32, want map[uint32]string) {
	t.Helper()
	seen := make(map[uint32]bool, len(got))
	for _, id := range got {
		seen[id] = true
		if _, expected := want[id]; !expected {
			t.Errorf("%s advertises 0x%04X, which matter.js does not require for this FeatureMap", what, id)
		}
	}
	for id, name := range want {
		if !seen[id] {
			t.Errorf("%s omits 0x%04X (%s), which matter.js makes mandatory for this FeatureMap", what, id, name)
		}
	}
}
