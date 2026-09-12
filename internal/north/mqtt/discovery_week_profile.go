// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strconv"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"
)

// WeekProfileDescriptor is the narrow read-side contract on a week-profile
// data point that the discovery builder needs. Implemented by
// [weekprofile.ProfileDataPoint]. Defining it here keeps the mqtt package
// free of a model import.
type WeekProfileDescriptor interface {
	// UniqueID returns the canonical stable identifier for the entity,
	// "<central>:<channelAddress>:WEEKPROFILE".
	UniqueID() string
	// AvailableProfiles returns the list of profile keys ("P1".."PN").
	// Returns nil for non-climate channels.
	AvailableProfiles() []string
	// CurrentProfile returns the active profile key ("P1".."PN") or an
	// empty string when unset or not applicable.
	CurrentProfile() string
	// OnChange registers a callback that is fired after the active
	// profile changes. The returned function unsubscribes.
	OnChange(fn func()) func()
}

// WeekProfileEvent carries the channel context needed to build topics
// and the discovery `device` block for a week-profile entity.
type WeekProfileEvent struct {
	// Central is the CCU identifier (required for topic scoping).
	Central string
	// Interface is the CCU interface identifier (e.g. "HmIP-RF").
	Interface string
	// DeviceAddress is the base device address (without channel suffix).
	DeviceAddress string
	// ChannelNo is the channel number within the device.
	ChannelNo int
	// DeviceName is the human-readable device name used in the HA device block.
	DeviceName string
	// Model is the CCU device model string (e.g. "HmIP-eTRV-2").
	Model string
	// Device, when non-nil, is consulted by deviceDescriptor for the
	// `payload:"info"` map — same as Event.Device.
	Device any
	// WP is the descriptor; must be non-nil.
	WP WeekProfileDescriptor
}

// weekProfileEntity is the week-profile select on the shared model: a
// [hamodel.Basic] with one description and the two bindings the render
// pipeline projects `state_topic` and `command_topic` from.
//
// It carries no capability interface and no [hadiscovery.Builder]: a select
// whose entire platform vocabulary is `options` needs neither.
type weekProfileEntity struct {
	hamodel.Basic
}

// weekProfileTopicLayout renders this plane's topics through this daemon's
// own [TopicBuilder], so the render pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused. A week-profile entity pins one channel, and
// [TopicBuilder] is the authority on how this daemon spells that channel's
// week-profile state, command and availability topics; deriving them from the
// slot again would be a second implementation of the same schema with nothing
// keeping the two in step.
type weekProfileTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	ev      WeekProfileEvent
	central string
}

// State implements the shared model's topic layout.
func (l weekProfileTopicLayout) State(hamodel.Slot) string {
	return l.d.TopicBuilder.WeekProfileState(l.central, l.ev.Interface, l.ev.DeviceAddress, l.ev.ChannelNo)
}

// Command implements the shared model's topic layout.
func (l weekProfileTopicLayout) Command(hamodel.Slot) string {
	return l.d.TopicBuilder.WeekProfileCommand(l.central, l.ev.Interface, l.ev.DeviceAddress, l.ev.ChannelNo)
}

// Availability implements the shared model's topic layout: the per-device
// availability topic, which is the second of the two entries every channel
// entity on this daemon carries.
func (l weekProfileTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l weekProfileTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// weekProfileDiscoveryContext is the render context for this plane: the
// standard one with this daemon's three identity strings substituted.
//
// All three are overridden because Home Assistant has no migration path for
// any of them, and this plane derives all three DIFFERENTLY from the same
// event — the unique id from the channel address alone, the node id from a
// slugged central plus the device address, the object id from the
// descriptor's own legacy identifier with the central embedded verbatim. No
// single derivation could produce all three.
type weekProfileDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
	objectID string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes. It carries no central: a real device's address is globally
// unique, and scoping it would re-key every week-profile entity on every
// fleet at once.
func (c weekProfileDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context]. It is central-scoped, so the same
// thermostat on two centrals publishes under two node ids.
func (c weekProfileDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context]. This plane is one that DOES
// publish an entity-id seed: `default_entity_id` is part of every
// week-profile config, so the seed is handed to the pipeline rather than
// suppressed. It is the same string the discovery topic's object-id segment
// carries, and the pipeline prefixes the platform itself.
func (c weekProfileDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string {
	return c.objectID
}

// BuildWeekProfileDiscovery builds the HA Discovery `select` payload for
// one climate channel's week-profile entity.
//
// The payload is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]), which attaches the device and origin
// blocks and omits `platform`.
//
// The returned [DiscoveryItem] uses the same (component, nodeID, objectID)
// shape as [DiscoveryItem] elsewhere — hand it to [Bridge.PublishHubDiscovery]
// or [Bridge.PublishWeekProfileDiscovery].
//
// Returns DiscoveryItem{OK: false} when:
// - wp is nil, AvailableProfiles is empty (non-climate channels), or
// JSON marshalling fails.
//
// Inbound writes on the command topic flow through
// [CommandSubscriber.handleWeekProfile] → [WeekProfileSink].
func (d *DefaultDiscoveryBuilder) BuildWeekProfileDiscovery(centralName string, ev WeekProfileEvent) DiscoveryItem {
	if ev.WP == nil {
		return DiscoveryItem{}
	}
	profiles := ev.WP.AvailableProfiles()
	if len(profiles) == 0 {
		return DiscoveryItem{}
	}

	// Build the canonical unique_id from the channel address and the
	// "WEEKPROFILE" parameter. The WeekProfileEvent carries DeviceAddress and
	// ChannelNo directly, so we compose the channel address string here.
	channelAddr := ev.DeviceAddress + ":" + strconv.Itoa(ev.ChannelNo)
	uniqueID, scoped := d.scopedUniqueID(centralName, channelAddr, "WEEKPROFILE", "")
	if !scoped {
		return DiscoveryItem{}
	}
	// objectID is the normalised (colon→underscore, lower-case) form of the
	// legacy "<central>:<addr>:WEEKPROFILE" identifier — kept for HA topic
	// continuity. The unique_id is the canonical routing-key form.
	rawUID := ev.WP.UniqueID()
	objectID := strings.NewReplacer(":", "_").Replace(strings.ToLower(rawUID))
	if !strings.HasPrefix(objectID, "openccu-loom_") {
		objectID = "openccu-loom_" + objectID
	}

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)

	// Compose the Event-like value needed by deviceDescriptor.
	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		ChannelNo:     ev.ChannelNo,
		Device:        ev.Device,
	}
	dev := modelDeviceFromInfo(deviceDescriptor(mockEv, d.hubURLFor(mockEv), d.SubDevicesEnabled))
	if dev == nil {
		return DiscoveryItem{}
	}

	// One slot, bound twice. The channel's week-profile datapoint is read on
	// the state topic and written on the command topic, and the render
	// pipeline resolves the two roles separately, so a single ReadWrite
	// binding on one role would project only one of the two topics.
	slot := hamodel.S(dev.UID(), strconv.Itoa(ev.ChannelNo), hamodel.BucketCustom, "WEEKPROFILE").
		In(centralName, ev.Interface)
	entity := &weekProfileEntity{
		Basic: hamodel.Basic{
			EntityKey:      "weekprofile",
			EntityPlatform: hacatalog.PlatformSelect,
			Description: hamodel.Description{
				NameKey: "discovery.week_profile",
				// The profile keys are their own labels: "P1".."PN" is what
				// the CCU understands and what Home Assistant stores as the
				// entity's state, so codes without labels render verbatim.
				Options: &hamodel.Enum{Codes: append([]string(nil), profiles...)},
			},
			Binds: []hamodel.Binding{
				{Role: hamodel.RoleState, Mode: hamodel.Read, Slot: slot},
				{Role: hamodel.RoleCommand, Mode: hamodel.Write, Slot: slot},
			},
		},
	}

	ctx := weekProfileDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: weekProfileTopicLayout{d: d, ev: ev, central: centralName},
			Lang:   d.Locale,
			// The week-profile state topic carries the bare profile key, so
			// there is nothing for a value template to reach into — and the
			// select platform does accept `value_template`, so the default
			// envelope encoding would project one matching no payload this
			// daemon publishes.
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID: uniqueID,
		nodeID:   nodeID,
		objectID: objectID,
	}

	comp, err := hadiscovery.RenderComponent(ctx, dev, entity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}
	// EntityJSON, not json.Marshal: the component keeps its platform so the
	// caller can name the topic segment, and Home Assistant declares the key
	// on no platform -- its extra=REMOVE_EXTRA schemas would drop it with no
	// error on the wire and no log line.
	buf, err := comp.EntityJSON()
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentSelect),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// PublishWeekProfileDiscovery publishes the HA Discovery payload for a
// week-profile `select` entity. The payload is retained and deduplicated
// through the same cache as all other discovery messages — identical bytes
// on a re-publish after reconnect produce zero broker traffic.
//
// No-ops when HA discovery is disabled on the bridge or when
// BuildWeekProfileDiscovery returns OK=false (non-climate channels,
// empty profile list).
func (b *Bridge) PublishWeekProfileDiscovery(ctx context.Context, centralName string, ev WeekProfileEvent) error {
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
	item := builder.BuildWeekProfileDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishWeekProfileState publishes the current active profile key
// (e.g. "P3") to the week-profile state topic with retain=true.
//
// An empty string currentProfile is published as an empty payload,
// which HA interprets as "no state". Retained.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishWeekProfileState(ctx context.Context, centralName, iface, address string, channel int, currentProfile string) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.WeekProfileState(centralName, iface, address, channel)
	return b.publishRawRetained(ctx, topic, []byte(currentProfile))
}
