// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// bundleBodyWithout is a per-entity sensor body for the test node with one
// key dropped, so a test can build exactly the component Home Assistant
// refuses.
func bundleBodyWithout(t *testing.T, name, drop string, extra map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"name":        name,
		"unique_id":   "openccu-loom_" + name,
		"state_topic": "loom/ccu/dev/1/values/" + name,
		"device": map[string]any{
			"identifiers": []string{"openccu-loom_ccu_0001abc"},
			"name":        "Thermostat",
		},
		"origin": map[string]any{"name": "openccu-loom"},
	}
	delete(body, drop)
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return raw
}

// publishedComponents decodes the `components` map of the last device
// document written for node.
func publishedComponents(t *testing.T, pub *mockPublisher, node string) map[string]map[string]any {
	t.Helper()
	raw := lastPublishedOn(pub, "homeassistant/device/"+node+"/config")
	if raw == "" {
		t.Fatalf("no device document was published for %s", node)
	}
	var doc struct {
		Components map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("device document is not JSON: %v", err)
	}
	return doc.Components
}

// TestARefusedComponentIsWithheldAndNeverTombstoned pins the containment the
// publisher runtime is configured with, and what this side's removal path
// does with the component it withholds.
//
// A component without `unique_id` makes Home Assistant refuse the whole
// device document (homeassistant/components/mqtt/schemas.py:199-204), so
// published as rendered it would cost every sibling entity. Withheld, the
// siblings arrive. And because the store keeps the rendered component, the
// next document — written because a sibling changed — must again simply omit
// it: a platform-only entry there would make Home Assistant delete the entity
// and its registry entry, and an empty retraction of its per-entity config
// would do the same during a migration.
func TestARefusedComponentIsWithheldAndNeverTombstoned(t *testing.T) {
	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.Collector = col })
	ctx := context.Background()
	const node = "loom_ccu_0001abc"

	store := newDiscoveryBundleStore()
	mustPut(t, store, node, "temperature", "sensor", bundleComponent("temperature"))
	mustPut(t, store, node, "broken", "sensor", bundleBodyWithout(t, "broken", "unique_id", nil))

	if err := b.publishDeviceBundle(ctx, "ccu-01", node, store); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	first := publishedComponents(t, pub, node)
	if _, ok := first["temperature"]; !ok {
		t.Errorf("the valid sibling was not published: %v", first)
	}
	if got, ok := first["broken"]; ok {
		t.Errorf("the component Home Assistant refuses was published: %v", got)
	}
	if got := col.DiscoveryInvalid("ccu-01").Value(); got != 1 {
		t.Errorf("discovery_invalid after the first document = %d, want 1 (the withheld component)", got)
	}

	// A sibling changes; the document is rewritten.
	mustPut(t, store, node, "humidity", "sensor", bundleComponent("humidity"))
	if err := b.publishDeviceBundle(ctx, "ccu-01", node, store); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	second := publishedComponents(t, pub, node)
	if _, ok := second["humidity"]; !ok {
		t.Errorf("the new sibling was not published: %v", second)
	}
	if got, ok := second["broken"]; ok {
		t.Errorf("the withheld component reappeared in the next document as %v; "+
			"a platform-only entry is a tombstone that deletes the entity", got)
	}
	for _, rec := range pub.recorded() {
		if rec.topic == "homeassistant/sensor/"+node+"/broken/config" {
			t.Errorf("the withheld component's per-entity config was touched (payload %q); "+
				"an empty retraction there deletes the entity", rec.payload)
		}
	}
}

// TestAWarningCountsButIsPublished: a key no platform declares is stripped by
// Home Assistant (the discovery schemas run with extra=REMOVE_EXTRA), so the
// component is published as rendered — containment withholds only what is
// refused — and counted, because the counter means "refused or altered".
func TestAWarningCountsButIsPublished(t *testing.T) {
	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.Collector = col })
	const node = "loom_ccu_0001abc"

	store := newDiscoveryBundleStore()
	mustPut(t, store, node, "temperature", "sensor", bundleComponent("temperature"))
	mustPut(t, store, node, "odd", "sensor", bundleBodyWithout(t, "odd", "", map[string]any{"nonsense": 1}))

	if err := b.publishDeviceBundle(context.Background(), "ccu-01", node, store); err != nil {
		t.Fatalf("publish: %v", err)
	}
	comps := publishedComponents(t, pub, node)
	if got, ok := comps["odd"]; !ok || got["nonsense"] == nil {
		t.Errorf("the component with a strippable key was not published as rendered: %v", got)
	}
	if got := col.DiscoveryInvalid("ccu-01").Value(); got != 1 {
		t.Errorf("discovery_invalid = %d, want 1 (the component carrying the unknown key)", got)
	}
}
