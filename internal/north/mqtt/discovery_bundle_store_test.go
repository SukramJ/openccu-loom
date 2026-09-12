// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"reflect"
	"testing"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hapublisher "github.com/SukramJ/go-hamqtt/publisher"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// bundleComponent is the per-entity body a producer marshals today: the
// entity's own keys plus the frame repeated in every config.
func bundleComponent(name string) []byte {
	return marshalComponent(hadiscovery.Component{
		Name:       name,
		UniqueID:   "openccu-loom_" + name,
		StateTopic: "loom/ccu/dev/1/values/" + name,
		Device: &hadiscovery.DeviceInfo{
			Identifiers: []string{"openccu-loom_ccu_0001abc"},
			Name:        "Thermostat",
		},
		Origin: &hadiscovery.Origin{Name: "openccu-loom"},
	})
}

func marshalComponent(c hadiscovery.Component) []byte {
	raw, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	// The per-entity form has no `platform` key; the bundle form adds it.
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		panic(err)
	}
	delete(body, "platform")
	raw, err = json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return raw
}

// componentBody marshals a component back to the object Home Assistant
// reads, which is what the assertions are about — not the Go struct.
func componentBody(t *testing.T, c hadiscovery.Component) map[string]any {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal component: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}
	return body
}

func mustPut(t *testing.T, s *discoveryBundleStore, nodeID, objectID, platform string, body []byte) {
	t.Helper()
	if err := s.Put(nodeID, objectID, platform, body); err != nil {
		t.Fatalf("Put %s/%s: %v", nodeID, objectID, err)
	}
}

// TestBundleLiftsTheFrameOutOfEveryComponent: the per-entity form repeats the
// device and origin blocks in every config, which is what the producers
// build. A bundle carries them once — a component that repeated them would
// be describing a device inside a device.
func TestBundleLiftsTheFrameOutOfEveryComponent(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))
	mustPut(t, s, "loom_ccu_0001abc", "humidity", "sensor", bundleComponent("humidity"))

	bundle, ok := s.Bundle("loom_ccu_0001abc")
	if !ok {
		t.Fatal("no bundle for a node with two components")
	}
	if bundle.Device.Name != "Thermostat" {
		t.Errorf("device block = %+v, want it lifted to the top", bundle.Device)
	}
	if bundle.Origin.Name != "openccu-loom" {
		t.Errorf("origin = %+v, want it lifted to the top", bundle.Origin)
	}
	for _, key := range bundle.Keys() {
		body := componentBody(t, bundle.Components[key])
		if _, dup := body["device"]; dup {
			t.Errorf("component %q still carries its own device block", key)
		}
		if _, dup := body["origin"]; dup {
			t.Errorf("component %q still carries its own origin block", key)
		}
	}
	if got := bundle.Keys(); !reflect.DeepEqual(got, []string{"humidity", "temperature"}) {
		t.Errorf("keys = %v", got)
	}
}

// TestRemovedComponentBecomesAPlatformOnlyTombstone pins the rule that is
// easy to get wrong in the direction that never shows: Home Assistant deletes
// a component only when its entry is present and carries a platform alone. An
// empty object is not enough, and dropping the key leaves the entity forever.
func TestRemovedComponentBecomesAPlatformOnlyTombstone(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "temperature", "sensor", bundleComponent("temperature"))
	mustPut(t, s, "node", "humidity", "sensor", bundleComponent("humidity"))
	s.Remove("node", "humidity")

	bundle, ok := s.Bundle("node")
	if !ok {
		t.Fatal("no bundle")
	}
	tomb, present := bundle.Components["humidity"]
	if !present {
		t.Fatal("the removed component is absent — Home Assistant would keep the entity")
	}
	if tomb.Platform != hacatalog.PlatformSensor {
		t.Errorf("tombstone platform = %q, want the platform it used to be", tomb.Platform)
	}

	raw, err := json.Marshal(tomb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("tombstone = %s, want the platform and nothing else", raw)
	}
}

// TestRemovingSomethingNeverSeenIsNotATombstone: without a remembered
// platform the entry would marshal to an empty object, which Home Assistant
// ignores — so it would be noise in every future publish for no effect.
func TestRemovingSomethingNeverSeenIsNotATombstone(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "temperature", "sensor", bundleComponent("temperature"))
	s.Remove("node", "never_existed")

	bundle, _ := s.Bundle("node")
	if _, present := bundle.Components["never_existed"]; present {
		t.Error("an unknown object id produced a tombstone with no platform to name")
	}
}

// TestReaddingClearsTheTombstone: an entity that comes back must stop being
// advertised as deleted, or the same document says both things at once.
func TestReaddingClearsTheTombstone(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "temperature", "sensor", bundleComponent("temperature"))
	s.Remove("node", "temperature")
	mustPut(t, s, "node", "temperature", "sensor", bundleComponent("temperature"))

	bundle, _ := s.Bundle("node")
	body := componentBody(t, bundle.Components["temperature"])
	if body["state_topic"] == nil {
		t.Errorf("the re-added component is still a tombstone: %v", body)
	}
}

// TestSupersededTopicsAreTheOnesTheMigrationMustRetractFirst. Measured
// against a live instance (ADR 0070, amendment 2026-09-10): a bundle
// published while a per-entity config for the same unique id is still
// retained is refused, with nothing but a log line to show for it.
//
// The list is derived by [hapublisher.SupersededTopics] now, from the
// document itself rather than from a second index beside it. What this
// pins is that the store renders a bundle the shared derivation reads the
// same way it read the store's own: same object-id keys, same platforms,
// and a tombstoned component included — its retained per-entity config is
// exactly what has to go.
func TestSupersededTopicsAreTheOnesTheMigrationMustRetractFirst(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))
	mustPut(t, s, "loom_ccu_0001abc", "valve", "number", bundleComponent("valve"))
	s.Remove("loom_ccu_0001abc", "valve")

	bundle, ok := s.Bundle("loom_ccu_0001abc")
	if !ok {
		t.Fatal("store rendered no bundle")
	}
	want := []string{
		"homeassistant/number/loom_ccu_0001abc/valve/config",
		"homeassistant/sensor/loom_ccu_0001abc/temperature/config",
	}
	got := hapublisher.SupersededTopics(naming.DiscoveryTopicPrefix, bundle)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("superseded = %v, want %v", got, want)
	}
}

// TestNoDeviceBlockNoBundle: Home Assistant refuses a bundle without one, and
// a device block with no identifiers registers a device under nothing.
// Neither is worth putting on a retained topic.
func TestNoDeviceBlockNoBundle(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "temperature", "sensor", marshalComponent(hadiscovery.Component{
		Name: "temperature", UniqueID: "x",
	}))
	if _, ok := s.Bundle("node"); ok {
		t.Error("rendered a bundle with no device block")
	}

	s2 := newDiscoveryBundleStore()
	mustPut(t, s2, "node", "temperature", "sensor", marshalComponent(hadiscovery.Component{
		Name: "temperature", UniqueID: "x",
		Device: &hadiscovery.DeviceInfo{Name: "No identifiers"},
	}))
	if _, ok := s2.Bundle("node"); ok {
		t.Error("rendered a bundle whose device names nothing")
	}
}

// TestFirstFrameWins: the hub and alarm planes attach a narrower device block
// to some entities of a node, and letting a later one overwrite would shrink
// the device Home Assistant already knows.
func TestFirstFrameWins(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "a", "sensor", bundleComponent("a"))

	mustPut(t, s, "node", "b", "sensor", marshalComponent(hadiscovery.Component{
		Name: "b", UniqueID: "y",
		Device: &hadiscovery.DeviceInfo{Identifiers: []string{"openccu-loom_ccu_0001abc"}},
	}))

	bundle, _ := s.Bundle("node")
	if bundle.Device.Name != "Thermostat" {
		t.Errorf("device name = %q, want the first, fuller block", bundle.Device.Name)
	}
}

// TestRenderedBundleValidatesAndLandsWhereHomeAssistantReads ties the store
// to the two things outside it that decide whether the document is usable at
// all: the platform schemas, and where it gets published.
//
// The topic comes from the shared module and is deliberately not re-derived
// here. This repository's naming layer owns every other discovery topic, and
// adding a second derivation of this one would create exactly the drift the
// assertion below would then have to guard against.
func TestRenderedBundleValidatesAndLandsWhereHomeAssistantReads(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	mustPut(t, s, "loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))

	bundle, ok := s.Bundle("loom_ccu_0001abc")
	if !ok {
		t.Fatal("no bundle")
	}
	if err := hadiscovery.Validate(bundle); err != nil {
		t.Errorf("the rendered bundle does not validate: %v", err)
	}

	if got := bundle.Topic(""); got != "homeassistant/device/loom_ccu_0001abc/config" {
		t.Errorf("topic = %q, not the shape Home Assistant reads", got)
	}
}

// TestTheComponentBodyIsTheOneTheEntityFormPublished is why the store takes
// bytes rather than a typed component.
//
// Every key and value a per-entity config carried has to arrive in the
// bundle unchanged — the migration re-shapes the document around an entity,
// it must not change the entity. Round-tripping through a typed struct would
// silently drop anything the struct does not model; carrying the decoded
// body cannot.
func TestTheComponentBodyIsTheOneTheEntityFormPublished(t *testing.T) {
	t.Parallel()

	published := marshalComponent(hadiscovery.Component{
		Name:          "Temperature",
		UniqueID:      "openccu-loom_ccu_0001abc_temperature",
		StateTopic:    "loom/ccu/0001abc/1/values/ACTUAL_TEMPERATURE",
		UnitOfMeasure: "°C",
		DeviceClass:   "temperature",
		Precision:     hadiscovery.Ptr(1),
		Device: &hadiscovery.DeviceInfo{
			Identifiers: []string{"openccu-loom_ccu_0001abc"},
			Name:        "Thermostat",
		},
		Origin: &hadiscovery.Origin{Name: "openccu-loom"},
		// A key the typed struct does not model at all. This daemon emits
		// one — `translation_key`, for cross-stack parity — and a typed
		// round-trip is exactly where it would disappear.
		Extra: map[string]any{"translation_key": "temperature"},
	})

	var want map[string]any
	if err := json.Unmarshal(published, &want); err != nil {
		t.Fatalf("unmarshal published body: %v", err)
	}
	delete(want, "device")
	delete(want, "origin")

	s := newDiscoveryBundleStore()
	mustPut(t, s, "node", "temperature", "sensor", published)
	bundle, ok := s.Bundle("node")
	if !ok {
		t.Fatal("no bundle")
	}

	got := componentBody(t, bundle.Components["temperature"])
	if got["platform"] != "sensor" {
		t.Errorf("platform = %v, want sensor", got["platform"])
	}
	delete(got, "platform")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("component body drifted\n got: %v\nwant: %v", got, want)
	}
}
