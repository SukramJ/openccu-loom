// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// The generic Switch declares its own OnOff cluster id, attribute ids, command
// ids, LT FeatureMap bit and cluster revision (switch_matter.go:34-72). Those
// values are a hand-written copy of what the matter.js snapshot carries, and
// the package's existing test compares each read against the very constant it
// is testing — which stays green however far the constants drift from the
// gold standard.
//
// These tests compare the copy against the snapshot instead. They do not
// deduplicate it: the same block is spelled out again in three custom-DP
// packages, and folding all four onto one exported set is a change across
// packages this file cannot make. What it can do is make this copy's drift
// loud.

type w2GenSchemaElement struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Conformance string `json:"conformance"`
}

type w2GenSchemaFeature struct {
	Name string `json:"name"`
	Bit  int    `json:"bit"`
}

type w2GenSchemaCluster struct {
	ID         uint32               `json:"id"`
	Name       string               `json:"name"`
	Revision   uint16               `json:"revision"`
	Features   []w2GenSchemaFeature `json:"features"`
	Attributes []w2GenSchemaElement `json:"attributes"`
	Commands   []w2GenSchemaElement `json:"commands"`
}

type w2GenSchema struct {
	Clusters []w2GenSchemaCluster `json:"clusters"`
}

func w2GenOnOffCluster(t *testing.T) w2GenSchemaCluster {
	t.Helper()
	var s w2GenSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
	}
	for _, c := range s.Clusters {
		if c.ID == matterGenericSwitchClusterOnOff {
			return c
		}
	}
	t.Fatalf("snapshot carries no cluster 0x%04X — matterGenericSwitchClusterOnOff names a "+
		"cluster the gold standard does not define", matterGenericSwitchClusterOnOff)
	return w2GenSchemaCluster{}
}

// TestW2GenParityMatterJSGenericOnOffIdentity pins the cluster identity: the
// id resolves to OnOff in the snapshot, and the revision the endpoint reports
// is the revision the snapshot declares.
func TestW2GenParityMatterJSGenericOnOffIdentity(t *testing.T) {
	t.Parallel()
	c := w2GenOnOffCluster(t)
	if c.Name != "OnOff" {
		t.Errorf("cluster 0x%04X is %q in matter.js, not OnOff", c.ID, c.Name)
	}
	if matterGenericOnOffClusterRevision != c.Revision {
		t.Errorf("matterGenericOnOffClusterRevision = %d, matter.js OnOff revision = %d",
			matterGenericOnOffClusterRevision, c.Revision)
	}
}

// TestW2GenParityMatterJSGenericOnOffLTBit pins the LT FeatureMap bit against
// the bit position the snapshot carries, rather than against a bit inferred
// from the feature's position in the list. Feature bits are sparse in several
// clusters, so position is not bit.
func TestW2GenParityMatterJSGenericOnOffLTBit(t *testing.T) {
	t.Parallel()
	c := w2GenOnOffCluster(t)
	for _, f := range c.Features {
		if f.Name != "LT" {
			continue
		}
		want := uint32(1) << uint(f.Bit)
		if matterGenericFeatureOnOffLT != want {
			t.Errorf("matterGenericFeatureOnOffLT = 0x%02X, matter.js LT is bit %d (0x%02X)",
				matterGenericFeatureOnOffLT, f.Bit, want)
		}
		return
	}
	t.Fatal("matter.js OnOff declares no LT feature — the endpoint advertises a FeatureMap bit " +
		"the gold standard does not define")
}

// TestW2GenParityMatterJSGenericOnOffAttributeIDs pins each declared attribute
// constant to the id the snapshot gives that attribute's name.
func TestW2GenParityMatterJSGenericOnOffAttributeIDs(t *testing.T) {
	t.Parallel()
	c := w2GenOnOffCluster(t)
	byName := map[string]w2GenSchemaElement{}
	for _, a := range c.Attributes {
		byName[a.Name] = a
	}
	for _, tc := range []struct {
		name  string
		value uint32
	}{
		{"OnOff", matterGenericSwitchAttrOnOff},
		{"GlobalSceneControl", matterGenericSwitchAttrGlobalSceneControl},
		{"OnTime", matterGenericSwitchAttrOnTime},
		{"OffWaitTime", matterGenericSwitchAttrOffWaitTime},
		{"StartUpOnOff", matterGenericSwitchAttrStartUpOnOff},
		{"FeatureMap", matterGenericSwitchAttrFeatureMap},
		{"ClusterRevision", matterGenericSwitchAttrClusterRevision},
	} {
		el, ok := byName[tc.name]
		if !ok {
			t.Errorf("matter.js OnOff has no attribute %q", tc.name)
			continue
		}
		if tc.value != el.ID {
			t.Errorf("%s = 0x%04X, matter.js says 0x%04X", tc.name, tc.value, el.ID)
		}
	}
}

// TestW2GenParityMatterJSGenericOnOffAdvertisedSets pins the two lists a
// controller reads — AttributeList and AcceptedCommandList — against the
// elements the snapshot's conformance terms admit for the FeatureMap this
// endpoint actually reports.
//
// The FeatureMap is read out of the projection rather than assumed, and every
// conformance term the snapshot carries is either evaluated or fails the
// test. Skipping an unrecognised term is how a conformance check comes back
// green without having checked anything.
func TestW2GenParityMatterJSGenericOnOffAdvertisedSets(t *testing.T) {
	t.Parallel()
	c := w2GenOnOffCluster(t)

	s := &Switch{}
	raw, ok := s.MatterRead(matterGenericSwitchAttrFeatureMap)
	if !ok {
		t.Fatal("Switch does not answer FeatureMap; the advertised feature set cannot be derived")
	}
	featureMap, ok := raw.(uint32)
	if !ok {
		t.Fatalf("FeatureMap read returned %T, want uint32", raw)
	}
	enabled := map[string]bool{}
	for _, f := range c.Features {
		enabled[f.Name] = featureMap&(uint32(1)<<uint(f.Bit)) != 0
	}

	// admits evaluates the conformance terms this cluster actually uses. An
	// unknown term is a failure, never a skip.
	admits := func(term string) (bool, error) {
		switch term {
		case "M":
			return true, nil
		case "LT":
			return enabled["LT"], nil
		case "!OFFONLY":
			return !enabled["OFFONLY"], nil
		case "":
			// A global element (FeatureMap, ClusterRevision); the dispatcher
			// merges those in, the cluster server does not list them.
			return false, nil
		default:
			return false, fmt.Errorf("unhandled conformance term %q", term)
		}
	}

	expect := func(els []w2GenSchemaElement) []uint32 {
		out := []uint32{}
		for _, el := range els {
			in, err := admits(el.Conformance)
			if err != nil {
				t.Fatalf("%s: %v — extend the evaluator rather than letting the element pass unchecked",
					el.Name, err)
			}
			if in {
				out = append(out, el.ID)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}

	if got, want := w2GenSorted(s.MatterAttributes()), expect(c.Attributes); !w2GenEqual(got, want) {
		t.Errorf("MatterAttributes() = %#x, matter.js conformance admits %#x for FeatureMap 0x%02X",
			got, want, featureMap)
	}
	if got, want := w2GenSorted(s.MatterAcceptedCommands()), expect(c.Commands); !w2GenEqual(got, want) {
		t.Errorf("MatterAcceptedCommands() = %#x, matter.js conformance admits %#x for FeatureMap 0x%02X",
			got, want, featureMap)
	}
}

func w2GenSorted(in []uint32) []uint32 {
	out := append([]uint32(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func w2GenEqual(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
