// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// securityDiscoveryBody unmarshals a [DiscoveryItem]'s payload for
// field-by-field assertions, mirroring alarmDiscoveryBody in
// alarm_discovery_test.go.
func securityDiscoveryBody(t *testing.T, item DiscoveryItem) map[string]any {
	t.Helper()
	if !item.OK {
		t.Fatalf("BuildSecurityDiscovery returned OK=false")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal discovery payload: %v (raw=%s)", err, item.Payload)
	}
	return body
}

// securityTestTr is a translation stand-in that always returns the
// English fallback — the discovery shape under test does not depend on
// which locale resolved the label.
func securityTestTr(_, fallback string) string { return fallback }

// TestSecuritySystemEntities_CommonShape covers the fields every entity
// from [securitySystemEntities] carries regardless of its component:
// OK=true, the shared "security" node, a unique_id/object_id of
// `loom_security_<key>`, the two-source availability list in "all"
// mode, and the `openccu-loom_security` device block carrying model +
// sw_version.
func TestSecuritySystemEntities_CommonShape(t *testing.T) {
	t.Parallel()
	for _, e := range securitySystemEntities(securityTestTr) {
		t.Run(e.key, func(t *testing.T) {
			t.Parallel()
			item := BuildSecurityDiscovery("gh", "Security & Safety", "", e)
			if !item.OK {
				t.Fatalf("BuildSecurityDiscovery(%q) returned OK=false", e.key)
			}
			if item.NodeID != securityDiscoveryNodeID {
				t.Errorf("NodeID = %q, want %q", item.NodeID, securityDiscoveryNodeID)
			}
			body := securityDiscoveryBody(t, item)

			wantUnique := "loom_security_" + e.key
			if got := body["unique_id"]; got != wantUnique {
				t.Errorf("unique_id = %v, want %v", got, wantUnique)
			}
			if got := body["object_id"]; got != wantUnique {
				t.Errorf("object_id = %v, want %v", got, wantUnique)
			}

			avail, ok := body["availability"].([]any)
			if !ok || len(avail) != 2 {
				t.Fatalf("availability = %v, want a 2-element list", body["availability"])
			}
			for i, entry := range avail {
				m, ok := entry.(map[string]any)
				if !ok {
					t.Fatalf("availability[%d] not an object: %v", i, entry)
				}
				if got, want := m["payload_available"], "online"; got != want {
					t.Errorf("availability[%d].payload_available = %v, want %v", i, got, want)
				}
				if got, want := m["payload_not_available"], "offline"; got != want {
					t.Errorf("availability[%d].payload_not_available = %v, want %v", i, got, want)
				}
			}
			if got, want := body["availability_mode"], "all"; got != want {
				t.Errorf("availability_mode = %v, want %v", got, want)
			}

			device, ok := body["device"].(map[string]any)
			if !ok {
				t.Fatalf("device block missing/not an object: %v", body["device"])
			}
			if got, want := device["model"], "Security & Safety"; got != want {
				t.Errorf("device.model = %v, want %v", got, want)
			}
			if got := device["sw_version"]; got != build.Version {
				t.Errorf("device.sw_version = %v, want %v", got, build.Version)
			}
		})
	}
}

// TestSecuritySystemEntities_EventEntitiesShape locks the structural
// difference of the two event entities ("event", "fault"): they carry
// the announced event_types vocabulary and nothing else the switch in
// BuildSecurityDiscovery reserves for non-event entities.
//
// A value_template on an event entity would try to extract a scalar
// from a payload the consumer instead parses whole as JSON (to read
// `event_type`), breaking that parse outright. A device_class on an
// event entity is likewise wrong: the consumer's event-entity
// vocabulary only defines doorbell/button/motion device classes, none
// of which describes a security event.
func TestSecuritySystemEntities_EventEntitiesShape(t *testing.T) {
	t.Parallel()
	for _, e := range securitySystemEntities(securityTestTr) {
		if !e.event {
			continue
		}
		t.Run(e.key, func(t *testing.T) {
			t.Parallel()
			item := BuildSecurityDiscovery("gh", "Security & Safety", "", e)
			body := securityDiscoveryBody(t, item)

			types, ok := body["event_types"].([]any)
			if !ok {
				t.Fatalf("event_types missing or not a list: %v", body["event_types"])
			}
			if len(types) != len(securityEventTypes) {
				t.Fatalf("event_types = %v, want %v", types, securityEventTypes)
			}
			for i, want := range securityEventTypes {
				if types[i] != want {
					t.Errorf("event_types[%d] = %v, want %v", i, types[i], want)
				}
			}

			for _, forbidden := range []string{"value_template", "device_class", "json_attributes_topic"} {
				if got, has := body[forbidden]; has {
					t.Errorf("event entity %q must not carry %q; got %v", e.key, forbidden, got)
				}
			}
		})
	}
}

// TestSecuritySystemEntities_BinarySensorShape covers the binary
// sensors that double their state topic as their attribute source
// ("alarm", "problem"): payload_on/payload_off are the HA ON/OFF
// tokens, a value_template extracts the state from the shared JSON
// envelope, and json_attributes_topic equals state_topic so the same
// publish serves both roles.
//
// "health" is a deliberate exception (plain "ON"/"OFF" wire payload, no
// JSON envelope) and is intentionally not covered here.
func TestSecuritySystemEntities_BinarySensorShape(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"alarm", "problem"} {
		var (
			target securityEntity
			found  bool
		)
		for _, e := range securitySystemEntities(securityTestTr) {
			if e.key == key {
				target, found = e, true
				break
			}
		}
		if !found {
			t.Fatalf("no system entity with key %q", key)
		}
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			item := BuildSecurityDiscovery("gh", "Security & Safety", "", target)
			body := securityDiscoveryBody(t, item)

			if got, want := body["payload_on"], "ON"; got != want {
				t.Errorf("payload_on = %v, want %v", got, want)
			}
			if got, want := body["payload_off"], "OFF"; got != want {
				t.Errorf("payload_off = %v, want %v", got, want)
			}
			vt, ok := body["value_template"].(string)
			if !ok || vt == "" {
				t.Fatalf("value_template missing/empty: %v", body["value_template"])
			}
			jat, ok := body["json_attributes_topic"].(string)
			if !ok || jat != body["state_topic"] {
				t.Errorf("json_attributes_topic = %v, want it to equal state_topic %v", body["json_attributes_topic"], body["state_topic"])
			}
		})
	}
}

// TestSecurityClassEntity_DeviceClassAndDiagnosticMapping locks the
// class-to-device_class table and the hazard/diagnostic split: hazard
// classes (smoke, water, gas, co, intrusion, panic) carry no
// entity_category, while the three fault classes (tamper, battery,
// technical) carry entity_category=diagnostic. If this drifts from
// [hmenum.SecurityClass.Hazard]/[hmenum.SecurityClass.Diagnostic] a
// hazard would silently start showing up as a collapsed diagnostic
// entity in the consumer's UI, or vice versa.
func TestSecurityClassEntity_DeviceClassAndDiagnosticMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class      hmenum.SecurityClass
		wantDevCls string
		diagnostic bool
	}{
		{hmenum.SecurityClassSmoke, "smoke", false},
		{hmenum.SecurityClassWater, "moisture", false},
		{hmenum.SecurityClassGas, "gas", false},
		{hmenum.SecurityClassCO, "carbon_monoxide", false},
		{hmenum.SecurityClassTamper, "tamper", true},
		{hmenum.SecurityClassBattery, "battery", true},
		{hmenum.SecurityClassTechnical, "problem", true},
		{hmenum.SecurityClassIntrusion, "safety", false},
		{hmenum.SecurityClassPanic, "safety", false},
	}
	if len(cases) != len(hmenum.SecurityClasses()) {
		t.Fatalf("test table covers %d classes, hmenum.SecurityClasses() has %d — keep them in sync",
			len(cases), len(hmenum.SecurityClasses()))
	}
	for _, c := range cases {
		t.Run(string(c.class), func(t *testing.T) {
			t.Parallel()
			e := securityClassEntity(c.class, securityTestTr)
			item := BuildSecurityDiscovery("gh", "Security & Safety", "", e)
			body := securityDiscoveryBody(t, item)

			if got := body["device_class"]; got != c.wantDevCls {
				t.Errorf("device_class = %v, want %v", got, c.wantDevCls)
			}
			gotCat, hasCat := body["entity_category"]
			if hasCat != c.diagnostic {
				t.Errorf("entity_category present = %v, want %v (class %s)", hasCat, c.diagnostic, c.class)
			}
			if c.diagnostic && gotCat != "diagnostic" {
				t.Errorf("entity_category = %v, want diagnostic", gotCat)
			}
		})
	}
}

// TestSecurityDeviceBlock_DistinctFromAlarmDeviceBlock guards the two
// planes' device blocks against sharing an identifier. Two publishers
// writing different blocks under one identifier set make the HA card
// name flap between "OpenCCU-Loom Alarm" and "Security & Safety" every
// time the other plane republishes its own device block.
func TestSecurityDeviceBlock_DistinctFromAlarmDeviceBlock(t *testing.T) {
	t.Parallel()
	secBlock := securityDeviceBlock("Security & Safety", "")
	alBlock := alarmDeviceBlock()

	secIDs, ok := secBlock["identifiers"].([]string)
	if !ok || len(secIDs) != 1 {
		t.Fatalf("security identifiers = %v, want a one-element []string", secBlock["identifiers"])
	}
	alIDs, ok := alBlock["identifiers"].([]string)
	if !ok || len(alIDs) != 1 {
		t.Fatalf("alarm identifiers = %v, want a one-element []string", alBlock["identifiers"])
	}
	if secIDs[0] == alIDs[0] {
		t.Fatalf("security and alarm device blocks share identifier %q", secIDs[0])
	}
	if secIDs[0] != "openccu-loom_security" {
		t.Errorf("security identifier = %q, want openccu-loom_security", secIDs[0])
	}
	if alIDs[0] != "openccu-loom_alarm" {
		t.Errorf("alarm identifier = %q, want openccu-loom_alarm", alIDs[0])
	}
}

// TestBuildSecurityDiscovery_EmptyKeyRejected guards against publishing
// a discovery config with no topic/unique-id segment.
func TestBuildSecurityDiscovery_EmptyKeyRejected(t *testing.T) {
	t.Parallel()
	item := BuildSecurityDiscovery("gh", "Security & Safety", "", securityEntity{})
	if item.OK {
		t.Fatalf("expected OK=false for an empty key, got %+v", item)
	}
}
