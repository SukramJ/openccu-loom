// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*TextDisplay)(nil)
	_ payload.Slotted  = (*TextDisplay)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. The
// HmIP-WRCD text display surfaces as HA's `text` platform.
func (t *TextDisplay) HAComponent() string { return "text" }

// TopicSlot returns the channels/<ch>/custom/text_display/ slot.
func (t *TextDisplay) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(t.Address)
	if !ok {
		deviceAddr = t.Address
		channel = 0
	}
	return payload.TopicSlot{
		Address:   deviceAddr,
		Channel:   channel,
		Bucket:    payload.BucketCustom,
		Parameter: "text_display",
	}
}
