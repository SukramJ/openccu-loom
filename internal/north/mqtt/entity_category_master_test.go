// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── MASTER-paramset entity_category rules ──────────────────────────────────
//
// All MASTER-paramset parameters must receive entity_category="config" by
// default so they surface in the HA "Configuration" section instead of the
// Primary entity list. This mirrors
// paramset (configuration parameters, not runtime state).
//
// Per-parameter overrides in EntityDescriptionFor take precedence: a MASTER
// param that already has a more-specific entity_category (e.g. "diagnostic")
// in the rules table keeps the rules-table value.

// TestMasterParamsetGetsCategoryConfig pins the default rule: a switch or
// sensor on the MASTER paramset receives entity_category="config".
// Per ADR 0011, every Event must carry a populated Category; the
// component is resolved via componentFromCategory(ev.Category).
func TestMasterParamsetGetsCategoryConfig(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")

	cases := []struct {
		name      string
		model     string
		parameter string
		category  hmenum.DataPointCategory
		writable  bool
		wantComp  HAComponent
	}{
		// MASTER switch (INHIBIT → DataPointCategorySwitch)
		{name: "switch_INHIBIT", model: "HmIP-BSM", parameter: "INHIBIT", category: hmenum.DataPointCategorySwitch, writable: true, wantComp: HAComponentSwitch},
		// MASTER number (SET_POINT_TEMPERATURE → DataPointCategoryNumber)
		{name: "number_SET_POINT_TEMPERATURE", model: "HmIP-BWTH", parameter: "SET_POINT_TEMPERATURE", category: hmenum.DataPointCategoryNumber, writable: true, wantComp: HAComponentNumber},
		// MASTER sensor (ACTUAL_TEMPERATURE → DataPointCategorySensor)
		{name: "sensor_ACTUAL_TEMPERATURE", model: "HmIP-BWTH", parameter: "ACTUAL_TEMPERATURE", category: hmenum.DataPointCategorySensor, writable: false, wantComp: HAComponentSensor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := Event{
				Central:       "ccu-01",
				Interface:     "HmIP-RF",
				DeviceAddress: "AABBCCDD",
				DeviceName:    "Test Device",
				Model:         tc.model,
				ChannelNo:     0,
				Parameter:     tc.parameter,
				Category:      tc.category,
				Writable:      tc.writable,
				Descriptor:    &pload.GenericConfig{Paramset: hmenum.ParamsetKeyMaster},
			}
			comp, _, _, buf, ok := db.Build(ev)
			if !ok {
				t.Fatalf("Build returned ok=false for parameter %s (category=%s)", tc.parameter, tc.category)
			}
			if HAComponent(comp) != tc.wantComp {
				t.Errorf("component=%q want %q for %s", comp, tc.wantComp, tc.parameter)
			}
			var payload map[string]any
			if err := json.Unmarshal(buf, &payload); err != nil {
				t.Fatalf("json unmarshal: %v", err)
			}
			got, _ := payload["entity_category"].(string)
			if got != EntityCategoryConfig {
				t.Errorf("entity_category=%q want %q for MASTER paramset parameter %s",
					got, EntityCategoryConfig, tc.parameter)
			}
		})
	}
}

// TestValuesParamsetNoAutoConfigCategory pins that VALUES-paramset parameters
// do NOT automatically receive entity_category="config". Only the
// per-parameter rules table (e.g. SCHEDULE_SWITCH, MOTION_DETECTION_ACTIVE)
// can assign "config" to VALUES params.
func TestValuesParamsetNoAutoConfigCategory(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")

	// STATE on VALUES paramset (generic switch) must NOT get entity_category.
	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		Model:         "HmIP-BSM",
		ChannelNo:     1,
		Parameter:     "STATE",
		Category:      hmenum.DataPointCategorySwitch,
		Writable:      true,
		Descriptor:    &pload.GenericConfig{Paramset: hmenum.ParamsetKeyValues},
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for VALUES STATE")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if cat, present := payload["entity_category"]; present {
		t.Errorf("entity_category=%v must be absent for VALUES-paramset STATE", cat)
	}
}

// TestMasterParamsetZeroValueTreatedAsValues pins that the Event zero value
// (Paramset == "") is treated as VALUES (no auto-config category).
func TestMasterParamsetZeroValueTreatedAsValues(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")

	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		Model:         "HmIP-BSM",
		ChannelNo:     1,
		Parameter:     "STATE",
		Category:      hmenum.DataPointCategorySwitch,
		Writable:      true,
		// Paramset intentionally zero — should not trigger MASTER default.
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if cat, present := payload["entity_category"]; present {
		t.Errorf("entity_category=%v must be absent when Paramset is zero value", cat)
	}
}

// TestMasterParamsetPerParamOverrideWins pins that a per-parameter override
// in EntityDescriptionFor takes precedence over the MASTER default. For
// example, SCHEDULE_SWITCH is already "config" in the switch rules table
// and RSSI_DEVICE is "diagnostic" in the sensor rules table — both should
// keep their specific category regardless of the Paramset field.
func TestMasterParamsetPerParamOverrideWins(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")

	// RSSI_DEVICE is diagnostic in sensorRulesByParam; when it also
	// arrives with Paramset=MASTER the "diagnostic" rule wins.
	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		Model:         "HmIP-STH",
		ChannelNo:     0,
		Parameter:     "RSSI_DEVICE",
		Category:      hmenum.DataPointCategorySensor,
		Writable:      false,
		Descriptor:    &pload.GenericConfig{Paramset: hmenum.ParamsetKeyMaster},
	}
	_, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for RSSI_DEVICE")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	// The per-param rule assigns "diagnostic"; the MASTER default sets
	// "config" first, then EntityDescriptionFor overwrites with "diagnostic".
	got, _ := payload["entity_category"].(string)
	if got != EntityCategoryDiagnostic {
		t.Errorf("entity_category=%q want %q (per-param override should win over MASTER default)",
			got, EntityCategoryDiagnostic)
	}
}

// TestMasterParamsetHmIPeTRVSnapshot is a device-level snapshot test for
// HmIP-eTRV MASTER-paramset parameters. It uses SET_POINT_TEMPERATURE
// (classifiable as "number") and checks that entity_category="config" appears.
func TestMasterParamsetHmIPeTRVSnapshot(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		DeviceName:    "Heizung Bad",
		Model:         "HmIP-eTRV-2",
		ChannelNo:     0,
		// SET_POINT_TEMPERATURE → DataPointCategoryNumber → HAComponentNumber
		Parameter: "SET_POINT_TEMPERATURE",
		Category:  hmenum.DataPointCategoryNumber,
		Writable:  true,
		Descriptor: &pload.GenericConfig{
			Paramset: hmenum.ParamsetKeyMaster,
			Min:      func() *float64 { v := 5.0; return &v }(),
			Max:      func() *float64 { v := 30.5; return &v }(),
		},
	}
	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for HmIP-eTRV SET_POINT_TEMPERATURE")
	}
	if comp != string(HAComponentNumber) {
		t.Errorf("component=%q want %q for writable float MASTER param", comp, HAComponentNumber)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entity_category"] != EntityCategoryConfig {
		t.Errorf("entity_category=%v want %q for HmIP-eTRV MASTER SET_POINT_TEMPERATURE",
			payload["entity_category"], EntityCategoryConfig)
	}
}

// TestMasterParamsetHmIPFROLLSnapshot pins that the HmIP-FROLL cover model
// MASTER-paramset parameters receive entity_category="config" when published
// through the per-parameter path (non-aggregate channel).
// Uses INHIBIT which is classifiable as a switch.
func TestMasterParamsetHmIPFROLLSnapshot(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		DeviceName:    "Rollladen Wohnzimmer",
		Model:         "HmIP-FROLL",
		// ch0 MASTER channel; no ChannelType → per-parameter path.
		// INHIBIT → DataPointCategorySwitch → HAComponentSwitch.
		ChannelNo:  0,
		Parameter:  "INHIBIT",
		Category:   hmenum.DataPointCategorySwitch,
		Writable:   true,
		Descriptor: &pload.GenericConfig{Paramset: hmenum.ParamsetKeyMaster},
	}
	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for HmIP-FROLL INHIBIT")
	}
	if comp != string(HAComponentSwitch) {
		t.Errorf("component=%q want %q for INHIBIT", comp, HAComponentSwitch)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entity_category"] != EntityCategoryConfig {
		t.Errorf("entity_category=%v want %q for HmIP-FROLL MASTER INHIBIT",
			payload["entity_category"], EntityCategoryConfig)
	}
}

// TestMasterParamsetHmIPBWTHSnapshot pins MASTER-paramset behavior for the
// HmIP-BWTH wall thermostat. CONTROL_MODE (a select) on a MASTER channel
// must get entity_category="config".
func TestMasterParamsetHmIPBWTHSnapshot(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	ev := Event{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "AABBCCDD",
		DeviceName:    "Wandthermostat Wohnzimmer",
		Model:         "HmIP-BWTH",
		ChannelNo:     0,
		Parameter:     "CONTROL_MODE",
		Category:      hmenum.DataPointCategorySelect,
		Writable:      true,
		Descriptor:    &pload.GenericConfig{Paramset: hmenum.ParamsetKeyMaster},
	}
	comp, _, _, buf, ok := db.Build(ev)
	if !ok {
		t.Fatal("Build returned ok=false for HmIP-BWTH CONTROL_MODE")
	}
	if comp != string(HAComponentSelect) {
		t.Errorf("component=%q want %q for CONTROL_MODE", comp, HAComponentSelect)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if payload["entity_category"] != EntityCategoryConfig {
		t.Errorf("entity_category=%v want %q for HmIP-BWTH MASTER CONTROL_MODE",
			payload["entity_category"], EntityCategoryConfig)
	}
}
