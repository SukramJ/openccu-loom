// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityPlaneIsVisibleToTheBridge pins the Security & Safety plane
// to the bridge's own publish path.
//
// The publisher used to reach past the bridge into the raw client. The
// plane was therefore invisible twice over: its retained topics never
// entered the bridge's retained-topic index, so no sweep and no
// retraction could ever reach them, and its publishes were counted by
// neither `messages_sent` nor `publish_errors` — the one plane whose
// entities an operator cannot watch failing was also the one plane whose
// publishes no metric could see.
func TestSecurityPlaneIsVisibleToTheBridge(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: col,
	}, obs)

	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	obs.settle(t)

	// Every retained topic the plane wrote must be in the index.
	prefix := base + "/security/"
	var retained []string
	for _, rec := range obs.records() {
		if strings.HasPrefix(rec.topic, prefix) && rec.retain && rec.payload != "" {
			retained = append(retained, rec.topic)
		}
	}
	if len(retained) == 0 {
		t.Fatal("the security plane wrote no retained topic — the fixture cannot show the bookkeeping")
	}
	bridge.mu.Lock()
	missing := make([]string, 0, len(retained))
	for _, topic := range retained {
		if _, ok := bridge.rawTopics[topic]; !ok {
			missing = append(missing, topic)
		}
	}
	bridge.mu.Unlock()
	if len(missing) > 0 {
		t.Errorf("retained security topics absent from the bridge's retained-topic index: %v", missing)
	}

	// And the publishes must be counted.
	if got := col.MessagesSent("").Value(); got < uint64(len(retained)) {
		t.Errorf("messages_sent = %d, want at least %d — the security plane's publishes are not counted",
			got, len(retained))
	}
}

// TestSecurityPlanePublishErrorsAreCounted pins the other half of the
// instrumentation: a broker that refuses a security publish has to show
// up in `publish_errors` like every other plane's failure does.
func TestSecurityPlanePublishErrorsAreCounted(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: col,
	}, &failingPublisher{failFor: "/security/"})

	ctx := context.Background()
	topic := securityStateTopic("openccu-loom", "state")
	if err := bridge.PublishSecurityState(ctx, topic, []byte(`{"state":"ok"}`)); err == nil {
		t.Fatal("PublishSecurityState: want the broker error to propagate")
	}
	if err := bridge.PublishSecurityEvent(ctx, securityStateTopic("openccu-loom", "event"), []byte(`{}`)); err == nil {
		t.Fatal("PublishSecurityEvent: want the broker error to propagate")
	}
	if err := bridge.RetractSecurityState(ctx, topic); err == nil {
		t.Fatal("RetractSecurityState: want the broker error to propagate")
	}
	if got := col.PublishErrors("").Value(); got != 3 {
		t.Errorf("publish_errors = %d, want 3 — a refused security publish is invisible", got)
	}
	// A failed publish must leave no claim in the index.
	bridge.mu.Lock()
	_, claimed := bridge.rawTopics[topic]
	bridge.mu.Unlock()
	if claimed {
		t.Errorf("a failed publish claimed %q in the retained-topic index", topic)
	}
}
