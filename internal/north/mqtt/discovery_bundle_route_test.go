// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
