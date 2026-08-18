// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityPlaneFeedsTheEntitiesItDeclares pins the security plane
// against the defect class this project keeps rediscovering: an entity that
// Home Assistant creates from a discovery config and then never receives a
// value for.
//
// Every topic the funnel writes under `<base>/security/` is a `state_topic`
// named by one of those configs, so the raw-plane switch must not silence
// them. Gated, the operator gets the full set of Security & Safety entities,
// all of them permanently unknown — which is strictly worse than either
// having them work or not having them at all.
func TestSecurityPlaneFeedsTheEntitiesItDeclares(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: false, HADiscoveryEnabled: true,
	}, obs)
	if err := bridge.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("bridge announce: %v", err)
	}

	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	obs.settle(t)

	statePrefix := base + "/security/"
	fed := 0
	for topic := range obs.publishedTopics() {
		if strings.HasPrefix(topic, statePrefix) {
			fed++
		}
	}
	if fed == 0 {
		t.Error("raw_enabled=false: the security plane declared entities but fed none of them")
	}

	sawDiscovery := false
	for _, rec := range obs.records() {
		if isDiscoveryConfigTopic(rec.topic) {
			sawDiscovery = true
			break
		}
	}
	if !sawDiscovery {
		t.Fatal("expected the security plane to declare its entities")
	}
}
