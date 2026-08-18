// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// securityDiscoveryNodeID groups every Security & Safety entity under
// one discovery node. Like the alarm plane the domain is daemon-level,
// so the node carries no central segment.
const securityDiscoveryNodeID = "security"

// Security topic builders. The tree sits beside `alarm/` and
// `bridge/` as the third daemon-level plane — see ADR 0052 and its
// extension in ADR 0059.
func securityStateTopic(base, key string) string { return base + "/security/" + key }

func securityClassTopic(base string, class hmenum.SecurityClass) string {
	return base + "/security/class/" + string(class)
}

func securityZoneTopic(base, slug string) string { return base + "/security/zone/" + slug }

func securityAvailabilityTopic(base string) string { return base + "/security/availability" }

// securityDeviceBlock is the HA device every Security & Safety entity
// belongs to.
//
// It is deliberately its own card rather than a reuse of the alarm
// card. The alarm block is rewritten on every panel discovery and on
// every broker reconnect; two publishers writing different blocks under
// one identifier set make the card name flap and would rename every
// existing alarm entity's friendly name. Two cards cost nothing and
// this is also the "own device" the domain was asked for.
func securityDeviceBlock(name, configURL string) map[string]any {
	block := map[string]any{
		"identifiers":  []string{"openccu-loom_security"},
		"name":         name,
		"manufacturer": "OpenCCU-Loom",
		"model":        "Security & Safety",
		"sw_version":   build.Version,
	}
	if configURL != "" {
		block["configuration_url"] = configURL
	}
	return block
}

// securityAvailability is the two-source availability list every entity
// carries: the bridge LWT plus the domain's own flag. With mode "all" a
// consumer marks the entity available only when both are online, so a
// daemon that is up but has lost the domain is distinguishable from a
// broker outage.
func securityAvailability(base string) []map[string]string {
	return []map[string]string{
		{
			"topic":                 alarmBridgeStatusTopic(base),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 securityAvailabilityTopic(base),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
}

// securityEventTypes is the announced vocabulary of the two event
// entities. A consumer rejects an event whose type was not announced,
// so the emitter and this list must never drift — they share this
// constant for that reason.
//
// SecurityVerbTest is deliberately absent: nothing constructs it yet.
// Announcing a type no producer emits is a promise the plane cannot
// keep, and a consumer that filters on the announced set would silently
// wait for an event that never arrives. It joins the list together with
// the endpoint that fires it.
var securityEventTypes = []string{
	string(hmenum.SecurityVerbTriggered),
	string(hmenum.SecurityVerbCleared),
	string(hmenum.SecurityVerbRaised),
}

// securityEntity is the shape of one discovery payload before
// marshalling.
type securityEntity struct {
	// component is the consumer entity type.
	component HAComponent
	// key is the topic suffix and the unique-id suffix.
	key string
	// name is the localized display name.
	name string
	// deviceClass is optional.
	deviceClass string
	// stateClass is optional (measurement counters).
	stateClass string
	// diagnostic marks a supporting entity rather than a primary one.
	diagnostic bool
	// enabledByDefault=false hides an entity until an operator asks.
	enabledByDefault bool
	// payloadOn/Off apply to binary sensors.
	payloadOn, payloadOff string
	// jsonAttributes publishes the state topic as the attribute source
	// too — the pattern hub discovery already uses.
	jsonAttributes bool
	// valueTemplate extracts the state from a JSON payload.
	valueTemplate string
	// event marks a non-retained event entity, which must carry no
	// value template and no device class.
	event bool
	// topic overrides the derived state topic. Class and zone entities
	// need it because their topics are nested (`security/class/<c>`)
	// while their keys are flat (`class_<c>`) — deriving the topic from
	// the key declared `security/class_smoke` while the publisher wrote
	// `security/class/smoke`, so every one of those entities appeared in
	// a consumer and stayed unavailable forever.
	topic string
	// options is the enum vocabulary. A sensor with device_class enum
	// and no options is refused outright, per the rule this package
	// already applies to CCU enum sensors.
	options []string
}

// BuildSecurityDiscovery builds the discovery payload for one Security
// & Safety entity.
//
// deviceName and configURL identify the card; name is the entity's own
// localized label.
func BuildSecurityDiscovery(base, deviceName, configURL string, e securityEntity) DiscoveryItem {
	if e.key == "" {
		return DiscoveryItem{}
	}
	uniqueID := "loom_security_" + e.key
	stateTopic := e.topic
	if stateTopic == "" {
		stateTopic = securityStateTopic(base, e.key)
	}

	body := map[string]any{
		"name":              e.name,
		"unique_id":         uniqueID,
		"default_entity_id": defaultEntityID(string(e.component), uniqueID),
		"state_topic":       stateTopic,
		"availability":      securityAvailability(base),
		"availability_mode": "all",
		"device":            securityDeviceBlock(deviceName, configURL),
		"origin":            BuildOriginInfo(),
	}
	switch {
	case e.event:
		// An event entity must not carry a value template — a scalar
		// destroys the JSON parsing — and must not carry a device
		// class, whose vocabulary is limited to doorbell/button/motion.
		body["event_types"] = securityEventTypes
	default:
		if e.deviceClass != "" {
			body["device_class"] = e.deviceClass
		}
		if len(e.options) > 0 {
			body["options"] = e.options
		}
		if e.stateClass != "" {
			body["state_class"] = e.stateClass
		}
		if e.payloadOn != "" {
			body["payload_on"] = e.payloadOn
			body["payload_off"] = e.payloadOff
		}
		if e.valueTemplate != "" {
			body["value_template"] = e.valueTemplate
		}
		if e.jsonAttributes {
			body["json_attributes_topic"] = stateTopic
			body["json_attributes_template"] = "{{ value_json | tojson }}"
		}
	}
	if e.diagnostic {
		body["entity_category"] = "diagnostic"
	}
	if !e.enabledByDefault {
		body["enabled_by_default"] = false
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(e.component),
		NodeID:    securityDiscoveryNodeID,
		ObjectID:  e.key,
		Payload:   buf,
		OK:        true,
	}
}
