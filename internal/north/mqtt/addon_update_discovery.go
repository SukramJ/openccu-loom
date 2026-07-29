// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "encoding/json"

// daemonDeviceIdentifier groups every daemon-level (not per-central)
// HA entity under one synthetic device card. Distinct from
// [hubDeviceBlock], which represents one configured CCU's central — a
// multi-CCU deployment has exactly one daemon process but N centrals,
// so grouping a daemon-level entity under an arbitrarily-chosen
// central's card would misrepresent it.
const daemonDeviceIdentifier = "openccu-loom_daemon"

// daemonDeviceBlock builds the synthetic HA `device` block for
// daemon-level entities.
func daemonDeviceBlock() map[string]any {
	return map[string]any{
		"identifiers":  []string{daemonDeviceIdentifier},
		"name":         "OpenCCU-Loom",
		"manufacturer": "OpenCCU-Loom",
		"model":        "Daemon",
	}
}

// addonUpdateUniqueID is the stable HA unique_id for the add-on
// self-update entity (ADR 0057). Unlike every hub/device entity in
// this package it carries no central-serial suffix: the daemon
// self-updates itself, not any one CCU, so there is exactly one
// instance of this entity per daemon process regardless of how many
// centrals are configured — nothing to disambiguate.
const addonUpdateUniqueID = "loom_addon_update"

// BuildAddonUpdateDiscovery exposes the CCU add-on self-updater's
// state as a HA `update` entity (ADR 0057), mirroring
// [DefaultDiscoveryBuilder.BuildHubUpdateDiscovery]'s state/
// latest-version topic shape. It additionally wires a command_topic:
// HA's `update` entity's "INSTALL" button publishes here, which the
// command subscriber (see command_subscriber.go's AddonUpdateSink)
// translates into the daemon's install trigger.
//
// Unlike every other builder in this package it takes no centralName:
// this entity is not scoped to any CCU.
func (d *DefaultDiscoveryBuilder) BuildAddonUpdateDiscovery() DiscoveryItem {
	topic := d.TopicBuilder.AddonUpdateState()
	body := map[string]any{
		"name":                    d.tr("discovery.addon_update"),
		"unique_id":               addonUpdateUniqueID,
		"object_id":               addonUpdateUniqueID,
		"state_topic":             topic,
		"value_template":          "{{ value_json.installed_version }}",
		"latest_version_topic":    topic,
		"latest_version_template": "{{ value_json.latest_version }}",
		"in_progress_template":    "{{ value_json.in_progress }}",
		"command_topic":           d.TopicBuilder.AddonUpdateCommand(),
		"payload_install":         "INSTALL",
		"entity_category":         "diagnostic",
		"enabled_by_default":      true,
		"availability":            hubAvailability(d.TopicBuilder),
		"availability_mode":       "all",
		"device":                  daemonDeviceBlock(),
		"origin":                  BuildOriginInfo(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentUpdate), NodeID: "daemon", ObjectID: "addon_update", Payload: buf, OK: true}
}
