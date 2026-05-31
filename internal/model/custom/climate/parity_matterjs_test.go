// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestParityMatterJS_ClimateClusterRevisions pins every cluster revision
// implemented by the climate package against matter.js HEAD so a stale
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

func TestParityMatterJS_ClimateClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterThermostat, "Thermostat", matterThermClusterRevision},
		{matterClusterThermostatUI, "ThermostatUserInterfaceConfiguration", matterThermUIClusterRevision},
		{matterClusterTemperatureMeasurement, "TemperatureMeasurement", matterTempMeasClusterRevision},
		{matterClusterRelativeHumidityMeasurement, "RelativeHumidityMeasurement", matterHumidityClusterRevision},
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

// TestParityMatterJS_ThermostatFeatureMapBits locks the FeatureMap bit
// positions for HEAT, COOL and AUTO against matter.js HEAD. The
// constraint field in the schema carries the bit index as a string.
func TestParityMatterJS_ThermostatFeatureMapBits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		bit     uint32
		wantBit int
	}{
		{"HEAT", matterThermFeatureHeat, 0},
		{"COOL", matterThermFeatureCool, 1},
		{"AUTO", matterThermFeatureAuto, 5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := int(c.bit)
			want := 1 << c.wantBit
			if got != want {
				t.Errorf("FeatureMap bit %s = 0x%X, want 0x%X (bit %d)",
					c.name, got, want, c.wantBit)
			}
		})
	}
}

// TestParityMatterJS_ThermostatMandatoryAttributeIDs verifies that every
// unconditionally-mandatory attribute (conformance "M") of Thermostat
// (0x0201) is present in MatterAttributes() and handled in MatterRead().
// The bridge advertises HEAT|AUTO (not COOL), so COOL-conditional attrs
// are excluded; this is documented in docs/parity/by_design.md.
func TestParityMatterJS_ThermostatMandatoryAttributeIDs(t *testing.T) {
	t.Parallel()

	mandatoryAttrs := []struct {
		id   uint32
		name string
	}{
		{0x0000, "LocalTemperature"},
		{0x001B, "ControlSequenceOfOperation"},
		{0x001C, "SystemMode"},
	}

	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	server := climateThermostatServer{c: r.climate}

	attrList := server.MatterAttributes()
	attrSet := make(map[uint32]bool, len(attrList))

	for _, id := range attrList {
		attrSet[id] = true
	}

	for _, a := range mandatoryAttrs {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()

			if !attrSet[a.id] {
				t.Errorf("mandatory attribute %s (0x%04X) missing from MatterAttributes()", a.name, a.id)
			}

			_, ok := server.MatterRead(a.id)
			if !ok {
				t.Errorf("mandatory attribute %s (0x%04X) not handled in MatterRead()", a.name, a.id)
			}
		})
	}
}

// TestParityMatterJS_ThermostatUIMandatoryAttributeIDs verifies that both
// mandatory attributes of ThermostatUserInterfaceConfiguration (0x0204) —
// TemperatureDisplayMode (0x0000) and KeypadLockout (0x0001) — are
// present in MatterAttributes() and handled in MatterRead().
func TestParityMatterJS_ThermostatUIMandatoryAttributeIDs(t *testing.T) {
	t.Parallel()

	mandatoryAttrs := []struct {
		id   uint32
		name string
	}{
		{0x0000, "TemperatureDisplayMode"},
		{0x0001, "KeypadLockout"},
	}

	server := climateThermostatUIServer{}

	attrList := server.MatterAttributes()
	attrSet := make(map[uint32]bool, len(attrList))

	for _, id := range attrList {
		attrSet[id] = true
	}

	for _, a := range mandatoryAttrs {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()

			if !attrSet[a.id] {
				t.Errorf("mandatory attribute %s (0x%04X) missing from MatterAttributes()", a.name, a.id)
			}

			_, ok := server.MatterRead(a.id)
			if !ok {
				t.Errorf("mandatory attribute %s (0x%04X) not handled in MatterRead()", a.name, a.id)
			}
		})
	}
}
