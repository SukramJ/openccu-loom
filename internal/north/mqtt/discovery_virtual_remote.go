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

// virtualRemoteDevice is the narrow read-side contract the discovery
// builder uses to recognise the CCU's virtual-remote pseudo-devices
// (HM-RCV-50 / HMW-RCV-50 / HmIP-RCV-50). `*device.Device` satisfies it
// via [Device.IsVirtualRemote]; defining it locally keeps the mqtt
// package free of the model import.
type virtualRemoteDevice interface {
	IsVirtualRemote() bool
}

// isVirtualRemotePressEvent reports whether ev is a PRESS_* parameter
// on a virtual-remote channel. Virtual-remote press parameters are
// writable fire-and-forget actions (pressing them from HA triggers the
// CCU-side key event); on physical devices the same parameters are
// pure event emitters and never get a button surface.
func isVirtualRemotePressEvent(ev Event) bool {
	if !isPressParameter(ev.Parameter) {
		return false
	}
	vr, ok := ev.Device.(virtualRemoteDevice)
	return ok && vr.IsVirtualRemote()
}

// BuildVirtualRemoteButton emits the HA `button` discovery payload for
// one virtual-remote press parameter. The reference stack renders every
// virtual-remote channel as TWO clickable buttons (press_short,
// press_long — both disabled by default) NEXT TO the per-channel
// keypress `event` entity; the regular [Build] press path only produces
// the aggregated event entity, so the bridge publishes this companion
// through [Bridge.publishVirtualRemotePressButton].
//
// The button's command topic is the press parameter's per-DP command
// topic; HA publishes `payload_press` ("PRESS") which the command
// subscriber coerces to the boolean `true` an ACTION parameter expects.
//
// Returns the zero DiscoveryItem (OK=false) when ev is not a
// virtual-remote press parameter.
func (d *DefaultDiscoveryBuilder) BuildVirtualRemoteButton(ev Event) DiscoveryItem {
	if !isVirtualRemotePressEvent(ev) {
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
		"name":              virtualRemoteButtonName(ev),
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

// virtualRemoteButtonName composes the friendly_name fragment for a
// virtual-remote press button so it is unique across channels and
// centrals once HA prepends the device name.
//
// The plain parameter label ("Press Short") is identical for every VR
// channel, so two virtual remotes that share a device/channel name (the
// Otto-Rem and Kearney-Loc HmIP-RCV both carry operator names like
// "Arbeitszimmer Markus …") collapse onto the same friendly_name and HA
// disambiguates with `_2` / `_3` entity-id suffixes. Mirror the
// reference stack's get_event_name: prefix the press-type label with the
// operator channel name when present, otherwise with ` ch<N>` so the
// channel number always disambiguates. The device name (which carries
// the central identity for VR pseudo-devices) is prepended by HA.
func virtualRemoteButtonName(ev Event) any {
	label, omitted := naming.EntityDisplayName(ev.descLabel(), ev.descLabelOmitted(), ev.Parameter)
	if omitted {
		// Primary-parameter omission would leave the button nameless and
		// collide on the device name alone — never omit for VR buttons;
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
