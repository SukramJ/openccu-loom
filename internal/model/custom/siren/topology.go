// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var (
	_ payload.HAEntity = (*Siren)(nil)
	_ payload.HAEntity = (*SmokeSiren)(nil)
	_ payload.HAEntity = (*SoundPlayer)(nil)
	_ payload.Slotted  = (*Siren)(nil)
	_ payload.Slotted  = (*SmokeSiren)(nil)
	_ payload.Slotted  = (*SoundPlayer)(nil)
)

// HAComponent reports the HA MQTT-Discovery component name. Every
// siren-domain custom-DP surfaces as `siren` — including SmokeSiren
// (HmIP-SWSD) which can be triggered via SMOKE_DETECTOR_COMMAND
func (s *Siren) HAComponent() string { return "siren" }

// HAComponent — see [Siren.HAComponent].
func (s *SmokeSiren) HAComponent() string { return "siren" }

// HAComponent — see [Siren.HAComponent].
func (sp *SoundPlayer) HAComponent() string { return "siren" }

// TopicSlot returns the channels/<ch>/custom/siren/ slot.
func (s *Siren) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(s.Address)
	if !ok {
		deviceAddr = s.Address
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "siren"}
}

// TopicSlot returns the channels/<ch>/custom/smoke_siren/ slot.
func (s *SmokeSiren) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(s.Address)
	if !ok {
		deviceAddr = s.Address
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "smoke_siren"}
}

// TopicSlot returns the channels/<ch>/custom/sound_player/ slot.
func (sp *SoundPlayer) TopicSlot() payload.TopicSlot {
	deviceAddr, channel, ok := hmtypes.SplitChannelAddress(sp.Address)
	if !ok {
		deviceAddr = sp.Address
		channel = 0
	}
	return payload.TopicSlot{Address: deviceAddr, Channel: channel, Bucket: payload.BucketCustom, Parameter: "sound_player"}
}
