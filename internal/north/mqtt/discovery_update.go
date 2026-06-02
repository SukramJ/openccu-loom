// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
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
	// Update is the firmware-update source. Must be non-nil.
	Update payload.HADiscoveryPayloadBuilder
}

// updateDiscoveryCtx is the bridge-side [payload.HADiscoveryContext]
// wired for device-level update entities. It overrides
// AggregatedStateTopic to use the dedicated update state topic and
// ServiceMethodCommandTopic("install") to use the update install topic.
type updateDiscoveryCtx struct {
	topics  *TopicBuilder
	central string
	iface   string
	address string
}

func (c updateDiscoveryCtx) AggregatedStateTopic() string {
	// The update entity reuses the AggregatedStateTopic slot in the
	// HADiscoveryContext interface for its retained state topic
	// (`<addr>/update`). It has no CustomDP slot, so
	// CustomDPStateTopic mirrors the same value.
	return c.topics.DeviceUpdateState(c.central, c.iface, c.address)
}

func (c updateDiscoveryCtx) CustomDPStateTopic() string {
	return c.topics.DeviceUpdateState(c.central, c.iface, c.address)
}

func (c updateDiscoveryCtx) ServiceMethodCommandTopic(_ string) string {
	// For the update entity there is exactly one service method: "install".
	// The payload_install HA field triggers it; always return the install
	// command topic regardless of the method name argument.
	return c.topics.DeviceUpdateCommand(c.central, c.iface, c.address)
}

func (c updateDiscoveryCtx) WireParameterCommandTopic(parameter string) string {
	// Update entities do not reference per-parameter command topics.
	return c.topics.DataPointCommand(c.central, c.iface, c.address, 0, parameter)
}

func (c updateDiscoveryCtx) WireParameterStateTopic(parameter string) string {
	return c.topics.DataPointState(c.central, c.iface, c.address, 0, parameter)
}

// BuildUpdateDiscovery builds the HA Discovery `update` payload for one
// device's firmware-update entity.
//
// Returns DiscoveryItem{OK: false} when ev.Update is nil or JSON
// marshalling fails.
func (d *DefaultDiscoveryBuilder) BuildUpdateDiscovery(centralName string, ev UpdateEvent) DiscoveryItem {
	if ev.Update == nil {
		return DiscoveryItem{}
	}
	ctx := updateDiscoveryCtx{
		topics:  d.TopicBuilder,
		central: centralName,
		iface:   ev.Interface,
		address: ev.DeviceAddress,
	}
	comp, body := ev.Update.HADiscoveryPayload(ctx)
	if body == nil || comp == "" {
		return DiscoveryItem{}
	}

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	// object_id is unique per device — there is exactly one update entity per device.
	objectID := routingkey.CanonicalUniqueID(d.serialSuffix(centralName), ev.DeviceAddress, "update", "")

	// Compose the mock Event needed by deviceDescriptor / channelBaseBody.
	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		Device:        ev.Device,
	}

	// Overlay the shared HA-Discovery scaffolding fields that every entity
	// must carry: name, unique_id, object_id, availability, device, origin.
	// Mirrors channelBaseBody but without a channel-number postfix.
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

	// HA composes entity_id as `<device-slug>_<entity-name-slug>`,
	// so the entity `name` must NOT contain the device name again
	// (otherwise the slug stutters into
	// "update.alarmsirene_fl_alarmsirene_fl_firmware"). Keep the
	// name relative to the device and let HA prefix.
	base := map[string]any{
		"name":              "Firmware",
		"unique_id":         objectID,
		"object_id":         objectID,
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(mockEv, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}
	// Overlay base on body; existing body keys (platform-specific) win.
	for k, v := range base {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: comp,
		NodeID:    nodeID,
		ObjectID:  objectID,
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
	return b.publishDiscovery(ctx, item.Component, item.NodeID, item.ObjectID, item.Payload)
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
	return b.client.Publish(ctx, topic, body, b.cfg.QoS.State, true)
}
