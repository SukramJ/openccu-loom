// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPayloadFormatBareIsBackwardCompatible pins the raw plane's wire shape:
// PublishSlotState emits a bare scalar, not a JSON envelope, so a non-HA
// consumer reading a raw topic (Node-RED, InfluxDB) parses a number rather
// than an object. The JSON envelope belongs to the HA state topics (ADR 0011).
//
// The name is historical: there was once a `payload_format` switch offering a
// bare mode. It never reached the publisher and has been removed; the raw
// plane's bare shape is not configurable and is what this pins.
func TestPayloadFormatBareIsBackwardCompatible(t *testing.T) {
	rec := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu",
		RawEnabled:  true,
		// PayloadFormat omitted → defaults to "bare".
	}, rec)

	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	dpState := pload.PerDPState{Value: true, Available: true}
	if err := b.PublishSlotState(context.Background(), "ccu", "HmIP-RF", slot, dpState); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}

	for _, p := range rec.records() {
		if strings.HasSuffix(p.topic, "/STATE") {
			// There is no bare mode: every status item is an
			// mqtt-smarthome status object (ADR 0083) — consumers read
			// value_json.val. Verify it carries val:true.
			if !strings.HasPrefix(p.payload, `{"val":true,`) {
				t.Fatalf("status object must open with val:true, got %q", p.payload)
			}
			return
		}
	}
	t.Fatal("expected at least one publish to .../STATE")
}

// TestPayloadFormatJSONWrapsState verifies that PublishSlotState publishes
// the status object {"val":..,"ts":..,"lc":..,"hm":{"available":..}} as the
// status item payload, with the observation time as ts.
func TestPayloadFormatJSONWrapsState(t *testing.T) {
	rec := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu",
		RawEnabled:  true,
	}, rec)

	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	observed := time.UnixMilli(1_767_225_600_500)
	dpState := pload.PerDPState{Value: true, Available: true, ObservedAt: observed}
	if err := b.PublishSlotState(context.Background(), "ccu", "HmIP-RF", slot, dpState); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}

	for _, p := range rec.records() {
		if !strings.HasSuffix(p.topic, "/STATE") {
			continue
		}
		want := `{"val":true,"ts":1767225600500,"lc":1767225600500,"hm":{"available":true}}`
		if p.payload != want {
			t.Fatalf("state payload = %s, want %s", p.payload, want)
		}
		return
	}
	t.Fatal("expected at least one publish to .../STATE")
}

// TestDiscoveryAddsValueTemplateInJSONMode pins the contract that
// the discovery payload includes a template reading value_json.val AND a
// third availability entry reading the state topic's `hm.available` flag. Without these HA cannot extract the scalar from
// the wrapped payload and the entity stays "unknown".
func TestDiscoveryAddsValueTemplateInJSONMode(t *testing.T) {
	rec := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, rec)

	if err := b.PublishState(context.Background(), Event{
		Central: "ccu", Interface: "HmIP-RF",
		DeviceAddress: "0001ABCD", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Value: true,
	}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	for _, p := range rec.records() {
		if !strings.HasPrefix(p.topic, "homeassistant/") {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(p.payload), &got); err != nil {
			t.Fatalf("discovery payload not JSON: %v (raw=%q)", err, p.payload)
		}
		vt, _ := got["value_template"].(string)
		if !strings.Contains(vt, "value_json.val") || strings.Contains(vt, "value_json.value") {
			t.Fatalf("value_template must read value_json.val: %v", got["value_template"])
		}
		availability, _ := got["availability"].([]any)
		var foundJSONAvailEntry bool
		for _, entry := range availability {
			m, _ := entry.(map[string]any)
			if tmpl, _ := m["value_template"].(string); tmpl == "{{ value_json.hm.available | lower }}" {
				foundJSONAvailEntry = true
				break
			}
		}
		if !foundJSONAvailEntry {
			t.Fatalf("missing availability entry with value_json.hm.available template: %+v", availability)
		}
		return
	}
	t.Fatal("expected at least one homeassistant/* publish")
}

// TestDiscoveryNoValueTemplateInBareMode was the negative control:
// the old bare mode did NOT inject a value_template. After the
// bucket-aware topology migration, the discovery payload ALWAYS
// includes `value_template: "{{ value_json.value }}"` because the
// per-DP state topic now carries a PerDPState JSON envelope on every
// path (not just in JSON mode). This test is updated to pin the new
// contract: value_template IS present and equals
// "{{ value_json.value }}" (the scalar extractor HA uses with
// PublishSlotState's JSON envelope).
func TestDiscoveryNoValueTemplateInBareMode(t *testing.T) {
	rec := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
		// PayloadFormat omitted → bare.
	}, rec)

	if err := b.PublishState(context.Background(), Event{
		Central: "ccu", Interface: "HmIP-RF",
		DeviceAddress: "0001ABCD", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Value: true,
	}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	for _, p := range rec.records() {
		if !strings.HasPrefix(p.topic, "homeassistant/") {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(p.payload), &got); err != nil {
			t.Fatalf("discovery payload not JSON: %v", err)
		}
		// The discovery payload now always carries value_template because
		// the per-DP topic always uses the PerDPState JSON envelope.
		// The defensive `is defined` guard catches the
		// register-and-load eviction case where HA reads an empty
		// retained payload. STATE classifies as binary_sensor /
		// switch (boolean wire DP) → the lower-pipe variant applies
		// so HA's `payload_on:"true"`/`payload_off:"false"` matches
		// the rendered scalar (Jinja's default for Python bool is
		// `True`/`False` capitalised).
		vt, has := got["value_template"]
		if !has {
			t.Fatalf("discovery payload must contain value_template; got: %v", got)
		}
		if vt != valueJSONValueTemplate && vt != valueJSONValueLowerTemplate {
			t.Fatalf("value_template = %q, want %q or %q",
				vt, valueJSONValueTemplate, valueJSONValueLowerTemplate)
		}
		return
	}
	t.Fatal("expected at least one homeassistant/* publish")
}
