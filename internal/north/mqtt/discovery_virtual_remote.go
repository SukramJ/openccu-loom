// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
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
		"name":              entityName(ev),
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
