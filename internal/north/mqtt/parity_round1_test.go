// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity-round-1 regression tests. Each section pins one of the
// MQTT-discovery parity fixes against the reference stack:
//
//  1. Hub unique_ids carry the per-central serial — no publish without it.
//  2. Sysvar typing keys on the extended-sysvar marker (tested in
//     hub_discovery_test.go's component-selection table).
//  3. Usage verdict gates per-parameter discovery; ActionNumber/Action
//     categories do not surface.
//  4. ce_primary / ce_secondary constituents are absorbed by the
//     channel aggregate.
//  5. Virtual-remote press parameters get clickable button companions.
//  6. ENUM tokens are lower-cased toward HA and restored on write.
package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Fix 1: hub unique_ids need the per-central serial ──────────────────────

// TestHubDiscoverySkipsWithoutSerial pins the no-empty-slot rule: every
// hub builder returns OK=false until the central's serial is known, so
// two CCUs can never collide on `loom__<kind>` unique_ids.
func TestHubDiscoverySkipsWithoutSerial(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu-01")
	items := map[string]DiscoveryItem{
		"sysvar":        db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{Name: "Foo", ValueType: hmenum.HubValueTypeLogic}),
		"program":       db.BuildProgramDiscovery("ccu-01", "PRG_1", "Prog"),
		"alarm":         db.BuildAlarmMessagesDiscovery("ccu-01"),
		"service":       db.BuildServiceMessagesDiscovery("ccu-01"),
		"inbox":         db.BuildInboxDiscovery("ccu-01"),
		"install_mode":  db.BuildInstallModeDiscovery("ccu-01"),
		"connectivity":  db.BuildConnectivityDiscovery("ccu-01", "HmIP-RF"),
		"system_health": db.BuildSystemHealthDiscovery("ccu-01"),
		"latency":       db.BuildConnectionLatencyDiscovery("ccu-01", "HmIP-RF"),
		"system_update": db.BuildHubUpdateDiscovery("ccu-01"),
	}
	for kind, item := range items {
		if item.OK {
			t.Errorf("%s: expected OK=false without a registered serial", kind)
		}
	}
}

// TestHubUniqueIDsDistinctAcrossCentrals pins the multi-central fix:
// two CCUs with different serials produce different hub unique_ids on
// the SAME builder, so HA keeps both centrals' hub planes.
func TestHubUniqueIDsDistinctAcrossCentrals(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "otto-rem")
	db.SetHubInfoFor("otto-rem", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("kearney-loc", HubInfo{Serial: "3014F711B0009876"})

	uid := func(central string) string {
		item := db.BuildAlarmMessagesDiscovery(central)
		if !item.OK {
			t.Fatalf("BuildAlarmMessagesDiscovery(%s) OK=false", central)
		}
		var m map[string]any
		if err := json.Unmarshal(item.Payload, &m); err != nil {
			t.Fatalf("payload: %v", err)
		}
		s, _ := m["unique_id"].(string)
		return s
	}
	a, b := uid("otto-rem"), uid("kearney-loc")
	if a == b {
		t.Fatalf("hub unique_ids collide across centrals: %q", a)
	}
	if a != "loom_11a0001234_alarm_messages" {
		t.Errorf("otto-rem uid = %q, want loom_11a0001234_alarm_messages", a)
	}
	if b != "loom_11b0009876_alarm_messages" {
		t.Errorf("kearney-loc uid = %q, want loom_11b0009876_alarm_messages", b)
	}
}

// TestBridgeDefaultBuilderSharesHubInfo pins the wiring contract:
// HubInfo registered on the BRIDGE is visible to the builder returned
// by [Bridge.DefaultBuilder] — the instance hub publishers must use.
func TestBridgeDefaultBuilderSharesHubInfo(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)
	b.SetHubInfoFor("c1", HubInfo{Serial: "3014F711A0001234"})

	dd := b.DefaultBuilder()
	if dd == nil {
		t.Fatal("DefaultBuilder returned nil for an auto-wired bridge")
	}
	item := dd.BuildAlarmMessagesDiscovery("c1")
	if !item.OK {
		t.Fatal("hub discovery skipped despite SetHubInfoFor on the bridge")
	}
	if !strings.Contains(string(item.Payload), "loom_11a0001234_alarm_messages") {
		t.Fatalf("payload misses the serial-scoped unique_id: %s", item.Payload)
	}
}

// ─── Fix 3: usage verdict + action categories ────────────────────────────────

// TestActionCategoriesNotDiscovered pins the reference behaviour for
// fire-and-forget write-only parameters: ActionNumber (ON_TIME,
// RAMP_TIME — empty whitelist in the reference stack) and plain Action
// (COMBINED_PARAMETER) never surface as HA entities.
func TestActionCategoriesNotDiscovered(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	for param, cat := range map[string]hmenum.DataPointCategory{
		"ON_TIME":            hmenum.DataPointCategoryActionNumber,
		"RAMP_TIME":          hmenum.DataPointCategoryActionNumber,
		"COMBINED_PARAMETER": hmenum.DataPointCategoryAction,
	} {
		_, _, _, _, ok := db.Build(Event{
			Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 1,
			Parameter: param, Category: cat, Writable: true,
		})
		if ok {
			t.Errorf("%s (%s) must not produce a discovery payload", param, cat)
		}
	}
}

// TestUsageGatePerParameterDiscovery pins the DataPointUsage gate on
// the per-parameter discovery path: no_create / ignored (suppressed
// everywhere) and ce_primary / ce_secondary (absorbed by the channel's
// custom-DP aggregate) are dropped; ce_visible and data_point pass.
func TestUsageGatePerParameterDiscovery(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	cases := []struct {
		usage  hmenum.DataPointUsage
		wantOK bool
	}{
		{hmenum.DataPointUsageNoCreate, false},
		{hmenum.DataPointUsageIgnored, false},
		{hmenum.DataPointUsageCDPPrimary, false},
		{hmenum.DataPointUsageCDPSecondary, false},
		{hmenum.DataPointUsageCDPVisible, true},
		{hmenum.DataPointUsageDataPoint, true},
		{"", true},
	}
	for _, tc := range cases {
		_, _, _, _, ok := db.Build(Event{
			Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 3,
			Parameter: "STATE", Category: hmenum.DataPointCategorySwitch,
			Writable: true, Usage: tc.usage,
		})
		if ok != tc.wantOK {
			t.Errorf("usage=%q: ok=%v want %v", tc.usage, ok, tc.wantOK)
		}
	}
}

// ─── Fix 5: virtual-remote press buttons ─────────────────────────────────────

// fakeVirtualRemote satisfies the virtualRemoteDevice contract.
type fakeVirtualRemote struct{ vr bool }

func (f fakeVirtualRemote) IsVirtualRemote() bool { return f.vr }

func vrPressEvent(param string, vr bool) Event {
	return Event{
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "HmIP-RCV-1",
		DeviceName:    "HmIP-RCV-50",
		Model:         "HmIP-RCV-50",
		ChannelNo:     12,
		Parameter:     param,
		Category:      hmenum.DataPointCategoryButton,
		Writable:      true,
		Device:        fakeVirtualRemote{vr: vr},
		Channel: &fakeChannelInspector{params: map[string]struct{}{
			"PRESS_SHORT": {},
			"PRESS_LONG":  {},
		}},
	}
}

// TestBuildVirtualRemoteButton pins the button companion payload for a
// virtual-remote press parameter: HA `button` component, per-DP command
// topic, payload_press="PRESS", disabled by default (mirrors the
// reference button factory), and the canonical press unique_id.
func TestBuildVirtualRemoteButton(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	db.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})

	item := db.BuildVirtualRemoteButton(vrPressEvent("PRESS_SHORT", true))
	if !item.OK {
		t.Fatal("expected OK=true for a virtual-remote press parameter")
	}
	if item.Component != string(HAComponentButton) {
		t.Fatalf("component=%q want button", item.Component)
	}
	var m map[string]any
	if err := json.Unmarshal(item.Payload, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if m["unique_id"] != "loom_11a0001234_hmip_rcv_1_12_press_short" {
		t.Fatalf("unique_id=%v want loom_11a0001234_hmip_rcv_1_12_press_short", m["unique_id"])
	}
	cmd, _ := m["command_topic"].(string)
	if !strings.HasSuffix(cmd, "/values/PRESS_SHORT/set") {
		t.Fatalf("command_topic=%q want .../values/PRESS_SHORT/set", cmd)
	}
	if m["payload_press"] != "PRESS" {
		t.Fatalf("payload_press=%v want PRESS", m["payload_press"])
	}
	if m["enabled_by_default"] != false {
		t.Fatalf("enabled_by_default=%v want false (reference button factory default)", m["enabled_by_default"])
	}
}

// TestBuildVirtualRemoteButtonSkipsPhysicalDevices pins that physical
// devices' press parameters (pure event emitters) never get a button.
func TestBuildVirtualRemoteButtonSkipsPhysicalDevices(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	if item := db.BuildVirtualRemoteButton(vrPressEvent("PRESS_SHORT", false)); item.OK {
		t.Fatal("physical-device press parameter must not produce a button")
	}
	if item := db.BuildVirtualRemoteButton(Event{Parameter: "LEVEL", Device: fakeVirtualRemote{vr: true}}); item.OK {
		t.Fatal("non-press parameter must not produce a button")
	}
}

// TestVirtualRemotePressPublishesEventAndButton pins the bridge-level
// behaviour: a virtual-remote press event publishes BOTH the aggregated
// keypress `event` entity and the press `button` companion — the
// reference stack's per-channel surface (event + press_short +
// press_long).
func TestVirtualRemotePressPublishesEventAndButton(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	b := newDeepBridge(t, rec)

	for _, param := range []string{"PRESS_SHORT", "PRESS_LONG"} {
		if err := b.PublishState(context.Background(), vrPressEvent(param, true)); err != nil {
			t.Fatalf("PublishState(%s): %v", param, err)
		}
	}

	var haveEvent, haveShort, haveLong bool
	for _, r := range rec.records() {
		switch {
		case strings.HasPrefix(r.topic, "homeassistant/event/"):
			haveEvent = true
		case strings.HasPrefix(r.topic, "homeassistant/button/") && strings.HasSuffix(r.topic, "12_press_short/config"):
			haveShort = true
		case strings.HasPrefix(r.topic, "homeassistant/button/") && strings.HasSuffix(r.topic, "12_press_long/config"):
			haveLong = true
		}
	}
	if !haveEvent {
		t.Error("keypress event entity discovery missing")
	}
	if !haveShort {
		t.Error("press_short button discovery missing")
	}
	if !haveLong {
		t.Error("press_long button discovery missing")
	}
}

// TestParseCommandPayloadPressToken pins the HA button contract: the
// `payload_press` token "PRESS" coerces to boolean true so write-only
// ACTION parameters (virtual-remote presses, RESET_MOTION) trigger.
func TestParseCommandPayloadPressToken(t *testing.T) {
	t.Parallel()
	if v := parseCommandPayload([]byte("PRESS")); v != true {
		t.Fatalf("parseCommandPayload(PRESS)=%v want true", v)
	}
}

// ─── Fix 6: ENUM lowercase toward HA ─────────────────────────────────────────

// TestEnumSensorLowercasesOptionsAndState pins the reference behaviour
// for enum sensors: options are lower-cased and the state template
// pipes through `| lower` so the rendered token matches the options.
func TestEnumSensorLowercasesOptionsAndState(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.Build(Event{
		Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 1,
		Parameter: "STATE", Model: "HmIP-SRH",
		Category:   hmenum.DataPointCategorySensor,
		Descriptor: &pload.GenericConfig{Type: hmenum.ParameterTypeEnum, ValueList: []string{"CLOSED", "TILTED", "OPEN"}},
	})
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if m["device_class"] != "enum" {
		t.Fatalf("device_class=%v want enum", m["device_class"])
	}
	opts, _ := m["options"].([]any)
	if len(opts) != 3 || opts[0] != "closed" || opts[1] != "tilted" || opts[2] != "open" {
		t.Fatalf("options=%v want lower-cased [closed tilted open]", opts)
	}
	vt, _ := m["value_template"].(string)
	if !strings.Contains(vt, "| lower") {
		t.Fatalf("value_template=%q must pipe through | lower", vt)
	}
}

// TestSelectLowercaseAndCommandUpper pins the select round-trip:
// lower-cased options + `| lower` state template + `| upper` command
// template restoring the CCU token on write.
func TestSelectLowercaseAndCommandUpper(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.Build(Event{
		Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 1,
		Parameter: "SET_POINT_MODE", Category: hmenum.DataPointCategorySelect, Writable: true,
		Descriptor: &pload.GenericConfig{Type: hmenum.ParameterTypeEnum, ValueList: []string{"AUTO_MODE", "MANU_MODE"}},
	})
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	opts, _ := m["options"].([]any)
	if len(opts) != 2 || opts[0] != "auto_mode" || opts[1] != "manu_mode" {
		t.Fatalf("options=%v want [auto_mode manu_mode]", opts)
	}
	vt, _ := m["value_template"].(string)
	if !strings.Contains(vt, "| lower") {
		t.Fatalf("value_template=%q must pipe through | lower", vt)
	}
	if ct, _ := m["command_template"].(string); !strings.Contains(ct, "| upper") {
		t.Fatalf("command_template=%q must restore the uppercase CCU token", ct)
	}
}

// TestActionSelectGetsConfigCategory pins the reference behaviour for
// write-only enum parameters: they surface as selects relegated to
// HA's Configuration section.
func TestActionSelectGetsConfigCategory(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	_, _, _, buf, ok := db.Build(Event{
		Central: "ccu", Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 1,
		Parameter: "WEEK_PROGRAM_TARGET_CHANNEL_LOCK", Category: hmenum.DataPointCategoryActionSelect, Writable: true,
		Descriptor: &pload.GenericConfig{Type: hmenum.ParameterTypeEnum, ValueList: []string{"LOCKED", "UNLOCKED"}},
	})
	if !ok {
		t.Fatal("Build returned ok=false")
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if m["entity_category"] != EntityCategoryConfig {
		t.Fatalf("entity_category=%v want %q", m["entity_category"], EntityCategoryConfig)
	}
}
