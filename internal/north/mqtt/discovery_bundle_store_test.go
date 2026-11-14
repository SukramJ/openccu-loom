// SPDX-License-Identifier: Apache-2.0
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
		Precision:     new(1),
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

// childBundleComponent is a per-entity body whose entity sits on a child
// device: the parent's block plus `child_device`.
func childBundleComponent(name, childID, childName string) []byte {
	return marshalComponent(hadiscovery.Component{
		Name:       name,
		UniqueID:   "openccu-loom_" + name,
		StateTopic: "loom/ccu/dev/1/values/" + name,
		Device: &hadiscovery.DeviceInfo{
			Identifiers: []string{"openccu-loom_ccu_0001abc"},
			Name:        "Thermostat",
			ChildDevice: &hadiscovery.ChildDeviceInfo{Identifiers: []string{childID}, Name: childName},
		},
		Origin: &hadiscovery.Origin{Name: "openccu-loom"},
	})
}

// bundleDocument is the document as Home Assistant reads it.
type bundleDocument struct {
	Device     map[string]any            `json:"device"`
	Components map[string]map[string]any `json:"components"`
	Children   []struct {
		Identifiers []string                  `json:"identifiers"`
		Name        string                    `json:"name"`
		Components  map[string]map[string]any `json:"components"`
	} `json:"child_devices"`
}

func renderBundleDocument(t *testing.T, s *discoveryBundleStore, nodeID string) bundleDocument {
	t.Helper()
	bundle, ok := s.Bundle(nodeID)
	if !ok {
		t.Fatal("no bundle")
	}
	if errs := hadiscovery.Inspect(bundle, hadiscovery.InspectOptions{}).Errors(); len(errs) > 0 {
		t.Fatalf("the bundle is one Home Assistant refuses: %v", errs)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	var doc bundleDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	return doc
}

// TestBundleListsChildDeviceComponentsUnderTheirChild: the per-entity form
// names the child inside the device block, a document lists the component
// under the child. The store moves it, whichever component arrives first —
// a device block that kept `child_device` would get the whole document
// refused (go-hamqtt KindChildDevice).
func TestBundleListsChildDeviceComponentsUnderTheirChild(t *testing.T) {
	t.Parallel()
	for _, childFirst := range []bool{true, false} {
		s := newDiscoveryBundleStore()
		put := func(objectID string, body []byte) {
			t.Helper()
			if err := s.Put("node", objectID, "sensor", body); err != nil {
				t.Fatalf("Put %s: %v", objectID, err)
			}
		}
		if childFirst {
			put("level_2", childBundleComponent("level_2", "openccu-loom_ccu_0001abc-2", "Gruppe 2"))
			put("temp", bundleComponent("temp"))
		} else {
			put("temp", bundleComponent("temp"))
			put("level_2", childBundleComponent("level_2", "openccu-loom_ccu_0001abc-2", "Gruppe 2"))
		}
		put("schedule", childBundleComponent("schedule", "openccu-loom_ccu_0001abc_schedule", "Zeitplan"))

		doc := renderBundleDocument(t, s, "node")
		if _, has := doc.Device["child_device"]; has {
			t.Errorf("childFirst=%v: the document's device block carries child_device", childFirst)
		}
		if _, ok := doc.Components["temp"]; !ok || len(doc.Components) != 1 {
			t.Errorf("childFirst=%v: device components = %v, want temp alone", childFirst, doc.Components)
		}
		got := map[string]string{}
		for _, child := range doc.Children {
			for key := range child.Components {
				got[key] = child.Identifiers[0] + "|" + child.Name
			}
		}
		want := map[string]string{
			"level_2":  "openccu-loom_ccu_0001abc-2|Gruppe 2",
			"schedule": "openccu-loom_ccu_0001abc_schedule|Zeitplan",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("childFirst=%v: child components = %v, want %v", childFirst, got, want)
		}
	}
}

// TestBundleChildAssignmentFollowsTheComponent: a component that moves off
// its child device — sub-devices switched off — goes back onto the device,
// and a removed one leaves its tombstone on the child it was on.
func TestBundleChildAssignmentFollowsTheComponent(t *testing.T) {
	t.Parallel()
	s := newDiscoveryBundleStore()
	for _, key := range []string{"level_2", "state_2"} {
		if err := s.Put("node", key, "sensor", childBundleComponent(key, "openccu-loom_ccu_0001abc-2", "Gruppe 2")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := s.Put("node", "level_2", "sensor", bundleComponent("level_2")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	s.Remove("node", "state_2")

	doc := renderBundleDocument(t, s, "node")
	if _, ok := doc.Components["level_2"]; !ok {
		t.Errorf("device components = %v, want level_2 back on the device", doc.Components)
	}
	if len(doc.Children) != 1 {
		t.Fatalf("child_devices = %+v, want the one child holding the tombstone", doc.Children)
	}
	want := map[string]map[string]any{"state_2": {"platform": "sensor"}}
	if !reflect.DeepEqual(doc.Children[0].Components, want) {
		t.Errorf("child components = %v, want %v", doc.Children[0].Components, want)
	}
}
