// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmDiscoveryBody unmarshals a [DiscoveryItem]'s payload for
// field-by-field assertions, mirroring the decode-then-assert style of
// discovery_ha_schema_test.go / discovery_payload_test.go.
func alarmDiscoveryBody(t *testing.T, item DiscoveryItem) map[string]any {
	t.Helper()
	if !item.OK {
		t.Fatalf("BuildAlarmPanelDiscovery returned OK=false")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal discovery payload: %v (raw=%s)", err, item.Payload)
	}
	return body
}

// TestBuildAlarmPanelDiscovery_AreaPanelShape covers the per-area
// discovery config: component/node/object routing, state+command
// topics, no value_template envelope (the topic carries the plain HA
// token directly), two-source availability with mode "all", both code
// flags hard-false, and supported_features tracking the configured
// modes.
func TestBuildAlarmPanelDiscovery_AreaPanelShape(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull, hmenum.AlarmModePerimeter}, false)

	if item.Component != string(HAComponentAlarmControlPanel) {
		t.Errorf("Component = %q, want %q", item.Component, HAComponentAlarmControlPanel)
	}
	if item.NodeID != alarmDiscoveryNodeID {
		t.Errorf("NodeID = %q, want %q", item.NodeID, alarmDiscoveryNodeID)
	}
	if item.ObjectID != "eg" {
		t.Errorf("ObjectID = %q, want %q", item.ObjectID, "eg")
	}

	body := alarmDiscoveryBody(t, item)

	if got, want := body["name"], "Erdgeschoss"; got != want {
		t.Errorf("name = %v, want %v", got, want)
	}
	if got, want := body["unique_id"], "openccu-loom_alarm_eg"; got != want {
		t.Errorf("unique_id = %v, want %v", got, want)
	}
	if got, want := body["object_id"], "openccu-loom_alarm_eg"; got != want {
		t.Errorf("object_id = %v, want %v", got, want)
	}
	if got, want := body["state_topic"], "gh/alarm/eg/state"; got != want {
		t.Errorf("state_topic = %v, want %v", got, want)
	}
	if got, want := body["command_topic"], "gh/alarm/eg/set"; got != want {
		t.Errorf("command_topic = %v, want %v", got, want)
	}
	if _, has := body["value_template"]; has {
		t.Errorf("discovery payload must not carry a value_template envelope; got %v", body["value_template"])
	}

	if got, want := body["code_arm_required"], false; got != want {
		t.Errorf("code_arm_required = %v, want %v", got, want)
	}
	if got, want := body["code_disarm_required"], false; got != want {
		t.Errorf("code_disarm_required = %v, want %v", got, want)
	}

	features, ok := body["supported_features"].([]any)
	if !ok {
		t.Fatalf("supported_features not a list: %v", body["supported_features"])
	}
	wantFeatures := []string{alarmpanel.HAAlarmFeatureArmHome, alarmpanel.HAAlarmFeatureArmAway}
	if len(features) != len(wantFeatures) {
		t.Fatalf("supported_features = %v, want %v", features, wantFeatures)
	}
	for i, want := range wantFeatures {
		if features[i] != want {
			t.Errorf("supported_features[%d] = %v, want %v", i, features[i], want)
		}
	}

	avail, ok := body["availability"].([]any)
	if !ok || len(avail) != 2 {
		t.Fatalf("availability = %v, want a 2-element list", body["availability"])
	}
	first, ok1 := avail[0].(map[string]any)
	second, ok2 := avail[1].(map[string]any)
	if !ok1 || !ok2 {
		t.Fatalf("availability entries not objects: %v", avail)
	}
	if got, want := first["topic"], "gh/bridge/status"; got != want {
		t.Errorf("availability[0].topic = %v, want %v", got, want)
	}
	if got, want := second["topic"], "gh/alarm/eg/availability"; got != want {
		t.Errorf("availability[1].topic = %v, want %v", got, want)
	}
	for i, entry := range []map[string]any{first, second} {
		if got, want := entry["payload_available"], "online"; got != want {
			t.Errorf("availability[%d].payload_available = %v, want %v", i, got, want)
		}
		if got, want := entry["payload_not_available"], "offline"; got != want {
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
	if got, want := device["name"], "OpenCCU-Loom Alarm"; got != want {
		t.Errorf("device.name = %v, want %v", got, want)
	}
	ids, ok := device["identifiers"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "openccu-loom_alarm" {
		t.Errorf("device.identifiers = %v, want [openccu-loom_alarm]", device["identifiers"])
	}

	if _, has := body["origin"]; !has {
		t.Errorf("origin block missing")
	}
}

// TestBuildAlarmPanelDiscovery_MasterPanel covers the aggregate master
// panel: the area segment is forced to the reserved "master" token
// regardless of the areaID argument, the caller-supplied (already
// localized) display name is used verbatim, and the topics/unique_id
// route through the master segment.
func TestBuildAlarmPanelDiscovery_MasterPanel(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "ignored-area-id", "Alarmanlage",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull}, true)

	if item.ObjectID != alarmMasterArea {
		t.Errorf("ObjectID = %q, want %q", item.ObjectID, alarmMasterArea)
	}
	body := alarmDiscoveryBody(t, item)
	if got, want := body["name"], "Alarmanlage"; got != want {
		t.Errorf("name = %v, want %v (localized name must pass through verbatim)", got, want)
	}
	if got, want := body["unique_id"], "openccu-loom_alarm_master"; got != want {
		t.Errorf("unique_id = %v, want %v", got, want)
	}
	if got, want := body["state_topic"], "gh/alarm/master/state"; got != want {
		t.Errorf("state_topic = %v, want %v", got, want)
	}
	if got, want := body["command_topic"], "gh/alarm/master/set"; got != want {
		t.Errorf("command_topic = %v, want %v", got, want)
	}
	avail, ok := body["availability"].([]any)
	if !ok || len(avail) != 2 {
		t.Fatalf("availability = %v, want a 2-element list", body["availability"])
	}
	second, ok := avail[1].(map[string]any)
	if !ok || second["topic"] != "gh/alarm/master/availability" {
		t.Errorf("availability[1].topic = %v, want gh/alarm/master/availability", avail[1])
	}
}

// TestBuildAlarmPanelDiscovery_MasterNameLocalizedBothLocales confirms
// the two locales this repo ships (en, de) carry distinct display
// strings under the "discovery.alarm_system" key that
// [AlarmMQTTPublisher.masterName] resolves — the discovery builder
// itself is locale-agnostic (it takes the resolved name as an
// argument), so this test locks the localization the publisher feeds
// it, not the builder's own logic.
func TestBuildAlarmPanelDiscovery_MasterNameLocalizedBothLocales(t *testing.T) {
	t.Parallel()
	names := map[string]string{
		"en": "Alarm system",
		"de": "Alarmanlage",
	}
	for locale, want := range names {
		item := BuildAlarmPanelDiscovery("gh", "", want, nil, true)
		body := alarmDiscoveryBody(t, item)
		if got := body["name"]; got != want {
			t.Errorf("locale %s: name = %v, want %v", locale, got, want)
		}
	}
}

// TestBuildAlarmPanelDiscovery_EmptyAreaIsRejected guards against
// publishing a discovery config with an empty topic/unique-id segment
// for a non-master panel with a blank areaID.
func TestBuildAlarmPanelDiscovery_EmptyAreaIsRejected(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "", "Nameless", nil, false)
	if item.OK {
		t.Fatalf("expected OK=false for an empty area segment, got %+v", item)
	}
}

// TestBuildAlarmPanelDiscovery_NoModesYieldsEmptyFeatureList covers an
// area with zero configured modes (e.g. mid-setup): the payload must
// still be valid, with an empty (not omitted/nil-crashing)
// supported_features list.
func TestBuildAlarmPanelDiscovery_NoModesYieldsEmptyFeatureList(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss", nil, false)
	body := alarmDiscoveryBody(t, item)
	features, ok := body["supported_features"].([]any)
	if !ok {
		t.Fatalf("supported_features not a list: %v", body["supported_features"])
	}
	if len(features) != 0 {
		t.Errorf("supported_features = %v, want empty", features)
	}
}
