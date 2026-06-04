// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package measurement

import (
	"encoding/json"
	"slices"
	"testing"

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
	ID         uint32 `json:"id"`
	Name       string `json:"name"`
	Revision   uint16 `json:"revision"`
	FeatureMap uint32 `json:"featureMap"`
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
