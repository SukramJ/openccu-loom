// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
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

// BuildWeekProfileDiscovery builds the HA Discovery `select` payload for
// one climate channel's week-profile entity.
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
func (d *DefaultDiscoveryBuilder) BuildWeekProfileDiscovery(central string, ev WeekProfileEvent) DiscoveryItem {
	if ev.WP == nil {
		return DiscoveryItem{}
	}
	profiles := ev.WP.AvailableProfiles()
	if len(profiles) == 0 {
		return DiscoveryItem{}
	}

	stateTopic := d.TopicBuilder.WeekProfileState(central, ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	commandTopic := d.TopicBuilder.WeekProfileCommand(central, ev.Interface, ev.DeviceAddress, ev.ChannelNo)

	uniqueID := ev.WP.UniqueID()
	// Normalise the unique_id to lower-case and replace colons (which occur
	// in the "<central>:<addr>:WEEKPROFILE" form) with underscores so it is
	// safe as both a topic segment and an HA object_id.
	objectID := strings.NewReplacer(":", "_").Replace(strings.ToLower(uniqueID))
	// Prefix with "openccu-loom_" to match the pattern used by all other entities.
	if !strings.HasPrefix(objectID, "openccu-loom_") {
		objectID = "openccu-loom_" + objectID
	}

	nodeID := discoveryNodeID(central, ev.DeviceAddress)

	// Compose the Event-like value needed by deviceDescriptor + channelBaseBody.
	mockEv := Event{
		Central:       central,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		ChannelNo:     ev.ChannelNo,
		Device:        ev.Device,
	}

	body := map[string]any{
		"name":              "Week Profile",
		"unique_id":         objectID,
		"object_id":         objectID,
		"state_topic":       stateTopic,
		"command_topic":     commandTopic,
		"options":           profiles,
		"availability":      buildWeekProfileAvailability(d, central, ev),
		"availability_mode": "all",
		"device":            deviceDescriptor(mockEv, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}

	buf, err := json.Marshal(body)
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

// buildWeekProfileAvailability builds the two-entry availability list
// (bridge/status + per-device availability) that mirrors every other
// channel entity.
func buildWeekProfileAvailability(d *DefaultDiscoveryBuilder, central string, ev WeekProfileEvent) []map[string]string {
	return []map[string]string{
		{
			"topic":                 d.TopicBuilder.BridgeStatus(),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 d.TopicBuilder.DeviceAvailability(central, ev.Interface, ev.DeviceAddress),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
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
func (b *Bridge) PublishWeekProfileDiscovery(ctx context.Context, central string, ev WeekProfileEvent) error {
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
	item := builder.BuildWeekProfileDiscovery(central, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishWeekProfileState publishes the current active profile key
// (e.g. "P3") to the week-profile state topic with retain=true.
//
// An empty string currentProfile is published as an empty payload,
// which HA interprets as "no state". Retained.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishWeekProfileState(ctx context.Context, central, iface, address string, channel int, currentProfile string) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if central == "" {
		central = b.cfg.CentralName
	}
	topic := b.topics.WeekProfileState(central, iface, address, channel)
	return b.client.Publish(ctx, topic, []byte(currentProfile), b.cfg.QoS.State, true)
}
