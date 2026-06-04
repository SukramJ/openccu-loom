// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"encoding/json"
	"testing"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// matter.js's MatterDefinition is the wire-level reference. The wire
// package implements Switch / GenericSwitch / AdministratorCommissioning
// / Schedules cluster servers; we pin every cluster ID + revision
// against matter.js HEAD here so a stale revision does not pass review.

type matterCluster struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
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

// TestParityMatterJS_WireClusterRevisions asserts every wire-package
// cluster server pins the same revision matter.js HEAD ships.
//
// The Schedules cluster (0x0024) is intentionally not in the list:
// matter.js does not define it (verified against @matter/model 0.16.11),
// and exposing it makes Apple Home's HAP
// service mapper reject the endpoint. The Schedules code stays in
// the package for revival once the Matter spec ships a canonical
// schedule cluster — but the bridge no longer attaches the server.
func TestParityMatterJS_WireClusterRevisions(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterAdminCommissioning, "AdministratorCommissioning", admCommClusterRevision},
		{matterClusterGenericSwitch, "Switch", switchClusterRevision},
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

// TestParityMatterJS_SchedulesClusterIsNotShipped guards the decision
// to remove the Schedules cluster (0x0024) from the bridge surface
// (see [climate.Climate.MatterClusterServers]). matter.js does not
// define a cluster with that ID; advertising it causes Apple Home
// pair-abort. If matter.js ever ships a
// Schedules cluster at 0x0024, this test fires so the bridge can
// re-attach the server.
func TestParityMatterJS_SchedulesClusterIsNotShipped(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	if _, ok := clusterByID(schema, SchedulesClusterID); ok {
		t.Errorf("matter.js HEAD now defines a cluster at 0x%04X — re-attach the Schedules server in climate.MatterClusterServers and update this test",
			SchedulesClusterID)
	}
}
