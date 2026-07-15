// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HAComponentAlarmControlPanel is the HA MQTT-Discovery component prefix
// for the alarm panel. Every area (and the aggregate master panel) maps
// onto one alarm_control_panel entity.
const HAComponentAlarmControlPanel HAComponent = "alarm_control_panel"

// alarmDiscoveryNodeID groups every alarm panel under one HA discovery
// node (`homeassistant/alarm_control_panel/alarm/<area>/config`). Areas
// are daemon-level, so the node carries no `<central>` segment.
const alarmDiscoveryNodeID = "alarm"

// alarmMasterArea is the reserved area segment of the aggregate panel
// that arms/disarms every area at once. A real area ID is a UUID, so it
// can never collide with this token.
const alarmMasterArea = "master"

// HA alarm_control_panel supported-feature tokens. HA reads these from
// the discovery payload to decide which arm buttons the panel renders.

// alarm topic builders. Areas are daemon-level, so the alarm plane omits
// the `<central>` segment every per-device topic carries — a deliberate
// extension of the topic schema precedented only by `<base>/bridge/*`
// (docs/mqtt-topic-schema.md, docs/alarm-concept.md §13.3).
func alarmStateTopic(base, area string) string { return base + "/alarm/" + area + "/state" }

func alarmAvailabilityTopic(base, area string) string {
	return base + "/alarm/" + area + "/availability"
}
func alarmEventTopic(base, area string) string   { return base + "/alarm/" + area + "/event" }
func alarmCommandTopic(base, area string) string { return base + "/alarm/" + area + "/set" }

// alarmBridgeStatusTopic is the retained bridge LWT topic the panel's
// availability list references as the first (transport-level) source.
func alarmBridgeStatusTopic(base string) string { return base + "/bridge/status" }

// alarmDeviceBlock is the single synthetic HA device that groups every
// area panel (and the master panel) under one card.
func alarmDeviceBlock() map[string]any {
	return map[string]any{
		"identifiers":  []string{"openccu-loom_alarm"},
		"name":         "OpenCCU-Loom Alarm",
		"manufacturer": "OpenCCU-Loom",
	}
}

// alarmAvailability is the two-source availability list every alarm panel
// carries: the bridge LWT plus the per-area alarm availability topic. With
// availability_mode "all" HA marks the panel available only when both are
// online (docs/alarm-concept.md §13.3).
func alarmAvailability(base, area string) []map[string]string {
	return []map[string]string{
		{
			"topic":                 alarmBridgeStatusTopic(base),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 alarmAvailabilityTopic(base, area),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
}

// BuildAlarmPanelDiscovery builds the HA Discovery payload for one alarm
// area (master==false) or the aggregate master panel (master==true). The
// caller resolves the display name — for the master panel it passes the
// i18n-localized "Alarm system" string, mirroring how hub discovery names
// its synthetic entities. When master is set the topic/unique-id segment
// is forced to the reserved master token regardless of areaID.
//
// code_arm_required / code_disarm_required are hard-false in this slice;
// the codes feature flips them per area policy in a later slice.
func BuildAlarmPanelDiscovery(base, areaID, areaName string, modes []hmenum.AlarmMode, master bool) DiscoveryItem {
	area := areaID
	if master {
		area = alarmMasterArea
	}
	if area == "" {
		return DiscoveryItem{}
	}
	uniqueID := "openccu-loom_alarm_" + area
	body := map[string]any{
		"name":                 areaName,
		"unique_id":            uniqueID,
		"object_id":            uniqueID,
		"state_topic":          alarmStateTopic(base, area),
		"command_topic":        alarmCommandTopic(base, area),
		"code_arm_required":    false,
		"code_disarm_required": false,
		"supported_features":   alarmpanel.SupportedFeatures(modes),
		"availability":         alarmAvailability(base, area),
		"availability_mode":    "all",
		"device":               alarmDeviceBlock(),
		"origin":               BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentAlarmControlPanel),
		NodeID:    alarmDiscoveryNodeID,
		ObjectID:  area,
		Payload:   buf,
		OK:        true,
	}
}
