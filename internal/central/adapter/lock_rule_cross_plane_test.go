// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// The two production read planes for a lock slot are the REST/WS one
// (parseSimpleScheduleWithDomain, reached from the schedules handler) and the
// week-profile one (defaultChannelLoader.Load, reached from
// bindDefaultScheduleIO and published over MQTT). Both derive lock_mode /
// lock_action / permission from the same (LEVEL, DURATION, TARGET_CHANNELS)
// triplet, so a slot that reads "granted" in the API and "not_granted" in the
// MQTT attributes is a defect no single-plane test can see.
//
// The guard feeds one raw MASTER paramset through both real entry points and
// compares the two verdicts against each other — never against a literal, so
// neither plane is checked against its own copy of the rule. It bites when one
// plane grows a private threshold or prefix rule again.

// lockCrossPlaneBits pins the three actor_sub keys these fixtures use to the
// real device channel numbers a lock-plus-permission device with virtual
// receivers on channels 1, 2 and 3 would resolve (bit = channel number - 1,
// see [weekprofile.TargetChannelBitsFrom]). Both read planes below must
// resolve the same map for the cross-plane comparison to mean anything: the
// REST leg takes it explicitly, the week-profile leg resolves it from the
// channel's attached WeekProfile, mirroring production wiring.
var lockCrossPlaneBits = weekprofile.TargetChannelBits{"1_1": 0, "2_1": 1, "1_3": 2}

// lockRuleCrossPlaneCase is one raw-paramset fixture: the target channels
// straddle the actor-1 prefix rule, the level straddles the permission
// threshold, and the duration pair straddles the "permanent" sentinel that
// separates the door-lock actions.
type lockRuleCrossPlaneCase struct {
	name      string
	channels  []string
	level     float64
	durBase   int
	durFactor int
}

// lockRuleCrossPlaneCases crosses the inputs each half of the rule reads.
// Only two distinct (base, factor) pairs exist in schedule.LockActionTable —
// (0,0) and (7,31); the actions themselves are separated by LEVEL.
func lockRuleCrossPlaneCases() []lockRuleCrossPlaneCase {
	channelSets := map[string][]string{
		"actor1":      {"1_1"},
		"actor2":      {"2_1"},
		"actor2plus1": {"2_1", "1_3"},
		"none":        nil,
	}
	durations := map[string][2]int{
		"dur_zero":      {0, 0},
		"dur_permanent": {7, 31},
	}
	levels := []float64{0.0, 0.49, 0.5, 1.0, 1.01}

	out := make([]lockRuleCrossPlaneCase, 0, len(channelSets)*len(durations)*len(levels))
	for chName, chs := range channelSets {
		for durName, dur := range durations {
			for _, lv := range levels {
				out = append(out, lockRuleCrossPlaneCase{
					name:      fmt.Sprintf("%s/%s/level_%.2f", chName, durName, lv),
					channels:  chs,
					level:     lv,
					durBase:   dur[0],
					durFactor: dur[1],
				})
			}
		}
	}
	return out
}

// lockRuleCrossPlaneRaw builds the single-slot MASTER paramset both planes read.
func lockRuleCrossPlaneRaw(c lockRuleCrossPlaneCase) map[string]any {
	mask, _ := weekprofile.TargetChannelsListToBitmask(c.channels, lockCrossPlaneBits)
	return map[string]any{
		"01_WP_WEEKDAY":         weekprofile.WeekdayListToBitmask([]schedule.Weekday{schedule.WeekdayMonday}),
		"01_WP_FIXED_HOUR":      7,
		"01_WP_FIXED_MINUTE":    30,
		"01_WP_LEVEL":           c.level,
		"01_WP_TARGET_CHANNELS": mask,
		"01_WP_DURATION_BASE":   c.durBase,
		"01_WP_DURATION_FACTOR": c.durFactor,
	}
}

// lockRuleCrossPlaneWeekProfileLoad runs the week-profile leg through the real
// wiring seam: a channel with an installed refresher, bound by
// bindDefaultScheduleIO, read back through the attached profile.
func lockRuleCrossPlaneWeekProfileLoad(t *testing.T, raw map[string]any) *schedule.Simple {
	t.Helper()
	ch := &device.Channel{Address: "VCU000LOCK:10"}
	ch.SetRefresher(&wpFakeRefresher{response: raw})
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "Test",
		ChannelAddress: ch.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	wp.SetAvailableTargetChannels(map[string]weekprofile.TargetChannelInfo{
		"1_1": {ChannelNo: 1, ChannelAddress: ch.Address, ChannelType: "primary"},
		"2_1": {ChannelNo: 2, ChannelAddress: ch.Address, ChannelType: "secondary"},
		"1_3": {ChannelNo: 3, ChannelAddress: ch.Address, ChannelType: "secondary"},
	})
	ch.AttachWeekProfile(wp)
	bindDefaultScheduleIO(ch, wp, "lock")
	if wp.Simple() == nil {
		t.Fatal("bindDefaultScheduleIO did not attach a Simple profile")
	}
	s, err := wp.Simple().Load(context.Background())
	if err != nil {
		t.Fatalf("week-profile Load: %v", err)
	}
	return s
}

// TestLockSlotVerdictAgreesAcrossReadPlanes pins that the REST/WS read path and
// the week-profile read path derive the same lock_mode / lock_action /
// permission from the same raw paramset.
//
// Only the three lock verdict fields are compared: the REST leg additionally
// runs stripUnsupportedFields, which nulls level_2 / ramp_time / duration for
// the lock domain, so those fields legitimately differ between the planes.
func TestLockSlotVerdictAgreesAcrossReadPlanes(t *testing.T) {
	t.Parallel()
	for _, c := range lockRuleCrossPlaneCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			raw := lockRuleCrossPlaneRaw(c)

			restEntries := parseSimpleScheduleWithDomain(raw, "lock", lockCrossPlaneBits)
			if len(restEntries) != 1 {
				t.Fatalf("REST leg: got %d entries, want 1", len(restEntries))
			}
			wpSchedule := lockRuleCrossPlaneWeekProfileLoad(t, raw)

			for _, rest := range restEntries {
				// Key by slot number, not by position: the REST leg is a
				// slice ordered by Slots(), the week-profile leg a map keyed
				// by group number.
				wpEntry, ok := wpSchedule.Entries[rest.SlotNo]
				if !ok {
					t.Fatalf("slot %d present in REST leg, missing from week-profile leg", rest.SlotNo)
				}
				if rest.LockMode != string(wpEntry.LockMode) {
					t.Errorf("slot %d lock_mode: rest=%q weekprofile=%q",
						rest.SlotNo, rest.LockMode, wpEntry.LockMode)
				}
				if rest.LockAction != string(wpEntry.LockAction) {
					t.Errorf("slot %d lock_action: rest=%q weekprofile=%q",
						rest.SlotNo, rest.LockAction, wpEntry.LockAction)
				}
				if rest.Permission != string(wpEntry.Permission) {
					t.Errorf("slot %d permission: rest=%q weekprofile=%q",
						rest.SlotNo, rest.Permission, wpEntry.Permission)
				}
			}
		})
	}
}
