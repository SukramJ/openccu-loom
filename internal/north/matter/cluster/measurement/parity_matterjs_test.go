// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package measurement

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matter.js's MatterDefinition tree is the de-facto reference Matter
// implementation. The measurement package implements the sensor-side
// clusters (Temperature/Humidity/Illuminance/Pressure/Occupancy/
// BooleanState/PowerSource/ElectricalPower/ElectricalEnergy +
// CO2/PM2.5/PM10 concentration) plus the standalone power-source
// cluster. We pin every cluster ID + cluster revision against the
// matter.js HEAD snapshot here so a stale revision does not pass
// review.

type matterCluster struct {
	ID         uint32            `json:"id"`
	Name       string            `json:"name"`
	Revision   uint16            `json:"revision"`
	FeatureMap uint32            `json:"featureMap"`
	Attributes []matterAttribute `json:"attributes"`
}

// matterAttribute is one attribute row of the snapshot. Conformance is
// the raw matter.js conformance expression ("M", "BAT", "IMPE & CUME",
// "[REPLC]", …) — the contract that decides whether an attribute may,
// must, or must not be published for a given FeatureMap.
type matterAttribute struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Conformance string `json:"conformance"`
}

type matterSchema struct {
	Clusters []matterCluster `json:"clusters"`
}

func loadMatterSchemaT(t *testing.T) *matterSchema {
	t.Helper()
	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
	}
	if len(s.Clusters) == 0 {
		t.Fatalf("matter-schema-snapshot.json appears empty: %d clusters", len(s.Clusters))
	}
	return &s
}

func clusterByID(s *matterSchema, id uint32) (matterCluster, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}
	return matterCluster{}, false
}

// TestParityMatterJS_MeasurementClusterRevisions asserts every
// measurement-side cluster openccu-loom implements pins the same
// revision matter.js HEAD ships. Per-cluster revision drift triggers
// Apple Home pair-abort; this
// test catches the same bug class for the sensor surface.
func TestParityMatterJS_MeasurementClusterRevisions(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{ClusterTemperatureMeasurement, "TemperatureMeasurement", tempMeasClusterRevision},
		{ClusterHumidityMeasurement, "RelativeHumidityMeasurement", humidityClusterRevision},
		{ClusterIlluminanceMeasurement, "IlluminanceMeasurement", illuminanceClusterRevision},
		{ClusterPressureMeasurement, "PressureMeasurement", pressureClusterRevision},
		{ClusterBooleanState, "BooleanState", booleanStateClusterRevision},
		{ClusterOccupancySensing, "OccupancySensing", occupancyClusterRevision},
		{ClusterAirQuality, "AirQuality", airQualityClusterRevision},
		{ClusterPowerSource, "PowerSource", powerSourceClusterRevision},
		{ClusterElectricalPower, "ElectricalPowerMeasurement", electricalPowerClusterRevision},
		{ClusterElectricalEnergy, "ElectricalEnergyMeasurement", electricalEnergyClusterRevision},
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

// TestParityMatterJS_PowerSourceMandatoryAttributes asserts that
// PowerSourceServer.MatterAttributes() covers every mandatory attribute
// required by matter.js HEAD (packages/model/src/standard/elements/
// power-source.element.ts), including EndpointList (0x001F).
func TestParityMatterJS_PowerSourceMandatoryAttributes(t *testing.T) {
	t.Parallel()
	srv := NewPowerSourceServer(stubBoolSrc(false))
	attrs := make(map[uint32]bool, len(srv.MatterAttributes()))
	for _, id := range srv.MatterAttributes() {
		attrs[id] = true
	}
	mandatory := []struct {
		id   uint32
		name string
	}{
		{attrPwrStatus, "Status (0x0000)"},
		{attrPwrOrder, "Order (0x0001)"},
		{attrPwrDescription, "Description (0x0002)"},
		{attrPwrBatChargeLevel, "BatChargeLevel (0x000E)"},
		{attrPwrBatReplacementNeeded, "BatReplacementNeeded (0x000F)"},
		{attrPwrBatReplaceability, "BatReplaceability (0x0010)"},
		{attrPwrEndpointList, "EndpointList (0x001F)"},
	}
	for _, m := range mandatory {
		if !attrs[m.id] {
			t.Errorf("PowerSource MatterAttributes() missing mandatory %s", m.name)
		}
	}
	// Also verify MatterRead returns (_, true) for each mandatory attr.
	for _, m := range mandatory {
		_, ok := srv.MatterRead(m.id)
		if !ok {
			t.Errorf("PowerSource MatterRead(0x%04X) returned ok=false for mandatory %s", m.id, m.name)
		}
	}
}

// stubBoolSrc is a minimal MatterBoolMeasurementSource for tests.
type stubBoolSrc bool

func (s stubBoolSrc) MatterBoolValue() (value, observed bool) { return bool(s), true }
func (stubBoolSrc) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementBattery
}

// stubFloatSrc is a minimal MatterFloatMeasurementSource for tests.
type stubFloatSrc float64

func (s stubFloatSrc) MatterFloatValue() (value float64, observed bool) { return float64(s), true }
func (stubFloatSrc) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementPower
}

// featureBitsByCluster pins the FeatureMap bit index of every feature
// the clusters below can advertise. The embedded schema snapshot carries
// feature names and their conformance but not the `constraint` field
// that encodes the bit index, so the indices are pinned here against the
// matter.js element files:
//
//	packages/model/src/standard/elements/power-source-cluster.element.ts
//	packages/model/src/standard/elements/electrical-energy-measurement.element.ts
//	packages/model/src/standard/elements/electrical-power-measurement.element.ts
var featureBitsByCluster = map[uint32]map[string]uint32{
	ClusterPowerSource:      {"WIRED": 0, "BAT": 1, "RECHG": 2, "REPLC": 3},
	ClusterElectricalEnergy: {"IMPE": 0, "EXPE": 1, "CUME": 2, "PERE": 3, "APPE": 4, "REAE": 5},
	ClusterElectricalPower:  {"DIRC": 0, "ALTC": 1, "POLY": 2, "HARM": 3, "PWRQ": 4},
}

// mandatoryUnderFeatureMap decides whether a matter.js conformance
// expression obliges a server advertising fm to publish the attribute.
// Only the unconditional forms are modelled: "M" (always mandatory) and
// a conjunction of feature names ("BAT", "IMPE & CUME"). Optional forms
// ("O", "[X]"), choice groups ("[REPLC | RECHG]") and desc-driven
// expressions carry no publish obligation in either direction and report
// modelled=false so the caller skips them.
func mandatoryUnderFeatureMap(conformance string, bits map[string]uint32, fm uint32) (required, modelled bool) {
	term := conformance
	// Trailing qualifiers such as ", D" (deprecated) do not change the
	// publish obligation of the leading term.
	if i := strings.Index(term, ","); i >= 0 {
		term = term[:i]
	}
	term = strings.TrimSpace(term)
	if term == "M" {
		return true, true
	}
	if term == "" || strings.ContainsAny(term, "[]|!()<>=") {
		return false, false
	}
	required = true
	for _, name := range strings.Split(term, "&") {
		bit, known := bits[strings.TrimSpace(name)]
		if !known {
			return false, false
		}
		if fm&(1<<bit) == 0 {
			required = false
		}
	}
	return required, true
}

// TestParityMatterJS_FeatureGatedAttributesMatchFeatureMap asserts that
// the attribute set each cluster server publishes is exactly the set its
// advertised FeatureMap makes mandatory. Both halves bite: a feature bit
// set without its mandatory attributes leaves a controller reading
// UnsupportedAttribute for something the cluster declared it has, and an
// attribute served while its gating feature is clear is a schematically
// inconsistent cluster — the shape that makes a conformance-checking
// controller drop the cluster wholesale.
func TestParityMatterJS_FeatureGatedAttributesMatchFeatureMap(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	// A cluster server that also advertises its attribute set — the pair
	// the dispatcher reads when it answers a wildcard read.
	type listingServer interface {
		interfaces.MatterClusterServer
		interfaces.MatterClusterAttributeLister
	}
	cases := []struct {
		name string
		srv  listingServer
	}{
		{"PowerSource", NewPowerSourceServer(stubBoolSrc(false))},
		{"ElectricalEnergyMeasurement", NewElectricalEnergyServer(stubFloatSrc(0))},
		{"ElectricalPowerMeasurement", NewElectricalPowerServer(stubFloatSrc(0))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			js, ok := clusterByID(schema, c.srv.MatterClusterID())
			if !ok {
				t.Fatalf("matter.js schema has no cluster 0x%04X", c.srv.MatterClusterID())
			}
			bits, ok := featureBitsByCluster[c.srv.MatterClusterID()]
			if !ok {
				t.Fatalf("no pinned feature-bit table for cluster 0x%04X", c.srv.MatterClusterID())
			}
			raw, ok := c.srv.MatterRead(cluster.AttrGlobalFeatureMap)
			if !ok {
				t.Fatal("MatterRead(FeatureMap) ok = false")
			}
			fm, ok := raw.(uint32)
			if !ok {
				t.Fatalf("FeatureMap is %T, want uint32", raw)
			}
			advertised := make(map[uint32]bool, len(c.srv.MatterAttributes()))
			for _, id := range c.srv.MatterAttributes() {
				advertised[id] = true
			}
			for _, a := range js.Attributes {
				required, modelled := mandatoryUnderFeatureMap(a.Conformance, bits, fm)
				if !modelled {
					continue
				}
				switch {
				case required && !advertised[a.ID]:
					t.Errorf("%s (0x%04X) is mandatory under FeatureMap 0x%02X (conformance %q) but is not published",
						a.Name, a.ID, fm, a.Conformance)
				case !required && advertised[a.ID]:
					t.Errorf("%s (0x%04X) is published but its gating feature(s) %q are absent from FeatureMap 0x%02X",
						a.Name, a.ID, a.Conformance, fm)
				}
			}
		})
	}
}

// TestParityMatterJS_ConcentrationClustersShareRevision pins the
// CO2 / PM2.5 / PM10 concentration clusters — they share the spec
// shape so we apply one revision constant across all three. matter.js
// is the source of truth for the per-id revision.
func TestParityMatterJS_ConcentrationClustersShareRevision(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	concentrationIDs := []struct {
		id   uint32
		name string
	}{
		{0x040D, "CarbonDioxideConcentrationMeasurement"},
		{0x042A, "Pm25ConcentrationMeasurement"},
		{0x042D, "Pm10ConcentrationMeasurement"},
	}
	for _, c := range concentrationIDs {
		js, ok := clusterByID(schema, c.id)
		if !ok {
			t.Errorf("matter.js schema has no cluster 0x%04X (%s)", c.id, c.name)
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			if concentrationClusterRevision != js.Revision {
				t.Errorf("code revision %d != matter.js %d for %s (0x%04X)",
					concentrationClusterRevision, js.Revision, js.Name, js.ID)
			}
		})
	}
	// Sanity — make sure we covered the "common shape" assumption.
	revs := []uint16{}
	for _, c := range concentrationIDs {
		js, ok := clusterByID(schema, c.id)
		if ok {
			revs = append(revs, js.Revision)
		}
	}
	slices.Sort(revs)
	if len(revs) > 0 && revs[0] != revs[len(revs)-1] {
		t.Errorf("concentration cluster revisions diverge in matter.js: %v — split the constants", revs)
	}
}
