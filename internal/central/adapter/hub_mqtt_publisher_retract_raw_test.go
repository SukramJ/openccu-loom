// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

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
		Serial:  "3014F711A0001F0123456789",
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

// TestAQueuedRetractSurvivesABrokerReconnect pins the guarantee
// [HubMQTTPublisher.RetractCentral]'s comment claims and the fan-out did not
// hold: that stopping the poller before the retract is queued makes the
// retract the last write to that CCU's topics.
//
// FIFO ordering holds only WITHIN one wiring generation, and the generation is
// torn down by the very event the publisher is wired to. The daemon's broker
// supervisor calls hubMQTT.Start from its on-connect hook
// (cmd/openccu-loom/daemon.go); Start begins with Stop; Stop retired the
// fan-out with [mqttFanout.stop], whose run() returns on a cancelled context
// WITHOUT draining. A reconnect landing between removeCentral's
// RetractCentral and the worker's drain therefore discarded the whole
// retract — every `.../config` retraction and RetractHubStatus with it. The
// unit stays in the shared registry across that span (Unit.Stop, which
// unregisters it, runs after evictModel, which is not short on a 200-device
// CCU), so the fresh generation re-wires the leaving central on top.
//
// What the operator sees: the removed CCU's hub entities keep their retained
// discovery configs and never leave Home Assistant.
// RunDiscoveryOrphanCleanupOnce is scoped to REGISTERED centrals, so nothing
// reaches them again — the daemon has no path back to those topics short of
// a manual broker purge.
//
// The interleaving is built here rather than argued: a job that occupies the
// worker for a beat (a slow broker publish, which is what a reconnect implies
// in the first place), the retract queued behind it, and Start called before
// the worker gets to it.
//
// Falsifiability: put [mqttFanout.stop] back in [HubMQTTPublisher.Stop] in
// place of stopDraining and this test reds on the retract count.
func TestAQueuedRetractSurvivesABrokerReconnect(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F0123456789"})
	conn := hub.NewConnectivity()
	conn.OnState("HmIP-RF", true)
	c.HubModel.SetConnectivity(conn)

	ctx := context.Background()
	publisher.Start(ctx)
	publisher.Flush()
	before := len(pub.Published())

	// Occupy the worker the way a slow broker does: in flight, not wedged.
	f := publisher.fanout.Load()
	entered := make(chan struct{})
	f.enqueueDurable(func() {
		close(entered)
		time.Sleep(20 * time.Millisecond)
	})
	<-entered

	publisher.RetractCentral(c, nil)
	// The broker reconnects while the removal is still in flight.
	publisher.Start(ctx)
	defer publisher.Stop()
	publisher.Flush()

	retracted := 0
	for _, p := range pub.Published()[before:] {
		if strings.HasSuffix(p.Topic, "/config") && len(p.Payload) == 0 {
			retracted++
		}
	}
	if retracted == 0 {
		t.Fatal("a broker reconnect during the removal window discarded the queued retract: " +
			"the removed CCU's retained discovery configs stay on the broker, its hub entities " +
			"stay in Home Assistant, and the orphan sweep is scoped to registered centrals so " +
			"nothing ever reaches those topics again")
	}
}
