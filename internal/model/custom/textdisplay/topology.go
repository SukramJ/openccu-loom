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

// HAComponent reports the HA MQTT-Discovery component name — the platform
// the HmIP-WRCD's state topic belongs to, which is HA's `text`.
//
// No `text` entity is discovered for it. The display's only Home Assistant
// surface is the bridge's notify companion, and this source publishes no
// discovery entity of its own; see the note on the [payload.Source]
// assertion in payload.go.
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
