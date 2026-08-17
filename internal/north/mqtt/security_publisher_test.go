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
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityPlaneRawTopicsRespectRawEnabled covers the single funnel
// [SecurityMQTTPublisher.publish] every raw security topic goes through
// (retained state, class/zone states, the plane's own availability, and
// non-retained event/fault reports): with the raw plane disabled none of
// them may reach the broker, matching every other raw-plane publisher on
// [Bridge] (e.g. [Bridge.PublishAvailability], which the security
// discovery entities' availability list itself depends on — see
// [securityAvailability]).
//
// Discovery configs are unaffected: [SecurityMQTTPublisher.declareEntities]
// publishes them through [Bridge.PublishAlarmDiscovery], which gates only
// on HADiscoveryEnabled, so an operator who disables raw topics but keeps
// discovery on still gets the entities declared — the same split the rest
// of the bridge already applies.
func TestSecurityPlaneRawTopicsRespectRawEnabled(t *testing.T) {
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
	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBaseAt(time.Now()),
		Class:      hmenum.SecurityClassSmoke,
		Severity:   hmenum.SecuritySeverityCritical,
		Subject:    "Rauch",
		Message:    "Rauch erkannt",
		AtMS:       time.Now().UnixMilli(),
		Retainable: true,
	})
	obs.settle(t)

	rawPrefix := base + "/security/"
	for topic := range obs.publishedTopics() {
		if strings.HasPrefix(topic, rawPrefix) {
			t.Errorf("raw_enabled=false: security plane published raw topic %q", topic)
		}
	}

	// Discovery stays on: declareEntities' own funnel is unaffected by the
	// raw-plane gate. publishedTopics() excludes discovery config topics by
	// design, so the raw records are checked directly.
	sawDiscovery := false
	for _, rec := range obs.records() {
		if isDiscoveryConfigTopic(rec.topic) {
			sawDiscovery = true
			break
		}
	}
	if !sawDiscovery {
		t.Fatal("raw_enabled=false, discovery_enabled=true: expected the security plane to still declare its entities")
	}
}
