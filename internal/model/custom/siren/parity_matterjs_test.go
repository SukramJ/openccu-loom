// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	matterparity "github.com/SukramJ/go-fabric/parity"
)

// TestParityMatterJS_SirenClusterRevisions pins every cluster revision
// implemented by the siren package against matter.js HEAD so a stale
// revision does not pass review unnoticed.

type matterClusterEntry struct {
	ID         uint32               `json:"id"`
	Name       string               `json:"name"`
	Revision   uint16               `json:"revision"`
	Features   []matterFeatureEntry `json:"features"`
	Attributes []matterElementEntry `json:"attributes"`
	Commands   []matterElementEntry `json:"commands"`
}

type matterFeatureEntry struct {
	Name string `json:"name"`
	Bit  int    `json:"bit"`
}

type matterElementEntry struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Conformance string `json:"conformance"`
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
// smoke-co-alarm-cluster.element.ts), including HardwareFaultAlert
// (0x0006) and EndOfServiceAlert (0x0007).
//
// SmokeState (conformance "SMOKE") is mandatory here because this
// projection advertises the SMOKE feature; CoState is not — see
// TestParityMatterJS_SmokeCOStateIsGatedOnTheCOFeature.
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

// TestParityMatterJS_SmokeCOStateIsGatedOnTheCOFeature pins CoState
// (0x0002) against its matter.js conformance, "CO": the attribute exists
// only while the FeatureMap carries the CO bit. Serving it beside a
// FeatureMap of SMOKE alone put a non-conformant attribute in
// AttributeList on every commissioner read, and offered controllers a
// carbon-monoxide reading from a device with no CO sensor.
func TestParityMatterJS_SmokeCOStateIsGatedOnTheCOFeature(t *testing.T) {
	t.Parallel()
	srv := smokeCOServer{s: &SmokeSiren{}}
	fm, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Fatal("SmokeCOAlarm MatterRead(FeatureMap) returned ok=false")
	}
	if fm.(uint32)&matterSmokeCOFeatureCO != 0 {
		t.Fatalf("FeatureMap=%v unexpectedly advertises CO; the rest of this test assumes smoke-only", fm)
	}
	for _, id := range srv.MatterAttributes() {
		if id == matterAttrCOState {
			t.Error("MatterAttributes() lists CoState (0x0002) while the FeatureMap omits CO")
		}
	}
	if _, ok := srv.MatterRead(matterAttrCOState); ok {
		t.Error("MatterRead(0x0002) must return ok=false while the FeatureMap omits CO")
	}
}

// TestParityMatterJS_SmokeExpressedStateUsesItsOwnEnum pins ExpressedState
// (0x0000) against ExpressedStateEnum rather than AlarmStateEnum. The two
// enums share a value space by accident only: AlarmStateEnum's Critical is
// 2, which in ExpressedStateEnum means CoAlarm — so a real smoke alarm
// used to surface as a carbon-monoxide alarm.
func TestParityMatterJS_SmokeExpressedStateUsesItsOwnEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status SmokeAlarmStatus
		want   uint8
	}{
		{SmokeStatusIdleOff, matterExpressedStateNormal},
		{SmokeStatusSecondaryAlarm, matterExpressedStateSmokeAlarm},
		{SmokeStatusPrimaryAlarm, matterExpressedStateSmokeAlarm},
		// INTRUSION_ALARM is Normal, not SmokeAlarm: the detector is being
		// used as a sounder for a burglar alarm and its smoke sensor has
		// detected nothing. See TestIntrusionIsNotReportedAsSmoke.
		{SmokeStatusIntrusion, matterExpressedStateNormal},
	}
	for _, c := range cases {
		if got := smokeStatusToExpressedState(c.status); got != c.want {
			t.Errorf("smokeStatusToExpressedState(%v) = %d, want %d", c.status, got, c.want)
		}
	}
}

// TestParityMatterJS_SirenOnOffNoSpuriousOptions locks the absence of
// OnOff.Options (0x000F) on the siren's OnOff server. Options is a
// Zigbee-Cluster-Library holdover the Matter spec dropped from OnOff;
// matter.js HEAD on-off.element.ts and chip HEAD both omit it.
func TestParityMatterJS_SirenOnOffNoSpuriousOptions(t *testing.T) {
	t.Parallel()
	const spuriousOptionsID uint32 = 0x000F
	_, srv, _ := matterSiren(t)
	if slices.Contains(srv.MatterAttributes(), spuriousOptionsID) {
		t.Errorf("siren OnOff MatterAttributes() advertises spurious OnOff.Options (0x000F)")
	}
	if _, ok := srv.MatterRead(spuriousOptionsID); ok {
		t.Errorf("siren OnOff MatterRead(0x000F) must return ok=false — Options is not in the OnOff schema")
	}
}

// TestParityMatterJS_OnOffAdvertisedSets pins the AttributeList and
// AcceptedCommandList of both OnOff servers this package serves — the
// siren's (OnOffPlugInUnit, LT mandatory) and the sound player's (Speaker,
// no feature) — against the elements the matter.js snapshot's conformance
// terms admit for the FeatureMap each server actually reports.
//
// The FeatureMap is read from the server rather than assumed, and every
// conformance term the snapshot carries is either evaluated or fails the
// test: skipping an unknown term would let the check pass unchecked.
func TestParityMatterJS_OnOffAdvertisedSets(t *testing.T) {
	t.Parallel()
	c, ok := clusterByID(loadMatterSchemaT(t), onoff.ClusterID)
	if !ok {
		t.Fatalf("matter.js schema has no cluster 0x%04X", onoff.ClusterID)
	}
	sp, _, _, _ := newSpeakerRig(t)
	speaker, _ := speakerServers(t, sp)
	_, siren, _ := matterSiren(t)
	cases := []struct {
		name   string
		srv    *onoff.Server
		wantFM uint32
	}{
		{"Siren", siren, onoff.FeatureLighting},
		{"SoundPlayer", speaker, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, ok := tc.srv.MatterRead(0xFFFC)
			if !ok {
				t.Fatal("server does not answer FeatureMap; the advertised set cannot be derived")
			}
			featureMap, ok := raw.(uint32)
			if !ok {
				t.Fatalf("FeatureMap read returned %T, want uint32", raw)
			}
			if featureMap != tc.wantFM {
				t.Errorf("FeatureMap = 0x%02X, want 0x%02X", featureMap, tc.wantFM)
			}
			enabled := map[string]bool{}
			for _, f := range c.Features {
				enabled[f.Name] = featureMap&(uint32(1)<<uint(f.Bit)) != 0
			}
			admits := func(term string) (bool, error) {
				switch term {
				case "M":
					return true, nil
				case "LT":
					return enabled["LT"], nil
				case "!OFFONLY":
					return !enabled["OFFONLY"], nil
				case "":
					// A global element (FeatureMap, ClusterRevision); the
					// dispatcher merges those in, the server does not list them.
					return false, nil
				default:
					return false, fmt.Errorf("unhandled conformance term %q", term)
				}
			}
			expect := func(els []matterElementEntry) []uint32 {
				out := []uint32{}
				for _, el := range els {
					in, err := admits(el.Conformance)
					if err != nil {
						t.Fatalf("%s: %v — extend the evaluator rather than letting the element pass unchecked", el.Name, err)
					}
					if in {
						out = append(out, el.ID)
					}
				}
				slices.Sort(out)
				return out
			}
			sorted := func(in []uint32) []uint32 {
				out := slices.Clone(in)
				slices.Sort(out)
				return out
			}
			if got, want := sorted(tc.srv.MatterAttributes()), expect(c.Attributes); !slices.Equal(got, want) {
				t.Errorf("MatterAttributes() = %#x, matter.js conformance admits %#x for FeatureMap 0x%02X", got, want, featureMap)
			}
			if got, want := sorted(tc.srv.MatterAcceptedCommands()), expect(c.Commands); !slices.Equal(got, want) {
				t.Errorf("MatterAcceptedCommands() = %#x, matter.js conformance admits %#x for FeatureMap 0x%02X", got, want, featureMap)
			}
		})
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
		{onoff.ClusterID, "OnOff", onoff.Revision()},
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
