// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// isPressButtonEvent reports whether ev is a click-event parameter the
// model classified as a clickable button entity: category=button AND
// usage=data_point. The reference stack's two-object model spawns BOTH a
// button (DpButton) and a keypress event for a writable press; the daemon
// carries one data point and signals the button surface through this
// category/usage pair. A writable press (a virtual-remote action, a plain
// KEY channel's PRESS_SHORT/PRESS_LONG, or an additional_data_points-
// promoted dimmer-input press) resolves to data_point; an event-only press
// (every KEY_TRANSCEIVER / MULTI_MODE_INPUT_TRANSMITTER transmitter, the
// central/long-press parameters) resolves to event and gets no button.
//
// The companion button is published IN ADDITION to the per-channel keypress
// `event` entity (which the regular [Build] press path emits): pressing the
// button from HA writes the action to the CCU, exactly as the keypress event
// observes a physical press.
func isPressButtonEvent(ev Event) bool {
	return ev.Category == hmenum.DataPointCategoryButton &&
		ev.Usage == hmenum.DataPointUsageDataPoint
}

// BuildPressButton emits the HA `button` discovery payload for one
// click-event parameter the model marked as a button (category=button,
// usage=data_point). The reference stack renders every such press as a
// clickable button (disabled by default) NEXT TO the per-channel keypress
// `event` entity; the regular [Build] press path only produces the event
// entity, so the bridge publishes this companion through
// [Bridge.publishPressButton].
//
// The button's command topic is the press parameter's per-DP command
// topic; HA publishes `payload_press` ("PRESS") which the command
// subscriber coerces to the boolean `true` an ACTION parameter expects.
//
// Returns the zero DiscoveryItem (OK=false) when ev is not a press-button
// parameter.
func (d *DefaultDiscoveryBuilder) BuildPressButton(ev Event) DiscoveryItem {
	if !isPressButtonEvent(ev) {
		return DiscoveryItem{}
	}
	pd := naming.NewDataPointPathData(
		hmenum.Interface(ev.Interface),
		ev.DeviceAddress,
		ev.ChannelNo,
		naming.Bucket(payload.BucketValues),
		ev.Parameter,
	)
	central := d.centralFor(ev)
	nodeID := pd.DiscoveryNodeID(central)
	objectID := pd.DiscoveryObjectID(ev.Parameter)
	uniqueID := routingkey.CanonicalUniqueID(d.serialSuffix(ev.Central), ev.DeviceAddress+":"+strconv.Itoa(ev.ChannelNo), ev.Parameter, "")
	commandTopic := pd.MQTTCommand(d.TopicBuilder.Base, central)
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
	body := map[string]any{
		"name":              pressButtonName(ev),
		"unique_id":         uniqueID,
		"command_topic":     commandTopic,
		"payload_press":     "PRESS",
		"availability":      availability,
		"availability_mode": "all",
		"device":            deviceDescriptor(ev, d.Hub.URL, d.SubDevicesEnabled),
		"origin":            BuildOriginInfo(),
	}
	// The button entity-description rules mirror the reference factory
	// defaults: PRESS_SHORT / PRESS_LONG buttons exist but are disabled
	// by default (the keypress event entity is the primary surface).
	applyEntityDescription(body, string(HAComponentButton), ev.Parameter, ev.Model, "", "")
	buf, err := json.Marshal(body)
	if err != nil {
		return DiscoveryItem{}
	}
	return DiscoveryItem{Component: string(HAComponentButton), NodeID: nodeID, ObjectID: objectID, Payload: buf, OK: true}
}

// pressButtonName composes the friendly_name fragment for a press button
// so it is unique across channels and centrals once HA prepends the device
// name.
//
// The plain parameter label ("Press Short") is identical for every press
// channel, so two devices that share a device/channel name collapse onto
// the same friendly_name and HA disambiguates with `_2` / `_3` entity-id
// suffixes. Mirror the reference stack's get_event_name: prefix the
// press-type label with the operator channel name when present, otherwise
// with ` ch<N>` so the channel number always disambiguates. The device name
// is prepended by HA.
func pressButtonName(ev Event) any {
	label, omitted := naming.EntityDisplayName(ev.descLabel(), ev.descLabelOmitted(), ev.Parameter)
	if omitted {
		// Primary-parameter omission would leave the button nameless and
		// collide on the device name alone — never omit for press buttons;
		// fall back to the title-cased parameter as the press-type label.
		label = naming.TitleCaseParameter(ev.Parameter)
	}
	prefix := ""
	if namer, ok := ev.Channel.(ChannelNamer); ok {
		if cn := namer.ChannelName(); cn != "" && !channelNameIsBareAddressNo(cn) {
			prefix = cn
		}
	}
	if prefix == "" && ev.ChannelNo > 0 {
		prefix = fmt.Sprintf("ch%d", ev.ChannelNo)
	}
	if prefix == "" {
		return label
	}
	return prefix + " " + label
}
