// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHubMQTTPublisherStartSurvivesADisabledBroker drives Start through a
// Wiring whose bridge has been swapped away — the state the MQTT
// supervisor installs when an operator disables MQTT at runtime, keeping
// the Wiring alive so in-flight publishes become no-ops.
//
// Without the nil check this panicked inside wireOneCentral, reaching for
// the bridge's discovery builder. Under the hub-discovery re-Start that
// panic was recovered and logged as
// `mqtt.hub_discovery.restart_on_ready.panic`, so the daemon kept running
// while the entire hub plane — sysvars, programs, the named central
// device — silently never published.
func TestHubMQTTPublisherStartSurvivesADisabledBroker(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// A hub model with content, so a publisher that does run reaches the
	// bridge rather than returning early on an empty model.
	c.HubModel.PutSysvar(hub.NewSysvar("ccu-01", "svTest", "", hmenum.HubValueTypeLogic, nil))
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, mqtt.NewNoopClient())
	wiring := mqtt.NewWiring(bridge, nil)
	// Exactly what the supervisor does on `north.mqtt.enabled: false`.
	wiring.SwapBridge(nil)

	publisher := NewHubMQTTPublisher(reg, wiring, nil)
	publisher.Start(context.Background())
	t.Cleanup(publisher.Stop)
}

// TestHubMQTTPublisherStartRecoversWhenTheBrokerReturns pins the other
// half: skipping a wire pass on a nil bridge must not leave the publisher
// permanently inert. The supervisor calls Start again on the next broker
// connect, and that pass has to wire the central as if nothing happened.
func TestHubMQTTPublisherStartRecoversWhenTheBrokerReturns(t *testing.T) {
	t.Parallel()

	_, c, pub, publisher := hubMQTTFixture(t)
	wiring := publisher.wiring

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "prog-1"}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)

	// Disabled: one pass that must publish nothing and not panic.
	restore := wiring.SwapBridge(nil)
	publisher.Start(context.Background())
	if got := len(pub.Published()); got != 0 {
		t.Fatalf("published %d messages with no bridge, want none", got)
	}

	// Re-enabled: the next Start must wire the central as if nothing happened.
	wiring.SwapBridge(restore)
	publisher.Start(context.Background())
	t.Cleanup(publisher.Stop)

	if !containsTopic(pub, "programs/prog-1") {
		t.Fatalf("central was not re-wired after the broker returned; topics=%v", publishedTopics(pub))
	}
}
