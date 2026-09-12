// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"fmt"
	"strconv"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"
)

// CombinedEvent carries the per-channel context needed to emit one HA
// entity for a combined data point.
//
// Component and Body come from the data point's own
// [payload.CombinedProjection]; everything else identifies the channel.
// The split is deliberate: the model layer knows what the entity *is*
// and the bridge knows *where* it lives, and neither has to learn the
// other's half. Before the projection seam existed there was one event
// type and one builder per combined kind, so a new kind that nobody
// remembered to add a builder for published nothing at all.
// loom:reachable:reason="constructed in EventBridge.publishCombinedProjection and passed to Bridge.PublishCombinedDiscovery on every combined data point the model carries; reached only as a composite literal at that call site"
type CombinedEvent struct {
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
	// Model is the CCU device model string (e.g. "HmIP-ASIR").
	Model string
	// Device, when non-nil, is consulted by deviceDescriptor for the
	// `payload:"info"` map — same as Event.Device.
	Device any
	// Kind is the combined-DP kind ("duration", "hs_color", …). Used both
	// as the topic segment and as the suffix on object_id / unique_id.
	Kind string
	// Component is what the projection produced: the platform it maps onto
	// plus its own discovery keys, typed. A component with no platform
	// declines discovery.
	//
	// The builder wraps the shared frame around it and never overwrites a
	// field the projection set.
	Component hadiscovery.Component
}

// combinedTopicLayout renders this plane's topics through this daemon's own
// [TopicBuilder], so the render pipeline produces exactly the strings
// already retained on the broker rather than a second spelling of them.
//
// The slot arguments are unused. One call frames one combined data point,
// whose channel and kind the event already pins; deriving them from the
// slot again would be a second implementation of the same schema with
// nothing keeping the two in step.
type combinedTopicLayout struct {
	d       *DefaultDiscoveryBuilder
	ev      CombinedEvent
	central string
}

// State implements the shared model's topic layout.
func (l combinedTopicLayout) State(hamodel.Slot) string {
	return l.d.TopicBuilder.CombinedState(
		l.central, l.ev.Interface, l.ev.DeviceAddress, l.ev.ChannelNo, l.ev.Kind,
	)
}

// Command implements the shared model's topic layout with the empty string.
// A combined data point's command topic — where it has one — is the
// projection's to name: only the model layer knows whether the entity is
// writable at all, and the frame has never published one.
func (l combinedTopicLayout) Command(hamodel.Slot) string { return "" }

// Availability implements the shared model's topic layout: the per-device
// availability topic, which is the second of the two entries every channel
// entity on this daemon carries.
func (l combinedTopicLayout) Availability(hamodel.Slot) string {
	return l.d.TopicBuilder.DeviceAvailability(l.central, l.ev.Interface, l.ev.DeviceAddress)
}

// Bridge implements the shared model's topic layout.
func (l combinedTopicLayout) Bridge() string { return l.d.TopicBuilder.BridgeStatus() }

// combinedDiscoveryContext is the render context for this plane: the
// standard one with this daemon's identity strings substituted.
//
// The unique id and the node id are derived two different ways from the
// same address — [discoveryNodeID] slugs the central and lower-cases the
// address, [physicalDeviceIdentifier] prefixes "openccu-loom_" and folds
// the central in only for an address family that repeats across CCUs — so
// no single derivation produces both.
type combinedDiscoveryContext struct {
	hadiscovery.StdContext

	uniqueID string
	nodeID   string
}

// UniqueID implements [hadiscovery.Context] with the id this daemon already
// publishes. A projection that filled its own wins over it — see
// [DefaultDiscoveryBuilder.BuildCombinedDiscovery].
func (c combinedDiscoveryContext) UniqueID(*hamodel.Device, hamodel.Entity) string {
	return c.uniqueID
}

// NodeID implements [hadiscovery.Context].
func (c combinedDiscoveryContext) NodeID(*hamodel.Device) string { return c.nodeID }

// ObjectID implements [hadiscovery.Context] with the empty string, which
// suppresses `default_entity_id`. This is the one plane of the daemon that
// has never published an entity-id seed: Home Assistant derives the entity
// id from the device name and the projection's `name` instead, and adding a
// seed now would rename every combined entity at once with nothing
// downstream able to undo it.
func (c combinedDiscoveryContext) ObjectID(*hamodel.Device, hamodel.Entity) string { return "" }

// BuildCombinedDiscovery builds the HA Discovery payload for one combined
// data point by wrapping the projection's Component in the frame every
// combined entity shares.
//
// The frame is rendered by the shared model's per-entity discovery form
// ([hadiscovery.RenderComponent]) from an entity that carries nothing but
// its platform and a read binding: the device and origin blocks, the state
// topic, the two-source availability list and the unique id all fall out of
// the pipeline rather than being stamped on here.
//
// The projection's keys still come FIRST and the frame only fills the gaps.
// A projection that needs a different state_topic (or none) must be able to
// say so, and silently discarding that would be the same class of bug the
// seam exists to prevent.
//
// Returns DiscoveryItem{OK: false} when required fields are missing, the
// projection declined (empty Component or Body), or the render fails.
func (d *DefaultDiscoveryBuilder) BuildCombinedDiscovery(centralName string, ev CombinedEvent) DiscoveryItem {
	if ev.Kind == "" || ev.DeviceAddress == "" || ev.Component.Platform == "" {
		return DiscoveryItem{}
	}
	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("%s_%d_%s",
		physicalDeviceIdentifier(centralName, ev.DeviceAddress), ev.ChannelNo, ev.Kind)

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

	// The frame entity describes nothing: every display key of a combined
	// entity — its name, its value template, its options, its bounds — is
	// the projection's, and a description that repeated any of them would
	// be a second source for a string the model layer already owns.
	frameEntity := &hamodel.Basic{
		EntityKey:      ev.Kind,
		EntityPlatform: ev.Component.Platform,
		Binds: []hamodel.Binding{
			{
				Role: hamodel.RoleState,
				Mode: hamodel.Read,
				Slot: hamodel.S(dev.UID(), strconv.Itoa(ev.ChannelNo), hamodel.BucketCustom, ev.Kind).
					In(centralName, ev.Interface),
			},
		},
	}
	ctx := combinedDiscoveryContext{
		StdContext: hadiscovery.StdContext{
			Layout: combinedTopicLayout{d: d, ev: ev, central: centralName},
			Lang:   d.Locale,
			// A combined state topic carries the projection's own JSON
			// document, read by the template the projection wrote, not the
			// `{"value":…}` envelope the datapoint planes publish — so the
			// frame must contribute no value template of its own.
			Enc:        hadiscovery.RawEncoding,
			Translator: d.tr,
		},
		uniqueID: objectID,
		nodeID:   nodeID,
	}
	frame, err := hadiscovery.RenderComponent(ctx, dev, frameEntity, *BuildOriginInfo())
	if err != nil {
		return DiscoveryItem{}
	}

	comp := ev.Component
	if comp.UniqueID == "" {
		comp.UniqueID = frame.UniqueID
	}
	if comp.StateTopic == "" {
		comp.StateTopic = frame.StateTopic
	}
	if len(comp.Availability) == 0 {
		comp.Availability = frame.Availability
	}
	if comp.AvailabilityMode == "" {
		comp.AvailabilityMode = frame.AvailabilityMode
	}
	if comp.Device == nil {
		comp.Device = frame.Device
	}
	if comp.Origin == nil {
		comp.Origin = frame.Origin
	}
	return discoveryItemFor(comp, nodeID, objectID)
}

// PublishCombinedDiscovery publishes the HA Discovery payload for one
// combined data point. Retained and deduplicated through the shared
// discovery cache.
//
// No-ops when HA discovery is disabled or the builder declines the event.
func (b *Bridge) PublishCombinedDiscovery(ctx context.Context, centralName string, ev CombinedEvent) error {
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
	item := builder.BuildCombinedDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishCombinedState publishes a combined data point's rendered state
// to its retained state topic.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishCombinedState(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	kind, state string,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.CombinedState(centralName, iface, address, channel, kind)
	return b.publishRawRetained(ctx, topic, []byte(state))
}
