// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestRetractCentralClearsRawPlaneHubState pins the raw-plane half of
// removing a central at runtime. RetractCentral already clears every
// HA-Discovery config the publisher declared, but retractHubDiscoveryItems
// only ever re-publishes `.../config` topics — it never touches the retained
// state topics those configs point at. A removed CCU's `hub/programs/<id>
// /state`, `hub/programs/<id>/execute_available`, `hub/sysvars/<name>/state`
// and `hub/update` topics therefore survived the removal, describing a CCU
// that is gone to any raw-plane consumer (and to a fresh HA discovery
// subscriber, which resurrects a dead entity from the retained state alone).
//
// The comparison is a declared-vs-retracted round trip, not a hand-written
// topic list: every state topic actually published before the removal must
// be re-published with an empty payload afterward.
func TestRetractCentralClearsRawPlaneHubState(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F5A4993D962",
	})

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "prog-9"}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)

	c.HubModel.Update.OnInfo(hub.UpdateInfo{CurrentFirmware: "3.79.6", AvailableFirmware: "3.79.6"})

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	// The raw-plane state topics this pass actually wrote — everything under
	// the central's hub subtree with a non-empty (i.e. live, not itself a
	// retract) payload. Discovery `.../config` topics are excluded: those are
	// covered by the existing discovery-retraction guard and are not this
	// test's claim.
	published := map[string]bool{}
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "/hub/") && !strings.HasSuffix(p.Topic, "/config") && len(p.Payload) > 0 {
			published[p.Topic] = true
		}
	}
	wantMarkers := []string{"hub/programs/prog-9/state", "hub/sysvars/", "hub/update"}
	for _, marker := range wantMarkers {
		found := false
		for topic := range published {
			if strings.Contains(topic, marker) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixture did not publish a topic containing %q; topics=%v", marker, publishedTopics(pub))
		}
	}

	publisher.RetractCentral(c, nil)
	publisher.Flush()

	retracted := map[string]bool{}
	for _, p := range pub.Published() {
		if published[p.Topic] && len(p.Payload) == 0 {
			retracted[p.Topic] = true
		}
	}
	for topic := range published {
		if !retracted[topic] {
			t.Errorf("raw-plane hub state topic %s was never retracted on central removal", topic)
		}
	}
}
