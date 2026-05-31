// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeChannelInspector lets the climate / cover aggregators see a
// custom set of parameters without dragging in the full Channel
// model.
// fakeChannelInspector is the lightweight ChannelInspector test fake
// used by discovery_fixes_test.go and parity_p0_fixes_test.go to drive
// per-parameter classifyComponent paths.
type fakeChannelInspector struct {
	params map[string]struct{}
}

func (f *fakeChannelInspector) HasParameter(name string) bool {
	_, ok := f.params[name]
	return ok
}

// TestClimatePresetModesExcludesNone pins HA's contract that "none"
// is the implicit unset preset and must never appear in preset_modes.
// ADR 0010: preset_modes comes from the model builder; the aggregator
// passes it through. The model is responsible for excluding "none".
func TestClimatePresetModesExcludesNone(t *testing.T) {
	t.Parallel()
	// Model supplies "boost" — "none" must NOT be in this list.
	src := &stubBuilder{
		component: "climate",
		body: map[string]any{
			"preset_modes": []string{"boost"},
		},
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "000A1709AF4FC9",
		DeviceName:    "Heizkörperthermostat WB",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
		Model:         "HmIP-eTRV-2",
	}

	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for climate channel")
	}
	if comp != string(HAComponentClimate) {
		t.Fatalf("component=%q want %q", comp, HAComponentClimate)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}

	raw, present := payload["preset_modes"]
	if !present {
		t.Fatal("preset_modes missing — builder supplies it")
	}
	list, _ := raw.([]any)
	if len(list) == 0 {
		t.Fatal("preset_modes must not be empty when builder provides it")
	}
	for _, m := range list {
		if m == "none" {
			t.Fatalf("preset_modes must not contain 'none' (HA reserves it); got %v", list)
		}
	}
	foundBoost := false
	for _, m := range list {
		if m == "boost" {
			foundBoost = true
		}
	}
	if !foundBoost {
		t.Errorf("preset_modes=%v missing 'boost'", list)
	}
}

// TestClimateOmitsPresetModesWhenBuilderExcludes it guards against
// accidentally emitting a preset_modes block when the model builder
// omits it. ADR 0010: the aggregator passes through the builder body
// without adding preset_modes on its own.
func TestClimateOmitsPresetModesWhenBuilderExcludes(t *testing.T) {
	t.Parallel()
	// Builder body has no preset_modes → aggregator must not add one.
	src := &stubBuilder{
		component: "climate",
		body:      map[string]any{"modes": []string{"auto", "heat"}},
	}
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "000A1709AF4FC9",
		ChannelNo:     1,
		ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
		Model:         "HM-CC-RT-no-boost",
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var payload map[string]any
	_ = json.Unmarshal(buf, &payload)
	if _, present := payload["preset_modes"]; present {
		t.Fatalf("preset_modes must be absent when builder does not supply it; got %v", payload["preset_modes"])
	}
}

// TestMaintenanceParametersClassifiedForDiscovery pins that maintenance /
// diagnostic parameters (DEFAULT_DATA_POINTS on ch0) carry the correct
// DataPointCategory so componentFromCategory resolves them. Previously
// checked via classifyComponent; now the source is responsible for
// populating Event.Category (ADR 0011).
func TestMaintenanceParametersClassifiedForDiscovery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		category hmenum.DataPointCategory
		want     HAComponent
	}{
		// Numeric maintenance sensors.
		{hmenum.DataPointCategorySensor, HAComponentSensor},
		// Boolean maintenance flags.
		{hmenum.DataPointCategoryBinarySensor, HAComponentBinarySensor},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.category), func(t *testing.T) {
			t.Parallel()
			comp, ok := componentFromCategory(tc.category)
			if !ok {
				t.Fatalf("componentFromCategory(%q) returned ok=false — HA Discovery would never emit", tc.category)
			}
			if comp != tc.want {
				t.Fatalf("componentFromCategory(%q)=%q want %q", tc.category, comp, tc.want)
			}
		})
	}
}

// TestSirenStatusFlagsClassifiedAsBinarySensor pins that status-flag
// parameters (ACOUSTIC_ALARM_ACTIVE / OPTICAL_ALARM_ACTIVE) carry
// DataPointCategoryBinarySensor so they surface as binary_sensor entities —
// not as siren, which would drop command_topic and trigger HA's
// `required key not provided @ data['command_topic']` reject.
func TestSirenStatusFlagsClassifiedAsBinarySensor(t *testing.T) {
	t.Parallel()
	comp, ok := componentFromCategory(hmenum.DataPointCategoryBinarySensor)
	if !ok {
		t.Fatalf("componentFromCategory(DataPointCategoryBinarySensor) returned ok=false")
	}
	if comp != HAComponentBinarySensor {
		t.Errorf("componentFromCategory(DataPointCategoryBinarySensor)=%q want %q", comp, HAComponentBinarySensor)
	}
}

// TestPerParameterSirenStatusFlagDiscoveryClassifiesAsBinarySensor walks
// the full Build path for a siren-status flag and asserts the produced
// payload satisfies HA's binary_sensor schema. binary_sensor is
// read-only — the payload must contain `state_topic` but MUST NOT
// contain `command_topic` / `payload_on` / `payload_off` /
// `state_on` / `state_off` (those are switch-only fields). Catches
// a regression where the flags would slip back into the Siren
// classification or pick up the read/write switch case-arm.
func TestPerParameterSirenStatusFlagDiscoveryClassifiesAsBinarySensor(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "000AD709A75609",
		DeviceName:    "Alarmsirene WZ",
		ChannelNo:     3,
		Model:         "HmIP-ASIR",
		Parameter:     "ACOUSTIC_ALARM_ACTIVE",
		// Status flags carry DataPointCategoryBinarySensor (read-only).
		Category: hmenum.DataPointCategoryBinarySensor,
	}

	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	if comp != string(HAComponentBinarySensor) {
		t.Fatalf("component=%q want %q", comp, HAComponentBinarySensor)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if _, present := payload["state_topic"]; !present {
		t.Error("missing required state_topic on binary_sensor")
	}
	// `command_topic`, `state_on`, `state_off` are switch-only and
	// MUST NOT appear on a read-only binary_sensor. `payload_on` /
	// `payload_off` ARE valid on binary_sensor — they tell HA which
	// post-template tokens count as "ON" / "OFF" (default would be
	// uppercase "ON"/"OFF" which never matches the lower-piped JSON
	// boolean rendering); we deliberately set them to "true"/"false".
	for _, switchOnly := range []string{"command_topic", "state_on", "state_off"} {
		if _, present := payload[switchOnly]; present {
			t.Errorf("read-only binary_sensor must not carry %q (switch-only field): payload[%q] = %v",
				switchOnly, switchOnly, payload[switchOnly])
		}
	}
}

// TestClimateBuilderBoundsPassThrough pins that min_temp / max_temp set by
// the model builder pass through the aggregator verbatim. ADR 0010: the model
// layer computes the correct bounds and the aggregator never adjusts them.
func TestClimateBuilderBoundsPassThrough(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    map[string]any
		wantMin float64
		wantMax float64
	}{
		{
			name:    "no_bounds_in_builder_body",
			body:    map[string]any{"modes": []string{"auto", "heat"}},
			wantMin: 0, // absent → zero from json.Unmarshal
			wantMax: 0,
		},
		{
			name:    "bounds_supplied_verbatim",
			body:    map[string]any{"min_temp": 5.0, "max_temp": 35.0},
			wantMin: 5.0,
			wantMax: 35.0,
		},
		{
			name:    "operator_override_values",
			body:    map[string]any{"min_temp": 12.0, "max_temp": 26.0},
			wantMin: 12.0,
			wantMax: 26.0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := &stubBuilder{component: "climate", body: tc.body}
			db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
			ev := Event{
				Source:        src,
				Interface:     "HmIP-RF",
				DeviceAddress: "0001ABCD",
				ChannelNo:     1,
				Model:         "HmIP-BWTH",
				ChannelType:   "CLIMATECONTROL_RT_TRANSCEIVER",
			}
			_, _, _, buf, ok := db.Build(ev)
			if !ok {
				t.Fatal("Build climate aggregate failed")
			}
			var payload map[string]any
			if err := json.Unmarshal(buf, &payload); err != nil {
				t.Fatalf("payload JSON: %v", err)
			}
			gotMin, _ := payload["min_temp"].(float64)
			gotMax, _ := payload["max_temp"].(float64)
			if gotMin != tc.wantMin {
				t.Errorf("min_temp=%v want %v", gotMin, tc.wantMin)
			}
			if gotMax != tc.wantMax {
				t.Errorf("max_temp=%v want %v", gotMax, tc.wantMax)
			}
		})
	}
}

// TestAlarmMessagesDiscoveryHasNoBinarySensorOnlyDeviceClass pins the
// fix for HA's reject `expected SensorDeviceClass …` on
// `homeassistant/sensor/<n>_messages/alarm/config`. `device_class:
// Problem` only validates on binary_sensor
// hub entry as a plain measurement sensor.
func TestAlarmMessagesDiscoveryHasNoBinarySensorOnlyDeviceClass(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	item := db.BuildAlarmMessagesDiscovery("ccu-01")
	if !item.OK {
		t.Fatal("Build returned OK=false")
	}
	if item.Component != string(HAComponentSensor) {
		t.Fatalf("component=%q want %q", item.Component, HAComponentSensor)
	}
	var payload map[string]any
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if dc, present := payload["device_class"]; present {
		t.Fatalf("device_class must not be set on the alarm-messages sensor entity (got %v); 'problem' is binary_sensor-only", dc)
	}
}
