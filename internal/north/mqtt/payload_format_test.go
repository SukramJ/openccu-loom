// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPayloadFormatBareIsBackwardCompatible pins the contract that
// the default (zero-value) PayloadFormat publishes bare scalars on
// the state topic, so existing non-HA consumers (Node-RED,
// InfluxDB) keep working when they upgrade.
// Raw per-DP state is now published via PublishSlotState (not PublishState).
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
			// Bare mode: the PerDPState JSON still wraps the value. The
			// legacy bare scalar is only emitted by PublishState's
			// renderStatePayload (legacy alias path). PublishSlotState
			// always uses the PerDPState JSON envelope — consumers read
			// value_json.value. Verify the JSON contains value:true.
			if !strings.Contains(p.payload, `"value":true`) {
				t.Fatalf("PerDPState envelope must contain value:true, got %q", p.payload)
			}
			return
		}
	}
	t.Fatal("expected at least one publish to .../STATE")
}

// TestPayloadFormatJSONWrapsState verifies that PublishSlotState publishes
// a PerDPState JSON envelope {"value":..,"available":..,"modified_at":..}
// as the state topic payload. JSON is now the only supported shape.
func TestPayloadFormatJSONWrapsState(t *testing.T) {
	rec := &recordingPublisher{}
	b := NewBridge(BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu",
		RawEnabled:  true,
	}, rec)

	slot := pload.TopicSlot{Address: "0001ABCD", Channel: 1, Bucket: pload.BucketValues, Parameter: "STATE"}
	dpState := pload.PerDPState{Value: true, Available: true}
	if err := b.PublishSlotState(context.Background(), "ccu", "HmIP-RF", slot, dpState); err != nil {
		t.Fatalf("PublishSlotState: %v", err)
	}

	for _, p := range rec.records() {
		if !strings.HasSuffix(p.topic, "/STATE") {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(p.payload), &got); err != nil {
			t.Fatalf("state payload not JSON: %v (raw=%q)", err, p.payload)
		}
		if got["value"] != true {
			t.Fatalf("wrong value field: %+v", got)
		}
		if got["available"] != true {
			t.Fatalf("missing/false available field: %+v", got)
		}
		return
	}
	t.Fatal("expected at least one publish to .../STATE")
}

// TestDiscoveryAddsValueTemplateInJSONMode pins the contract that
// the discovery payload includes the {{ value_json.value }}
// template AND a third availability entry sourced from the state
// topic's JSON. Without these HA cannot extract the scalar from
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
		if !strings.Contains(vt, "value_json.value") {
			t.Fatalf("missing value_template referencing value_json.value: %v", got["value_template"])
		}
		availability, _ := got["availability"].([]any)
		var foundJSONAvailEntry bool
		for _, entry := range availability {
			m, _ := entry.(map[string]any)
			if tmpl, _ := m["value_template"].(string); strings.Contains(tmpl, "value_json.available") {
				foundJSONAvailEntry = true
				break
			}
		}
		if !foundJSONAvailEntry {
			t.Fatalf("missing availability entry with value_json.available template: %+v", availability)
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
