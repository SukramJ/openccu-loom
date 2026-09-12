// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// UpdateEvent carries the per-device context needed to build discovery
// and state topics for an HA `update` entity. It is separate from the
// per-channel [Event] type because the update entity is device-level
// (no channel number).
type UpdateEvent struct {
	// Central is the CCU identifier (required for topic scoping).
	Central string
	// Interface is the CCU interface identifier (e.g. "HmIP-RF").
	Interface string
	// DeviceAddress is the base device address (without channel suffix).
	DeviceAddress string
	// DeviceName is the human-readable device name used in the HA device block.
	DeviceName string
	// Model is the CCU device model string (e.g. "HmIP-eTRV-2").
	Model string
	// Device, when non-nil, is consulted by deviceDescriptor for the
	// `payload:"info"` map — same as Event.Device.
	Device any
	// Update is the firmware-update source. Must be non-nil: it is what
	// marks the device as updatable at all, and a device with no firmware
	// surface gets no entity.
	Update payload.HADiscoveryComponentBuilder
}

// updateEntityKey is the entity's key in the shared model and the
// `parameter` segment the unique id is derived from. The published
// `loom_<address>_update` spelling is a function of it, so it is a
// constant rather than a literal repeated at both call sites.
const updateEntityKey = "update"

// updateValueTemplate reads the installed version out of the four-field
// firmware document [Bridge.PublishUpdateState] publishes. The state
// topic carries that document rather than a scalar, so the render
// pipeline's envelope default would reach for a `value` key no payload on
// this plane has.
const updateValueTemplate = "{{ value_json.firmware }}"

// updateLatestVersionTemplate reads the target version out of the same
// document. Home Assistant compares it against the installed version to
// decide whether the entity reports an available update.
const updateLatestVersionTemplate = "{{ value_json.latest_firmware }}"

// updateJSONAttributesTemplate republishes the whole firmware document as
// entity attributes, so an operator can inspect all four fields
// (firmware, latest_firmware, in_progress, firmware_update_state) from the
// entity rather than from the broker.
const updateJSONAttributesTemplate = "{{ value_json | tojson }}"

// updateEntity is the per-device firmware updater on the shared model: a
// [hamodel.Basic] with one description and a single readable binding.
//
// It reads and never writes. Home Assistant's `update` entity would
// publish its install payload straight to the broker on a button press,
// and nothing in the daemon subscribes to that topic — a device flash is
// gated behind the operator-confirmed `POST /devices/{addr}/firmware/update`
// instead, for the same reason the CCU's own firmware update is. Declaring
// no writable binding is what keeps `command_topic` off the payload: the
// render pipeline projects that key from a write binding, and the `update`
// platform does accept it.
type updateEntity struct {
	hamodel.Basic

	stateTopic string
	title      string
}

// BuildDiscovery implements [hadiscovery.Builder] for the keys the model
// does not carry.
//
// Three of them are platform `update`'s own vocabulary —
// latest_version_topic, latest_version_template and title — and belong in
// the platform's typed [hadiscovery.UpdateFields] rather than in a
// description that says what an entity is. `display_precision` is update's
// own spelling too: the model's Precision projects to
// `suggested_display_precision`, which this platform does not declare and
// Home Assistant would drop in silence.
//
// The two json_attributes keys are typed [hadiscovery.Component] fields
// with no home in [hamodel.Description], so a builder is the only stage
// that can set them.
func (e *updateEntity) BuildDiscovery(_ hadiscovery.Context, comp *hadiscovery.Component) error {
	comp.JSONAttributesTopic = e.stateTopic
	comp.JSONAttributesTemplate = updateJSONAttributesTemplate
	comp.Fields = hadiscovery.UpdateFields{
		LatestVersionTopic:    e.stateTopic,
		LatestVersionTemplate: updateLatestVersionTemplate,
		Title:                 e.title,
		DisplayPrecision:      hadiscovery.Ptr(0),
	}
	return nil
}

// updateTopicLayout renders this plane's topics through this daemon's own
// [TopicBuilder], so the render pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused. An update entity pins one device, and
// [TopicBuilder] is the authority on how this daemon spells that device's
// update state, install and availability topics; deriving them from the
// slot again would be a second implementation of the same schema with
// nothing keeping the two in step.
type updateTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	ev      UpdateEvent
	central string
}

// State implements the shared model's topic layout.
func (l updateTopicLayout) State(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceUpdateState(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Command implements the shared model's topic layout.
//
// The entity declares no write binding, so nothing projects this string
// onto the payload. It still answers with the canonical install topic
// rather than the empty string, so the spelling has one home if the
// command path is ever wired.
func (l updateTopicLayout) Command(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceUpdateCommand(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Availability implements the shared model's topic layout: the per-device
// availability topic, which is the second of the two entries every device
// entity on this daemon carries.
func (l updateTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l updateTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// updateDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// They are overridden because Home Assistant has no migration path for any
// of them, and this plane derives them two different ways from one event —
// the unique id (and the entity-id seed with it) from the device address
// alone, the node id from a slugged central plus that address. No single
// derivation could produce both.
type updateDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon
// already publishes. It carries no central: a real device's address is
// globally unique, and scoping it would re-key every firmware-update
// entity on every fleet at once.
func (c updateDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. It is central-scoped, so the
// same device on two centrals publishes under two node ids.
func (c updateDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context]. This plane is one that DOES
// publish an entity-id seed: `default_entity_id` is part of every update
// config. The seed is the unique id — the same string the discovery
// topic's object-id segment carries — and the pipeline prefixes the
// platform itself.
func (c updateDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// updateModelDevice lifts the device descriptor this daemon harvests into
// the shared model's [hamodel.Device], so the render pipeline emits the
// device block instead of a builder stamping one on afterwards.
//
// The identifiers keep an EMPTY namespace, which the shared model renders
// verbatim. That is what lets the published `openccu-loom_<address>` and
// `openccu-loom_central_<central>` spellings survive: Home Assistant keys
// its device registry on those strings and has no migration path for them
// either.
func updateModelDevice(info *hadiscovery.DeviceInfo) *hamodel.Device {
	if info == nil {
		return nil
	}
	dev := &hamodel.Device{
		Name:          hamodel.L(info.Name),
		Manufacturer:  info.Manufacturer,
		Model:         info.Model,
		ModelID:       info.ModelID,
		SWVersion:     info.SWVersion,
		HWVersion:     info.HWVersion,
		SerialNumber:  info.SerialNumber,
		SuggestedArea: info.SuggestedArea,
		ConfigURL:     info.ConfigurationURL,
	}
	for _, id := range info.Identifiers {
		dev.Identity.IDs = append(dev.Identity.IDs, hamodel.Identifier{Value: id})
	}
	for _, conn := range info.Connections {
		dev.Identity.Connections = append(dev.Identity.Connections,
			hamodel.Connection{Type: conn[0], Value: conn[1]})
	}
	if info.ViaDevice != "" {
		dev.Via = &hamodel.Identity{IDs: []hamodel.Identifier{{Value: info.ViaDevice}}}
	}
	return dev
}

// updateSlot is the coordinate of the device's firmware datapoint.
//
// The context renders every topic from the [TopicBuilder], so the slot's
// job here is to be a valid, stable coordinate carrying the read mode the
// render pipeline projects `state_topic` from. The channel is empty
// because the update entity is device-level — it is the one entity on this
// daemon that names no channel at all.
func updateSlot(dev *hamodel.Device, centralName string, ev UpdateEvent) hamodel.Slot {
	return hamodel.S(dev.UID(), "", hamodel.BucketUnset, updateEntityKey).
		In(centralName, ev.Interface)
}

// BuildUpdateDiscovery builds the HA Discovery `update` payload for one
// device's firmware-update entity.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`.
//
// Returns DiscoveryItem{OK: false} when ev.Update is nil, when the device
// descriptor carries no identity, when the unique id cannot be scoped, or
// when rendering or marshalling fails.
func (d *DefaultDiscoveryBuilder) BuildUpdateDiscovery(centralName string, ev UpdateEvent) DiscoveryItem {
	if ev.Update == nil {
		return DiscoveryItem{}
	}
	// unique_id does three jobs — the entity key, the discovery topic's
	// object-id segment and the entity-id seed — and Home Assistant can
	// migrate none of them.
	uniqueID, scoped := d.scopedUniqueID(centralName, ev.DeviceAddress, updateEntityKey, "")
	if !scoped {
		return DiscoveryItem{}
	}
	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)

	// Compose the Event-like value needed by deviceDescriptor. It carries
	// no Channel, so the sub-device branch cannot fire: the update entity
	// is device-level and has no channel to inspect a group on. That is
	// deliberate — an update entity re-homed onto a sub-device card would
	// report the firmware of the whole physical device from a slice of it.
	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		Device:        ev.Device,
	}
	dev := updateModelDevice(deviceDescriptor(mockEv, d.hubURLFor(mockEv), d.SubDevicesEnabled))
	if dev == nil {
		return DiscoveryItem{}
	}

	stateTopic := d.TopicBuilder.DeviceUpdateState(centralName, ev.Interface, ev.DeviceAddress)
	entity := &updateEntity{
		Basic: hamodel.Basic{
			EntityKey:      updateEntityKey,
			EntityPlatform: hacatalog.PlatformUpdate,
			Description: hamodel.Description{
				// HA composes entity_id as `<device-slug>_<entity-name-slug>`,
				// so the entity `name` must NOT contain the device name again
				// (otherwise the slug stutters into
				// "update.alarmsirene_fl_alarmsirene_fl_firmware"). Keep the
				// name relative to the device and let HA prefix.
				NameKey:       "discovery.firmware",
				DeviceClass:   hamodel.DeviceClass(hacatalog.UpdateDeviceClassFirmware),
				Category:      hacatalog.EntityCategoryConfig,
				ValueTemplate: updateValueTemplate,
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: updateSlot(dev, centralName, ev)},
			},
		},
		stateTopic: stateTopic,
		// The title is the model string, not a translated label: it names
		// the firmware's product, and the CCU's model spelling is what an
		// operator matches against eQ-3's release notes.
		title: ev.Model + " Firmware",
	}

	ctx := updateDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: updateTopicLayout{d: d, ev: ev, central: centralName},
			Lang:   d.Locale,
			// The description's own template wins over the encoding, so
			// this only decides what an unset one would have produced. It
			// is set for the same reason it is set on the other planes:
			// this daemon's state topics carry no envelope.
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
	}

	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	buf, err := json.Marshal(comp)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentUpdate),
		NodeID:    nodeID,
		ObjectID:  uniqueID,
		Payload:   buf,
		OK:        true,
	}
}

// PublishUpdateDiscovery publishes the HA Discovery payload for a
// device's firmware-update entity. The payload is retained and
// deduplicated through the same cache as all other discovery messages.
//
// No-ops when HA discovery is disabled on the bridge, when
// BuildUpdateDiscovery returns OK=false, or when no DefaultDiscoveryBuilder
// is wired.
func (b *Bridge) PublishUpdateDiscovery(ctx context.Context, centralName string, ev UpdateEvent) error {
	if !b.cfg.HADiscoveryEnabled {
		return nil
	}
	if b.cfg.DiscoveryBuilder == nil {
		return nil
	}
	builder, ok := b.cfg.DiscoveryBuilder.(*DefaultDiscoveryBuilder)
	if !ok {
		return nil
	}
	item := builder.BuildUpdateDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishUpdateState publishes the firmware-state JSON to the per-device
// retained update state topic.
//
// { "firmware":              "<installed>", "latest_firmware":
// "<target>", "in_progress":           <bool>, "firmware_update_state":
// "<state-string>" }
//
// No-ops when the raw plane is disabled or state is nil.
func (b *Bridge) PublishUpdateState(ctx context.Context, centralName, iface, address string, state payload.StatePayload) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if state == nil {
		state = map[string]any{}
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	topic := b.topics.DeviceUpdateState(centralName, iface, address)
	return b.publishRawRetained(ctx, topic, body)
}
