// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

func bundleBridge(t *testing.T) (*Bridge, *mockPublisher) {
	t.Helper()
	return newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryBundles = true })
}

// TestBundleModeIsOffUnlessAskedFor. Switching it on migrates every retained
// config on the broker, so it must never arrive with an upgrade.
func TestBundleModeIsOffUnlessAskedFor(t *testing.T) {
	b, pub := newTestBridge(t)
	if b.bundles != nil {
		t.Fatal("bundle mode is on by default")
	}
	if err := b.publishDiscovery(context.Background(), "ccu-01", "sensor", "node", "temperature",
		bundleComponent("temperature")); err != nil {
		t.Fatalf("publishDiscovery: %v", err)
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sent) != 1 || pub.sent[0].topic != "homeassistant/sensor/node/temperature/config" {
		t.Errorf("want the per-entity topic, got %+v", pub.sent)
	}
}

// TestBundleModeDivertsTheSameCall: the eight producers are untouched. The
// same publishDiscovery call from the same call sites lands in a document.
func TestBundleModeDivertsTheSameCall(t *testing.T) {
	b, pub := bundleBridge(t)
	ctx := context.Background()

	for _, obj := range []string{"temperature", "humidity"} {
		if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", obj, bundleComponent(obj)); err != nil {
			t.Fatalf("publishDiscovery %s: %v", obj, err)
		}
	}

	pub.mu.Lock()
	sent := append([]publishRecord(nil), pub.sent...)
	pub.mu.Unlock()

	var bundles int
	for _, rec := range sent {
		if strings.HasPrefix(rec.topic, "homeassistant/sensor/") && rec.payload != "" {
			t.Errorf("a per-entity config was published in bundle mode: %q", rec.topic)
		}
		if rec.topic == "homeassistant/device/node/config" {
			bundles++
		}
	}
	if bundles == 0 {
		t.Fatal("no bundle was published")
	}

	last := sent[len(sent)-1]
	if last.topic != "homeassistant/device/node/config" {
		t.Fatalf("last publish = %q", last.topic)
	}
	var doc struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal([]byte(last.payload), &doc); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if len(doc.Components) != 2 {
		t.Errorf("bundle carries %d components, want both entities", len(doc.Components))
	}
}

// TestRetractionBecomesATombstone: in a document a removal is not an
// absence. Home Assistant deletes a component only when its entry is present
// and carries a platform alone; dropping the key leaves the entity forever.
func TestRetractionBecomesATombstone(t *testing.T) {
	b, pub := bundleBridge(t)
	ctx := context.Background()

	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "temperature",
		bundleComponent("temperature")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "humidity",
		bundleComponent("humidity")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// An empty payload is how every producer retracts an entity today.
	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "humidity", nil); err != nil {
		t.Fatalf("retract: %v", err)
	}

	pub.mu.Lock()
	last := pub.sent[len(pub.sent)-1]
	pub.mu.Unlock()

	var doc struct {
		Components map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal([]byte(last.payload), &doc); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	tomb, present := doc.Components["humidity"]
	if !present {
		t.Fatal("the retracted entity is absent from the document, so Home Assistant keeps it")
	}
	if len(tomb) != 1 || tomb["platform"] != "sensor" {
		t.Errorf("tombstone = %v, want the platform and nothing else", tomb)
	}
	if _, still := doc.Components["temperature"]; !still {
		t.Error("retracting one entity dropped the other")
	}
}

// TestAMalformedBodyIsReportedNotDropped: a body this daemon just marshalled
// and cannot read back is a bug here, not a broker problem. Dropping the
// entity silently is how one goes missing for a release.
func TestAMalformedBodyIsReportedNotDropped(t *testing.T) {
	b, _ := bundleBridge(t)
	err := b.publishDiscovery(context.Background(), "ccu-01", "sensor", "node", "temperature",
		[]byte("{not json"))
	if err == nil {
		t.Fatal("a body that does not parse was accepted")
	}
	if !strings.Contains(err.Error(), "node/temperature") {
		t.Errorf("error does not name the component: %v", err)
	}
}

// TestTheDocumentCarriesTheFrameOnce: every per-entity config repeats the
// device block, and a bundle must not.
func TestTheDocumentCarriesTheFrameOnce(t *testing.T) {
	b, pub := bundleBridge(t)
	if err := b.publishDiscovery(context.Background(), "ccu-01", "sensor", "node", "temperature",
		bundleComponent("temperature")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	pub.mu.Lock()
	last := pub.sent[len(pub.sent)-1]
	pub.mu.Unlock()

	var doc struct {
		Device     hadiscovery.DeviceInfo    `json:"device"`
		Origin     hadiscovery.Origin        `json:"origin"`
		Components map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal([]byte(last.payload), &doc); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if doc.Device.Name != "Thermostat" {
		t.Errorf("device block = %+v, want it at the top", doc.Device)
	}
	if doc.Origin.Name != "openccu-loom" {
		t.Errorf("origin = %+v, want it at the top", doc.Origin)
	}
	if _, dup := doc.Components["temperature"]["device"]; dup {
		t.Error("the component repeats the device block")
	}
}

// TestBatchingWritesEachDeviceOnce is the point of the batch. Without it the
// boot snapshot rewrites a device's whole document once per datapoint, and
// the document grows with every one.
func TestBatchingWritesEachDeviceOnce(t *testing.T) {
	b, pub := bundleBridge(t)
	ctx := context.Background()

	b.BeginBundleBatch()
	for _, obj := range []string{"a", "b", "c", "d"} {
		if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", obj, bundleComponent(obj)); err != nil {
			t.Fatalf("publishDiscovery %s: %v", obj, err)
		}
	}

	pub.mu.Lock()
	during := len(pub.sent)
	pub.mu.Unlock()
	if during != 0 {
		t.Fatalf("%d messages published during the batch, want none", during)
	}

	if err := b.FlushBundles(ctx); err != nil {
		t.Fatalf("FlushBundles: %v", err)
	}

	pub.mu.Lock()
	sent := append([]publishRecord(nil), pub.sent...)
	pub.mu.Unlock()

	bundles := 0
	for _, rec := range sent {
		if rec.topic == "homeassistant/device/node/config" {
			bundles++
		}
	}
	if bundles != 1 {
		t.Errorf("the document was written %d times, want once for four entities", bundles)
	}

	var doc struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal([]byte(sent[len(sent)-1].payload), &doc); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if len(doc.Components) != 4 {
		t.Errorf("the flushed document carries %d components, want all four", len(doc.Components))
	}
}

// TestFlushEndsTheBatch: a runtime change after boot must reach the broker
// when it happens, not wait for a flush nobody will call.
func TestFlushEndsTheBatch(t *testing.T) {
	b, pub := bundleBridge(t)
	ctx := context.Background()

	b.BeginBundleBatch()
	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "a", bundleComponent("a")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := b.FlushBundles(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	pub.mu.Lock()
	afterFlush := len(pub.sent)
	pub.mu.Unlock()

	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "b", bundleComponent("b")); err != nil {
		t.Fatalf("publish after flush: %v", err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sent) == afterFlush {
		t.Error("a publish after the flush was still batched")
	}
}

// TestAFailedFlushKeepsTheNodeDirty: one device whose publish fails must not
// abort the rest, and must not be forgotten either — a node dropped from the
// dirty set is a device with no discovery until something else changes it.
func TestAFailedFlushKeepsTheNodeDirty(t *testing.T) {
	b, pub := bundleBridge(t)
	ctx := context.Background()

	b.BeginBundleBatch()
	for _, node := range []string{"node_a", "node_b"} {
		if err := b.publishDiscovery(ctx, "ccu-01", "sensor", node, "x", bundleComponent("x")); err != nil {
			t.Fatalf("publish %s: %v", node, err)
		}
	}

	pub.err = errors.New("broker down")
	err := b.FlushBundles(ctx)
	if err == nil {
		t.Fatal("a failed flush reported success")
	}

	b.mu.Lock()
	dirty := len(b.bundleDirty)
	b.mu.Unlock()
	if dirty != 2 {
		t.Errorf("%d nodes still dirty, want both — a forgotten node never gets discovery", dirty)
	}

	// The broker comes back and a later flush completes the work.
	pub.err = nil
	b.BeginBundleBatch()
	if err := b.FlushBundles(ctx); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	published := map[string]bool{}
	for _, rec := range pub.sent {
		published[rec.topic] = true
	}
	for _, want := range []string{"homeassistant/device/node_a/config", "homeassistant/device/node_b/config"} {
		if !published[want] {
			t.Errorf("%s was never published after the broker recovered", want)
		}
	}
}

// TestBatchIsANoOpOutsideBundleMode so the boot path can call it
// unconditionally rather than branching on a mode it does not own.
func TestBatchIsANoOpOutsideBundleMode(t *testing.T) {
	b, pub := newTestBridge(t)
	ctx := context.Background()

	b.BeginBundleBatch()
	if err := b.publishDiscovery(ctx, "ccu-01", "sensor", "node", "a", bundleComponent("a")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	pub.mu.Lock()
	sent := len(pub.sent)
	pub.mu.Unlock()
	if sent != 1 {
		t.Errorf("per-entity mode published %d messages, want 1 — the batch swallowed it", sent)
	}
	if err := b.FlushBundles(ctx); err != nil {
		t.Errorf("FlushBundles outside bundle mode: %v", err)
	}
}

// TestRollbackClearsOurDocumentsBeforeAnythingIsPublished. Home Assistant's
// refusal is symmetric: a per-entity config published while a device
// document for the same entity is still retained is refused just as the
// reverse is, with the same silence. Measured on a live instance, ADR 0070.
func TestRollbackClearsOurDocumentsBeforeAnythingIsPublished(t *testing.T) {
	const ours = "homeassistant/device/ccu-a_000a/config"
	broker := newFilterKeyedBroker([]retainedMsg{
		{topic: ours, payload: []byte(`{"device":{"identifiers":["x"]},"components":{}}`)},
	})
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, broker).WithSubscriber(broker)

	n, err := bridge.RunBundleRollbackOnce(context.Background(), "ccu-a", 120*time.Millisecond)
	if err != nil {
		t.Fatalf("RunBundleRollbackOnce: %v", err)
	}
	broker.wg.Wait()
	if n != 1 {
		t.Errorf("cleared %d documents, want 1", n)
	}
	if !broker.evicted()[ours] {
		t.Errorf("our own retained document survived: %q", ours)
	}
}

// TestRollbackLeavesOtherIntegrationsAlone. A parallel Zigbee2MQTT on the
// same broker publishes device documents of its own, and clearing one is far
// worse than leaving ours in place.
func TestRollbackLeavesOtherIntegrationsAlone(t *testing.T) {
	const theirs = "homeassistant/device/0x00158d0001abcdef/config"
	broker := newFilterKeyedBroker([]retainedMsg{
		{topic: theirs, payload: []byte(`{"device":{"identifiers":["z"]},"components":{}}`)},
	})
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, broker).WithSubscriber(broker)

	n, err := bridge.RunBundleRollbackOnce(context.Background(), "ccu-a", 120*time.Millisecond)
	if err != nil {
		t.Fatalf("RunBundleRollbackOnce: %v", err)
	}
	broker.wg.Wait()
	if n != 0 {
		t.Errorf("cleared %d foreign documents, want none", n)
	}
	if broker.evicted()[theirs] {
		t.Errorf("cleared another integration's document: %q", theirs)
	}
}

// TestRollbackIsSilentInBundleMode: a daemon that is publishing documents
// must not spend its boot clearing them.
func TestRollbackIsSilentInBundleMode(t *testing.T) {
	broker := newFilterKeyedBroker([]retainedMsg{
		{topic: "homeassistant/device/ccu-a_000a/config", payload: []byte(`{"x":1}`)},
	})
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true, HADiscoveryBundles: true,
	}, broker).WithSubscriber(broker)

	n, err := bridge.RunBundleRollbackOnce(context.Background(), "ccu-a", 120*time.Millisecond)
	if err != nil || n != 0 {
		t.Errorf("n=%d err=%v, want a no-op in bundle mode", n, err)
	}
	if len(broker.evicted()) != 0 {
		t.Errorf("bundle mode cleared its own documents: %v", broker.evicted())
	}
}

// TestRollbackIgnoresAnAlreadyEmptyDocument: an empty retained payload is a
// topic the broker is already clearing, and retracting it again is a message
// for nothing on every single boot.
func TestRollbackIgnoresAnAlreadyEmptyDocument(t *testing.T) {
	broker := newFilterKeyedBroker([]retainedMsg{
		{topic: "homeassistant/device/ccu-a_000a/config", payload: nil},
	})
	bridge := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, broker).WithSubscriber(broker)

	n, err := bridge.RunBundleRollbackOnce(context.Background(), "ccu-a", 120*time.Millisecond)
	if err != nil {
		t.Fatalf("RunBundleRollbackOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("cleared %d already-empty documents, want none", n)
	}
}
