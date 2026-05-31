// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Irrigation)(nil)
	_ payload.HAEntity = (*Modulating)(nil)
	_ payload.Slotted  = (*Irrigation)(nil)
	_ payload.Slotted  = (*Modulating)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Both
// valve variants surface as `valve` — the platform handles the
// binary vs. modulating distinction via `reports_position`.
func (v *Irrigation) HAComponent() string { return "valve" }

// HAComponent — see [Irrigation.HAComponent].
func (v *Modulating) HAComponent() string { return "valve" }

// TopicSlot returns the channels/<ch>/custom/valve_irrigation/ slot.
func (v *Irrigation) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(v.Address())
	if !ok {
		deviceAddr = v.Address()
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "valve_irrigation"}
}

// TopicSlot returns the channels/<ch>/custom/valve_modulating/ slot.
func (v *Modulating) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(v.Address())
	if !ok {
		deviceAddr = v.Address()
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "valve_modulating"}
}
