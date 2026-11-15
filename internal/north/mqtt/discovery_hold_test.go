// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"testing"
)

const (
	holdCentral = "ccu01"
	holdNode    = "ccu01_dev1"
	holdAddr    = "DEV1"
)

// emptiesTo counts the empty (retracting) publishes to topic.
func emptiesTo(pub *mockPublisher, topic string) int {
	pub.mu.Lock()
	defer pub.mu.Unlock()
	n := 0
	for _, rec := range pub.sent {
		if rec.topic == topic && rec.payload == "" {
			n++
		}
	}
	return n
}

// TestAHeldConfigIsCancelledByAnIdenticalRepublish pins where the hold is
// settled: at the entry of the discovery funnel, before the dedup gate. A
// reloaded model republishes an unchanged config byte for byte, the runtime
// does not send it, and the config is still one the model declares — so
// only the config nobody republished is retracted.
func TestAHeldConfigIsCancelledByAnIdenticalRepublish(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	ctx := context.Background()
	kept := b.topics.DiscoveryConfig("sensor", holdNode, "temperature")
	gone := b.topics.DiscoveryConfig("sensor", holdNode, "humidity")
	for _, obj := range []string{"temperature", "humidity"} {
		if err := b.publishDiscovery(ctx, holdCentral, "sensor", holdNode, obj, bundleComponent(obj)); err != nil {
			t.Fatalf("publish %s: %v", obj, err)
		}
	}

	if n := b.HoldDiscoveryForCentralDevice(holdCentral, holdAddr); n != 2 {
		t.Fatalf("held %d configs, want 2", n)
	}
	if emptiesTo(pub, kept)+emptiesTo(pub, gone) != 0 {
		t.Fatal("holding retracted a config")
	}
	if err := b.publishDiscovery(ctx, holdCentral, "sensor", holdNode, "temperature", bundleComponent("temperature")); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if n := b.RetractHeldDiscovery(ctx, holdCentral, DeviceHoldGroup(holdAddr)); n != 1 {
		t.Errorf("retracted %d held configs, want 1", n)
	}
	if emptiesTo(pub, kept) != 0 {
		t.Error("the config the reloaded model republished unchanged was retracted")
	}
	if emptiesTo(pub, gone) != 1 {
		t.Error("the config the reloaded model no longer declares was not retracted")
	}
	if g := b.HeldDiscoveryGroups(holdCentral); len(g) != 0 {
		t.Errorf("groups still held after the retraction: %v", g)
	}
}

// TestADroppedHoldRetractsNothing: a group the caller found still right ends
// without a single empty publish.
func TestADroppedHoldRetractsNothing(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	ctx := context.Background()
	topic := b.topics.DiscoveryConfig("sensor", holdNode, "temperature")
	if err := b.publishDiscovery(ctx, holdCentral, "sensor", holdNode, "temperature", bundleComponent("temperature")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	b.HoldDiscoveryForCentralDevice(holdCentral, holdAddr)
	if n := b.DropHeldDiscovery(holdCentral, DeviceHoldGroup(holdAddr)); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	b.RetractHeldDiscovery(ctx, holdCentral, DeviceHoldGroup(holdAddr))
	if emptiesTo(pub, topic) != 0 {
		t.Error("a dropped hold was retracted")
	}
}

// TestAHoldSurvivesTheRuntimeGateReset: a broker reconnect resets the
// runtime gates for the reseed; a deferred retraction is not a gate, and
// forgetting it would leave a config the reloaded model dropped on the
// broker until the next boot.
func TestAHoldSurvivesTheRuntimeGateReset(t *testing.T) {
	t.Parallel()
	b, _ := newTestBridge(t)
	if err := b.publishDiscovery(context.Background(), holdCentral, "sensor", holdNode, "temperature", bundleComponent("temperature")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	b.HoldDiscoveryForCentralDevice(holdCentral, holdAddr)
	b.ResetRuntimeGates()
	if g := b.HeldDiscoveryGroups(holdCentral); len(g) != 1 || g[0] != DeviceHoldGroup(holdAddr) {
		t.Errorf("held groups after the reset = %v", g)
	}
}

// TestHeldBundleComponentIsRetractedAsATombstone: in bundle mode the rest of
// a surviving device leaves as a platform-only entry in the node's document,
// while the component the model republished stays live.
func TestHeldBundleComponentIsRetractedAsATombstone(t *testing.T) {
	t.Parallel()
	b, pub := bundleBridge(t)
	ctx := context.Background()
	for _, obj := range []string{"temperature", "humidity"} {
		if err := b.publishDiscovery(ctx, holdCentral, "sensor", holdNode, obj, bundleComponent(obj)); err != nil {
			t.Fatalf("publish %s: %v", obj, err)
		}
	}
	if n := b.HoldDiscoveryForCentralDevice(holdCentral, holdAddr); n != 2 {
		t.Fatalf("held %d components, want 2", n)
	}
	if err := b.publishDiscovery(ctx, holdCentral, "sensor", holdNode, "temperature", bundleComponent("temperature")); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if n := b.RetractHeldDiscovery(ctx, holdCentral, DeviceHoldGroup(holdAddr)); n != 1 {
		t.Fatalf("retracted %d, want 1", n)
	}

	pub.mu.Lock()
	last := pub.sent[len(pub.sent)-1]
	pub.mu.Unlock()
	var doc struct {
		Components map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal([]byte(last.payload), &doc); err != nil {
		t.Fatalf("unmarshal bundle %q: %v", last.topic, err)
	}
	if tomb := doc.Components["humidity"]; len(tomb) != 1 || tomb["platform"] != "sensor" {
		t.Errorf("humidity = %v, want a tombstone", tomb)
	}
	if live := doc.Components["temperature"]; len(live) <= 1 {
		t.Errorf("temperature = %v, want the live component", live)
	}
}

// TestAHeldHubItemIsSettledByItsDeclare: hub configs reach the funnel with
// no central, so the hold is keyed by the topic alone and a declare of the
// same item settles it whatever central label it carries.
func TestAHeldHubItemIsSettledByItsDeclare(t *testing.T) {
	t.Parallel()
	b, pub := newTestBridge(t)
	ctx := context.Background()
	item := DiscoveryItem{Component: "sensor", NodeID: "ccu01_hub", ObjectID: "sysvar_1", Payload: bundleComponent("sv"), OK: true}
	other := DiscoveryItem{Component: "sensor", NodeID: "ccu01_hub", ObjectID: "sysvar_2", Payload: bundleComponent("sv2"), OK: true}
	for _, it := range []DiscoveryItem{item, other} {
		if err := b.PublishHubDiscovery(ctx, it); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	b.HoldHubDiscovery(holdCentral, "hub/sysvar/1", []DiscoveryItem{item})
	b.HoldHubDiscovery(holdCentral, "hub/sysvar/2", []DiscoveryItem{other})
	if err := b.PublishHubDiscovery(ctx, item); err != nil {
		t.Fatalf("redeclare: %v", err)
	}
	if g := b.HeldDiscoveryGroups(holdCentral); len(g) != 1 || g[0] != "hub/sysvar/2" {
		t.Fatalf("held groups = %v, want only the undeclared sysvar", g)
	}
	b.RetractHeldDiscovery(ctx, holdCentral, "hub/sysvar/2")
	if emptiesTo(pub, b.topics.DiscoveryConfig("sensor", "ccu01_hub", "sysvar_2")) != 1 {
		t.Error("the undeclared hub config was not retracted")
	}
}
