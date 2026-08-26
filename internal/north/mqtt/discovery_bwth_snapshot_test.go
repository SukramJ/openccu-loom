// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"testing"
)

// TestHmIPBWTHClimateSnapshot is the dataset-vs-dataset comparison
// anchor for the HmIP-BWTH climate discovery payload. It exercises the
// ADR 0010 fast path with an HmIP-BWTH-shaped stubBuilder and pins the
// expected HA-Discovery payload on the aggregator's emit boundary.
//
// The builder owns all platform-specific fields; the aggregator overlays
// the base body (name, unique_id, availability, device, origin).
// Expectations mirror what.py and
//
// - min_temp = 5.0, max_temp = 30.5, temp_step = 0.5
// - temperature_unit = "C"
// - modes = ["auto","heat"]
// - preset_modes = ["boost"]
// - current_humidity_topic present (HUMIDITY available)
func TestHmIPBWTHClimateSnapshot(t *testing.T) {
	t.Parallel()
	// Simulate what Climate.HADiscoveryPayload would return for HmIP-BWTH.
	src := &stubBuilder{
		component: "climate",
		body: map[string]any{
			"min_temp":                  5.0,
			"max_temp":                  30.5,
			"temp_step":                 0.5,
			"temperature_unit":          "C",
			"modes":                     []string{"auto", "heat"},
			"preset_modes":              []string{"boost"},
			"current_humidity_topic":    "gh/ccu-01/HmIP-RF/0001ABCD/1/state",
			"current_humidity_template": "{{ value_json.current_humidity }}",
		},
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	ev := Event{
		Source:         src,
		Central:        "ccu-01",
		Interface:      "HmIP-RF",
		DeviceAddress:  "0001ABCD",
		ChannelNo:      1,
		ChannelAddress: "0001ABCD:1",
		Model:          "HmIP-BWTH",
		ChannelType:    "CLIMATECONTROL_RT_TRANSCEIVER",
	}
	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for HmIP-BWTH")
	}
	if comp != string(HAComponentClimate) {
		t.Fatalf("component=%q want climate", comp)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}

	tt := []struct {
		key  string
		want any
	}{
		{"min_temp", 5.0},
		{"max_temp", 30.5},
		{"temp_step", 0.5},
		{"temperature_unit", "C"},
	}
	for _, tc := range tt {
		got, present := payload[tc.key]
		if !present {
			t.Errorf("missing %s in HmIP-BWTH discovery payload", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %v (%T) want %v", tc.key, got, got, tc.want)
		}
	}

	// preset_modes carries boost because the builder supplies it.
	pm, ok := payload["preset_modes"].([]any)
	if !ok {
		t.Fatalf("preset_modes missing or wrong type: %T", payload["preset_modes"])
	}
	if len(pm) != 1 || pm[0] != "boost" {
		t.Errorf("preset_modes=%v want [boost]", pm)
	}

	// current_humidity_topic present because builder includes it.
	if _, present := payload["current_humidity_topic"]; !present {
		t.Error("current_humidity_topic missing despite builder including it")
	}

	// Verify the OFF-floor: a builder with min_temp=4.5 must not be corrected
	// by the bridge. The aggregator passes builder body verbatim.
	src2 := &stubBuilder{
		component: "climate",
		body:      map[string]any{"min_temp": 4.5, "max_temp": 30.5},
	}
	ev2 := Event{
		Source:        src2,
		Interface:     "HmIP-RF",
		DeviceAddress: "0001BWTH2",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
	}
	_, _, _, buf2, ok2 := db.Build(ev2)
	if !ok2 {
		t.Fatal("Build returned ok=false for OFF-floor check")
	}
	var payload2 map[string]any
	if err := json.Unmarshal(buf2, &payload2); err != nil {
		t.Fatalf("payload2 JSON: %v", err)
	}
	if gotMin, _ := payload2["min_temp"].(float64); gotMin != 4.5 {
		t.Errorf("OFF-floor passthrough: min_temp=%v want 4.5 (bridge passes builder body verbatim)", gotMin)
	}
}
