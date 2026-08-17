// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestAlarmPlaneTopicsRoundTrip asserts that every topic the alarm
// plane declares in a panel's discovery payload is a topic the plane
// actually writes to (state) or actually subscribes to (command).
//
// It exists for the same reason as [TestSecurityPlaneTopicsRoundTrip]:
// the two halves of a plane can each pass their own tests while
// disagreeing with each other. Here the risk is concrete —
// [BuildAlarmPanelDiscovery] derives `state_topic`/`command_topic` from
// [alarmStateTopic]/[alarmCommandTopic], while [AlarmMQTTPublisher.reconcile]
// writes the state through [alarmStateTopic] and [CommandSubscriber.Start]
// subscribes the command plane through its own, independently written
// wildcard. A future edit to either helper's topic shape without a matching
// edit on the other side would leave a panel's state forever unavailable, or
// its arm/disarm button silently unheard by the daemon.
//
// Both sides are observed from one run of the real components against a
// recording broker: the declarations are read back out of the discovery
// configs the publisher published, the state topics are the writes of the
// same run, and the command topics are matched against the filters the real
// [CommandSubscriber] registered. Nothing is restated — a "published" set
// rebuilt from the plane's own topic helpers agrees with the declaration
// whatever either one says.
//
// The comparison is one-directional: a declared topic nobody
// writes/subscribes is the defect. A topic written without a
// declaration (the non-retained `<base>/alarm/<zone>/event` stream,
// which HA reaches through the raw plane rather than through a
// discovered entity) is reported via t.Logf only.
//
// The table runs twice, once with the trailing slash an operator may
// configure on `topic_base`. Every panel names the bridge-status topic
// as its first availability source while the bridge publishes that
// status through its topic builder, which trims the base — one slash of
// disagreement takes every panel down rather than one value, because
// `availability_mode` is "all".
func TestAlarmPlaneTopicsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"gh", "gh/"} {
		obs := runAlarmPlane(t, base)
		planeRoundTrip(t, "base "+base,
			obs.declaredTopics(t), obs.publishedTopics(), obs.subscribedFilters(), nil)
	}
}

// runAlarmPlane drives the real alarm plane against a recording broker
// and returns everything it carried.
//
// Two zones are seeded because the aggregate master panel is only
// declared once the engine knows more than one — a single-zone run would
// silently skip half the plane's entities.
func runAlarmPlane(t *testing.T, base string) *observedPlane {
	t.Helper()
	ctx := context.Background()
	svc := newAlarmFixtureService(t)
	seedRoundTripZone(t, svc, "eg", "Erdgeschoss")
	seedRoundTripZone(t, svc, "og", "Obergeschoss")

	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)
	if err := bridge.AnnounceOnline(ctx); err != nil {
		t.Fatalf("bridge announce: %v", err)
	}

	pub := NewAlarmMQTTPublisher(svc, NewWiring(bridge, slog.Default()), slog.Default())
	pub.Start()
	t.Cleanup(pub.Stop)

	// The command half is observed rather than mirrored: the real
	// subscriber registers its own wildcards, and a panel's command topic
	// counts as heard only when one of them matches it.
	cs := NewCommandSubscriber(obs, NewTopicBuilder(base), nil, slog.Default())
	if err := cs.Start(ctx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cs.Close)

	obs.settle(t)
	return obs
}

// seedRoundTripZone persists one zone with no exit or entry delay and
// reloads the service, so the publisher's reconcile sees it.
func seedRoundTripZone(t *testing.T, svc *alarm.Service, id, name string) {
	t.Helper()
	cfg, err := json.Marshal(zeroDelayFullMode())
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := svc.Stores().Zones.Upsert(context.Background(), sqlitestore.AlarmZoneRow{
		ID: id, Name: name, ConfigJSON: string(cfg), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed zone %s: %v", id, err)
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatalf("reload after seeding zone %s: %v", id, err)
	}
}
