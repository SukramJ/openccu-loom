// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// TextDisplay discovery tests
// ---------------------------------------------------------------------------

// TestBuildTextDisplayPayloadSchema verifies that the ADR 0010 builder path
// emits a valid HA `text` discovery payload with the mandatory fields. The
// stubBuilder supplies the platform-specific fields; the aggregator overlays
// the base body.
func TestBuildTextDisplayPayloadSchema(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Source: &stubBuilder{
			component: "text",
			body: map[string]any{
				"command_topic":  "gh/ccu/HmIP-RF/0012WRCD/3/DISPLAY_DATA_STRING/set",
				"state_topic":    "gh/ccu/HmIP-RF/0012WRCD/3/state",
				"mode":           "text",
				"min":            0,
				"max":            64,
				"value_template": `{{ value_json.text | default("") }}`,
			},
		},
		Interface:     "HmIP-RF",
		DeviceAddress: "0012WRCD",
		DeviceName:    "Keller Display",
		ChannelNo:     3,
		ChannelType:   "IPTextDisplay",
		Model:         "HmIP-WRCD",
	}

	comp, nodeID, objectID, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for IPTextDisplay channel")
	}
	if comp != string(HAComponentText) {
		t.Fatalf("component=%q want %q", comp, HAComponentText)
	}
	if nodeID == "" {
		t.Fatal("nodeID must not be empty")
	}
	if objectID == "" {
		t.Fatal("objectID must not be empty")
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}

	requiredKeys := []string{
		"command_topic",
		"state_topic",
		"mode",
		"min",
		"max",
		"name",
		"unique_id",
		"availability",
		"device",
		"origin",
	}
	for _, k := range requiredKeys {
		if _, present := payload[k]; !present {
			t.Errorf("missing required field %q in TextDisplay discovery payload", k)
		}
	}
	if _, present := payload["object_id"]; present {
		t.Error("object_id must be absent in payload — HA derives entity_id from device.name + name")
	}

	if payload["mode"] != "text" {
		t.Errorf("mode=%v want \"text\"", payload["mode"])
	}
	// Max length mirrors.
	if maxV, _ := payload["max"].(float64); int(maxV) != 64 {
		t.Errorf("max=%v want 64", payload["max"])
	}
	if minV, _ := payload["min"].(float64); int(minV) != 0 {
		t.Errorf("min=%v want 0", payload["min"])
	}

	// command_topic must not be empty.
	cmdTopic, _ := payload["command_topic"].(string)
	if cmdTopic == "" {
		t.Error("command_topic must not be empty")
	}

	// value_template must be present for proper state display.
	if _, present := payload["value_template"]; !present {
		t.Error("value_template must be present in TextDisplay discovery payload")
	}
}

// TestBuildTextDisplayChannelTypeBuilderDispatch verifies that the aggregator
// dispatches to the HADiscoveryPayloadBuilder fast path for a text display
// channel (IPTEXTDISPLAY). The legacy domainForChannelType routing has been
// removed in ADR 0010. This test pins the end-to-end Build result.
func TestBuildTextDisplayChannelTypeBuilderDispatch(t *testing.T) {
	cases := []string{
		"IPTextDisplay",
		"iptextdisplay",
		"IPTEXTDISPLAY",
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	for _, ct := range cases {
		ev := Event{
			Source: &stubBuilder{
				component: "text",
				body:      map[string]any{"mode": "text"},
			},
			Interface:     "HmIP-RF",
			DeviceAddress: "0012WRCD",
			ChannelNo:     3,
			ChannelType:   ct,
			Model:         "HmIP-WRCD",
		}
		comp, _, _, _, ok := db.Build(ev)
		if !ok {
			t.Errorf("Build(ChannelType=%q) returned ok=false; stubBuilder must dispatch", ct)
			continue
		}
		if comp != string(HAComponentText) {
			t.Errorf("Build(ChannelType=%q): component=%q want %q", ct, comp, HAComponentText)
		}
	}
}

// TestTextDisplayEntityDescription verifies that LookupTextDisplayByDevice
// returns a valid EntityDescription for HmIP-WRCD (exact and prefix).
func TestTextDisplayEntityDescription(t *testing.T) {
	cases := []struct {
		model   string
		wantHit bool
	}{
		{"HmIP-WRCD", true},
		{"HmIP-WRCD-2", true}, // future variant — prefix match
		{"HmIP-STH", false},   // thermostat — no text display
	}
	for _, tc := range cases {
		d, ok := LookupTextDisplayByDevice(tc.model)
		if ok != tc.wantHit {
			t.Errorf("LookupTextDisplayByDevice(%q): ok=%v want %v", tc.model, ok, tc.wantHit)
		}
		if ok && !d.EnabledByDefault {
			t.Errorf("LookupTextDisplayByDevice(%q): EnabledByDefault must be true", tc.model)
		}
	}
}

// ---------------------------------------------------------------------------
// Button Press-Type event discovery tests
// ---------------------------------------------------------------------------

// TestPressTypeClassifyAllVariants verifies that DataPointCategoryEvent
// resolves to HAComponentEvent via componentFromCategory. Per ADR 0011,
// PRESS_* parameters carry DataPointCategoryEvent on the wire — no
// parameter-name heuristic needed.
func TestPressTypeClassifyAllVariants(t *testing.T) {
	comp, ok := componentFromCategory(hmenum.DataPointCategoryEvent)
	if !ok {
		t.Errorf("componentFromCategory(DataPointCategoryEvent) returned ok=false, want HAComponentEvent")
		return
	}
	if comp != HAComponentEvent {
		t.Errorf("componentFromCategory(DataPointCategoryEvent)=%q want %q", comp, HAComponentEvent)
	}
}

// TestPressTypeDiscoveryPayloadHasEventTypes verifies that a PRESS_SHORT
// parameter produces a valid `event` discovery payload with `event_types`
// and `device_class: "button"`. value_template is intentionally absent —
// HA's mqtt.event component parses the post-template payload as JSON and
// extracts `event_type` from it, so a scalar-extracting template breaks
// the parser. The bridge already publishes a `{"event_type": ...}`
// envelope on the channel/event topic.
func TestPressTypeDiscoveryPayloadHasEventTypes(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0034BTN",
		DeviceName:    "Flur Taster",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
		Category:      hmenum.DataPointCategoryEvent,
	}

	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for PRESS_SHORT")
	}
	if comp != string(HAComponentEvent) {
		t.Fatalf("component=%q want %q", comp, HAComponentEvent)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}

	// event_types must be present and contain "press_short".
	etRaw, present := payload["event_types"]
	if !present {
		t.Fatal("event_types missing from PRESS_SHORT discovery payload")
	}
	etList, _ := etRaw.([]any)
	if len(etList) == 0 {
		t.Fatal("event_types must not be empty")
	}
	found := false
	for _, et := range etList {
		if et == "press_short" {
			found = true
		}
	}
	if !found {
		t.Errorf("event_types=%v does not contain \"press_short\"", etList)
	}

	// device_class must be "button".
	if dc := payload["device_class"]; dc != "button" {
		t.Errorf("device_class=%v want \"button\"", dc)
	}

	// value_template must be ABSENT — HA reads `event_type` directly
	// from the JSON envelope without a template.
	if _, present := payload["value_template"]; present {
		t.Errorf("value_template must be absent from event discovery payload (HA parses raw JSON)")
	}
}

// TestPressTypeThreeSubeventsThreeDiscoveries verifies that SHORT, LONG, and
// LONG_RELEASE each produce a separate and distinct discovery payload —
// i.e. three Events from the same channel produce three unique `event` entities
// with differing event_types lists.
func TestPressTypeThreeSubeventsThreeDiscoveries(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	baseEv := Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0034BTN",
		DeviceName:    "Flur Taster",
		ChannelNo:     1,
		Category:      hmenum.DataPointCategoryEvent,
	}

	pressParams := map[string]string{
		"PRESS_SHORT":        "press_short",
		"PRESS_LONG":         "press_long",
		"PRESS_LONG_RELEASE": "press_long_release",
	}

	type result struct {
		comp      string
		objectID  string
		eventType string
	}
	results := make([]result, 0, len(pressParams))

	for param, wantET := range pressParams {
		ev := baseEv
		ev.Parameter = param
		comp, _, objectID, buf, ok := db.Build(ev)
		if !ok {
			t.Fatalf("Build(%q) returned ok=false", param)
		}
		var payload map[string]any
		if err := json.Unmarshal(buf, &payload); err != nil {
			t.Fatalf("Build(%q): invalid JSON: %v", param, err)
		}
		etRaw, _ := payload["event_types"].([]any)
		found := false
		for _, et := range etRaw {
			if et == wantET {
				found = true
			}
		}
		if !found {
			t.Errorf("Build(%q): event_types=%v does not contain %q", param, etRaw, wantET)
		}
		results = append(results, result{comp, objectID, wantET})
	}

	// All three must be HAComponentEvent.
	for _, r := range results {
		if r.comp != string(HAComponentEvent) {
			t.Errorf("component=%q want %q for %s", r.comp, HAComponentEvent, r.eventType)
		}
	}

	// All three object_ids must be distinct (no dedup collision across press types).
	seen := make(map[string]bool)
	for _, r := range results {
		if seen[r.objectID] {
			t.Errorf("objectID %q appears more than once — press-type entities must not share object_id", r.objectID)
		}
		seen[r.objectID] = true
	}
}

// TestPressEventTypesFor pins the pressEventTypesFor helper against all
// known HM parameter names including the lower-case fallback.
func TestPressEventTypesFor(t *testing.T) {
	cases := map[string]string{
		"PRESS_SHORT":        "press_short",
		"PRESS_LONG":         "press_long",
		"PRESS_LONG_RELEASE": "press_long_release",
		"PRESS_LONG_START":   "press_long_start",
		"PRESS_UNKNOWN":      "press_unknown", // fallback: lower-case
	}
	for param, wantFirst := range cases {
		got := pressEventTypesFor(param)
		if len(got) == 0 {
			t.Errorf("pressEventTypesFor(%q) returned empty slice", param)
			continue
		}
		if got[0] != wantFirst {
			t.Errorf("pressEventTypesFor(%q)[0]=%q want %q", param, got[0], wantFirst)
		}
	}
}

// TestLookupEventDescriptions verifies that all four PRESS_* parameters
// have EntityDescriptions in the event table and that device_class is "button".
func TestLookupEventDescriptions(t *testing.T) {
	pressParams := []string{
		"PRESS_SHORT",
		"PRESS_LONG",
		"PRESS_LONG_RELEASE",
		"PRESS_LONG_START",
	}
	for _, p := range pressParams {
		d, ok := LookupEvent(p)
		if !ok {
			t.Errorf("LookupEvent(%q) returned ok=false", p)
			continue
		}
		if d.DeviceClass != "button" {
			t.Errorf("LookupEvent(%q).DeviceClass=%q want \"button\"", p, d.DeviceClass)
		}
		if !d.EnabledByDefault {
			t.Errorf("LookupEvent(%q).EnabledByDefault=false, want true", p)
		}
	}
}
