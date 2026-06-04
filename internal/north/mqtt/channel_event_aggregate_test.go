// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// multiPressChannel is a fakeChannelInspector pre-loaded with all four
// canonical PRESS_* parameters to simulate a HmIP-WRC2 button channel.
func multiPressChannel() *fakeChannelInspector {
	return &fakeChannelInspector{params: map[string]struct{}{
		"PRESS_SHORT":        {},
		"PRESS_LONG":         {},
		"PRESS_LONG_RELEASE": {},
		"PRESS_LONG_START":   {},
	}}
}

// singlePressChannel is a fakeChannelInspector with only PRESS_SHORT.
func singlePressChannel() *fakeChannelInspector {
	return &fakeChannelInspector{params: map[string]struct{}{
		"PRESS_SHORT": {},
	}}
}

// ---------------------------------------------------------------------------
// Test 1 — channel with 4 PRESS_* params → ONE event-discovery entity
// ---------------------------------------------------------------------------

// TestChannelEventAggregateDiscovery verifies that a button channel
// exposing all four PRESS_* parameters produces exactly ONE HA `event`
// discovery payload with all four types in event_types, the correct
// state_topic, device_class, and value_template. This is the primary
// Parity test against
func TestChannelEventAggregateDiscovery(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ch := multiPressChannel()

	for _, pressParam := range []string{"PRESS_SHORT", "PRESS_LONG", "PRESS_LONG_RELEASE", "PRESS_LONG_START"} {
		ev := Event{
			Interface:     "HmIP-RF",
			DeviceAddress: "0034WRC2",
			DeviceName:    "Flur Taster",
			ChannelNo:     1,
			Parameter:     pressParam,
			Channel:       ch,
		}

		comp, nodeID, objectID, buf, ok := db.Build(ev)
		if !ok {
			t.Fatalf("Build(%q) returned ok=false for multi-press channel", pressParam)
		}

		// Must be HAComponentEvent.
		if comp != string(HAComponentEvent) {
			t.Fatalf("Build(%q): component=%q want %q", pressParam, comp, HAComponentEvent)
		}

		// nodeID must be non-empty.
		if nodeID == "" {
			t.Fatalf("Build(%q): nodeID must not be empty", pressParam)
		}

		// objectID must contain "event" suffix (channel-aggregate pattern).
		if !strings.Contains(objectID, "event") {
			t.Fatalf("Build(%q): objectID=%q must contain \"event\"", pressParam, objectID)
		}

		// All four events share the SAME objectID (same entity, dedup by cache).
		t.Run("objectID_stable_for_"+pressParam, func(t *testing.T) {
			t.Parallel()
			if objectID != "1_event" {
				t.Errorf("objectID=%q want \"1_event\"", objectID)
			}
		})

		var payload map[string]any
		if err := json.Unmarshal(buf, &payload); err != nil {
			t.Fatalf("Build(%q): invalid JSON: %v", pressParam, err)
		}

		// event_types must contain all four press types.
		etRaw, present := payload["event_types"]
		if !present {
			t.Fatalf("Build(%q): event_types missing from aggregate discovery payload", pressParam)
		}
		etList, _ := etRaw.([]any)
		wantTypes := map[string]bool{
			"press_short":        false,
			"press_long":         false,
			"press_long_release": false,
			"press_long_start":   false,
		}
		for _, et := range etList {
			if s, ok := et.(string); ok {
				wantTypes[s] = true
			}
		}
		for typ, found := range wantTypes {
			if !found {
				t.Errorf("Build(%q): event_types missing %q; got %v", pressParam, typ, etList)
			}
		}
		if len(etList) != 4 {
			t.Errorf("Build(%q): event_types len=%d want 4; got %v", pressParam, len(etList), etList)
		}

		// device_class must be "button".
		if dc := payload["device_class"]; dc != "button" {
			t.Errorf("Build(%q): device_class=%v want \"button\"", pressParam, dc)
		}

		// value_template must NOT be set: HA's mqtt.event component
		// parses the post-template payload as JSON and reads
		// `event_type` from it. A scalar-extracting template
		// (`{{ value_json.event_type }}`) returns a bare string that
		// HA then fails to JSON-decode — surfaced in the HA log as
		// `No valid JSON event payload detected, value after
		// processing payload 'press_long'`. The bridge already
		// publishes a `{"event_type": ...}` envelope to the
		// `<channel>/event` topic, so HA reads `event_type`
		// directly from the raw payload without a template.
		if _, set := payload["value_template"]; set {
			t.Errorf("Build(%q): value_template must be absent; HA reads event_type directly from JSON payload", pressParam)
		}

		// state_topic must end with "/<channel>/event".
		stateTopic, _ := payload["state_topic"].(string)
		if !strings.HasSuffix(stateTopic, "/1/event") {
			t.Errorf("Build(%q): state_topic=%q must end with \"/1/event\"", pressParam, stateTopic)
		}

		// Mandatory base fields must be present. Note: `object_id`
		// is deliberately absent — HA derives entity_id from
		// device.name + payload `name`.
		for _, k := range []string{"name", "unique_id", "availability", "device", "origin"} {
			if _, present := payload[k]; !present {
				t.Errorf("Build(%q): missing required field %q", pressParam, k)
			}
		}
		// object_id must NOT be set: previous bug overwrote HA's
		// entity-id derivation with the long unique_id form.
		if _, present := payload["object_id"]; present {
			t.Errorf("Build(%q): object_id must be absent in payload", pressParam)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2 — ChannelEvent topic-builder format
// ---------------------------------------------------------------------------

// TestChannelEventAggregateTopicFormat pins the topic path emitted by
// TopicBuilder.ChannelEvent for the canonical multi-press button channel.
func TestChannelEventAggregateTopicFormat(t *testing.T) {
	t.Parallel()
	tb := NewTopicBuilder("openccu-loom")

	cases := []struct {
		centralName, iface, addr string
		channel                  int
		want                     string
	}{
		{
			centralName: "ccu", iface: "HmIP-RF", addr: "0034WRC2", channel: 1,
			want: "openccu-loom/ccu/HmIP-RF/0034WRC2/1/event",
		},
		{
			centralName: "ccu2", iface: "BidCos-RF", addr: "0ABC", channel: 3,
			want: "openccu-loom/ccu2/BidCos-RF/0ABC/3/event",
		},
		// Characters that are unsafe for MQTT topic levels must be sanitised.
		{
			centralName: "ccu", iface: "HmIP/RF", addr: "0034+WRC2", channel: 2,
			want: "openccu-loom/ccu/HmIP_RF/0034_WRC2/2/event",
		},
	}

	for _, tc := range cases {
		got := tb.ChannelEvent(tc.centralName, tc.iface, tc.addr, tc.channel)
		if got != tc.want {
			t.Errorf("ChannelEvent(%q,%q,%q,%d)=%q want %q",
				tc.centralName, tc.iface, tc.addr, tc.channel, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3 — Bridge.PublishChannelEventState payload format
// ---------------------------------------------------------------------------

// TestChannelEventStatePayloadFormat verifies that
// Bridge.PublishChannelEventState publishes a non-retained JSON
// payload to the channel event topic with the required fields:
// event_type (lower-cased), available, and modified_at.
func TestChannelEventStatePayloadFormat(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	bridge := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu", RawEnabled: true,
	}, pub)

	ctx := context.Background()
	if err := bridge.PublishChannelEventState(ctx, "ccu", "HmIP-RF", "0034WRC2", 1, "PRESS_SHORT"); err != nil {
		t.Fatalf("PublishChannelEventState: %v", err)
	}

	if len(pub.sent) == 0 {
		t.Fatal("expected 1 publish; got 0")
	}
	rec := pub.sent[0]

	// Topic must match ChannelEvent format.
	wantTopic := "gh/ccu/HmIP-RF/0034WRC2/1/event"
	if rec.topic != wantTopic {
		t.Errorf("topic=%q want %q", rec.topic, wantTopic)
	}

	// Must be non-retained (HA event entity contract).
	if rec.retain {
		t.Error("channel event publish must be non-retained (retain=false)")
	}

	// Payload must be valid JSON with required fields.
	var body map[string]any
	if err := json.Unmarshal([]byte(rec.payload), &body); err != nil {
		t.Fatalf("payload not valid JSON: %v; payload=%q", err, rec.payload)
	}

	// event_type must be lower-cased.
	if et, _ := body["event_type"].(string); et != "press_short" {
		t.Errorf("event_type=%q want \"press_short\"", et)
	}

	// available must be present and true.
	if avail, _ := body["available"].(bool); !avail {
		t.Errorf("available=%v want true", body["available"])
	}

	// modified_at must be present.
	if _, present := body["modified_at"]; !present {
		t.Error("modified_at missing from channel event state payload")
	}
}

// ---------------------------------------------------------------------------
// Test 4 — per-parameter PRESS_* discovery is suppressed for multi-press channels
// ---------------------------------------------------------------------------

// TestPerParameterPressEventSuppressedForMultiPress verifies that when a
// channel has multiple PRESS_* parameters the per-parameter Build path
// returns a single shared aggregate entity (not one-per-parameter). The
// proof is that ALL four PRESS_* events produce the SAME objectID — the
// bridge's dedup cache then coalesces them into a single Discovery publish.
// Additionally, a single-press channel (only PRESS_SHORT) must still use
// the per-parameter path and produce a per-parameter objectID.
func TestPerParameterPressEventSuppressedForMultiPress(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")

	// --- multi-press: all four PRESS_* params → same objectID each time ---
	ch4 := multiPressChannel()
	seen := make(map[string]int)
	for _, p := range []string{"PRESS_SHORT", "PRESS_LONG", "PRESS_LONG_RELEASE", "PRESS_LONG_START"} {
		_, _, oid, _, ok := db.Build(Event{
			Interface:     "HmIP-RF",
			DeviceAddress: "0034WRC2",
			ChannelNo:     1,
			Parameter:     p,
			Channel:       ch4,
		})
		if !ok {
			t.Fatalf("Build(%q) returned ok=false", p)
		}
		seen[oid]++
	}
	// All four PRESS_* events must map to the same objectID.
	if len(seen) != 1 {
		t.Errorf("multi-press channel produced %d distinct objectIDs (want 1): %v", len(seen), seen)
	}
	// That objectID must be the channel-aggregate form.
	for oid := range seen {
		if !strings.Contains(oid, "event") {
			t.Errorf("multi-press channel objectID=%q should contain \"event\"", oid)
		}
		if strings.Contains(oid, "press") {
			t.Errorf("multi-press channel objectID=%q must NOT contain per-parameter press name", oid)
		}
	}

	// --- single-press: only PRESS_SHORT → per-parameter per-entity ---
	ch1 := singlePressChannel()
	_, _, oidSingle, _, ok := db.Build(Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0034WRC2",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
		Category:      hmenum.DataPointCategoryEvent,
		Channel:       ch1,
	})
	if !ok {
		t.Fatal("Build(PRESS_SHORT, single-press channel) returned ok=false")
	}
	// Per-parameter objectID must contain the parameter name (lowercased).
	if !strings.Contains(oidSingle, "press_short") {
		t.Errorf("single-press channel objectID=%q should contain \"press_short\"", oidSingle)
	}
}
