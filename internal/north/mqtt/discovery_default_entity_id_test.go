// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDiscoveryBuildersEmitDefaultEntityIDNotObjectID walks every
// hand-built discovery builder in this package (hub, alarm, security,
// add-on-update, per-device firmware-update, week-profile) through its
// real exported entry point and asserts each emitted payload:
//
//   - never carries the removed `object_id` discovery key, and
//   - carries `default_entity_id` as exactly `<component>.<object>`,
//     where `<component>` is the item's own [DiscoveryItem.Component]
//     and `<object>` is the same value the site used to pass as
//     `object_id`.
//
// Home Assistant core commit 87b83dcc1bc (HA 2026.3) dropped
// `object_id` from the MQTT discovery schema; every platform schema
// extends `PLATFORM_SCHEMA_MODERN` with `extra=vol.REMOVE_EXTRA`, so a
// stray `object_id` key is silently stripped rather than rejected —
// nothing in CI or the broker ever complained while the daemon kept
// emitting a field HA had already stopped reading.
func TestDiscoveryBuildersEmitDefaultEntityIDNotObjectID(t *testing.T) {
	t.Parallel()

	type tc struct {
		name string
		item DiscoveryItem
		// wantObject overrides the default expectation that the
		// default_entity_id object part equals the payload's own
		// unique_id. Only discovery_update.go and
		// discovery_week_profile.go pass a dedicated `objectID`
		// variable instead of the site-local `uniqueID` every other
		// builder below uses; discovery_update.go's `objectID` happens
		// to equal its own unique_id (one shared variable feeds both),
		// while discovery_week_profile.go's is a legacy normalised
		// form that deliberately differs from the canonical
		// routing-key unique_id. Both are pinned against
		// [DiscoveryItem.ObjectID], which the two builders set from
		// that same variable.
		wantObject string
	}

	db := newHubBuilder()
	const central = "ccu-01"

	cases := make([]tc, 0, 64)
	cases = append(
		cases,
		tc{name: "sysvar/binary_sensor", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "AlarmFlag", ValueType: hmenum.HubValueTypeLogic,
		})},
		tc{name: "sysvar/switch", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "SwitchFlag", ValueType: hmenum.HubValueTypeLogic, Writable: true, IsExtended: true,
		})},
		tc{name: "sysvar/select", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "ListSelect", ValueType: hmenum.HubValueTypeList, ValueList: []string{"a", "b"}, Writable: true, IsExtended: true,
		})},
		tc{name: "sysvar/enum_sensor", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "ListSensor", ValueType: hmenum.HubValueTypeList, ValueList: []string{"a", "b"},
		})},
		tc{name: "sysvar/text", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "StringText", ValueType: hmenum.HubValueTypeString, Writable: true, IsExtended: true,
		})},
		tc{name: "sysvar/number", item: db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: "NumberEditable", ValueType: hmenum.HubValueTypeNumber, Writable: true, IsExtended: true,
		})},
		tc{name: "program/legacy_switch", item: db.BuildProgramDiscovery(central, HubProgramSpec{ID: "PRG_1", Name: "Morning"})},
		tc{name: "alarm_messages", item: db.BuildAlarmMessagesDiscovery(central)},
		tc{name: "service_messages", item: db.BuildServiceMessagesDiscovery(central)},
		tc{name: "inbox", item: db.BuildInboxDiscovery(central)},
		tc{name: "install_mode_sensor", item: db.BuildInstallModeSensorDiscovery(central, "HmIP-RF")},
		tc{name: "install_mode_button", item: db.BuildInstallModeButtonDiscovery(central, "HmIP-RF")},
		tc{name: "connectivity", item: db.BuildConnectivityDiscovery(central, "HmIP-RF")},
		tc{name: "system_health", item: db.BuildSystemHealthDiscovery(central)},
		tc{name: "connection_latency", item: db.BuildConnectionLatencyDiscovery(central)},
		tc{name: "last_event_age", item: db.BuildLastEventAgeDiscovery(central)},
		tc{name: "system_update", item: db.BuildHubUpdateDiscovery(central)},
		tc{name: "addon_update", item: db.BuildAddonUpdateDiscovery()},
		tc{name: "alarm_panel", item: BuildAlarmPanelDiscovery("gh", "eg", "Erdgeschoss",
			[]hmenum.AlarmMode{hmenum.AlarmModeFull, hmenum.AlarmModePerimeter}, false, false, false)},
		tc{name: "alarm_motion_reset", item: BuildAlarmMotionResetDiscovery("gh", "eg", "Erdgeschoss", "Reset", false)},
		tc{name: "alarm_triggered_motion", item: BuildAlarmTriggeredMotionDiscovery("gh", "eg", "Erdgeschoss", "Bewegung", false)},
	)

	// Every daemon-level Security & Safety entity, across every
	// component the plane declares (event, sensor, binary_sensor, …).
	for _, e := range securitySystemEntities(securityTestTr) {
		cases = append(cases, tc{name: "security/" + e.key, item: BuildSecurityDiscovery("gh", "Security & Safety", "", e)})
	}

	// Program roles are built through the real production role
	// declaration ([hub.Program.MQTTRoles]) — the same call site
	// internal/central/adapter/hub_mqtt_publisher.go uses via
	// Bridge.ProgramRoles — so this also exercises the real
	// role-declaration path, not a hand-rolled substitute.
	prog := hub.NewProgram(central, "PRG_2", "Evening", "", false, nil)
	for i, item := range db.BuildProgramDiscoveryRoles(central, HubProgramSpec{ID: prog.ID, Name: prog.Name}, prog.MQTTRoles("gh", central)) {
		cases = append(cases, tc{name: fmt.Sprintf("program_role[%d]", i), item: item})
	}

	updateItem := db.BuildUpdateDiscovery(central, UpdateEvent{
		Interface: "HmIP-RF", DeviceAddress: "0001ABCD", Update: fakeUpdateSource{},
	})
	cases = append(cases, tc{name: "device_update", item: updateItem, wantObject: updateItem.ObjectID})

	weekProfileItem := db.BuildWeekProfileDiscovery(central, WeekProfileEvent{
		Interface: "HmIP-RF", DeviceAddress: "0001ABCD", ChannelNo: 1,
		WP: newFakeWP([]string{"P1", "P2"}, "P1"),
	})
	cases = append(cases, tc{name: "week_profile", item: weekProfileItem, wantObject: weekProfileItem.ObjectID})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !c.item.OK {
				t.Fatalf("%s: DiscoveryItem.OK=false", c.name)
			}
			var body map[string]any
			if err := json.Unmarshal(c.item.Payload, &body); err != nil {
				t.Fatalf("%s: payload not JSON: %v", c.name, err)
			}
			if v, present := body["object_id"]; present {
				t.Errorf("%s: payload still carries the removed object_id key: %v", c.name, v)
			}

			got, _ := body["default_entity_id"].(string)
			if got == "" {
				t.Fatalf("%s: default_entity_id missing from payload", c.name)
			}
			if n := strings.Count(got, "."); n != 1 {
				t.Fatalf("%s: default_entity_id=%q must carry exactly one domain-separating dot, has %d", c.name, got, n)
			}
			domain, object, _ := strings.Cut(got, ".")
			if domain != c.item.Component {
				t.Errorf("%s: default_entity_id domain=%q want %q (item.Component)", c.name, domain, c.item.Component)
			}

			wantObject := c.wantObject
			if wantObject == "" {
				uid, _ := body["unique_id"].(string)
				wantObject = uid
			}
			if object != wantObject {
				t.Errorf("%s: default_entity_id object=%q want %q", c.name, object, wantObject)
			}
		})
	}
}
