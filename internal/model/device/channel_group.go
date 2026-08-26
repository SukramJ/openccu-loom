// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

// RebasedChannelGroupConfig holds the channel-group membership for one
// group of a multi-channel device after the group offset has been applied.
//
// GroupNumber is the group identifier (first channel number of the group,
// as assigned by AddChannelToGroup). ChannelNumbers is the sorted set of
// absolute channel numbers belonging to that group.
//
// This type is the Go equivalent of the profile-bound Python class of the
// same name, reduced to the pure membership fields that the device model
// tracks. North-bound adapters (MQTT sub-devices, REST topology) use it to
// split a device into per-group sub-device representations.
type RebasedChannelGroupConfig struct {
	GroupNumber    int
	ChannelNumbers []int
}
