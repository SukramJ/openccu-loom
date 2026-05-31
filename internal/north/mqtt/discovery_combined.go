// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CombinedTimerEvent carries the per-channel context the discovery builder
// needs to emit an HA `number` entity for a combined Timer DP.
type CombinedTimerEvent struct {
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
	// Kind is the combined-DP kind (e.g. "duration"). Used both as the
	// topic-segment and the suffix on object_id / unique_id.
	Kind string
	// Label is the operator-facing entity name (e.g. "Zeitdauer"). HA
	// derives entity_id from `device.name` + this label.
	Label string
	// Unit is the engineering unit shown in HA (e.g. "s" for seconds).
	// Optional.
	Unit string
	// Min / Max bound the input range. MaxSeconds=0 falls back to no
	// constraint.
	MinSeconds float64
	MaxSeconds float64
	// Step is the HA `step` value — 1 for integer seconds.
	Step float64
}

// BuildCombinedTimerDiscovery builds the HA Discovery `number` payload
// for a combined Timer DP (DURATION_VALUE + DURATION_UNIT → seconds).
//
// Returns DiscoveryItem{OK: false} when required fields are missing or
// JSON marshalling fails.
func (d *DefaultDiscoveryBuilder) BuildCombinedTimerDiscovery(central string, ev CombinedTimerEvent) DiscoveryItem {
	if ev.Kind == "" || ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.CombinedState(central, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind)
	commandTopic := d.TopicBuilder.CombinedCommand(central, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind)

	nodeID := discoveryNodeID(central, ev.DeviceAddress)
	objectID := fmt.Sprintf("openccu-loom_%s_%d_%s",
		strings.ToLower(ev.DeviceAddress), ev.ChannelNo, ev.Kind)

	mockEv := Event{
		Central:       central,
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
			"topic":                 d.TopicBuilder.DeviceAvailability(central, ev.Interface, ev.DeviceAddress),
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}

	step := ev.Step
	if step <= 0 {
		step = 1
	}
	body := map[string]any{
		"name":              ev.Label,
		"unique_id":         objectID,
		"state_topic":       stateTopic,
		"command_topic":     commandTopic,
		"min":               ev.MinSeconds,
		"step":              step,
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(mockEv, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
		"entity_category":   EntityCategoryConfig,
		"mode":              "box",
		"optimistic":        false,
	}
	if ev.MaxSeconds > 0 {
		body["max"] = ev.MaxSeconds
	}
	if ev.Unit != "" {
		body["unit_of_measurement"] = ev.Unit
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentNumber),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// PublishCombinedTimerDiscovery publishes the HA Discovery payload for a
// combined Timer `number` entity. Retained and deduplicated through the
// shared discovery cache.
//
// No-ops when HA discovery is disabled or the builder declines the event.
func (b *Bridge) PublishCombinedTimerDiscovery(ctx context.Context, central string, ev CombinedTimerEvent) error {
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
	item := builder.BuildCombinedTimerDiscovery(central, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishCombinedTimerState publishes the current seconds value to the
// combined Timer's retained state topic. seconds < 0 is clamped to 0.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishCombinedTimerState(
	ctx context.Context,
	central, iface, address string,
	channel int,
	kind string,
	seconds float64,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if central == "" {
		central = b.cfg.CentralName
	}
	if seconds < 0 {
		seconds = 0
	}
	topic := b.topics.CombinedState(central, iface, address, channel, kind)
	payload := []byte(formatSeconds(seconds))
	return b.client.Publish(ctx, topic, payload, b.cfg.QoS.State, true)
}

// formatSeconds renders a seconds value as a decimal string with no
// trailing ".0" when the value is integral.
func formatSeconds(s float64) string {
	if s == float64(int64(s)) {
		return fmt.Sprintf("%d", int64(s))
	}
	return fmt.Sprintf("%g", s)
}
