// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"
)

// daemonDeviceIdentifier groups every daemon-level (not per-central)
// HA entity under one synthetic device card. Distinct from
// [hubDeviceBlock], which represents one configured CCU's central — a
// multi-CCU deployment has exactly one daemon process but N centrals,
// so grouping a daemon-level entity under an arbitrarily-chosen
// central's card would misrepresent it.
const daemonDeviceIdentifier = "openccu-loom_daemon"

// daemonDevice is the synthetic device daemon-level entities hang off,
// expressed in the shared model (ADR 0070).
//
// The identifier is carried in a [hamodel.Identifier] with an EMPTY
// namespace on purpose. An empty namespace renders the value verbatim,
// which is the only way the already-published spelling
// "openccu-loom_daemon" survives: Home Assistant keys its device
// registry on that string and has no migration path for it, so a
// namespaced rendering ("loom:openccu-loom_daemon") would leave the old
// device card behind — with its area, its name override and its place
// in the hierarchy — and move the entity to a new one, silently.
func daemonDevice() *hamodel.Device {
	return &hamodel.Device{
		Identity: hamodel.Identity{
			IDs: []hamodel.Identifier{{Value: daemonDeviceIdentifier}},
		},
		Name:         hamodel.L("OpenCCU-Loom"),
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

// addonUpdateNodeID is the discovery topic's node segment for this
// plane. A literal, like the object id beside it: the entity belongs to
// the process, not to a central, so neither segment may follow the
// central name the way [hubNodeID] does.
const addonUpdateNodeID = "daemon"

// addonUpdateObjectID is the discovery topic's object segment, and the
// entity's key in the shared model.
const addonUpdateObjectID = "addon_update"

// addonUpdateEntity is the add-on self-updater as a shared-model
// entity.
//
// It implements [hadiscovery.Builder] for the three `update`-platform
// keys the model does not carry — latest_version_topic,
// latest_version_template and payload_install. That is the designed use
// of a Builder: platform vocabulary lives in the platform's own Fields
// struct, and only the entity knows which of its topics feeds which key.
type addonUpdateEntity struct {
	hamodel.Basic

	latestVersionTopic string
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *addonUpdateEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.Fields = hadiscovery.UpdateFields{
		LatestVersionTopic:    e.latestVersionTopic,
		LatestVersionTemplate: "{{ value_json.latest_version }}",
		PayloadInstall:        "INSTALL",
	}
	return nil
}

// addonUpdateContext is the render context for this plane: the daemon's
// own spellings for the strings the shared model must not invent.
//
// Every override below exists because the string it produces is already
// retained on operators' brokers and Home Assistant has no migration
// path for it — see the golden pin's header for what each one orphans
// if it moves.
type addonUpdateContext struct {
	hadiscovery.StdContext

	topics *TopicBuilder
}

// StateTopic implements [hadiscovery.Context].
//
// The daemon's own [TopicBuilder] renders it. This plane's single
// datapoint is not addressed by coordinate anywhere in the daemon —
// there is one add-on updater per process — and the state topic is
// already spelled by the builder that [Bridge.PublishAddonUpdateState]
// and the command subscriber share, so deriving it from a topic layout
// instead would make this the one plane whose topic is written twice.
func (c addonUpdateContext) StateTopic(hamodel.Slot) string {
	return c.topics.AddonUpdateState()
}

// CommandTopic implements [hadiscovery.Context]. HA's `update` entity
// publishes "INSTALL" here; see command_subscriber.go's AddonUpdateSink.
func (c addonUpdateContext) CommandTopic(hamodel.Slot) string {
	return c.topics.AddonUpdateCommand()
}

// Availability implements [hadiscovery.Context] with this daemon's
// bridge-status entry, the same one every other plane in this package
// emits.
func (c addonUpdateContext) Availability(*hamodel.Device, hamodel.Entity) []hadiscovery.AvailabilityEntry {
	return hubAvailability(c.topics)
}

// UniqueID implements [hadiscovery.Context]. The published id is a bare
// constant with no namespace prefix, so [hadiscovery.StdContext]'s
// namespaced default cannot produce it.
func (c addonUpdateContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return addonUpdateUniqueID
}

// NodeID implements [hadiscovery.Context]: the literal "daemon", not the
// device identifier the default derives it from.
func (c addonUpdateContext) NodeID(*hamodel.Device) string { return addonUpdateNodeID }

// ObjectID implements [hadiscovery.Context], seeding `default_entity_id`
// as `update.loom_addon_update`. The seed is the unique id rather than
// the entity key, and it must not follow the locale: Home Assistant
// derives the entity id from it once and will not rename afterwards.
func (c addonUpdateContext) ObjectID(*hamodel.Device, hamodel.Entity) string {
	return addonUpdateUniqueID
}

// addonUpdateSlot is the coordinate of the add-on updater's datapoint.
//
// It names the daemon device rather than a CCU because that is what owns
// it. The context renders both topics from the [TopicBuilder], so the
// slot's job here is to be a valid, stable coordinate carrying the read
// and write modes the render pipeline projects the two topics from.
func addonUpdateSlot() hamodel.Slot {
	return hamodel.S(daemonDeviceIdentifier, "", hamodel.BucketUnset, addonUpdateObjectID)
}

// buildAddonUpdateEntity assembles the model entity and the context it
// renders under.
func (d *DefaultDiscoveryBuilder) buildAddonUpdateEntity() (*addonUpdateEntity, addonUpdateContext) {
	ent := &addonUpdateEntity{
		Basic: hamodel.Basic{
			EntityKey:      addonUpdateObjectID,
			EntityPlatform: hacatalog.PlatformUpdate,
			Description: hamodel.Description{
				NameKey:  "discovery.addon_update",
				Category: hacatalog.EntityCategoryDiagnostic,
				Enabled:  hamodel.Ptr(true),
				// Bridge only, mode `all`: the daemon's LWT is the sole
				// source gating this entity — there is no device
				// reachability behind a synthetic daemon card to add.
				Availability: hamodel.BridgeOnly(),
				// No `value_template`: HA's MQTT update platform parses the
				// raw state_topic payload natively against its state-payload
				// schema (installed_version, latest_version, in_progress)
				// when no value_template narrows it to a scalar first.
				// `in_progress_template` is not a schema option at all — HA
				// reads `in_progress` only from that native parse — so
				// setting either one here left the entity showing no
				// install-in-progress indication.
				ValueTemplate: hamodel.NoValueTemplate,
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Slot: addonUpdateSlot(), Mode: hamodel.Read},
				{Role: hamodel.RoleCommand, Slot: addonUpdateSlot(), Mode: hamodel.Write},
			},
		},
		latestVersionTopic: d.TopicBuilder.AddonUpdateState(),
	}
	ctx := addonUpdateContext{
		StdContext: hadiscovery.StdContext{
			Lang:       d.Locale,
			Translator: d.tr,
		},
		topics: d.TopicBuilder,
	}
	return ent, ctx
}

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
	ent, ctx := d.buildAddonUpdateEntity()
	comp, err := hadiscovery.RenderComponent(ctx, daemonDevice(), ent, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	return discoveryItemFor(comp, addonUpdateNodeID, addonUpdateObjectID)
}
