// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// TestRetractionLeavesNoClaimBehind: an empty payload is a retraction, not
// a declaration. A superseded topic left in the claim set would keep a
// cleared entity in the set the orphan sweeps treat as live, and the
// Home-Assistant-birth replay would re-publish the empty payload to a topic
// the broker no longer retains.
func TestRetractionLeavesNoClaimBehind(t *testing.T) {
	b, pub := newTestBridge(t)
	superseded := "homeassistant/sensor/node/temperature/config"
	seedDeclared(t, b, superseded, []byte(`{"unique_id":"x"}`))

	store := bundleStoreWith("node", "temperature")
	if err := b.publishDeviceBundle(context.Background(), "ccu-01", "node", store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}

	if isDeclared(b, superseded) {
		t.Error("the superseded topic is still declared")
	}
	if !isDeclared(b, "homeassistant/device/node/config") {
		t.Error("the bundle was not recorded as declared")
	}

	// The observable half of "no claim behind": a birth replay must not
	// write the superseded topic again.
	pub.reset()
	if err := b.RepublishDiscovery(context.Background()); err != nil {
		t.Fatalf("RepublishDiscovery: %v", err)
	}
	for _, rec := range pub.publications() {
		if rec.topic == superseded {
			t.Error("the birth replay resurrected the superseded topic")
		}
	}
}

// TestSupersededTopicsAreRetractedOncePerProcess pins the one thing the
// move up changed here on purpose.
//
// This daemon used to retract the superseded per-entity topics on every
// change of the document. After the first retraction the broker holds
// nothing there, so a second is a message for nothing — and a boot that
// rewrites a sixteen-entity device forty times sent forty rounds of them.
// The shared runtime retracts each superseded topic once per process
// instead.
func TestSupersededTopicsAreRetractedOncePerProcess(t *testing.T) {
	b, pub := newTestBridge(t)
	ctx := context.Background()
	store := bundleStoreWith("node", "temperature")

	if err := b.publishDeviceBundle(ctx, "ccu-01", "node", store); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	pub.reset()

	// Change the document, so the dedup gate does not swallow the second
	// publish, and publish again.
	mustPut(t, store, "node", "humidity", "sensor", bundleComponent("humidity"))
	if err := b.publishDeviceBundle(ctx, "ccu-01", "node", store); err != nil {
		t.Fatalf("second publish: %v", err)
	}

	const alreadyCleared = "homeassistant/sensor/node/temperature/config"
	for _, rec := range pub.publications() {
		if rec.topic == alreadyCleared && rec.payload == "" {
			t.Error("a superseded topic was retracted a second time")
		}
	}
	// The new component's own per-entity topic has never been cleared, so
	// this publish is the one that must do it.
	var sawNew bool
	for _, rec := range pub.publications() {
		if rec.topic == "homeassistant/sensor/node/humidity/config" && rec.payload == "" {
			sawNew = true
		}
	}
	if !sawNew {
		t.Error("the newly added component's per-entity config was never retracted")
	}
}

// TestBundleIsClaimedBeforeItReachesTheBroker: the broker fans a message out
// to its subscribers — the orphan sweep's own snapshot subscription
// included — before Publish returns, so a claim taken afterwards arrives too
// late to keep the sweep off a document this daemon is publishing right now.
//
// The claim itself is not observable from here any more; it lives inside
// the shared runtime. What is observable is the thing it exists for, and
// this drives it directly: a sweep window is open, and the broker delivers
// the bundle to it from inside the very Publish call that writes it.
func TestBundleIsClaimedBeforeItReachesTheBroker(t *testing.T) {
	const (
		nodeID = "ccu-a_000a"
		topic  = "homeassistant/device/" + nodeID + "/config"
	)
	broker := newFanoutBroker()
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true, HADiscoveryBundles: true,
	}, broker).WithSubscriber(broker)
	broker.fanout = topic

	sweepDone := make(chan error, 1)
	go func() {
		_, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 300*time.Millisecond)
		sweepDone <- err
	}()
	broker.waitForSubscription(t)

	store := bundleStoreWith(nodeID, "temperature")
	if err := b.publishDeviceBundle(context.Background(), "ccu-a", nodeID, store); err != nil {
		t.Fatalf("publishDeviceBundle: %v", err)
	}
	if err := <-sweepDone; err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if broker.evicted()[topic] {
		t.Error("a sweep running across the publish retracted the document it was writing")
	}
}

// fanoutBroker delivers one nominated topic back to the open subscription
// from inside the Publish call that writes it — which is what a real broker
// does, and the reason the claim has to be taken first.
type fanoutBroker struct {
	mu         sync.Mutex
	handlers   map[string]MessageHandler
	published  []publishedMsg
	subscribed chan struct{}
	fanout     string
}

func newFanoutBroker() *fanoutBroker {
	return &fanoutBroker{handlers: map[string]MessageHandler{}, subscribed: make(chan struct{}, 4)}
}

func (b *fanoutBroker) Publish(_ context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	b.mu.Lock()
	b.published = append(b.published, publishedMsg{topic: topic, payload: payload, retain: retain})
	var deliver []MessageHandler
	if topic == b.fanout && len(payload) > 0 {
		for _, h := range b.handlers {
			deliver = append(deliver, h)
		}
	}
	b.mu.Unlock()
	for _, h := range deliver {
		h(&Message{Topic: topic, Payload: payload, Retain: true})
	}
	return nil
}

func (b *fanoutBroker) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	b.mu.Lock()
	b.handlers[filter] = handler
	b.mu.Unlock()
	select {
	case b.subscribed <- struct{}{}:
	default:
	}
	return SubscribeResult{}, nil
}

func (b *fanoutBroker) Unsubscribe(_ context.Context, filter string) error {
	b.mu.Lock()
	delete(b.handlers, filter)
	b.mu.Unlock()
	return nil
}

func (b *fanoutBroker) waitForSubscription(t *testing.T) {
	t.Helper()
	select {
	case <-b.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweep never opened its snapshot window")
	}
}

func (b *fanoutBroker) evicted() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for _, p := range b.published {
		if p.retain && len(p.payload) == 0 {
			out[p.topic] = true
		}
	}
	return out
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
