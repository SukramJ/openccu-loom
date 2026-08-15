// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
func (d *DefaultDiscoveryBuilder) BuildCombinedTimerDiscovery(centralName string, ev CombinedTimerEvent) DiscoveryItem {
	if ev.Kind == "" || ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.CombinedState(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind)
	commandTopic := d.TopicBuilder.CombinedCommand(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("openccu-loom_%s_%d_%s",
		strings.ToLower(ev.DeviceAddress), ev.ChannelNo, ev.Kind)

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
		"device":            deviceDescriptor(mockEv, d.hubURLFor(mockEv), d.SubDevicesEnabled),
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
func (b *Bridge) PublishCombinedTimerDiscovery(ctx context.Context, centralName string, ev CombinedTimerEvent) error {
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
	item := builder.BuildCombinedTimerDiscovery(centralName, ev)
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
	centralName, iface, address string,
	channel int,
	kind string,
	seconds float64,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	if seconds < 0 {
		seconds = 0
	}
	topic := b.topics.CombinedState(centralName, iface, address, channel, kind)
	return b.publishRawRetained(ctx, topic, []byte(formatSeconds(seconds)))
}

// formatSeconds renders a seconds value as a decimal string with no
// trailing ".0" when the value is integral.
func formatSeconds(s float64) string {
	if s == float64(int64(s)) {
		return strconv.FormatInt(int64(s), 10)
	}
	return fmt.Sprintf("%g", s)
}

// CombinedSensorEvent carries the per-channel context the discovery builder
// needs to emit an HA `sensor` entity for a combined DP (LevelCombined or
// HSColor). The state payload is a JSON object; ValueTemplate extracts the
// primary scalar from it.
type CombinedSensorEvent struct {
	// Central is the CCU identifier (required for topic scoping).
	Central string
	// Interface is the CCU interface identifier.
	Interface string
	// DeviceAddress is the base device address.
	DeviceAddress string
	// ChannelNo is the channel number within the device.
	ChannelNo int
	// DeviceName is the human-readable device name.
	DeviceName string
	// Model is the CCU device model string.
	Model string
	// Device, when non-nil, is used by deviceDescriptor for the HA device block.
	Device any
	// Kind is the combined-DP kind ("level_combined", "hs_color"). Used as the
	// topic-segment and suffix on object_id / unique_id.
	Kind string
	// Label is the operator-facing entity name.
	Label string
	// ValueTemplate is the HA Jinja2 template that extracts the primary value
	// from the JSON state payload (e.g. "{{ value_json.level }}").
	ValueTemplate string
	// Unit is the engineering unit shown in HA. Optional.
	Unit string
}

// BuildCombinedSensorDiscovery builds the HA Discovery `sensor` payload for a
// combined DP that publishes a JSON object as its state.
func (d *DefaultDiscoveryBuilder) BuildCombinedSensorDiscovery(centralName string, ev CombinedSensorEvent) DiscoveryItem {
	if ev.Kind == "" || ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.CombinedState(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo, ev.Kind)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("openccu-loom_%s_%d_%s",
		strings.ToLower(ev.DeviceAddress), ev.ChannelNo, ev.Kind)

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

	body := map[string]any{
		"name":              ev.Label,
		"unique_id":         objectID,
		"state_topic":       stateTopic,
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(mockEv, d.hubURLFor(mockEv), d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
		"entity_category":   EntityCategoryDiagnostic,
	}
	if ev.ValueTemplate != "" {
		body["value_template"] = ev.ValueTemplate
	}
	if ev.Unit != "" {
		body["unit_of_measurement"] = ev.Unit
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentSensor),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// PublishCombinedSensorDiscovery publishes the HA Discovery payload for a
// combined-DP sensor entity. No-ops when HA discovery is disabled.
func (b *Bridge) PublishCombinedSensorDiscovery(ctx context.Context, centralName string, ev CombinedSensorEvent) error {
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
	item := builder.BuildCombinedSensorDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishCombinedSensorState publishes the current JSON state to the combined
// sensor's retained state topic.
func (b *Bridge) PublishCombinedSensorState(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	kind, jsonState string,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.CombinedState(centralName, iface, address, channel, kind)
	return b.publishRawRetained(ctx, topic, []byte(jsonState))
}
