// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestPin_ConcentrationClusters_SchemaRevision5 pins that the embedded
// matter.js HEAD schema snapshot reports revision 5 for the three
// concentration-measurement sub-clusters (0x040C family), mirroring
// matter.js HEAD concentration-measurement.element.ts:19 (default: 5).
// The runtime cluster servers already advertise revision 5; this test
// ensures the schema snapshot used by parity tests is consistent with
// that value.
func TestPin_ConcentrationClusters_SchemaRevision5(t *testing.T) {
	t.Parallel()
	raw := parity.SchemaJSON()
	var schema struct {
		Clusters []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Revision int    `json:"revision"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parity.SchemaJSON: unmarshal failed: %v", err)
	}

	want := map[int]string{
		1036: "CarbonMonoxideConcentrationMeasurement",
		1037: "CarbonDioxideConcentrationMeasurement",
		1043: "NitrogenDioxideConcentrationMeasurement",
	}

	found := make(map[int]bool)
	for _, c := range schema.Clusters {
		if name, ok := want[c.ID]; ok {
			found[c.ID] = true
			if c.Revision != 5 {
				t.Errorf("cluster 0x%04X (%s): schema revision = %d, want 5", c.ID, name, c.Revision)
			}
		}
	}
	for id, name := range want {
		if !found[id] {
			t.Errorf("cluster 0x%04X (%s) not found in schema", id, name)
		}
	}
}
