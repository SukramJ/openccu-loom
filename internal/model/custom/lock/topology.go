// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Lock)(nil)
	_ payload.Slotted  = (*Lock)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Lock
// custom-DPs always surface as `lock` regardless of variant.
func (l *Lock) HAComponent() string { return "lock" }

// TopicSlot returns the channels/<ch>/custom/lock/ slot.
func (l *Lock) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(l.Address)
	if !ok {
		deviceAddr = l.Address
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "lock",
	}
}
