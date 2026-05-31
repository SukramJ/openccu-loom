// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Light)(nil)
	_ payload.Slotted  = (*Light)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Light
// custom-DPs always surface as `light`.
func (l *Light) HAComponent() string { return "light" }

// TopicSlot returns the channels/<ch>/custom/light/ slot.
func (l *Light) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(l.Address())
	if !ok {
		deviceAddr = l.Address()
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "light",
	}
}
