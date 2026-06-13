// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"strings"
	"testing"
)

// TestBuildLastEventAgeDiscovery pins the new last-event-age hub sensor
// (reference parity hub_last-event-age): component=sensor, duration
// device_class, seconds unit, diagnostic, and a last_event_age uid /
// translation_key.
func TestBuildLastEventAgeDiscovery(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildLastEventAgeDiscovery("ccu-01")
	if !item.OK {
		t.Fatal("BuildLastEventAgeDiscovery returned ok=false")
	}
	if item.Component != string(HAComponentSensor) {
		t.Fatalf("component=%q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["device_class"] != "duration" {
		t.Errorf("device_class=%v want duration", m["device_class"])
	}
	if m["unit_of_measurement"] != "s" {
		t.Errorf("unit=%v want s", m["unit_of_measurement"])
	}
	if m["entity_category"] != "diagnostic" {
		t.Errorf("entity_category=%v want diagnostic", m["entity_category"])
	}
	if m["translation_key"] != "last_event_age" {
		t.Errorf("translation_key=%v want last_event_age", m["translation_key"])
	}
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_last_event_age") {
		t.Errorf("unique_id=%q should end with _last_event_age", uid)
	}
	st, _ := m["state_topic"].(string)
	if !strings.HasSuffix(st, "/system/last_event_age") {
		t.Errorf("state_topic=%q should end with /system/last_event_age", st)
	}
}

// TestBuildLastEventAgeDiscovery_EmptyCentral pins the safe no-op for an
// empty central name.
func TestBuildLastEventAgeDiscovery_EmptyCentral(t *testing.T) {
	t.Parallel()
	if db := newHubBuilder(); db.BuildLastEventAgeDiscovery("").OK {
		t.Error("empty central should produce ok=false")
	}
}

// TestBuildHubUpdateDiscovery_SystemUpdateSlug pins that the hub
// firmware-update entity uid is scoped to "system_update" so it no
// longer collides conceptually with per-device firmware-update entities
// (loom_<addr>_update). Reference parity: hub_system-update.
func TestBuildHubUpdateDiscovery_SystemUpdateSlug(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildHubUpdateDiscovery("ccu-01")
	if !item.OK {
		t.Fatal("BuildHubUpdateDiscovery returned ok=false")
	}
	if item.Component != string(HAComponentUpdate) {
		t.Fatalf("component=%q want update", item.Component)
	}
	if item.ObjectID != "system_update" {
		t.Errorf("object_id=%q want system_update", item.ObjectID)
	}
	m := jsonMap(t, item)
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_system_update") {
		t.Errorf("unique_id=%q should end with _system_update", uid)
	}
}
