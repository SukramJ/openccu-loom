// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// HAComponentAlarmControlPanel is the HA MQTT-Discovery component prefix
// for the alarm panel. Every zone (and the aggregate master panel) maps
// onto one alarm_control_panel entity.
const HAComponentAlarmControlPanel HAComponent = "alarm_control_panel"

// alarmDiscoveryNodeID groups every alarm panel under one HA discovery
// node (`homeassistant/alarm_control_panel/alarm/<zone>/config`). Zones
// are daemon-level, so the node carries no `<central>` segment.
const alarmDiscoveryNodeID = "alarm"

// alarmMasterZone is the reserved zone segment of the aggregate panel
// that arms/disarms every zone at once. A real zone ID is a UUID, so it
// can never collide with this token.
const alarmMasterZone = "master"

// HA alarm_control_panel supported-feature tokens. HA reads these from
// the discovery payload to decide which arm buttons the panel renders.

// alarm topic builders. Zones are daemon-level, so the alarm plane omits
// the `<central>` segment every per-device topic carries — a deliberate
// extension of the topic schema precedented only by `<base>/bridge/*`
// (docs/mqtt-topic-schema.md, notes/concepts/alarm-concept.md §13.3).
func alarmStateTopic(base, zone string) string { return base + "/alarm/" + zone + "/state" }

func alarmAvailabilityTopic(base, zone string) string {
	return base + "/alarm/" + zone + "/availability"
}
func alarmEventTopic(base, zone string) string   { return base + "/alarm/" + zone + "/event" }
func alarmCommandTopic(base, zone string) string { return base + "/alarm/" + zone + "/set" }

// alarmBridgeStatusTopic is the retained bridge LWT topic the panel's
// availability list references as the first (transport-level) source.
// The security plane declares the same source.
//
// It goes through the topic builder instead of assembling the topic a
// second time: the bridge publishes its status on the builder's
// normalised base, and with `availability_mode: "all"` an availability
// source that differs from it by a single slash never receives a
// payload, which leaves every entity of both planes unavailable
// forever rather than costing one value.
func alarmBridgeStatusTopic(base string) string { return NewTopicBuilder(base).BridgeStatus() }

// alarmDeviceBlock is the single synthetic HA device that groups every
// zone panel (and the master panel) under one card.
func alarmDeviceBlock() map[string]any {
	return map[string]any{
		"identifiers":  []string{"openccu-loom_alarm"},
		"name":         "OpenCCU-Loom Alarm",
		"manufacturer": "OpenCCU-Loom",
	}
}

// alarmAvailability is the two-source availability list every alarm panel
// carries: the bridge LWT plus the per-zone alarm availability topic. With
// availability_mode "all" HA marks the panel available only when both are
// online (notes/concepts/alarm-concept.md §13.3).
func alarmAvailability(base, zone string) []map[string]string {
	return []map[string]string{
		{
			"topic":                 alarmBridgeStatusTopic(base),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 alarmAvailabilityTopic(base, zone),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
}

// alarmCommandTemplate is the HA command template used when a panel
// requires a code: it wraps the plain HA action and the entered code into
// the JSON envelope the raw command plane parses
// (`{"action":"ARM_AWAY","code":"1234"}`). Verified against the HA
// alarm_control_panel.mqtt docs (notes/reference/alarm-assumptions.md, Alarmo/HA-app
// section). Paired with code:"REMOTE_CODE" so HA prompts for a free-form
// code rather than a fixed on-device PIN.
const alarmCommandTemplate = `{"action":"{{ action }}","code":"{{ code }}"}`

// alarmRemoteCode is HA's sentinel for "the code is validated remotely
// (by loom), not fixed in the discovery config".
const alarmRemoteCode = "REMOTE_CODE"

// alarmFeatureTrigger advertises the HA TRIGGER capability so the panel
// exposes a panic/trigger affordance; the raw command plane routes a
// TRIGGER payload onto the engine's loud panic path
// (notes/concepts/alarm-concept.md §7).
const alarmFeatureTrigger = "trigger"

// BuildAlarmPanelDiscovery builds the HA Discovery payload for one alarm
// zone (master==false) or the aggregate master panel (master==true). The
// caller resolves the display name — for the master panel it passes the
// i18n-localized "Alarm system" string, mirroring how hub discovery names
// its synthetic entities. When master is set the topic/unique-id segment
// is forced to the reserved master token regardless of zoneID.
//
// codeArmRequired / codeDisarmRequired reflect the zone's per-verb code
// policy (notes/concepts/alarm-concept.md §11). When either is set the panel
// advertises code:"REMOTE_CODE" and a command template that folds the
// entered code into the raw command JSON, so HA prompts for the code and
// loom validates it.
func BuildAlarmPanelDiscovery(base, zoneID, zoneName string, modes []hmenum.AlarmMode, master, codeArmRequired, codeDisarmRequired bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	uniqueID := "openccu-loom_alarm_" + zone
	body := map[string]any{
		"name":                 zoneName,
		"unique_id":            uniqueID,
		"default_entity_id":    defaultEntityID(string(HAComponentAlarmControlPanel), uniqueID),
		"state_topic":          alarmStateTopic(base, zone),
		"command_topic":        alarmCommandTopic(base, zone),
		"code_arm_required":    codeArmRequired,
		"code_disarm_required": codeDisarmRequired,
		"supported_features":   append(alarmpanel.SupportedFeatures(modes), alarmFeatureTrigger),
		"availability":         alarmAvailability(base, zone),
		"availability_mode":    "all",
		"device":               alarmDeviceBlock(),
		"origin":               BuildOriginInfo(),
	}
	// A code-gated panel folds the entered code into the JSON command the
	// raw plane parses; without a template HA sends the bare action and
	// the code never reaches loom's validator.
	if codeArmRequired || codeDisarmRequired {
		body["code"] = alarmRemoteCode
		body["command_template"] = alarmCommandTemplate
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentAlarmControlPanel),
		NodeID:    alarmDiscoveryNodeID,
		ObjectID:  zone,
		Payload:   buf,
		OK:        true,
	}
}

// alarmTriggeredMotionTopic carries the number of latched motion
// detectors of a zone. It is a state topic like the panel's, so the
// round-trip guard covers it the same way.
func alarmTriggeredMotionTopic(base, zone string) string {
	return base + "/alarm/" + zone + "/triggered-motion"
}

// BuildAlarmMotionResetDiscovery builds the "clear latched motion
// detectors" button for one zone (or the master aggregate).
//
// It rides the panel's existing command topic with a `RESET_MOTION`
// press payload rather than opening a second command plane: the
// subscriber already wildcards `<base>/alarm/+/set`, so one plane keeps
// one subscription and the round-trip guard keeps checking one shape.
//
// The button is an entity in its own right rather than a panel feature
// because HA's alarm_control_panel has no vocabulary for it — without a
// separate entity there is nothing for an automation to press.
func BuildAlarmMotionResetDiscovery(base, zoneID, zoneName, label string, master bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	uniqueID := "openccu-loom_alarm_" + zone + "_reset_motion"
	body := map[string]any{
		"name":              zoneName + " — " + label,
		"unique_id":         uniqueID,
		"default_entity_id": defaultEntityID(string(HAComponentButton), uniqueID),
		"command_topic":     alarmCommandTopic(base, zone),
		"payload_press":     alarmCommandResetMotion,
		"icon":              "mdi:motion-sensor-off",
		// No entity_category on purpose. Home Assistant files `config`
		// entities away in a collapsed section of the device page and
		// keeps them out of dashboards and the entity picker's default
		// view — right for a knob that tunes behaviour, wrong for
		// something an operator presses during an incident. This is a
		// control belonging to the panel's main purpose, like the panel
		// entity itself, which carries no category either. The
		// latched-detector count next to it stays `diagnostic`; that one
		// really is a readout.
		"availability":      alarmAvailability(base, zone),
		"availability_mode": "all",
		"device":            alarmDeviceBlock(),
		"origin":            BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentButton),
		NodeID:    alarmDiscoveryNodeID,
		ObjectID:  zone + "_reset_motion",
		Payload:   buf,
		OK:        true,
	}
}

// BuildAlarmTriggeredMotionDiscovery builds the sensor that reports how
// many detectors the reset button would clear.
//
// It exists so an automation can decide rather than guess: pressing the
// button blindly writes to the radio for nothing, and a non-zero count
// on a disarmed zone is usually the reason an arm refuses.
func BuildAlarmTriggeredMotionDiscovery(base, zoneID, zoneName, label string, master bool) DiscoveryItem {
	zone := zoneID
	if master {
		zone = alarmMasterZone
	}
	if zone == "" {
		return DiscoveryItem{}
	}
	uniqueID := "openccu-loom_alarm_" + zone + "_triggered_motion"
	body := map[string]any{
		"name":                zoneName + " — " + label,
		"unique_id":           uniqueID,
		"default_entity_id":   defaultEntityID(string(HAComponentSensor), uniqueID),
		"state_topic":         alarmTriggeredMotionTopic(base, zone),
		"state_class":         "measurement",
		"icon":                "mdi:motion-sensor",
		"entity_category":     "diagnostic",
		"unit_of_measurement": "detectors",
		"availability":        alarmAvailability(base, zone),
		"availability_mode":   "all",
		"device":              alarmDeviceBlock(),
		"origin":              BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentSensor),
		NodeID:    alarmDiscoveryNodeID,
		ObjectID:  zone + "_triggered_motion",
		Payload:   buf,
		OK:        true,
	}
}
