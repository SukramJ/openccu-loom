// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// ScheduleTargetChannel is one member channel of a schedule-relevant
// channel group. Primary marks the group's primary channel (sub-index
// 1 in the `<actor>_<sub>` key enumeration); every other member is a
// secondary/virtual receiver.
type ScheduleTargetChannel struct {
	ChannelNo int
	Primary   bool
}

// ScheduleTargetGroup is one schedule-relevant channel group of a
// device. Channels is ordered for sub-index enumeration: primary
// first, then the secondary channels in profile-schema order.
type ScheduleTargetGroup struct {
	GroupNo  int
	Channels []ScheduleTargetChannel
}

// ScheduleRelevantChannelGroups returns the device's custom-DP channel
// groups that participate in the non-climate week-schedule target
// channel map, ordered by ascending group number.
//
// Mirrors the reference stack's `_get_schedule_relevant_channel_groups`
// plus the per-group channel enumeration of `_build_target_channel_map`:
//
//   - Every channel that carries a custom data point contributes its
//     group (keyed by the channel's group number) with the profile's rebased
//     channel-group schema: primary channel first, then the secondary
//     channels.
//   - When at least one custom DP's profile declares an explicit
//     ScheduleChannelNo, only the groups of those profiles count. This
//     filters out CDPs the schedule does not control (e.g. the
//     button-lock CDP on multi-config devices like HmIP-DLP).
//   - Devices without any custom DP yield nil — the reference stack
//     only builds a non-climate week profile from a custom DP, so a
//     CDP-less schedule channel (e.g. HmIP-MIO16-PCB) has no target map.
//
// Note: a custom DP whose primary channel is channel 0 carries the Go
// sentinel group number 0 ("ungrouped") and is skipped. Such CDPs
// (HmIP-DLP IPButtonLock) are filtered out by the explicit-schedule
// rule in the reference stack as well, so the result is identical.
func ScheduleRelevantChannelGroups(dev *device.Device, registry *Registry) []ScheduleTargetGroup {
	if dev == nil || registry == nil {
		return nil
	}
	type groupEntry struct {
		rebased  RebasedChannelGroupConfig
		schedule bool
	}
	all := make(map[int]groupEntry)
	scheduled := make(map[int]groupEntry)
	for _, ch := range dev.Channels() {
		cdp := ch.CustomDataPoint()
		groupNo := ch.GroupNumber()
		if cdp == nil || groupNo == 0 {
			continue
		}
		profile, ok := lookupProfileForCustomDP(registry, dev.Model, cdp)
		if !ok || profile.Config == nil {
			continue
		}
		entry := groupEntry{
			rebased:  profile.Rebase(groupNo),
			schedule: profile.ScheduleChannelNo != nil,
		}
		if _, seen := all[groupNo]; !seen {
			all[groupNo] = entry
		}
		if entry.schedule {
			scheduled[groupNo] = entry
		}
	}
	src := all
	if len(scheduled) > 0 {
		src = scheduled
	}
	groupNos := make([]int, 0, len(src))
	for no := range src {
		groupNos = append(groupNos, no)
	}
	sort.Ints(groupNos)

	out := make([]ScheduleTargetGroup, 0, len(groupNos))
	for _, no := range groupNos {
		entry := src[no]
		var members []ScheduleTargetChannel
		if entry.rebased.PrimaryChannel != nil {
			members = append(members, ScheduleTargetChannel{ChannelNo: *entry.rebased.PrimaryChannel, Primary: true})
		}
		for _, sec := range entry.rebased.SecondaryChannels {
			members = append(members, ScheduleTargetChannel{ChannelNo: sec})
		}
		if len(members) == 0 {
			continue
		}
		out = append(out, ScheduleTargetGroup{GroupNo: no, Channels: members})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
