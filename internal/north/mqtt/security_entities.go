// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// stateValueTemplate extracts the state from the JSON payload that
// doubles as the attribute source.
const stateValueTemplate = "{{ value_json.state }}"

// securitySystemEntities are the entities that exist regardless of what
// the installation has: the folded state, the two aggregate flags, the
// engine health, the two event streams and the two retained
// last-report sensors.
//
// tr resolves a display name with an English fallback.
func securitySystemEntities(tr func(key, fallback string) string) []securityEntity {
	return []securityEntity{
		{
			component: HAComponentEvent, key: "event", event: true,
			name:             tr("security.entity.event", "Security event"),
			enabledByDefault: true,
		},
		{
			component: HAComponentEvent, key: "fault", event: true,
			name:             tr("security.entity.fault_event", "Security fault event"),
			enabledByDefault: true, diagnostic: true,
		},
		{
			component: HAComponentSensor, key: "state",
			name:        tr("security.entity.state", "Security state"),
			deviceClass: "enum",
			// An enum sensor without options is refused; the vocabulary
			// is the domain's severity ladder, in its own ascending order.
			options:          securitySeverityOptions(),
			valueTemplate:    stateValueTemplate,
			jsonAttributes:   true,
			enabledByDefault: true,
		},
		{
			component: HAComponentBinarySensor, key: "alarm",
			name:        tr("security.entity.alarm", "Security alarm"),
			deviceClass: "safety",
			payloadOn:   "ON", payloadOff: "OFF",
			valueTemplate:    stateValueTemplate,
			jsonAttributes:   true,
			enabledByDefault: true,
		},
		{
			component: HAComponentBinarySensor, key: "problem",
			name:        tr("security.entity.problem", "Security problem"),
			deviceClass: "problem",
			payloadOn:   "ON", payloadOff: "OFF",
			valueTemplate:    stateValueTemplate,
			jsonAttributes:   true,
			diagnostic:       true,
			enabledByDefault: true,
		},
		{
			component: HAComponentBinarySensor, key: "health",
			name:        tr("security.entity.health", "Alarm engine problem"),
			deviceClass: "problem",
			payloadOn:   "ON", payloadOff: "OFF",
			diagnostic:       true,
			enabledByDefault: true,
		},
		{
			component: HAComponentSensor, key: "last_alarm",
			name:             tr("security.entity.last_alarm", "Last security alarm"),
			deviceClass:      "timestamp",
			valueTemplate:    "{{ value_json.at | default('') }}",
			jsonAttributes:   true,
			enabledByDefault: true,
		},
		{
			component: HAComponentSensor, key: "last_fault",
			name:             tr("security.entity.last_fault", "Last security fault"),
			deviceClass:      "timestamp",
			valueTemplate:    "{{ value_json.at | default('') }}",
			jsonAttributes:   true,
			diagnostic:       true,
			enabledByDefault: true,
		},
	}
}

// securityClassDeviceClass maps a hazard or fault class onto the
// consumer's binary-sensor vocabulary.
var securityClassDeviceClass = map[hmenum.SecurityClass]string{
	hmenum.SecurityClassSmoke:     "smoke",
	hmenum.SecurityClassWater:     "moisture",
	hmenum.SecurityClassGas:       "gas",
	hmenum.SecurityClassCO:        "carbon_monoxide",
	hmenum.SecurityClassTamper:    "tamper",
	hmenum.SecurityClassBattery:   "battery",
	hmenum.SecurityClassTechnical: "problem",
	hmenum.SecurityClassIntrusion: "safety",
	hmenum.SecurityClassPanic:     "safety",
}

// securityClassEntity declares one class aggregate. It is only ever
// called for a class the installation actually has a source for.
func securityClassEntity(base string, class hmenum.SecurityClass, tr func(key, fallback string) string) securityEntity {
	return securityEntity{
		component:        HAComponentBinarySensor,
		key:              "class_" + string(class),
		topic:            securityClassTopic(base, class),
		name:             tr("security.entity.class."+string(class), string(class)),
		deviceClass:      securityClassDeviceClass[class],
		payloadOn:        "ON",
		payloadOff:       "OFF",
		valueTemplate:    stateValueTemplate,
		jsonAttributes:   true,
		diagnostic:       class.Diagnostic(),
		enabledByDefault: true,
	}
}

// securityZoneEntity declares one zone aggregate. Its state is the
// count of currently active sources, which makes it a measurement
// rather than a status — the attributes carry the detail.
func securityZoneEntity(base, slug, name string, tr func(key, fallback string) string) securityEntity {
	label := name
	if label == "" {
		label = slug
	}
	return securityEntity{
		component:        HAComponentSensor,
		key:              "zone_" + slug,
		topic:            securityZoneTopic(base, slug),
		name:             tr("security.entity.zone", "Zone") + " " + label,
		stateClass:       "measurement",
		valueTemplate:    stateValueTemplate,
		jsonAttributes:   true,
		enabledByDefault: true,
	}
}

// securitySeverityOptions renders the domain's severity ladder as the string
// list an HA enum sensor declares. The order is the domain's, because the
// ladder is ordered — a rung added there must appear here, and a copy typed
// out locally would not.
func securitySeverityOptions() []string {
	ladder := hmenum.SecuritySeverities()
	out := make([]string, 0, len(ladder))
	for _, s := range ladder {
		out = append(out, string(s))
	}
	return out
}
