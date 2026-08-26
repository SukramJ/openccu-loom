// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Switch)(nil)
	_ payload.Slotted  = (*Switch)(nil)
	_ payload.HAEntity = (*AccessPermission)(nil)
	_ payload.Slotted  = (*AccessPermission)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Switch
// custom-DPs always surface as `switch`.
func (s *Switch) HAComponent() string { return "switch" }

// TopicSlot returns the channels/<ch>/custom/switch/ slot.
func (s *Switch) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(s.Address())
	if !ok {
		deviceAddr = s.Address()
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "switch",
	}
}

// HAComponent reports the HA MQTT-Discovery component name. A per-user
// access permission is a switch to Home Assistant — granted / revoked.
func (a *AccessPermission) HAComponent() string { return "switch" }

// TopicSlot returns the channels/<ch>/custom/access_permission/ slot.
//
// Both wire data points behind this custom DP are invisible on their own
// — STATE is the permission read-back and ACCESS_AUTHORIZATION is forced
// to no_create — so this slot is the only place the permission is
// published. Without it the event bridge skips the DP at its
// [payload.Slotted] assertion and nothing about these channels ever
// reaches MQTT.
func (a *AccessPermission) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(a.Address)
	if !ok {
		deviceAddr = a.Address
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "access_permission",
	}
}
