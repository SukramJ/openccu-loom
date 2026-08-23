// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Component is the HA component the projection maps onto ("number",
	// "sensor", "select", …). An empty Component declines discovery.
	Component string
	// Body carries the data-point-specific discovery keys. The builder
	// merges the shared frame around it and never overwrites a key the
	// projection set.
	Body map[string]any
}

// BuildCombinedDiscovery builds the HA Discovery payload for one combined
// data point by wrapping the projection's Body in the frame every
// combined entity shares.
//
// Returns DiscoveryItem{OK: false} when required fields are missing, the
// projection declined (empty Component or Body), or JSON marshalling
// fails.
func (d *DefaultDiscoveryBuilder) BuildCombinedDiscovery(centralName string, ev CombinedEvent) DiscoveryItem {
	if ev.Kind == "" || ev.DeviceAddress == "" || ev.Component == "" || len(ev.Body) == 0 {
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

	availability := []map[string]string{
		{
			"topic":                 d.TopicBuilder.BridgeStatus(),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 d.TopicBuilder.DeviceAvailability(centralName, ev.Interface, ev.DeviceAddress),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}

	// The frame first, the projection's keys second: a projection that
	// needs a different state_topic (or none) must be able to say so,
	// and silently discarding that would be the same class of bug the
	// seam exists to prevent.
	body := map[string]any{
		"unique_id":         objectID,
		"state_topic":       d.TopicBuilder.CombinedState(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind),
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(mockEv, d.hubURLFor(mockEv), d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}
	for k, v := range ev.Body {
		body[k] = v
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: ev.Component,
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
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
