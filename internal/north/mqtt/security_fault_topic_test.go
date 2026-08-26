// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeSecuritySnapshotSource is the minimal [SecuritySnapshotSource] the
// fault-topic producer test needs. It never carries fault content: the
// test drives the publisher through bus events directly and only needs
// reconcile() (called on Start and on every state/class/zone/fault bus
// event) to have something coherent, if empty, to render.
type fakeSecuritySnapshotSource struct{}

func (fakeSecuritySnapshotSource) Snapshot() security.Snapshot { return security.Snapshot{} }

// waitForSecurityPublish polls mp for a publish to topic satisfying
// want, failing after a bounded deadline. The publisher only enqueues
// on its bus-handler goroutine; the actual broker write happens on a
// separate worker goroutine (SecurityMQTTPublisher.run), so a caller
// cannot assume synchronous delivery even though bus dispatch itself is
// synchronous in the uncontended case.
func waitForSecurityPublish(t *testing.T, mp *mockPublisher, topic string, want func(publishRecord) bool) publishRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last publishRecord
	var lastOK bool
	for time.Now().Before(deadline) {
		mp.mu.Lock()
		for _, rec := range mp.sent {
			if rec.topic != topic {
				continue
			}
			last, lastOK = rec, true
			if want(rec) {
				mp.mu.Unlock()
				return rec
			}
		}
		mp.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	if lastOK {
		t.Fatalf("publish to %s never matched; last seen payload=%q", topic, last.payload)
	} else {
		t.Fatalf("no publish to %s observed within timeout", topic)
	}
	return publishRecord{}
}

// countSecurityPublishes returns how many recorded publishes carry topic.
func countSecurityPublishes(mp *mockPublisher, topic string) int {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	n := 0
	for _, rec := range mp.sent {
		if rec.topic == topic {
			n++
		}
	}
	return n
}

// TestSecurityFaultTopicHasExactlyOneProducer pins the M5 MQTT fix.
//
// The fault event topic (`<base>/security/fault`) must have exactly one
// producer: onNotification, rendering the domain's own report. Before
// this fix, onFaultChanged — a second, independent handler for the raw
// ledger transition — wrote its own, differently-shaped payload
// (fault_id, open_count, no text) to the SAME topic that onNotification
// writes a rendered report to (subject, message, sources, link, no
// id). Every raise and clear therefore arrived twice with two different
// shapes, so a consumer's event entity — which parses one shape per
// topic — got the fields it needs on only half the messages, and got
// an acknowledgement re-announced as a fresh "raised" verb on top of
// that.
//
// This locks the invariant going forward: a bare
// SecurityFaultChangedEvent (the ledger transition alone) must produce
// no message on the topic, while a SecurityNotificationEvent{Fault:
// true} (the rendered report onFaultChanged now defers to) must.
func TestSecurityFaultTopicHasExactlyOneProducer(t *testing.T) {
	t.Parallel()
	bridge, mp := newTestBridge(t, func(cfg *BridgeConfig) { cfg.HADiscoveryEnabled = false })
	wiring := NewWiring(bridge, slog.Default())
	pub := NewSecurityMQTTPublisher(fakeSecuritySnapshotSource{}, wiring, "en", "", slog.Default())
	bus := events.NewBus()
	pub.Start(bus)
	t.Cleanup(pub.Stop)

	faultTopic := securityStateTopic("openccu-loom", "fault")

	// Let the initial reconcile (Start's catch-up publish) settle before
	// taking the baseline; the fault-topic count must not include it.
	waitForSecurityPublish(t, mp, securityAvailabilityTopic("openccu-loom"), func(r publishRecord) bool { return r.payload == "online" })
	before := countSecurityPublishes(mp, faultTopic)

	events.Publish(bus, hmevent.SecurityFaultChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()), FaultID: "f1", Class: hmenum.SecurityClassTechnical,
		Reason: hmenum.SecurityFaultReasonUnreachable, Severity: hmenum.SecuritySeverityInfo,
		Open: true, OpenCount: 1,
	})
	// onFaultChanged only calls reconcile(); give the worker goroutine a
	// moment to drain whatever reconcile enqueued (retained aggregates,
	// never the fault topic) before checking nothing new landed there.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := countSecurityPublishes(mp, faultTopic); after != before {
		t.Fatalf("SecurityFaultChangedEvent alone produced %d new publish(es) to %s, want 0 — onFaultChanged must only reconcile the retained plane, never write the event topic itself", after-before, faultTopic)
	}

	events.Publish(bus, hmevent.SecurityNotificationEvent{
		Base: hmevent.NewBaseAt(time.Now()), Class: hmenum.SecurityClassTechnical,
		Severity: hmenum.SecuritySeverityInfo, Verb: hmenum.SecurityVerbRaised,
		Subject: "Sensor unreachable", Message: "Device 1 is unreachable.",
		Fault: true, Retainable: true,
	})
	rec := waitForSecurityPublish(t, mp, faultTopic, func(r publishRecord) bool { return true })
	if rec.retain {
		t.Errorf("the fault event-topic publish must not be retained; got retain=true")
	}
}
