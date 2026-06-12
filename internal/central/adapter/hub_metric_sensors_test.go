// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// hubMQTTDiscoveryFixture is like hubMQTTFixture but enables HA Discovery so
// PublishHubDiscovery actually writes to the broker.
func hubMQTTDiscoveryFixture(t *testing.T) (
	reg *central.Registry,
	c *central.Unit,
	pub *mqtt.NoopClient,
	publisher *HubMQTTPublisher,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	pub = mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	// Hub discovery payloads embed the per-central serial in their
	// unique_ids and skip publishing while it is unknown — stamp it like
	// the daemon's post-hydration HubInfo pass does.
	bridge.SetHubInfoFor("ccu-01", mqtt.HubInfo{Serial: "3014F711A0001234"})
	wiring := mqtt.NewWiring(bridge, nil)
	publisher = NewHubMQTTPublisher(reg, wiring, nil)
	return reg, c, pub, publisher
}

// TestMetricHubSensors_SystemHealthDiscoveryPublished verifies that
// BuildSystemHealthDiscovery is called (i.e. a topic containing
// "health_score" appears) when the publisher starts.
func TestMetricHubSensors_SystemHealthDiscoveryPublished(t *testing.T) {
	t.Parallel()
	_, _, pub, publisher := hubMQTTDiscoveryFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()

	var found bool
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "health_score") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("system-health discovery not published; topics=%v", publishedTopics(pub))
	}
}

// TestMetricHubSensors_LatencyDiscoveryPublishedOnConnectivity verifies
// that a connection-latency discovery topic appears after the first
// ConnectivityChangedEvent for an interface.
func TestMetricHubSensors_LatencyDiscoveryPublishedOnConnectivity(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTDiscoveryFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()

	events.Publish(c.EventBus, hmevent.ConnectivityChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Reachable:   true,
	})

	var found bool
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "latency") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("latency discovery not published; topics=%v", publishedTopics(pub))
	}
}
