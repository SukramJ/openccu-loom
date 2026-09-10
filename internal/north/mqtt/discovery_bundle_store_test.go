// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"reflect"
	"testing"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

func bundleComponent(name string) hadiscovery.Component {
	return hadiscovery.Component{
		Platform:   hacatalog.PlatformSensor,
		Name:       name,
		UniqueID:   "openccu-loom_" + name,
		StateTopic: "loom/ccu/dev/1/values/" + name,
		Device: &hadiscovery.DeviceInfo{
			Identifiers: []string{"openccu-loom_ccu_0001abc"},
			Name:        "Thermostat",
		},
		Origin: &hadiscovery.Origin{Name: "openccu-loom"},
	}
}

// TestBundleLiftsTheFrameOutOfEveryComponent: the per-entity form repeats the
// device and origin blocks in every config, which is what the producers
// build. A bundle carries them once — a component that repeated them would
// be describing a device inside a device.
func TestBundleLiftsTheFrameOutOfEveryComponent(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	s.Put("loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))
	s.Put("loom_ccu_0001abc", "humidity", "sensor", bundleComponent("humidity"))

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
	for key, comp := range bundle.Components {
		if comp.Device != nil || comp.Origin != nil {
			t.Errorf("component %q still carries its own frame", key)
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
	s.Put("node", "temperature", "sensor", bundleComponent("temperature"))
	s.Put("node", "humidity", "sensor", bundleComponent("humidity"))
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
	s.Put("node", "temperature", "sensor", bundleComponent("temperature"))
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
	s.Put("node", "temperature", "sensor", bundleComponent("temperature"))
	s.Remove("node", "temperature")
	s.Put("node", "temperature", "sensor", bundleComponent("temperature"))

	bundle, _ := s.Bundle("node")
	if got := bundle.Components["temperature"].StateTopic; got == "" {
		t.Error("the re-added component is still a tombstone")
	}
}

// TestSupersededTopicsAreTheOnesTheMigrationMustRetractFirst. Measured
// against a live instance (ADR 0070, amendment 2026-09-10): a bundle
// published while a per-entity config for the same unique id is still
// retained is refused, with nothing but a log line to show for it.
func TestSupersededTopicsAreTheOnesTheMigrationMustRetractFirst(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	s.Put("loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))
	s.Put("loom_ccu_0001abc", "valve", "number", bundleComponent("valve"))
	s.Remove("loom_ccu_0001abc", "valve")

	want := []string{
		"homeassistant/number/loom_ccu_0001abc/valve/config",
		"homeassistant/sensor/loom_ccu_0001abc/temperature/config",
	}
	if got := s.SupersededTopics("loom_ccu_0001abc"); !reflect.DeepEqual(got, want) {
		t.Errorf("superseded = %v, want %v", got, want)
	}
}

// TestNoDeviceBlockNoBundle: Home Assistant refuses a bundle without one, and
// a device block with no identifiers registers a device under nothing.
// Neither is worth putting on a retained topic.
func TestNoDeviceBlockNoBundle(t *testing.T) {
	t.Parallel()

	s := newDiscoveryBundleStore()
	comp := bundleComponent("temperature")
	comp.Device = nil
	s.Put("node", "temperature", "sensor", comp)

	if _, ok := s.Bundle("node"); ok {
		t.Error("rendered a bundle with no device block")
	}

	s2 := newDiscoveryBundleStore()
	bare := bundleComponent("temperature")
	bare.Device = &hadiscovery.DeviceInfo{Name: "No identifiers"}
	s2.Put("node", "temperature", "sensor", bare)
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
	s.Put("node", "a", "sensor", bundleComponent("a"))

	narrow := bundleComponent("b")
	narrow.Device = &hadiscovery.DeviceInfo{Identifiers: []string{"openccu-loom_ccu_0001abc"}}
	s.Put("node", "b", "sensor", narrow)

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
	s.Put("loom_ccu_0001abc", "temperature", "sensor", bundleComponent("temperature"))

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
