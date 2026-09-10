// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// daemonDeviceIdentifier groups every daemon-level (not per-central)
// HA entity under one synthetic device card. Distinct from
// [hubDeviceBlock], which represents one configured CCU's central — a
// multi-CCU deployment has exactly one daemon process but N centrals,
// so grouping a daemon-level entity under an arbitrarily-chosen
// central's card would misrepresent it.
const daemonDeviceIdentifier = "openccu-loom_daemon"

// daemonDeviceBlock builds the synthetic HA `device` block for
// daemon-level entities.
func daemonDeviceBlock() *hadiscovery.DeviceInfo {
	return &hadiscovery.DeviceInfo{
		Identifiers:  []string{daemonDeviceIdentifier},
		Name:         "OpenCCU-Loom",
		Manufacturer: "OpenCCU-Loom",
		Model:        "Daemon",
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
	comp := hadiscovery.Component{
		Platform:        hacatalog.PlatformUpdate,
		Name:            d.tr("discovery.addon_update"),
		UniqueID:        addonUpdateUniqueID,
		DefaultEntityID: defaultEntityID(string(HAComponentUpdate), addonUpdateUniqueID),
		// No `value_template`: HA's MQTT update platform parses the raw
		// state_topic payload natively against its state-payload schema
		// (installed_version, latest_version, in_progress) when no
		// value_template narrows it to a scalar first. `in_progress_template`
		// is not a schema option at all — HA reads `in_progress` only from
		// that native parse — so setting either one here left the entity
		// showing no install-in-progress indication.
		StateTopic:       topic,
		CommandTopic:     d.TopicBuilder.AddonUpdateCommand(),
		EntityCategory:   "diagnostic",
		EnabledByDefault: hadiscovery.Ptr(true),
		Availability:     hubAvailability(d.TopicBuilder),
		AvailabilityMode: "all",
		Device:           daemonDeviceBlock(),
		Origin:           BuildOriginInfo(),
		Fields: hadiscovery.UpdateFields{
			LatestVersionTopic:    topic,
			LatestVersionTemplate: "{{ value_json.latest_version }}",
			PayloadInstall:        "INSTALL",
		},
	}
	return discoveryItemFor(comp, "daemon", "addon_update")
}
