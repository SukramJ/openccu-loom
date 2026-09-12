// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSecurityQueueDropsTheNewestAndSaysSo pins the two planes of this
// daemon to one overflow policy, and makes the loss observable.
//
// The alarm plane drops the newest message; the security plane dropped
// the oldest, which is the wrong end. Not every queued message is
// recoverable: a state is corrected by the next reconcile, while a
// retraction has no next attempt — the class or zone it evacuates has
// already left the known-sets, so nothing enqueues it again and the
// retained topic keeps feeding an entity for something that no longer
// exists. Discarding from the front therefore threw away exactly the
// unrepeatable messages.
//
// Either way a drop must not be silent. It is counted in
// `publish_errors`, because a message that never reaches the broker is
// a failed publish however it failed.
func TestSecurityQueueDropsTheNewestAndSaysSo(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, Collector: col,
	}, &mockPublisher{})

	// Deliberately not Start()ed: with no worker draining the queue the
	// overflow is deterministic.
	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())

	first := securityMsg{kind: securityMsgRetract, topic: "openccu-loom/security/zone/gone"}
	p.enqueue(first)
	for i := range cap(p.msgCh) {
		p.enqueue(securityMsg{topic: "openccu-loom/security/filler", payload: []byte{byte(i)}})
	}

	if got := col.PublishErrors("").Value(); got == 0 {
		t.Error("a dropped message was not counted in publish_errors")
	}
	drained := make([]securityMsg, 0, cap(p.msgCh))
	for len(p.msgCh) > 0 {
		drained = append(drained, <-p.msgCh)
	}
	if len(drained) == 0 {
		t.Fatal("queue drained empty")
	}
	if drained[0].topic != first.topic || drained[0].kind != securityMsgRetract {
		t.Errorf("head of the queue = %+v, want the first-enqueued retraction %+v — the oldest message was discarded",
			drained[0], first)
	}
}

// TestSecurityReconcileEnqueuesRetractionsBeforeStates pins the order
// the drop-the-newest policy depends on.
//
// A retraction has no second attempt, so it must be queued ahead of the
// messages a later pass repairs. Reconcile used to enqueue it last,
// which put the one unrepeatable message at the end of the queue —
// exactly where the discard happens.
func TestSecurityReconcileEnqueuesRetractionsBeforeStates(t *testing.T) {
	t.Parallel()

	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true,
	}, &mockPublisher{})
	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())

	// A zone that carried retained state and has since disappeared.
	p.knownZones["keller"] = true
	p.knownClasses[hmenum.SecurityClassGas] = true

	p.reconcile()

	firstRetract, firstState := -1, -1
	i := 0
	for len(p.msgCh) > 0 {
		m := <-p.msgCh
		if m.kind == securityMsgRetract && firstRetract < 0 {
			firstRetract = i
		}
		if m.kind == securityMsgState && firstState < 0 {
			firstState = i
		}
		i++
	}
	if firstRetract < 0 {
		t.Fatal("reconcile enqueued no retraction for the gone zone/class")
	}
	if firstState < 0 {
		t.Fatal("reconcile enqueued no state")
	}
	if firstRetract > firstState {
		t.Errorf("first retraction at %d, first state at %d — a retraction must be queued before the states that a later pass can repair",
			firstRetract, firstState)
	}
}

// TestAlarmEventDropIsCounted pins the alarm plane's drop as observable
// too. It was logged and nothing more, so on a deployment that scrapes
// metrics rather than reading logs an alarm event vanished silently.
func TestAlarmEventDropIsCounted(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, Collector: col,
	}, &mockPublisher{})

	// Not Start()ed, so nothing drains eventCh.
	p := NewAlarmMQTTPublisher(nil, NewWiring(bridge, slog.Default()), slog.Default())
	for range cap(p.eventCh) + 1 {
		p.enqueueEvent("erdgeschoss", alarmEventPayload{Type: "TRIGGER", ZoneID: "erdgeschoss"})
	}
	if got := col.PublishErrors("").Value(); got == 0 {
		t.Error("a dropped alarm event was not counted in publish_errors")
	}
}

// TestAvailabilityIsAlwaysQoS1 pins one delivery guarantee across every
// availability topic this daemon writes.
//
// An availability marker is the one payload whose loss the next publish
// cannot repair: it is written on a flip, so a marker dropped at QoS 0
// leaves the entity in the state it last carried until something flips
// it again — and for the `offline` marker a crash suppresses the
// broker's last-will over, that is never. The device and alarm planes
// pinned QoS 1; the security plane used the state profile, which is
// QoS 0 in practice.
func TestAvailabilityIsAlwaysQoS1(t *testing.T) {
	t.Parallel()

	mp := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true,
	}, mp)
	// The state profile is QoS 0, which is what makes the divergence
	// observable at all.
	if bridge.cfg.QoS.State == QoS1 {
		t.Fatalf("fixture: state QoS is already 1, the test cannot show the divergence")
	}

	ctx := t.Context()
	if err := bridge.PublishSecurityAvailability(ctx, securityAvailabilityTopic("openccu-loom"), true); err != nil {
		t.Fatalf("PublishSecurityAvailability: %v", err)
	}
	if err := bridge.PublishAlarmAvailability(ctx, alarmAvailabilityTopic("openccu-loom", "erdgeschoss"), true); err != nil {
		t.Fatalf("PublishAlarmAvailability: %v", err)
	}
	if err := bridge.PublishAvailability(ctx, "ccu-01", "HmIP-RF", "000A", true); err != nil {
		t.Fatalf("PublishAvailability: %v", err)
	}

	seen := 0
	for _, rec := range mp.sent {
		if !strings.HasSuffix(rec.topic, "/availability") {
			continue
		}
		seen++
		if rec.qos != QoS1 {
			t.Errorf("%s published at QoS %v, want QoS 1", rec.topic, rec.qos)
		}
		if !rec.retain {
			t.Errorf("%s published non-retained, want retained", rec.topic)
		}
	}
	if seen != 3 {
		t.Fatalf("observed %d availability publishes, want 3", seen)
	}
}
