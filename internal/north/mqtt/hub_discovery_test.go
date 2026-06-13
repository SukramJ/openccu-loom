// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// helper: build a standard builder for all hub-discovery tests. The
// per-central serial is stamped up front because hub discovery payloads
// skip publishing (OK=false) until the serial that feeds their
// unique_ids is known.
func newHubBuilder() *DefaultDiscoveryBuilder {
	b := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01")
	b.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	return b
}

// jsonMap decodes a DiscoveryItem's Payload into a map for assertion.
func jsonMap(t *testing.T, item DiscoveryItem) map[string]any {
	t.Helper()
	if !item.OK {
		t.Fatal("DiscoveryItem.OK=false, cannot decode payload")
	}
	var m map[string]any
	if err := json.Unmarshal(item.Payload, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return m
}

// ─── Sysvar component selection (table-driven) ──────────────────────────────

func TestSysvarComponentSelection(t *testing.T) {
	t.Parallel()

	fptr := func(v float64) *float64 { return &v }
	valueList2 := []string{"A", "B"}

	cases := []struct {
		name      string
		sv        HubSysvarSpec
		wantComp  string
		wantOK    bool
		wantNode  string
		wantObjID string // derived from lower-cased sv.Name
	}{
		{
			name:      "Logic extended writable → switch",
			sv:        HubSysvarSpec{Name: "Active", ValueType: hmenum.HubValueTypeLogic, Writable: true, IsExtended: true},
			wantComp:  "switch",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "active",
		},
		{
			name:      "Logic writable but not extended → binary_sensor",
			sv:        HubSysvarSpec{Name: "Active", ValueType: hmenum.HubValueTypeLogic, Writable: true},
			wantComp:  "binary_sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "active",
		},
		{
			name:      "Logic read-only → binary_sensor",
			sv:        HubSysvarSpec{Name: "Active", ValueType: hmenum.HubValueTypeLogic, Writable: false},
			wantComp:  "binary_sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "active",
		},
		{
			name:      "Alarm extended writable → switch",
			sv:        HubSysvarSpec{Name: "Alarm", ValueType: hmenum.HubValueTypeAlarm, Writable: true, IsExtended: true},
			wantComp:  "switch",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "alarm",
		},
		{
			name:      "Alarm writable but not extended → binary_sensor",
			sv:        HubSysvarSpec{Name: "Alarm", ValueType: hmenum.HubValueTypeAlarm, Writable: true},
			wantComp:  "binary_sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "alarm",
		},
		{
			name:      "Alarm read-only → binary_sensor",
			sv:        HubSysvarSpec{Name: "Alarm", ValueType: hmenum.HubValueTypeAlarm, Writable: false},
			wantComp:  "binary_sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "alarm",
		},
		{
			name:      "List extended writable with 2 entries → select",
			sv:        HubSysvarSpec{Name: "Mode", ValueType: hmenum.HubValueTypeList, Writable: true, IsExtended: true, ValueList: valueList2},
			wantComp:  "select",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "mode",
		},
		{
			name:      "List writable but not extended → sensor",
			sv:        HubSysvarSpec{Name: "Mode", ValueType: hmenum.HubValueTypeList, Writable: true, ValueList: valueList2},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "mode",
		},
		{
			name:      "List read-only with 2 entries → sensor",
			sv:        HubSysvarSpec{Name: "Mode", ValueType: hmenum.HubValueTypeList, Writable: false, ValueList: valueList2},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "mode",
		},
		{
			name:      "List extended writable empty → sensor (not select)",
			sv:        HubSysvarSpec{Name: "Mode", ValueType: hmenum.HubValueTypeList, Writable: true, IsExtended: true, ValueList: nil},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "mode",
		},
		{
			name:      "List read-only empty → sensor",
			sv:        HubSysvarSpec{Name: "Mode", ValueType: hmenum.HubValueTypeList, Writable: false, ValueList: nil},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "mode",
		},
		{
			name:      "String extended writable → text",
			sv:        HubSysvarSpec{Name: "Msg", ValueType: hmenum.HubValueTypeString, Writable: true, IsExtended: true},
			wantComp:  "text",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "msg",
		},
		{
			name:      "String writable but not extended → sensor (text caps at 255)",
			sv:        HubSysvarSpec{Name: "Msg", ValueType: hmenum.HubValueTypeString, Writable: true},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "msg",
		},
		{
			name:      "String read-only → sensor",
			sv:        HubSysvarSpec{Name: "Msg", ValueType: hmenum.HubValueTypeString, Writable: false},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "msg",
		},
		{
			name:      "Number extended writable → number",
			sv:        HubSysvarSpec{Name: "Count", ValueType: hmenum.HubValueTypeNumber, Writable: true, IsExtended: true, Min: fptr(0), Max: fptr(100)},
			wantComp:  "number",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "count",
		},
		{
			name:      "Number writable but not extended → sensor",
			sv:        HubSysvarSpec{Name: "Count", ValueType: hmenum.HubValueTypeNumber, Writable: true},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "count",
		},
		{
			name:      "Number read-only → sensor",
			sv:        HubSysvarSpec{Name: "Count", ValueType: hmenum.HubValueTypeNumber, Writable: false},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "count",
		},
		{
			name:      "Float extended writable → number",
			sv:        HubSysvarSpec{Name: "Temp", ValueType: hmenum.HubValueTypeFloat, Writable: true, IsExtended: true},
			wantComp:  "number",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "temp",
		},
		{
			name:      "Float read-only → sensor",
			sv:        HubSysvarSpec{Name: "Temp", ValueType: hmenum.HubValueTypeFloat, Writable: false},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "temp",
		},
		{
			name:      "Integer extended writable → number",
			sv:        HubSysvarSpec{Name: "Idx", ValueType: hmenum.HubValueTypeInteger, Writable: true, IsExtended: true},
			wantComp:  "number",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "idx",
		},
		{
			name:      "Integer read-only → sensor",
			sv:        HubSysvarSpec{Name: "Idx", ValueType: hmenum.HubValueTypeInteger, Writable: false},
			wantComp:  "sensor",
			wantOK:    true,
			wantNode:  "ccu-01_sysvars",
			wantObjID: "idx",
		},
	}

	db := newHubBuilder()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := db.BuildSysvarDiscovery("ccu-01", tc.sv)
			if item.OK != tc.wantOK {
				t.Fatalf("OK: got %v want %v", item.OK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if item.Component != tc.wantComp {
				t.Fatalf("Component: got %q want %q", item.Component, tc.wantComp)
			}
			if item.NodeID != tc.wantNode {
				t.Fatalf("NodeID: got %q want %q", item.NodeID, tc.wantNode)
			}
			if item.ObjectID != tc.wantObjID {
				t.Fatalf("ObjectID: got %q want %q", item.ObjectID, tc.wantObjID)
			}
		})
	}
}

// ─── Sysvar empty name → OK==false ──────────────────────────────────────────

func TestSysvarEmptyNameReturnsNotOK(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{ValueType: hmenum.HubValueTypeLogic})
	if item.OK {
		t.Fatal("expected OK=false for empty sysvar name")
	}
}

// ─── Sysvar payload fields — LOGIC writable ─────────────────────────────────

func TestSysvarPayloadLogicWritable(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Active",
		ValueType:  hmenum.HubValueTypeLogic,
		Writable:   true,
		IsExtended: true,
	})
	m := jsonMap(t, item)
	if _, hasCmd := m["command_topic"]; !hasCmd {
		t.Fatal("expected command_topic in LOGIC writable payload")
	}
	if m["payload_on"] != "true" {
		t.Fatalf("payload_on: got %v want %q", m["payload_on"], "true")
	}
	if m["payload_off"] != "false" {
		t.Fatalf("payload_off: got %v want %q", m["payload_off"], "false")
	}
	if m["state_on"] != "true" {
		t.Fatalf("state_on: got %v want %q", m["state_on"], "true")
	}
	if m["state_off"] != "false" {
		t.Fatalf("state_off: got %v want %q", m["state_off"], "false")
	}
}

// ─── Sysvar payload fields — NUMBER writable with Min/Max/Unit ──────────────

func TestSysvarPayloadNumberWritable(t *testing.T) {
	t.Parallel()
	fptr := func(v float64) *float64 { return &v }
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Brightness",
		ValueType:  hmenum.HubValueTypeNumber,
		Writable:   true,
		IsExtended: true,
		Min:        fptr(0),
		Max:        fptr(100),
		Unit:       "%",
	})
	m := jsonMap(t, item)
	if _, hasCmd := m["command_topic"]; !hasCmd {
		t.Fatal("expected command_topic in NUMBER writable payload")
	}
	if m["min"] != float64(0) {
		t.Fatalf("min: got %v want 0", m["min"])
	}
	if m["max"] != float64(100) {
		t.Fatalf("max: got %v want 100", m["max"])
	}
	if m["unit_of_measurement"] != "%" {
		t.Fatalf("unit_of_measurement: got %v want %%", m["unit_of_measurement"])
	}
	if m["mode"] != "auto" {
		t.Fatalf("mode: got %v want %q", m["mode"], "auto")
	}
}

// ─── Sysvar payload fields — INTEGER writable uses mode:box ─────────────────

func TestSysvarPayloadIntegerWritableHasModeBox(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Counter",
		ValueType:  hmenum.HubValueTypeInteger,
		Writable:   true,
		IsExtended: true,
	})
	m := jsonMap(t, item)
	if m["mode"] != "box" {
		t.Fatalf("mode: got %v want %q", m["mode"], "box")
	}
}

// ─── Sysvar payload fields — LIST writable options ──────────────────────────

func TestSysvarPayloadListWritableOptions(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Scene",
		ValueType:  hmenum.HubValueTypeList,
		Writable:   true,
		IsExtended: true,
		ValueList:  []string{"A", "B", "C"},
	})
	m := jsonMap(t, item)
	raw, ok := m["options"]
	if !ok {
		t.Fatal("expected options in LIST writable payload")
	}
	opts, _ := raw.([]any)
	if len(opts) != 3 {
		t.Fatalf("options length: got %d want 3", len(opts))
	}
	if opts[0] != "A" || opts[1] != "B" || opts[2] != "C" {
		t.Fatalf("options values: %v", opts)
	}
}

// ─── Program builder ─────────────────────────────────────────────────────────

func TestProgramBuilderHappyPath(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildProgramDiscovery("ccu-01", "PRG_42", "Morning Lights")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "switch" {
		t.Fatalf("Component: got %q want %q", item.Component, "switch")
	}
	if item.NodeID != "ccu-01_programs" {
		t.Fatalf("NodeID: got %q want %q", item.NodeID, "ccu-01_programs")
	}
	if item.ObjectID != "prg_42" {
		t.Fatalf("ObjectID: got %q want %q", item.ObjectID, "prg_42")
	}

	m := jsonMap(t, item)
	if m["name"] != "Morning Lights" {
		t.Fatalf("name: got %v want %q", m["name"], "Morning Lights")
	}
	if m["unique_id"] != "loom_11a0001234_program_morning-lights" {
		t.Fatalf("unique_id: got %v want loom_11a0001234_program_morning-lights", m["unique_id"])
	}
	if _, hasCmd := m["command_topic"]; !hasCmd {
		t.Fatal("expected command_topic")
	}
	if _, hasSt := m["state_topic"]; !hasSt {
		t.Fatal("expected state_topic")
	}
	// command_topic must be the canonical hub program trigger topic.
	wantCmd := naming.MQTTHubProgramTrigger("openccu-loom", "ccu-01", "PRG_42")
	if m["command_topic"] != wantCmd {
		t.Fatalf("command_topic: got %v want %q", m["command_topic"], wantCmd)
	}
}

func TestProgramBuilderEmptyNameFallsBackToID(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildProgramDiscovery("ccu-01", "PRG_42", "")
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	m := jsonMap(t, item)
	if m["name"] != "PRG_42" {
		t.Fatalf("name: got %v want %q", m["name"], "PRG_42")
	}
}

func TestProgramBuilderEmptyIDReturnsNotOK(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildProgramDiscovery("ccu-01", "", "X")
	if item.OK {
		t.Fatal("expected OK=false for empty program id")
	}
}

// ─── AlarmMessages ──────────────────────────────────────────────────────────

func TestAlarmMessagesDiscovery(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildAlarmMessagesDiscovery("ccu-01")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "sensor" {
		t.Fatalf("Component: got %q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["value_template"] != "{{ value_json | length }}" {
		t.Fatalf("value_template: got %v", m["value_template"])
	}
	if _, ok := m["json_attributes_topic"]; !ok {
		t.Fatal("expected json_attributes_topic")
	}
	// `device_class: problem` would be rejected on a sensor entity
	// (it only exists on binary_sensor)
	// emits HmAlarmMessagesSensor with state_class=measurement instead.
	if _, has := m["device_class"]; has {
		t.Fatalf("device_class must not be set on the alarm-messages sensor: got %v", m["device_class"])
	}
	if m["state_class"] != "measurement" {
		t.Fatalf("state_class: got %v want %q", m["state_class"], "measurement")
	}
	if m["entity_category"] != "diagnostic" {
		t.Fatalf("entity_category: got %v want %q", m["entity_category"], "diagnostic")
	}
}

// ─── ServiceMessages ────────────────────────────────────────────────────────

func TestServiceMessagesDiscovery(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildServiceMessagesDiscovery("ccu-01")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "sensor" {
		t.Fatalf("Component: got %q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["value_template"] != "{{ value_json | length }}" {
		t.Fatalf("value_template: got %v", m["value_template"])
	}
	if _, ok := m["json_attributes_topic"]; !ok {
		t.Fatal("expected json_attributes_topic")
	}
	if m["entity_category"] != "diagnostic" {
		t.Fatalf("entity_category: got %v want %q", m["entity_category"], "diagnostic")
	}
	// ServiceMessages must NOT have device_class (contrast with Alarm).
	if dc, has := m["device_class"]; has {
		t.Fatalf("service messages must not have device_class, got %v", dc)
	}
}

// ─── InstallMode (per interface) ─────────────────────────────────────────────

func TestInstallModeSensorDiscovery(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildInstallModeSensorDiscovery("ccu-01", "HmIP-RF")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "sensor" {
		t.Fatalf("Component: got %q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["device_class"] != "duration" {
		t.Fatalf("device_class: got %v want %q", m["device_class"], "duration")
	}
	if m["unit_of_measurement"] != "s" {
		t.Fatalf("unit_of_measurement: got %v want %q", m["unit_of_measurement"], "s")
	}
	if m["state_class"] != "measurement" {
		t.Fatalf("state_class: got %v want %q", m["state_class"], "measurement")
	}
	if m["entity_category"] != "diagnostic" {
		t.Fatalf("entity_category: got %v want %q", m["entity_category"], "diagnostic")
	}
	// Reference parity: per-interface suffix in uid + translation key.
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_install_mode_hmip") {
		t.Fatalf("unique_id: got %q want suffix _install_mode_hmip", uid)
	}
	if m["translation_key"] != "install_mode_hmip" {
		t.Fatalf("translation_key: got %v want install_mode_hmip", m["translation_key"])
	}
	if !strings.Contains(m["state_topic"].(string), "/hub/install_mode/HmIP-RF") {
		t.Fatalf("state_topic: got %v want per-interface topic", m["state_topic"])
	}
}

func TestInstallModeButtonDiscovery(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildInstallModeButtonDiscovery("ccu-01", "HmIP-RF")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "button" {
		t.Fatalf("Component: got %q want button", item.Component)
	}
	m := jsonMap(t, item)
	// Reference parity: uid slug "<suffix>-button", translation key
	// "<suffix>_button", entity_category config, press command topic.
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_install_mode_hmip-button") {
		t.Fatalf("unique_id: got %q want suffix _install_mode_hmip-button", uid)
	}
	if m["translation_key"] != "install_mode_hmip_button" {
		t.Fatalf("translation_key: got %v want install_mode_hmip_button", m["translation_key"])
	}
	if m["entity_category"] != "config" {
		t.Fatalf("entity_category: got %v want config", m["entity_category"])
	}
	if m["payload_press"] != "PRESS" {
		t.Fatalf("payload_press: got %v want PRESS", m["payload_press"])
	}
	if !strings.Contains(m["command_topic"].(string), "/hub/install_mode/HmIP-RF/set") {
		t.Fatalf("command_topic: got %v want per-interface set topic", m["command_topic"])
	}
}

func TestInstallModeSuffixBidcos(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildInstallModeSensorDiscovery("ccu-01", "BidCos-RF")
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	m := jsonMap(t, item)
	if m["translation_key"] != "install_mode_bidcos" {
		t.Fatalf("translation_key: got %v want install_mode_bidcos", m["translation_key"])
	}
}

func TestInstallModeDiscoveryEmptyIface(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	if item := db.BuildInstallModeSensorDiscovery("ccu-01", ""); item.OK {
		t.Fatal("empty interface must return OK=false (sensor)")
	}
	if item := db.BuildInstallModeButtonDiscovery("ccu-01", ""); item.OK {
		t.Fatal("empty interface must return OK=false (button)")
	}
}

// ─── Connectivity ────────────────────────────────────────────────────────────

func TestConnectivityDiscoveryHappyPath(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildConnectivityDiscovery("ccu-01", "HmIP-RF")

	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != "binary_sensor" {
		t.Fatalf("Component: got %q want binary_sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["device_class"] != "connectivity" {
		t.Fatalf("device_class: got %v want %q", m["device_class"], "connectivity")
	}
	if m["payload_on"] != "true" {
		t.Fatalf("payload_on: got %v want %q", m["payload_on"], "true")
	}
	if m["payload_off"] != "false" {
		t.Fatalf("payload_off: got %v want %q", m["payload_off"], "false")
	}
	if m["name"] != "Connectivity HmIP-RF" {
		t.Fatalf("name: got %v want %q", m["name"], "Connectivity HmIP-RF")
	}
}

func TestConnectivityDiscoveryEmptyIfaceReturnsNotOK(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildConnectivityDiscovery("ccu-01", "")
	if item.OK {
		t.Fatal("expected OK=false for empty interface name")
	}
}

// ─── Hub device block ────────────────────────────────────────────────────────

func TestHubDeviceBlockContent(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Active",
		ValueType:  hmenum.HubValueTypeLogic,
		Writable:   true,
		IsExtended: true,
	})
	m := jsonMap(t, item)

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}

	// identifiers
	ids, _ := dev["identifiers"].([]any)
	if len(ids) == 0 || ids[0] != "openccu-loom_central_ccu-01" {
		t.Fatalf("identifiers: got %v want [openccu-loom_central_ccu-01]", ids)
	}
	if dev["name"] != "ccu-01" {
		t.Fatalf("name: got %v want %q", dev["name"], "ccu-01")
	}
	if dev["manufacturer"] != "eQ-3" {
		t.Fatalf("manufacturer: got %v want %q", dev["manufacturer"], "eQ-3")
	}
	if dev["model"] != "HomeMatic Central" {
		t.Fatalf("model: got %v want %q", dev["model"], "HomeMatic Central")
	}
}

// ─── PublishHubDiscovery plumbing ────────────────────────────────────────────

func TestPublishHubDiscoveryHappyPath(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.HADiscoveryEnabled = true
		c.Base = "openccu-loom"
		c.CentralName = "ccu"
	})
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	builder.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})
	spec := HubSysvarSpec{
		Name:       "foo",
		ValueType:  hmenum.HubValueTypeLogic,
		Writable:   true,
		IsExtended: true,
	}
	item := builder.BuildSysvarDiscovery("ccu", spec)
	if !item.OK {
		t.Fatal("builder returned OK=false")
	}

	if err := b.PublishHubDiscovery(context.Background(), item); err != nil {
		t.Fatalf("PublishHubDiscovery: %v", err)
	}

	wantTopic := "homeassistant/switch/ccu_sysvars/foo/config"
	if _, found := rec.findTopic(wantTopic); !found {
		t.Fatalf("topic %q not published; got: %v", wantTopic, rec.records())
	}
}

func TestPublishHubDiscoveryNotOKIsNoop(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.HADiscoveryEnabled = true
	})
	// OK=false item (empty sysvar name).
	item := DiscoveryItem{OK: false}
	if err := b.PublishHubDiscovery(context.Background(), item); err != nil {
		t.Fatalf("PublishHubDiscovery: %v", err)
	}
	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes for OK=false item, got %d", n)
	}
}

func TestPublishHubDiscoveryDisabledIsNoop(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.HADiscoveryEnabled = false
	})
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	builder.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})
	spec := HubSysvarSpec{
		Name:       "foo",
		ValueType:  hmenum.HubValueTypeLogic,
		Writable:   true,
		IsExtended: true,
	}
	item := builder.BuildSysvarDiscovery("ccu", spec)
	if !item.OK {
		t.Fatal("builder returned OK=false")
	}
	if err := b.PublishHubDiscovery(context.Background(), item); err != nil {
		t.Fatalf("PublishHubDiscovery: %v", err)
	}
	n := rec.countPrefix("homeassistant/")
	if n != 0 {
		t.Fatalf("expected 0 discovery publishes when HADiscoveryEnabled=false, got %d", n)
	}
}

// ─── Edge: sysvar with upper-case name gets lower-cased object ID ────────────

func TestSysvarObjectIDIsLowercased(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "MyVar",
		ValueType: hmenum.HubValueTypeString,
		Writable:  false,
	})
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.ObjectID != "myvar" {
		t.Fatalf("ObjectID: got %q want %q", item.ObjectID, "myvar")
	}
}

// ─── Sysvar description used as display name when set ────────────────────────

func TestSysvarDescriptionUsedAsDisplayName(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:        "sv_active",
		Description: "Is System Active",
		ValueType:   hmenum.HubValueTypeLogic,
		Writable:    false,
	})
	m := jsonMap(t, item)
	if m["name"] != "Is System Active" {
		t.Fatalf("name: got %v want %q", m["name"], "Is System Active")
	}
}

// ─── Sysvar name used as display name when description is empty ───────────────

func TestSysvarNameUsedWhenDescriptionEmpty(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "sv_active",
		ValueType: hmenum.HubValueTypeLogic,
		Writable:  false,
	})
	m := jsonMap(t, item)
	if m["name"] != "sv_active" {
		t.Fatalf("name: got %v want %q", m["name"], "sv_active")
	}
}

// ─── Sysvar unique_id format ─────────────────────────────────────────────────

func TestSysvarUniqueIDFormat(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "MyVar",
		ValueType: hmenum.HubValueTypeLogic,
		Writable:  true,
	})
	m := jsonMap(t, item)
	uid, _ := m["unique_id"].(string)
	if !strings.HasPrefix(uid, "loom_") {
		t.Fatalf("unique_id %q does not have loom_ prefix", uid)
	}
	if !strings.Contains(uid, "_sysvar_") {
		t.Fatalf("unique_id %q does not contain _sysvar_", uid)
	}
	if !strings.HasSuffix(uid, "myvar") {
		t.Fatalf("unique_id %q does not end with myvar", uid)
	}
}

// ─── BuildSystemHealthDiscovery ──────────────────────────────────────────────

// TestBuildSystemHealthDiscovery_HappyPath pins that the system-health
// sensor emits component=sensor, correct unique_id, unit=%, and
// entity_category=diagnostic.
func TestBuildSystemHealthDiscovery_HappyPath(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSystemHealthDiscovery("ccu-01")
	if !item.OK {
		t.Fatal("BuildSystemHealthDiscovery returned ok=false")
	}
	if item.Component != string(HAComponentSensor) {
		t.Errorf("component=%q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["unit_of_measurement"] != "%" {
		t.Errorf("unit=%v want %%", m["unit_of_measurement"])
	}
	if m["entity_category"] != "diagnostic" {
		t.Errorf("entity_category=%v want diagnostic", m["entity_category"])
	}
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_system_health") {
		t.Errorf("unique_id=%q should end with _system_health (reference parity)", uid)
	}
	if m["translation_key"] != "system_health" {
		t.Errorf("translation_key=%v want system_health", m["translation_key"])
	}
}

// TestBuildSystemHealthDiscovery_EmptyCentral_ReturnsNoOp pins that
// an empty central name produces an ok=false item (safe no-op).
func TestBuildSystemHealthDiscovery_EmptyCentral_ReturnsNoOp(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSystemHealthDiscovery("")
	if item.OK {
		t.Error("empty central should produce ok=false")
	}
}

// ─── BuildConnectionLatencyDiscovery ────────────────────────────────────────

// TestBuildConnectionLatencyDiscovery_HappyPath pins that the aggregated
// latency sensor emits component=sensor, unit=ms, diagnostic category,
// the central-wide topic, and the connection_latency translation key.
func TestBuildConnectionLatencyDiscovery_HappyPath(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildConnectionLatencyDiscovery("ccu-01")
	if !item.OK {
		t.Fatal("BuildConnectionLatencyDiscovery returned ok=false")
	}
	if item.Component != string(HAComponentSensor) {
		t.Errorf("component=%q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if m["unit_of_measurement"] != "ms" {
		t.Errorf("unit=%v want ms", m["unit_of_measurement"])
	}
	if m["entity_category"] != "diagnostic" {
		t.Errorf("entity_category=%v want diagnostic", m["entity_category"])
	}
	if m["translation_key"] != "connection_latency" {
		t.Errorf("translation_key=%v want connection_latency", m["translation_key"])
	}
	uid, _ := m["unique_id"].(string)
	if !strings.HasSuffix(uid, "_connection_latency") {
		t.Errorf("unique_id=%q want suffix _connection_latency", uid)
	}
	// Aggregated central-wide topic: no per-interface segment.
	st, _ := m["state_topic"].(string)
	if !strings.HasSuffix(st, "/system/latency") {
		t.Errorf("state_topic=%q want central-wide /system/latency", st)
	}
}

// TestBuildConnectionLatencyDiscovery_EmptyCentral_ReturnsNoOp pins that
// a missing central name returns ok=false.
func TestBuildConnectionLatencyDiscovery_EmptyCentral_ReturnsNoOp(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildConnectionLatencyDiscovery("")
	if item.OK {
		t.Error("empty central should produce ok=false")
	}
}

// ─── Fix 1: safeLower slug rules ─────────────────────────────────────────────

func TestSafeLower(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Watchdog: CCU-Jack", "watchdog_ccu-jack"},
		{"s0_Sensoren_Hülle_EG", "s0_sensoren_huelle_eg"},
		{"S0_Sensoren_Hüllschutz", "s0_sensoren_huellschutz"},
		{"svEnergyCounter_14007_0001dbe9915be4:6", "svenergycounter_14007_0001dbe9915be4_6"},
		// é is not a German umlaut so it collapses to '_'; the literal '_'
		// separator following is kept, yielding a double underscore.
		{"Café_München", "caf__muenchen"},
		{"ABC", "abc"},
		{"", "x"},
		{"!!!", "x"},
		// Literal '_' chars pass through emit() unchanged; only consecutive
		// flush() calls (non-allowed chars) are deduplicated.
		{"a___b", "a___b"},
		{"_a_", "a"},
		{"Heizung Modus", "heizung_modus"},
		{"Belüftungsanlage Stufe", "belueftungsanlage_stufe"},
		{"Straße", "strasse"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := safeLower(tc.in)
			if got != tc.want {
				t.Fatalf("safeLower(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBuildSysvarDiscovery_ObjectIDIsHASafe verifies that a sysvar whose
// name contains ':' (e.g. "Watchdog: CCU-Jack") produces a DiscoveryItem
// with an HA-safe ObjectID containing neither ':' nor non-ASCII chars.
func TestBuildSysvarDiscovery_ObjectIDIsHASafe(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "Watchdog: CCU-Jack",
		ValueType: hmenum.HubValueTypeLogic,
		Writable:  false,
	})
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.ObjectID != "watchdog_ccu-jack" {
		t.Fatalf("ObjectID=%q want %q", item.ObjectID, "watchdog_ccu-jack")
	}
	for _, r := range item.ObjectID {
		if r > 127 {
			t.Fatalf("ObjectID %q contains non-ASCII rune %q", item.ObjectID, r)
		}
		if r == ':' {
			t.Fatalf("ObjectID %q contains ':'", item.ObjectID)
		}
	}
	for _, r := range item.NodeID {
		if r > 127 {
			t.Fatalf("NodeID %q contains non-ASCII rune %q", item.NodeID, r)
		}
		if r == ':' {
			t.Fatalf("NodeID %q contains ':'", item.NodeID)
		}
	}
}

// ─── Fix 2: number range fallback + step ─────────────────────────────────────

func TestSysvarDiscoveryNumberWideFallback_WhenUnbounded(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Counter",
		ValueType:  hmenum.HubValueTypeFloat,
		Writable:   true,
		IsExtended: true,
		Min:        nil,
		Max:        nil,
	})
	m := jsonMap(t, item)
	if m["min"] != -1e9 {
		t.Fatalf("min=%v want -1e9", m["min"])
	}
	if m["max"] != 1e9 {
		t.Fatalf("max=%v want 1e9", m["max"])
	}
	if m["step"] != 0.01 {
		t.Fatalf("step=%v want 0.01", m["step"])
	}
}

func TestSysvarDiscoveryIntegerStep(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Count",
		ValueType:  hmenum.HubValueTypeInteger,
		Writable:   true,
		IsExtended: true,
		Min:        nil,
		Max:        nil,
	})
	m := jsonMap(t, item)
	if m["step"] != float64(1) {
		t.Fatalf("step=%v want 1", m["step"])
	}
	if m["min"] != -1e9 {
		t.Fatalf("min=%v want -1e9", m["min"])
	}
	if m["max"] != 1e9 {
		t.Fatalf("max=%v want 1e9", m["max"])
	}
	if m["mode"] != "box" {
		t.Fatalf("mode=%v want box", m["mode"])
	}
}

func TestSysvarDiscoveryNumberRespectsBounds(t *testing.T) {
	t.Parallel()
	fptr := func(v float64) *float64 { return &v }
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:       "Brightness",
		ValueType:  hmenum.HubValueTypeFloat,
		Writable:   true,
		IsExtended: true,
		Min:        fptr(0),
		Max:        fptr(50),
	})
	m := jsonMap(t, item)
	if m["min"] != float64(0) {
		t.Fatalf("min=%v want 0", m["min"])
	}
	if m["max"] != float64(50) {
		t.Fatalf("max=%v want 50", m["max"])
	}
	if m["step"] != 0.01 {
		t.Fatalf("step=%v want 0.01", m["step"])
	}
}

func TestSysvarDiscoveryReadOnlyNumber_NoFallbackBounds(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "SunshineHours",
		ValueType: hmenum.HubValueTypeFloat,
		Writable:  false,
		Min:       nil,
		Max:       nil,
	})
	m := jsonMap(t, item)
	if item.Component != string(HAComponentSensor) {
		t.Fatalf("component=%q want sensor", item.Component)
	}
	if _, hasMin := m["min"]; hasMin {
		t.Fatal("read-only sensor must not carry min in discovery body")
	}
	if _, hasMax := m["max"]; hasMax {
		t.Fatal("read-only sensor must not carry max in discovery body")
	}
}

// ─── Fix 3: string sysvar always maps to sensor ───────────────────────────────

func TestSysvarDiscoveryStringWritable_IsSensor(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "AlleServicemeldungen",
		ValueType: hmenum.HubValueTypeString,
		Writable:  true,
	})
	if !item.OK {
		t.Fatal("expected OK=true")
	}
	if item.Component != string(HAComponentSensor) {
		t.Fatalf("component=%q want sensor", item.Component)
	}
	m := jsonMap(t, item)
	if _, hasCmd := m["command_topic"]; hasCmd {
		t.Fatal("string sysvar sensor must not carry command_topic")
	}
}
