// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// discoveryCall is one publishDiscovery call as the producing planes make it.
type discoveryCall struct {
	central   string
	component string
	nodeID    string
	objectID  string
	body      []byte
}

// modeParityCalls is a cross-section of what the eight producing planes emit:
// several entities on one device, two devices, a hub node, a daemon-level
// plane, a platform other than sensor, and a retraction.
func modeParityCalls() []discoveryCall {
	dev := func(node, name string) *hadiscovery.DeviceInfo {
		return &hadiscovery.DeviceInfo{Identifiers: []string{node}, Name: name, Manufacturer: "eQ-3"}
	}
	body := func(node, obj string, c hadiscovery.Component) []byte {
		c.UniqueID = node + "_" + obj
		c.Device = dev(node, node)
		c.Origin = &hadiscovery.Origin{Name: "openccu-loom", SW: "0.77.0"}
		raw, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			panic(err)
		}
		delete(m, "platform")
		raw, err = json.Marshal(m)
		if err != nil {
			panic(err)
		}
		return raw
	}

	return []discoveryCall{
		{"ccu-a", "sensor", "ccu-a_000a", "1_temperature", body("ccu-a_000a", "1_temperature", hadiscovery.Component{
			Name: "Temperature", StateTopic: "loom/ccu-a/000A/1/values/ACTUAL_TEMPERATURE",
			UnitOfMeasure: "°C", DeviceClass: "temperature", Precision: hadiscovery.Ptr(1),
		})},
		{"ccu-a", "number", "ccu-a_000a", "1_level", body("ccu-a_000a", "1_level", hadiscovery.Component{
			Name: "Level", StateTopic: "loom/ccu-a/000A/1/values/LEVEL",
			CommandTopic: "loom/ccu-a/000A/1/values/LEVEL/set",
			Min:          hadiscovery.Ptr(0.0), Max: hadiscovery.Ptr(100.0),
			Extra: map[string]any{"translation_key": "pipe_level"},
		})},
		{"ccu-a", "binary_sensor", "ccu-a_000a", "0_unreach", body("ccu-a_000a", "0_unreach", hadiscovery.Component{
			Name: "Unreach", StateTopic: "loom/ccu-a/000A/0/values/UNREACH",
			DeviceClass: "problem", EntityCategory: "diagnostic",
		})},
		{"ccu-a", "switch", "ccu-a_000b", "1_state", body("ccu-a_000b", "1_state", hadiscovery.Component{
			Name: "State", StateTopic: "loom/ccu-a/000B/1/values/STATE",
			CommandTopic: "loom/ccu-a/000B/1/values/STATE/set",
		})},
		{"", "sensor", "ccu-a_sysvars", "anwesenheit", body("ccu-a_sysvars", "anwesenheit", hadiscovery.Component{
			Name: "Anwesenheit", StateTopic: "loom/ccu-a/sysvar/anwesenheit",
		})},
		{"", "binary_sensor", "openccu-loom_security", "class_smoke", body("openccu-loom_security", "class_smoke", hadiscovery.Component{
			Name: "Smoke", StateTopic: "loom/security/class/smoke", DeviceClass: "smoke",
		})},
		// A retraction: the entity existed and goes away.
		{"ccu-a", "sensor", "ccu-a_000a", "1_retired", body("ccu-a_000a", "1_retired", hadiscovery.Component{
			Name: "Retired", StateTopic: "loom/ccu-a/000A/1/values/RETIRED",
		})},
		{"ccu-a", "sensor", "ccu-a_000a", "1_retired", nil},
	}
}

// entityView is what Home Assistant ends up holding for one entity: the
// platform it renders as, and the body it renders from. Which topic carried
// it there is exactly the thing the two modes disagree about, so it is not
// part of the view.
type entityView struct {
	platform string
	body     map[string]any
}

// collectPerEntity replays the calls through a bridge in per-entity mode and
// reads the entities back off the wire.
func collectPerEntity(t *testing.T, calls []discoveryCall) map[string]entityView {
	t.Helper()
	b, pub := newTestBridge(t)
	ctx := context.Background()
	for _, c := range calls {
		if err := b.publishDiscovery(ctx, c.central, c.component, c.nodeID, c.objectID, c.body); err != nil {
			t.Fatalf("per-entity publish %s/%s: %v", c.nodeID, c.objectID, err)
		}
	}

	out := map[string]entityView{}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, rec := range pub.sent {
		parts := strings.Split(strings.TrimPrefix(rec.topic, "homeassistant/"), "/")
		if len(parts) != 4 {
			t.Fatalf("unexpected topic in per-entity mode: %q", rec.topic)
		}
		key := parts[1] + "/" + parts[2]
		if rec.payload == "" {
			delete(out, key)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(rec.payload), &body); err != nil {
			t.Fatalf("unmarshal %s: %v", rec.topic, err)
		}
		out[key] = entityView{platform: parts[0], body: body}
	}
	return out
}

// collectBundles replays the same calls in device-bundle mode and reads the
// entities back out of the documents.
func collectBundles(t *testing.T, calls []discoveryCall) map[string]entityView {
	t.Helper()
	b, pub := newTestBridge(t, func(c *BridgeConfig) { c.HADiscoveryBundles = true })
	ctx := context.Background()
	b.BeginBundleBatch()
	for _, c := range calls {
		if err := b.publishDiscovery(ctx, c.central, c.component, c.nodeID, c.objectID, c.body); err != nil {
			t.Fatalf("bundle publish %s/%s: %v", c.nodeID, c.objectID, err)
		}
	}
	if err := b.FlushBundles(ctx); err != nil {
		t.Fatalf("FlushBundles: %v", err)
	}

	// Last document per node wins, exactly as a retained topic does.
	latest := map[string]string{}
	pub.mu.Lock()
	for _, rec := range pub.sent {
		if !strings.HasPrefix(rec.topic, "homeassistant/device/") {
			continue
		}
		latest[rec.topic] = rec.payload
	}
	pub.mu.Unlock()

	out := map[string]entityView{}
	for topic, payload := range latest {
		node := strings.TrimSuffix(strings.TrimPrefix(topic, "homeassistant/device/"), "/config")
		var doc struct {
			Components map[string]map[string]any `json:"components"`
			Device     map[string]any            `json:"device"`
			Origin     map[string]any            `json:"origin"`
		}
		if err := json.Unmarshal([]byte(payload), &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", topic, err)
		}
		for obj, comp := range doc.Components {
			platform, _ := comp["platform"].(string)
			body := map[string]any{}
			for k, v := range comp {
				if k != "platform" {
					body[k] = v
				}
			}
			if len(body) == 0 {
				// A platform-only entry is a tombstone: Home Assistant
				// removes the entity, so it must not appear in the view.
				continue
			}
			// The frame lives at the top of a document and on every config
			// in the other form. Put it back so the two are comparable.
			body["device"] = doc.Device
			body["origin"] = doc.Origin
			out[node+"/"+obj] = entityView{platform: platform, body: body}
		}
	}
	return out
}

// TestBothDiscoveryModesDescribeTheSameEntities is the invariant the whole of
// step 13 rests on, and the one the operator was promised: switching the mode
// changes which topics carry the entities, and nothing about the entities.
//
// It compares what Home Assistant ends up holding — platform, unique id and
// every other key — rather than the bytes on any one topic, because the topic
// is precisely what the two modes are allowed to disagree about.
func TestBothDiscoveryModesDescribeTheSameEntities(t *testing.T) {
	calls := modeParityCalls()
	perEntity := collectPerEntity(t, calls)
	bundled := collectBundles(t, calls)

	if len(perEntity) == 0 {
		t.Fatal("the per-entity run produced nothing; every comparison below would pass vacuously")
	}

	keys := func(m map[string]entityView) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if a, b := keys(perEntity), keys(bundled); !reflect.DeepEqual(a, b) {
		t.Fatalf("the two modes describe different entities\nper-entity: %v\nbundled:   %v", a, b)
	}

	for _, key := range keys(perEntity) {
		a, b := perEntity[key], bundled[key]
		if a.platform != b.platform {
			t.Errorf("%s: platform %q vs %q", key, a.platform, b.platform)
		}
		if !reflect.DeepEqual(a.body, b.body) {
			t.Errorf("%s: body differs\nper-entity: %v\nbundled:   %v", key, a.body, b.body)
		}
	}
}

// TestTheRetractedEntityIsGoneFromBothModes: the retraction in the fixture is
// there to be checked, not just to be survived. A tombstone that Home
// Assistant does not read as a deletion is the failure mode that leaves an
// entity on screen forever.
func TestTheRetractedEntityIsGoneFromBothModes(t *testing.T) {
	calls := modeParityCalls()
	for _, m := range []map[string]entityView{collectPerEntity(t, calls), collectBundles(t, calls)} {
		if _, still := m["ccu-a_000a/1_retired"]; still {
			t.Error("the retracted entity is still described")
		}
	}
}
