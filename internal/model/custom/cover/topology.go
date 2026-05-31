// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Cover)(nil)
	_ payload.HAEntity = (*Blind)(nil)
	_ payload.HAEntity = (*Garage)(nil)
	_ payload.Slotted  = (*Cover)(nil)
	_ payload.Slotted  = (*Blind)(nil)
	_ payload.Slotted  = (*Garage)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Every
// cover variant surfaces as `cover` regardless of subtype — the HA
// platform handles position vs. position+tilt vs. door-state via
// the discovery payload's optional fields.
func (c *Cover) HAComponent() string { return "cover" }

// HAComponent — see [Cover.HAComponent].
func (b *Blind) HAComponent() string { return "cover" }

// HAComponent — see [Cover.HAComponent].
func (g *Garage) HAComponent() string { return "cover" }

// TopicSlot returns the channels/<ch>/custom/cover/ slot.
func (c *Cover) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(c.Address())
	if !ok {
		deviceAddr = c.Address()
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "cover"}
}

// TopicSlot returns the channels/<ch>/custom/blind/ slot.
func (b *Blind) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(b.Address())
	if !ok {
		deviceAddr = b.Address()
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "blind"}
}

// TopicSlot returns the channels/<ch>/custom/garage/ slot.
func (g *Garage) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(g.Address)
	if !ok {
		deviceAddr = g.Address
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "garage"}
}
