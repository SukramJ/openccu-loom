// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmDevWGTGroupDevice builds the two-profile channel-group overlap that
// HmIP-WGT produces: a dimmer profile whose base channel is 2 claims
// channels 1-4, a switch profile whose base channel is 4 claims channels
// 3-6, and the dimmer runs first because the profile registry sorts by
// category ("light" before "switch").
//
// It returns the device and the two channels both profiles claim.
func hmDevWGTGroupDevice(t *testing.T) (dev *Device, shared []*Channel) {
	t.Helper()
	dev = New(Config{InterfaceID: "HmIP-RF", Address: "WGT0001", Model: "HmIP-WGT"})
	channels := make(map[int]*Channel, 6)
	for no := 1; no <= 6; no++ {
		channels[no] = dev.AddChannel(
			dev.Address+":"+string(rune('0'+no)), no, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues,
		)
	}
	// Profile 1 (dimmer, group 2) claims 1-4, profile 2 (switch, group 4)
	// claims 3-6 — the order internal/model/custom/materialize.go applies
	// them in, one AddChannelToGroup plus one AssignGroupNumber per channel.
	for _, claim := range []struct {
		groupNo  int
		channels []int
	}{
		{groupNo: 2, channels: []int{1, 2, 3, 4}},
		{groupNo: 4, channels: []int{3, 4, 5, 6}},
	} {
		for _, no := range claim.channels {
			dev.AddChannelToGroup(claim.groupNo, no)
			channels[no].AssignGroupNumber(claim.groupNo)
		}
	}
	return dev, []*Channel{channels[3], channels[4]}
}

// TestHmDevChannelGroupTieBreakIsFirstWinsOnBothStores pins that the
// device-level reverse map and the channel's own group number answer the
// same question the same way.
//
// [Channel.AssignGroupNumber] documents the tie-break: the first non-zero
// assignment wins, so a channel two profiles claim keeps the group of
// whichever profile claimed it first, independent of iteration order.
// [Device.AddChannelToGroup] maintains a second store of that same fact,
// and it must not apply the opposite rule — a device-level lookup that
// answers 4 where the channel answers 2 re-points the sub-device split for
// whichever caller reaches for it first.
func TestHmDevChannelGroupTieBreakIsFirstWinsOnBothStores(t *testing.T) {
	dev, shared := hmDevWGTGroupDevice(t)

	for _, ch := range shared {
		channelSide := ch.GroupNumber()
		deviceSide := dev.GetChannelGroupNo(ch.Number)
		if channelSide != deviceSide {
			t.Errorf("channel %d: Channel.GroupNumber() = %d but Device.GetChannelGroupNo() = %d — "+
				"the two stores of the channel-group assignment disagree",
				ch.Number, channelSide, deviceSide)
		}
		if channelSide != 2 {
			t.Errorf("channel %d: Channel.GroupNumber() = %d, want 2 (first claim wins)", ch.Number, channelSide)
		}
	}
}

// TestHmDevChannelGroupSeedDoesNotOverwriteAnEarlierClaim covers the
// group-number-maps-to-itself seed in [Device.AddChannelToGroup]: channel 4
// is claimed by group 2 first, so the later group 4 must not re-point
// channel 4 at itself.
func TestHmDevChannelGroupSeedDoesNotOverwriteAnEarlierClaim(t *testing.T) {
	dev, _ := hmDevWGTGroupDevice(t)

	if got := dev.GetChannelGroupNo(4); got != 2 {
		t.Errorf("Device.GetChannelGroupNo(4) = %d, want 2 (channel 4 was claimed by group 2 first)", got)
	}
	// The forward map keeps both memberships — a channel really is a member
	// of both groups; only the single-answer reverse lookup has to pick one.
	if got := dev.GroupChannels(2); len(got) != 4 {
		t.Errorf("GroupChannels(2) = %v, want four members", got)
	}
	if got := dev.GroupChannels(4); len(got) != 4 {
		t.Errorf("GroupChannels(4) = %v, want four members", got)
	}
}
