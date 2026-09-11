// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func bundleStoreWith(nodeID string, objects ...string) *discoveryBundleStore {
	s := newDiscoveryBundleStore()
	for _, o := range objects {
		s.Put(nodeID, o, "sensor", bundleComponent(o))
	}
	return s
}

// TestBundlePublishRetractsBeforeItPublishes is the whole point of this
// file. Measured against a live Home Assistant (ADR 0070, amendment
// 2026-09-10): a bundle published while a per-entity config for the same
// unique id is still retained is refused, and the only sign is a warning in
// the consumer's log.
func TestBundlePublishRetractsBeforeItPublishes(t *testing.T) {
	b, pub := newTestBridge(t)
	store := bundleStoreWith("loom_ccu_0001abc", "temperature", "humidity")

	if err := b.publishDeviceBundle(context.Background(), "ccu-01", "loom_ccu_0001abc", store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}

	pub.mu.Lock()
	sent := append([]publishRecord(nil), pub.sent...)
	pub.mu.Unlock()

	if len(sent) != 3 {
		t.Fatalf("want two retractions and one bundle, got %d: %+v", len(sent), sent)
	}
	for i, want := range []string{
		"homeassistant/sensor/loom_ccu_0001abc/humidity/config",
		"homeassistant/sensor/loom_ccu_0001abc/temperature/config",
	} {
		if sent[i].topic != want {
			t.Errorf("publish %d = %q, want the superseded %q", i, sent[i].topic, want)
		}
		if sent[i].payload != "" {
			t.Errorf("publish %d carries a payload, a retraction must be empty", i)
		}
		if !sent[i].retain {
			t.Errorf("publish %d is not retained, so the broker keeps the old config", i)
		}
	}
	last := sent[len(sent)-1]
	if last.topic != "homeassistant/device/loom_ccu_0001abc/config" {
		t.Errorf("last publish = %q, want the bundle", last.topic)
	}
	if last.payload == "" {
		t.Error("the bundle payload is empty")
	}
}

// TestAFailedRetractionDoesNotPublishTheBundle. Between the retraction and
// the bundle the entity is absent, not merely unavailable. Pressing on after
// a failed retraction risks the one outcome worse than not starting: the old
// config gone and the new one refused anyway.
func TestAFailedRetractionDoesNotPublishTheBundle(t *testing.T) {
	b, pub := newTestBridge(t)
	sentinel := errors.New("broker down")
	pub.err = sentinel

	store := bundleStoreWith("node", "temperature")
	err := b.publishDeviceBundle(context.Background(), "ccu-01", "node", store)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the broker error", err)
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, rec := range pub.sent {
		if rec.topic == "homeassistant/device/node/config" {
			t.Error("the bundle went out after the retraction failed")
		}
	}
}

// TestAnUnchangedBundleTouchesNothing: the dedup gate is checked before the
// retraction, so a repeat publish does not tear down the per-entity topics a
// second time. On a steady-state boot this is the common path.
func TestAnUnchangedBundleTouchesNothing(t *testing.T) {
	b, pub := newTestBridge(t)
	store := bundleStoreWith("node", "temperature")
	ctx := context.Background()

	if err := b.publishDeviceBundle(ctx, "ccu-01", "node", store); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	pub.mu.Lock()
	first := len(pub.sent)
	pub.mu.Unlock()

	if err := b.publishDeviceBundle(ctx, "ccu-01", "node", store); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	pub.mu.Lock()
	second := len(pub.sent)
	pub.mu.Unlock()

	if second != first {
		t.Errorf("an unchanged bundle published again: %d → %d messages", first, second)
	}
}

// TestRetractionLeavesNoClaimBehind: an empty payload is a retraction, not a
// declaration. A topic left in `declared` would keep a retracted entity in
// the set the orphan sweeps treat as live, and the Home-Assistant-birth
// replay would re-publish the empty payload to a topic the broker no longer
// retains.
func TestRetractionLeavesNoClaimBehind(t *testing.T) {
	b, _ := newTestBridge(t)
	superseded := "homeassistant/sensor/node/temperature/config"

	b.mu.Lock()
	b.declared[superseded] = []byte(`{"unique_id":"x"}`)
	b.announced[superseded] = true
	b.mu.Unlock()

	store := bundleStoreWith("node", "temperature")
	if err := b.publishDeviceBundle(context.Background(), "ccu-01", "node", store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, still := b.declared[superseded]; still {
		t.Error("the superseded topic is still declared")
	}
	if b.announced[superseded] {
		t.Error("the superseded topic is still announced")
	}
	if _, ok := b.declared["homeassistant/device/node/config"]; !ok {
		t.Error("the bundle was not recorded as declared")
	}
}

// TestBundleIsClaimedBeforeItReachesTheBroker: the broker fans a message out
// to its subscribers — the orphan sweep's own snapshot subscription
// included — before Publish returns, so a claim taken afterwards arrives too
// late to keep the sweep off a document this daemon is publishing right now.
func TestBundleIsClaimedBeforeItReachesTheBroker(t *testing.T) {
	b, pub := newTestBridge(t)
	const topic = "homeassistant/device/node/config"

	claimedDuringPublish := false
	pub.onPublish = func(t string) {
		if t != topic {
			return
		}
		b.mu.Lock()
		claimedDuringPublish = b.announced[topic]
		b.mu.Unlock()
	}

	store := bundleStoreWith("node", "temperature")
	if err := b.publishDeviceBundle(context.Background(), "ccu-01", "node", store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}
	if !claimedDuringPublish {
		t.Error("the bundle was not claimed before it reached the broker")
	}
}

// TestDiscoveryDisabledPublishesNothing: the bundle path honours the same
// switch as every other discovery publish, so turning discovery off does not
// leave one plane still writing to `homeassistant/`.
func TestDiscoveryDisabledPublishesNothing(t *testing.T) {
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryEnabled = false })
	store := bundleStoreWith("node", "temperature")

	if err := b.publishDeviceBundle(context.Background(), "ccu-01", "node", store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sent) != 0 {
		t.Errorf("published %d messages with discovery off", len(pub.sent))
	}
}

// TestCountersAreLabelledWithTheCentralThatOwnsTheNode. This daemon serves
// several CCUs, and every counter it raises is per central — a bundle
// attributed to the wrong one makes "how many discovery configs did ccu-b
// send" unanswerable. The node id does not carry the central in a form worth
// parsing, so the caller passes it, and this is what checks it arrives.
func TestCountersAreLabelledWithTheCentralThatOwnsTheNode(t *testing.T) {
	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	b, _ := newTestBridge(t, func(c *BridgeConfig) { c.Collector = col })

	ctx := context.Background()
	if err := b.publishDeviceBundle(ctx, "ccu-a", "node_a", bundleStoreWith("node_a", "temperature")); err != nil {
		t.Fatalf("ccu-a: %v", err)
	}
	if err := b.publishDeviceBundle(ctx, "ccu-b", "node_b", bundleStoreWith("node_b", "temperature")); err != nil {
		t.Fatalf("ccu-b: %v", err)
	}

	if got := col.DiscoverySent("ccu-a").Value(); got != 1 {
		t.Errorf("discovery_sent{ccu-a} = %d, want 1", got)
	}
	if got := col.DiscoverySent("ccu-b").Value(); got != 1 {
		t.Errorf("discovery_sent{ccu-b} = %d, want 1", got)
	}
}
