// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// ScheduleEntityEvent carries the device + channel context the discovery
// builder needs to emit the Zeitplan-sensor HA entity — an HA sensor
// with rich json_attributes carrying the week-profile state.
type ScheduleEntityEvent struct {
	// Central is the CCU identifier (required for topic scoping).
	Central string
	// Interface is the CCU interface identifier (e.g. "HmIP-RF").
	Interface string
	// DeviceAddress is the base device address (without channel suffix).
	DeviceAddress string
	// ChannelNo is the schedule channel number on the device — the
	// channel that carries the WEEK_PROFILE / week_program data.
	ChannelNo int
	// DeviceName is the human-readable device name for the HA device block.
	DeviceName string
	// Model is the CCU device model string (e.g. "HmIP-MIO16-PCB").
	Model string
	// Device, when non-nil, is consulted by deviceDescriptor for the
	// `payload:"info"` map — same as Event.Device.
	Device any
}

// BuildScheduleEntityDiscovery builds the HA Discovery `sensor` payload
// for a device's Zeitplan entity. The native state is the count of
// active schedule entries; the rich schedule structure is exposed via
// json_attributes_topic.
//
// The entity lives on a **sub-device** "<device-name> Zeitplan" linked
// to the parent device via HA's `via_device` mechanism so each
// schedule surface gets its own HA device card.
func (d *DefaultDiscoveryBuilder) BuildScheduleEntityDiscovery(centralName string, ev ScheduleEntityEvent) DiscoveryItem {
	if ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.ScheduleEntityState(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo)
	attrsTopic := d.TopicBuilder.ScheduleEntityAttrs(centralName, ev.Interface, ev.DeviceAddress, ev.ChannelNo)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("openccu-loom_%s_%d_schedule",
		strings.ToLower(ev.DeviceAddress), ev.ChannelNo)

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
		"name":                     d.tr("discovery.schedule"),
		"unique_id":                objectID,
		"state_topic":              stateTopic,
		"json_attributes_topic":    attrsTopic,
		"json_attributes_template": "{{ value_json | tojson }}",
		"icon":                     "mdi:calendar-clock",
		"availability":             availability,
		"availability_mode":        "all",
		"device":                   scheduleSubDeviceDescriptor(mockEv, d.hubURLFor(mockEv), d.tr("discovery.schedule")),
		"origin":                   BuildOriginInfo(),
		"entity_category":          EntityCategoryDiagnostic,
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

// PublishScheduleEntityDiscovery publishes the Zeitplan-sensor HA
// Discovery payload. Retained and deduplicated through the shared
// discovery cache.
//
// No-ops when HA discovery is disabled.
func (b *Bridge) PublishScheduleEntityDiscovery(ctx context.Context, centralName string, ev ScheduleEntityEvent) error {
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
	item := builder.BuildScheduleEntityDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// PublishScheduleEntityState publishes the active-entry count to the
// Zeitplan sensor's state topic.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishScheduleEntityState(
	ctx context.Context,
	centralName, iface, address string,
	channel, count int,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	topic := b.topics.ScheduleEntityState(centralName, iface, address, channel)
	return b.publishRawRetained(ctx, topic, fmt.Appendf(nil, "%d", count))
}

// PublishScheduleEntityAttrs publishes the rich schedule structure to
// the Zeitplan sensor's json_attributes topic. attrs is JSON-marshalled
// verbatim — callers populate it from
// [weekprofile.ProfileDataPoint] state.
//
// No-ops when the raw plane is disabled.
func (b *Bridge) PublishScheduleEntityAttrs(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	attrs map[string]any,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	if attrs == nil {
		attrs = map[string]any{}
	}
	body, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	topic := b.topics.ScheduleEntityAttrs(centralName, iface, address, channel)
	return b.publishRawRetained(ctx, topic, body)
}

// ScheduleSwitchEvent carries the context the discovery builder needs
// to emit one HA `switch` entity per ScheduleChannelSwitch on a
// device.
type ScheduleSwitchEvent struct {
	Central       string
	Interface     string
	DeviceAddress string
	// ScheduleChannelNo is the channel that hosts the WeekProfile /
	// COMBINED_PARAMETER write target.
	ScheduleChannelNo int
	DeviceName        string
	Model             string
	Device            any
	// Key is the channel key ("<actor>_<sub>", e.g. "1_1").
	Key string
	// TargetChannelNo is the receiver channel this switch governs.
	TargetChannelNo int
	// Label is the operator-facing entity name (e.g. "Zeitplan Kanal 18").
	Label string
}

// BuildScheduleSwitchDiscovery builds the HA Discovery `switch` payload
// for one ScheduleChannelSwitch. Each schedule device emits N switches
// (one per available target channel); HA renders them as a row of
// toggles for enabling / disabling the schedule per receiver.
func (d *DefaultDiscoveryBuilder) BuildScheduleSwitchDiscovery(centralName string, ev ScheduleSwitchEvent) DiscoveryItem {
	if ev.Key == "" || ev.DeviceAddress == "" {
		return DiscoveryItem{}
	}
	stateTopic := d.TopicBuilder.ScheduleSwitchState(centralName, ev.Interface, ev.DeviceAddress, ev.ScheduleChannelNo, ev.Key)
	commandTopic := d.TopicBuilder.ScheduleSwitchCommand(centralName, ev.Interface, ev.DeviceAddress, ev.ScheduleChannelNo, ev.Key)

	nodeID := discoveryNodeID(centralName, ev.DeviceAddress)
	objectID := fmt.Sprintf("openccu-loom_%s_%d_schedule_%s",
		strings.ToLower(ev.DeviceAddress), ev.ScheduleChannelNo, ev.Key)

	mockEv := Event{
		Central:       centralName,
		Interface:     ev.Interface,
		DeviceAddress: ev.DeviceAddress,
		DeviceName:    ev.DeviceName,
		Model:         ev.Model,
		ChannelNo:     ev.ScheduleChannelNo,
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
		"command_topic":     commandTopic,
		"payload_on":        "true",
		"payload_off":       "false",
		"state_on":          "true",
		"state_off":         "false",
		"icon":              "mdi:calendar-check",
		"availability":      availability,
		"availability_mode": "all",
		"device":            scheduleSubDeviceDescriptor(mockEv, d.hubURLFor(mockEv), d.tr("discovery.schedule")),
		"origin":            BuildOriginInfo(),
		"entity_category":   EntityCategoryConfig,
		"optimistic":        false,
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{
		Component: string(HAComponentSwitch),
		NodeID:    nodeID,
		ObjectID:  objectID,
		Payload:   buf,
		OK:        true,
	}
}

// PublishScheduleSwitchDiscovery publishes the HA Discovery payload for
// one ScheduleChannelSwitch entity. Retained + deduplicated.
func (b *Bridge) PublishScheduleSwitchDiscovery(ctx context.Context, centralName string, ev ScheduleSwitchEvent) error {
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
	item := builder.BuildScheduleSwitchDiscovery(centralName, ev)
	if !item.OK {
		return nil
	}
	return b.publishDiscovery(ctx, centralName, item.Component, item.NodeID, item.ObjectID, item.Payload)
}

// scheduleSubDeviceDescriptor builds the HA `device` block for the
// schedule sub-device: the Zeitplan sensor + ScheduleChannelSwitches
// all land on a dedicated HA device card "<parent-name> Zeitplan",
// `via_device`-linked to the parent. This keeps the parent device card
// uncluttered (sensors / switches that control the real outputs stay
// there; schedule administration lives in its own card).
//
// Identifiers diverge from the parent (`openccu-loom_<addr>_schedule`
// vs. `openccu-loom_<addr>`), so HA creates a separate device. The
// parent device is referenced via the `via_device` field so HA renders
// the schedule sub-device as a child entry under the parent in the
// Devices view.
//
// Manufacturer / model / model_id / sw_version are copied from the
// parent device descriptor so the schedule card carries the same
// hardware identity as the parent — HA shows them as related units.
// suggested_area is also inherited so the sub-device falls into the
// same room.
func scheduleSubDeviceDescriptor(ev Event, hubURL, scheduleLabel string) map[string]any {
	parentID := "openccu-loom_" + strings.ToLower(ev.DeviceAddress)
	subID := parentID + "_schedule"
	parentName := ev.DeviceName
	if parentName == "" {
		parentName = ev.DeviceAddress
	}
	desc := map[string]any{
		"identifiers":  []string{subID},
		"name":         parentName + " " + scheduleLabel,
		"manufacturer": "eQ-3",
		"via_device":   parentID,
	}
	if hubURL != "" {
		desc["configuration_url"] = hubURL
	}
	// Pull the parent's model / sw_version / serial / area info from
	// the device-info `payload:"info"` map so the schedule card carries
	// the same hardware identity. The fields are HA-whitelisted via
	// haDeviceFields.
	if ev.Device != nil {
		info := payload.ForWith(ev.Device, payload.KindInfo, payload.Options{UseAltNames: true})
		for k, v := range info {
			if _, ok := haDeviceFields[k]; !ok {
				continue
			}
			// `name` is reserved for the sub-device label.
			if k == "name" {
				continue
			}
			desc[k] = v
		}
		// suggested_area fallback: copy the parent's room if present
		// and not already set by the info map. The parent device
		// resolves the singular room behind its own lock, so it is
		// asked rather than reflected over (see [deviceWithRoom]).
		if _, has := desc["suggested_area"]; !has {
			if dwr, ok := ev.Device.(deviceWithRoom); ok && dwr.Room() != "" {
				desc["suggested_area"] = dwr.Room()
			}
		}
	}
	return desc
}

// PublishScheduleSwitchState publishes the boolean state of one
// ScheduleChannelSwitch (true=enabled, false=disabled). Retained.
func (b *Bridge) PublishScheduleSwitchState(
	ctx context.Context,
	centralName, iface, address string,
	channel int,
	key string,
	enabled bool,
) error {
	if !b.cfg.RawEnabled {
		return nil
	}
	if centralName == "" {
		centralName = b.cfg.CentralName
	}
	msg := []byte("false")
	if enabled {
		msg = []byte("true")
	}
	topic := b.topics.ScheduleSwitchState(centralName, iface, address, channel, key)
	return b.publishRawRetained(ctx, topic, msg)
}
