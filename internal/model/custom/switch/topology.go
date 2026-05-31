// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Switch)(nil)
	_ payload.Slotted  = (*Switch)(nil)
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
