// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// Wire identities of the two events a controller reads out-of-band right
// after CASE. Spelled out rather than imported so the pin fails if a cluster
// constant is renumbered underneath it.
const (
	basicInformationClusterID     uint32 = 0x0028
	basicInformationStartUpEvent  uint32 = 0x0000
	generalDiagnosticsClusterID   uint32 = 0x0033
	generalDiagnosticsBootReasonE uint32 = 0x0003
)

// TestMatterBootEventsAreReadableAfterWiring asserts the effect of the
// boot-lifecycle emit rather than its presence: after the Matter runtime is
// wired, a read of BasicInformation StartUp and GeneralDiagnostics BootReason
// answers with an event, through the same [im.BuildEventReports] path the
// receive dispatcher uses for a controller's ReadRequest.
//
// Apple Home's MTRDevice waits for these Critical events as part of its
// Subscribe-Initial state machine; without them the controller transitions
// Subscribing → Unsubscribed and surfaces the bridge as "added but not
// supported". Nothing else in the repository notices when they go missing:
// the emit happens once at boot from the composition root, so a unit test of
// the cluster server proves only that EmitStartUp works when something calls
// it. The failure was found by the nightly chip-tool suite, four days after
// it shipped.
//
// Two ways this has broken, both of which this pin catches:
//
//   - The emit is not reached — the cluster servers' event emitters are bound
//     during topology assembly, so an emit ordered before the first
//     reassemble silently no-ops.
//   - The events are emitted and then evicted before anyone can read them,
//     because ordinary traffic shares their priority class in the event
//     buffer.
func TestMatterBootEventsAreReadableAfterWiring(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-boot-events", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-boot-events")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wiring, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil,
		slog.New(slog.DiscardHandler), nil,
	)
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})

	bridge, ok := wiring.reassembler.(*matterbridge.Bridge)
	if !ok || bridge == nil {
		t.Fatalf("matter runtime did not produce a bridge (reassembler=%T); without it this pin asserts nothing", wiring.reassembler)
	}

	for _, tc := range []struct {
		name    string
		cluster uint32
		event   uint32
	}{
		{"BasicInformation.StartUp", basicInformationClusterID, basicInformationStartUpEvent},
		{"GeneralDiagnostics.BootReason", generalDiagnosticsClusterID, generalDiagnosticsBootReasonE},
	} {
		path := im.ConcreteEventPath{
			HasEndpoint: true, Endpoint: 0,
			HasCluster: true, Cluster: tc.cluster,
			HasEvent: true, Event: tc.event,
		}
		reports := im.BuildEventReports([]im.ConcreteEventPath{path}, bridge.EventLog(), nil)
		if len(reports) == 0 {
			t.Errorf("%s: read-event returned no report after boot — a controller reads an empty EventReportIBs list here", tc.name)
			continue
		}
		if got := reports[0].Priority; got != im.EventPriorityCritical {
			t.Errorf("%s: priority = %v, want Critical (matter.js: both events are declared critical)", tc.name, got)
		}
	}
}

// TestMatterBootEventsSurviveAnInterfaceFlap is the second half: the events
// are still readable after the volume of traffic that used to displace them.
//
// A CCU interface flap forces every bridged device unavailable and back, and
// each flip fires a BridgedDeviceBasicInformation ReachableChanged event. On
// a 36-device central that is 72 events from one flap. While those were
// emitted as Critical into a 64-entry critical class, they pushed out the two
// boot events — which is exactly how this broke in production.
func TestMatterBootEventsSurviveAnInterfaceFlap(t *testing.T) {
	t.Parallel()

	log := im.NewEventLog()
	log.Append(im.EventRecord{Priority: im.EventPriorityCritical, Endpoint: 0, Cluster: basicInformationClusterID, EventID: basicInformationStartUpEvent})
	log.Append(im.EventRecord{Priority: im.EventPriorityCritical, Endpoint: 0, Cluster: generalDiagnosticsClusterID, EventID: generalDiagnosticsBootReasonE})

	// Two flips per device, on the scale of a large central — well past the
	// buffer's harvesting threshold, so this measures retention and not
	// headroom.
	for range 5000 {
		log.Append(im.EventRecord{Priority: im.EventPriorityInfo, Endpoint: 1, Cluster: 0x0039, EventID: 0x0003})
	}

	for _, tc := range []struct {
		name    string
		cluster uint32
		event   uint32
	}{
		{"BasicInformation.StartUp", basicInformationClusterID, basicInformationStartUpEvent},
		{"GeneralDiagnostics.BootReason", generalDiagnosticsClusterID, generalDiagnosticsBootReasonE},
	} {
		path := im.ConcreteEventPath{
			HasEndpoint: true, Endpoint: 0,
			HasCluster: true, Cluster: tc.cluster,
			HasEvent: true, Event: tc.event,
		}
		if reports := im.BuildEventReports([]im.ConcreteEventPath{path}, log, nil); len(reports) == 0 {
			t.Errorf("%s: evicted by ReachableChanged traffic", tc.name)
		}
	}
}
