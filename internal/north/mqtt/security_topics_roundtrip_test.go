// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityPlaneTopicsRoundTrip asserts that every topic the plane
// declares is a topic the plane writes.
//
// It exists because the two halves disagreed and both passed their own
// tests. Discovery derived the state topic from the entity key, so a
// class entity declared `<base>/security/class_smoke`, while the
// publisher wrote `<base>/security/class/smoke`. Payload-shape tests
// checked the declaration, publish tests checked the write, and nothing
// compared the two sets — so every class and zone entity appeared in a
// consumer and stayed unavailable forever.
//
// Both sets come from ONE run of the real publisher against a recording
// broker: the declarations are read out of the discovery configs the plane
// published, the writes are the state topics of the same run. The earlier
// version assembled the "published" side by calling the plane's own topic
// helpers a second time, which made the two halves move in lockstep by
// construction — a rename inside [SecurityMQTTPublisher.reconcile] left
// this guard, and the whole package, green.
//
// The comparison is one-directional on purpose: a declared topic nobody
// writes is the defect. A topic written without a declaration is a
// lesser problem (an operator simply does not get an entity for it) and
// is reported separately rather than failed on.
// The table runs the whole comparison for a plain base and for a base
// that carries the trailing slash an operator is free to configure. The
// second row is what the availability half needs: every payload names
// the bridge-status topic as its first availability source, and the
// bridge publishes that status through its topic builder, which trims
// the base. A source spelled one slash differently never receives a
// payload, and `availability_mode: "all"` then leaves every entity of
// the plane unavailable forever.
func TestSecurityPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"gh", "gh/"} {
		obs := runSecurityPlane(t, base)
		planeRoundTrip(t, "base "+base,
			obs.declaredTopics(t), obs.publishedTopics(), obs.subscribedFilters(), nil)
	}
}

// runSecurityPlane drives the real plane end to end against a recording
// broker and returns everything it carried.
//
// Every write comes from production code: the bridge announces its own
// status, the publisher reconciles the retained half and declares the
// entities, and one hazard plus one fault report drive the four topics
// only [SecurityMQTTPublisher.onNotification] produces. The domain event
// is what lifts the declaration gate — the plane deliberately withholds
// its configs until the domain has spoken once (see
// [SecurityMQTTPublisher.declareEntities]) — so without it the run would
// observe no declarations at all.
func runSecurityPlane(t *testing.T, base string) *observedPlane {
	t.Helper()
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
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
	for _, fault := range []bool{false, true} {
		events.Publish(bus, hmevent.SecurityNotificationEvent{
			Base:       hmevent.NewBaseAt(time.Now()),
			Class:      hmenum.SecurityClassSmoke,
			Severity:   hmenum.SecuritySeverityCritical,
			Subject:    "Rauch",
			Message:    "Rauch erkannt",
			AtMS:       time.Now().UnixMilli(),
			Fault:      fault,
			Retainable: true,
		})
	}
	obs.settle(t)
	return obs
}

func roundTripSnapshot() security.Snapshot {
	return security.Snapshot{
		Severity: hmenum.SecuritySeverityWarning,
		Classes: map[hmenum.SecurityClass]security.ClassState{
			hmenum.SecurityClassSmoke: {Class: hmenum.SecurityClassSmoke, Known: 1},
			hmenum.SecurityClassWater: {
				Class: hmenum.SecurityClassWater, Known: 2, Active: true,
				Sources: []hmevent.SecuritySourceRef{{Ref: "c|i|ABC:1|MOISTURE_DETECTED", Name: "Keller"}},
			},
		},
		Zones: map[string]security.ZoneState{
			"erdgeschoss": {ID: "z1", Slug: "erdgeschoss", Name: "Erdgeschoss"},
		},
		EngineHealthy: true,
	}
}
