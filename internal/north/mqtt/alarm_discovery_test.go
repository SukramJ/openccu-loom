// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"log/slog"
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

// TestBuildAlarmPanelDiscovery_AreaPanelShape covers the per-zone
// discovery config: component/node/object routing, state+command
// topics, no value_template envelope (the topic carries the plain HA
// token directly), two-source availability with mode "all", both code
// flags hard-false, and supported_features tracking the configured
// modes.
func TestBuildAlarmPanelDiscovery_AreaPanelShape(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull, hmenum.AlarmModePerimeter}, false, false, false)

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
	if got, want := body["unique_id"], "gh_openccu-loom_alarm_eg"; got != want {
		t.Errorf("unique_id = %v, want %v", got, want)
	}
	if got, want := body["default_entity_id"], "alarm_control_panel.gh_openccu-loom_alarm_eg"; got != want {
		t.Errorf("default_entity_id = %v, want %v", got, want)
	}
	if _, has := body["object_id"]; has {
		t.Errorf("discovery payload must not carry the removed object_id key; got %v", body["object_id"])
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
	// The panel always advertises the TRIGGER capability after the arm
	// modes (the raw command plane routes a TRIGGER payload onto the loud
	// panic path).
	wantFeatures := []string{alarmpanel.HAAlarmFeatureArmHome, alarmpanel.HAAlarmFeatureArmAway, alarmFeatureTrigger}
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
// panel: the zone segment is forced to the reserved "master" token
// regardless of the zoneID argument, the caller-supplied (already
// localized) display name is used verbatim, and the topics/unique_id
// route through the master segment.
func TestBuildAlarmPanelDiscovery_MasterPanel(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "ignored-zone-id", "Alarmanlage",
		[]hmenum.AlarmMode{hmenum.AlarmModeFull}, true, false, false)

	if item.ObjectID != alarmMasterZone {
		t.Errorf("ObjectID = %q, want %q", item.ObjectID, alarmMasterZone)
	}
	body := alarmDiscoveryBody(t, item)
	if got, want := body["name"], "Alarmanlage"; got != want {
		t.Errorf("name = %v, want %v (localized name must pass through verbatim)", got, want)
	}
	if got, want := body["unique_id"], "gh_openccu-loom_alarm_master"; got != want {
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
// the two locales this repo ships (en, de) carry distinct display strings
// under the "discovery.alarm_system" key that
// [AlarmMQTTPublisher.masterName] resolves.
//
// It used to claim that while proving nothing. The old body passed `want`
// into BuildAlarmPanelDiscovery and asserted that `want` came back out — the
// builder is locale-agnostic and takes the resolved name as an argument, so
// the catalogues were never read. Deleting "discovery.alarm_system" from
// both of them left it green, and the `locale` loop variable was used for
// nothing but the failure message.
//
// So it now resolves the name the way the publisher does, through
// [AlarmMQTTPublisher.masterName] and the real catalogues, and feeds *that*
// to the builder. The literals below are the assertion; the catalogue is the
// input.
func TestBuildAlarmPanelDiscovery_MasterNameLocalizedBothLocales(t *testing.T) {
	t.Parallel()
	names := map[string]string{
		"en": "Alarm system",
		"de": "Alarmanlage",
	}
	for locale, want := range names {
		got := alarmMasterNameForLocale(t, locale)
		if got != want {
			t.Errorf("locale %s: masterName() resolved %q, want %q — the catalogue entry for %s is "+
				"missing or has moved, and the alarm panel's Home Assistant name moves with it",
				locale, got, want, alarmMasterNameKey)
		}
		item := BuildAlarmPanelDiscovery("gh", "", got, nil, true, false, false)
		body := alarmDiscoveryBody(t, item)
		if name := body["name"]; name != want {
			t.Errorf("locale %s: published name = %v, want %v", locale, name, want)
		}
	}
}

// TestAlarmMasterNameFallsBackWhenTheCatalogueMisses is the branch an
// operator can reach: a `locale` matching no shipped catalogue resolves
// through the default one rather than rendering the raw key as the panel's
// name. The value it lands on happens to equal alarmMasterNameFallback, which
// is why the constant reads naturally here — but the Go literal itself is
// only reached when catalogue construction fails, and this does not exercise
// that path. See the note on
// TestSecurityPlaneDeviceNameUnknownLocaleUsesTheDefaultCatalogue.
func TestAlarmMasterNameFallsBackWhenTheCatalogueMisses(t *testing.T) {
	t.Parallel()
	if got := alarmMasterNameForLocale(t, "xx-unknown"); got != alarmMasterNameFallback {
		t.Errorf("unknown locale resolved the alarm master name to %q, want the fallback %q",
			got, alarmMasterNameFallback)
	}
}

// alarmMasterNameForLocale resolves the alarm panel's display name through a
// real publisher in the given locale, catalogues and all.
func alarmMasterNameForLocale(t *testing.T, locale string) string {
	t.Helper()
	bridge := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true, Locale: locale,
	}, newObservedPlane())
	p := NewAlarmMQTTPublisher(nil, NewWiring(bridge, slog.Default()), slog.Default())
	if p.locale != locale {
		t.Fatalf("publisher locale = %q, want %q; it is no longer taking the locale from the bridge "+
			"config and this test is resolving against the wrong catalogue", p.locale, locale)
	}
	return p.masterName()
}

// TestBuildAlarmPanelDiscovery_EmptyAreaIsRejected guards against
// publishing a discovery config with an empty topic/unique-id segment
// for a non-master panel with a blank zoneID.
func TestBuildAlarmPanelDiscovery_EmptyAreaIsRejected(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "", "Nameless", nil, false, false, false)
	if item.OK {
		t.Fatalf("expected OK=false for an empty zone segment, got %+v", item)
	}
}

// TestBuildAlarmPanelDiscovery_NoModesYieldsTriggerOnlyFeatureList covers
// an zone with zero configured modes (e.g. mid-setup): the payload must
// still be valid, carrying only the always-present TRIGGER capability and
// no arm-mode features.
func TestBuildAlarmPanelDiscovery_NoModesYieldsTriggerOnlyFeatureList(t *testing.T) {
	t.Parallel()
	item := BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss", nil, false, false, false)
	body := alarmDiscoveryBody(t, item)
	features, ok := body["supported_features"].([]any)
	if !ok {
		t.Fatalf("supported_features not a list: %v", body["supported_features"])
	}
	if len(features) != 1 || features[0] != alarmFeatureTrigger {
		t.Errorf("supported_features = %v, want [%s]", features, alarmFeatureTrigger)
	}
}

// TestAlarmEntityCategoriesMatchTheirRole pins where Home Assistant
// files each alarm entity.
//
// The reset button shipped as `entity_category: "config"`, which puts it
// in a collapsed section of the device page and keeps it out of
// dashboards and the entity picker's default view. It is a control an
// operator presses during an incident, not a setting, and it was
// reported as missing by someone looking for it. The count beside it is
// a readout and stays diagnostic.
func TestAlarmEntityCategoriesMatchTheirRole(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, item DiscoveryItem) map[string]any {
		t.Helper()
		if !item.OK {
			t.Fatal("discovery item not OK")
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return body
	}

	reset := decode(t, BuildAlarmMotionResetDiscovery("base", "zone-1", "Zone", "Reset motion", false))
	if cat, ok := reset["entity_category"]; ok {
		t.Errorf("reset button carries entity_category %q; a control belongs to the "+
			"device's main surface like the panel entity, which has none", cat)
	}

	panel := decode(t, BuildAlarmPanelDiscovery("base", "zone-1", "Zone", nil, false, false, false))
	if cat, ok := panel["entity_category"]; ok {
		t.Errorf("panel carries entity_category %q, want none", cat)
	}

	count := decode(t, BuildAlarmTriggeredMotionDiscovery("base", "zone-1", "Zone", "Triggered motion detectors", false))
	if got := count["entity_category"]; got != "diagnostic" {
		t.Errorf("triggered-motion count entity_category = %v, want diagnostic", got)
	}
}
