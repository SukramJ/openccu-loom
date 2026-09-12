// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// TestAlarmPlaneIsVisibleToTheBridge pins the alarm plane's retained
// topics to the bridge's retained-topic index.
//
// [Bridge.PublishAlarmState] and [Bridge.PublishAlarmAvailability] used
// to write straight to the client, so the alarm surface was invisible
// twice over — the same defect the Security & Safety plane carried. Its
// retained state and availability topics never entered `rawTopics`, so
// neither the orphan sweep nor a device-removal retraction could ever
// reach them, and its publishes were counted by neither `messages_sent`
// nor `publish_errors`. An alarm panel is the last entity whose retained
// ghost an operator should have to clear by hand.
func TestAlarmPlaneIsVisibleToTheBridge(t *testing.T) {
	t.Parallel()
	f := newAlarmPublisherFixture(t)
	f.seedZone("z1", "Ground floor", zeroDelayFullMode())
	f.start()

	stateTopic := alarmStateTopic(f.base, "z1")
	f.waitForPublish(stateTopic, func(rec publishRecord) bool { return rec.retain && rec.payload != "" })
	availTopic := alarmAvailabilityTopic(f.base, "z1")
	f.waitForPublish(availTopic, func(rec publishRecord) bool { return rec.payload == "online" })
	motionTopic := alarmTriggeredMotionTopic(f.base, "z1")
	f.waitForPublish(motionTopic, func(rec publishRecord) bool { return rec.retain })

	bridge := f.pub.wiring.Bridge()
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for _, topic := range []string{stateTopic, availTopic, motionTopic} {
		if _, ok := bridge.rawTopics[topic]; !ok {
			t.Errorf("retained alarm topic %q is absent from the bridge's retained-topic index — no sweep can reach it", topic)
		}
	}
}

// TestAlarmPlaneRetractionForgetsTheTopic pins the other half of the
// index bookkeeping: a retraction has to drop its topic again, so nothing
// retracts an already-empty topic a second time — a retained-message
// delete for a message that no longer exists, counted as a publish and,
// on a strict broker, as a publish error.
func TestAlarmPlaneRetractionForgetsTheTopic(t *testing.T) {
	t.Parallel()
	bridge, _ := newTestBridge(t)
	stateTopic := alarmStateTopic("openccu-loom", "z1")
	// Seeded directly rather than through a publish, so this pin isolates
	// the retraction's own bookkeeping: a mutation that drops only
	// forgetRawTopic still has to fail it.
	bridge.rememberRawTopic(stateTopic)

	if err := bridge.RetractAlarmTopic(context.Background(), stateTopic); err != nil {
		t.Fatalf("RetractAlarmTopic: %v", err)
	}
	bridge.mu.Lock()
	_, claimed := bridge.rawTopics[stateTopic]
	bridge.mu.Unlock()
	if claimed {
		t.Errorf("a retracted alarm topic %q is still claimed in the retained-topic index", stateTopic)
	}
}

// TestAlarmPlanePublishErrorsAreCounted pins the instrumentation half: a
// broker that refuses an alarm publish has to show up in
// `publish_errors` like every other plane's failure does, and a failed
// publish must leave no claim in the index.
func TestAlarmPlanePublishErrorsAreCounted(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: col,
	}, &failingPublisher{failFor: "/alarm/"})

	ctx := context.Background()
	stateTopic := alarmStateTopic(base, "z1")
	availTopic := alarmAvailabilityTopic(base, "z1")
	if err := bridge.PublishAlarmState(ctx, stateTopic, "disarmed"); err == nil {
		t.Fatal("PublishAlarmState: want the broker error to propagate")
	}
	if err := bridge.PublishAlarmAvailability(ctx, availTopic, true); err == nil {
		t.Fatal("PublishAlarmAvailability: want the broker error to propagate")
	}
	if err := bridge.PublishAlarmEvent(ctx, alarmEventTopic(base, "z1"), []byte(`{}`)); err == nil {
		t.Fatal("PublishAlarmEvent: want the broker error to propagate")
	}
	if err := bridge.RetractAlarmTopic(ctx, stateTopic); err == nil {
		t.Fatal("RetractAlarmTopic: want the broker error to propagate")
	}
	if got := col.PublishErrors("").Value(); got != 4 {
		t.Errorf("publish_errors = %d, want 4 — a refused alarm publish is invisible", got)
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for _, topic := range []string{stateTopic, availTopic} {
		if _, claimed := bridge.rawTopics[topic]; claimed {
			t.Errorf("a failed publish claimed %q in the retained-topic index", topic)
		}
	}
}

// TestAlarmPlanePublishesAreCounted pins `messages_sent` for the alarm
// plane's three retained publishers and its event pulse. Before the fix
// none of the four incremented a counter at all, so a deployment
// scraping metrics saw an idle daemon while the alarm surface was being
// written.
func TestAlarmPlanePublishesAreCounted(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: col,
	}, &mockPublisher{})

	ctx := context.Background()
	stateTopic := alarmStateTopic(base, "z1")
	if err := bridge.PublishAlarmState(ctx, stateTopic, "disarmed"); err != nil {
		t.Fatalf("PublishAlarmState: %v", err)
	}
	if err := bridge.PublishAlarmAvailability(ctx, alarmAvailabilityTopic(base, "z1"), true); err != nil {
		t.Fatalf("PublishAlarmAvailability: %v", err)
	}
	if err := bridge.PublishAlarmEvent(ctx, alarmEventTopic(base, "z1"), []byte(`{}`)); err != nil {
		t.Fatalf("PublishAlarmEvent: %v", err)
	}
	if err := bridge.RetractAlarmTopic(ctx, stateTopic); err != nil {
		t.Fatalf("RetractAlarmTopic: %v", err)
	}
	if got := col.MessagesSent("").Value(); got != 4 {
		t.Errorf("messages_sent = %d, want 4 — the alarm plane's publishes are not counted", got)
	}
}

// TestAlarmAvailabilityIsPinnedToQoS1 pins the delivery guarantee of the
// alarm plane's availability marker.
//
// Availability is the one topic whose loss the next publish cannot
// repair: the plane writes it only on a flip, so a marker dropped at
// QoS 0 leaves the entity in the state it last carried. For the
// `offline` marker written on shutdown that means "until the next
// start", and for a crash that suppresses the broker's last-will it
// means never — every alarm entity stays available, showing a frozen
// armed/disarmed token. The device and Security & Safety planes pin
// QoS 1 for the same reason.
func TestAlarmAvailabilityIsPinnedToQoS1(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"

	bridge, mp := newTestBridge(t)
	topic := alarmAvailabilityTopic(base, "z1")
	if err := bridge.PublishAlarmAvailability(context.Background(), topic, true); err != nil {
		t.Fatalf("PublishAlarmAvailability: %v", err)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	found := false
	for _, rec := range mp.sent {
		if rec.topic != topic {
			continue
		}
		found = true
		if rec.qos != QoS1 {
			t.Errorf("alarm availability published at QoS %d, want QoS 1", rec.qos)
		}
		if !rec.retain {
			t.Error("alarm availability published non-retained, want retained")
		}
	}
	if !found {
		t.Fatalf("no publish to %s observed", topic)
	}
}
