// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// scheduleTargetFakeCDP satisfies [device.AttachableDataPoint] so the
// channel-group walker can resolve the producing profile through the
// DataPointKey's channel address.
type scheduleTargetFakeCDP struct {
	key hmtypes.DataPointKey
}

func (f *scheduleTargetFakeCDP) DataPointKey() hmtypes.DataPointKey { return f.key }

// newScheduleTargetDevice builds a device of the given model with the
// channels 0..maxChannel and attaches a fake custom DP (plus the
// matching group bookkeeping) on every channel listed in cdpChannels.
// Group membership mirrors [addChannelGroupsToDevice] for profiles
// with PrimaryChannel == 0: group number == primary channel number,
// secondaries follow from the profile schema at lookup time.
func newScheduleTargetDevice(t *testing.T, model, address string, maxChannel int, cdpChannels []int) *device.Device {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID:  "Test-HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      address,
		Model:        model,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := 0; i <= maxChannel; i++ {
		d.AddChannel(fmt.Sprintf("%s:%d", address, i), i, "T", hmenum.ParamsetKeyValues)
	}
	for _, no := range cdpChannels {
		chAddr := fmt.Sprintf("%s:%d", address, no)
		ch := d.Channel(chAddr)
		if ch == nil {
			t.Fatalf("channel %s missing", chAddr)
		}
		ch.GroupNo = no
		d.AddChannelToGroup(no, no)
		ch.SetCustomDataPoint(&scheduleTargetFakeCDP{
			key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
		})
	}
	return d
}

// flattenGroups renders the group list as "<groupNo>:[p|s]<ch>…"
// strings for compact assertions.
func flattenGroups(groups []ScheduleTargetGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d:", g.GroupNo)
		for _, m := range g.Channels {
			kind := "s"
			if m.Primary {
				kind = "p"
			}
			fmt.Fprintf(&sb, "%s%d ", kind, m.ChannelNo)
		}
		out = append(out, sb.String())
	}
	return out
}

// TestScheduleRelevantChannelGroupsIrrigationValve verifies the
// ELV-SH-WSM topology: a single IPIrrigationValve group with primary
// channel 4 and secondary channels 5 + 6.
func TestScheduleRelevantChannelGroupsIrrigationValve(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetDevice(t, "ELV-SH-WSM", "0052E3C0002EC3", 7, []int{4})
	got := flattenGroups(ScheduleRelevantChannelGroups(d, DefaultRegistry()))
	want := []string{"4:p4 s5 s6 "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScheduleRelevantChannelGroups = %v, want %v", got, want)
	}
}

// TestScheduleRelevantChannelGroupsSoundPlayer verifies the HmIP-MP3P
// topology: two single-channel groups (sound player ch2, LED ch6) —
// the virtual receiver channels 3/4 and 7/8 are NOT part of the
// profiles' channel groups and must not appear.
func TestScheduleRelevantChannelGroupsSoundPlayer(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetDevice(t, "HmIP-MP3P", "0015226998783B", 9, []int{2, 6})
	got := flattenGroups(ScheduleRelevantChannelGroups(d, DefaultRegistry()))
	want := []string{"2:p2 ", "6:p6 "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScheduleRelevantChannelGroups = %v, want %v", got, want)
	}
}

// TestScheduleRelevantChannelGroupsWallRemote verifies the
// HmIP-WRC6-230 topology: the IPSwitch group (primary 9, secondaries
// 10/11) followed by seven single-channel LED groups 12..18.
func TestScheduleRelevantChannelGroupsWallRemote(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetDevice(t, "HmIP-WRC6-230", "00536409A5E82D", 20,
		[]int{9, 12, 13, 14, 15, 16, 17, 18})
	got := flattenGroups(ScheduleRelevantChannelGroups(d, DefaultRegistry()))
	want := []string{
		"9:p9 s10 s11 ",
		"12:p12 ", "13:p13 ", "14:p14 ", "15:p15 ",
		"16:p16 ", "17:p17 ", "18:p18 ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScheduleRelevantChannelGroups = %v, want %v", got, want)
	}
}

// TestScheduleRelevantChannelGroupsExplicitScheduleFilter verifies the
// explicit-schedule rule: when one profile carries a ScheduleChannelNo,
// only its groups survive (multi-config devices like HmIP-DLP).
func TestScheduleRelevantChannelGroupsExplicitScheduleFilter(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	schedCh := 14
	mustReg := func(p Profile) {
		t.Helper()
		if err := reg.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", p.Name, err)
		}
	}
	lockCfg := NewProfileConfig("TestLock", ChannelGroupConfig{PrimaryChannelSet: true})
	auxCfg := NewProfileConfig("TestAux", ChannelGroupConfig{PrimaryChannelSet: true})
	mustReg(Profile{
		Name:              "TestLock",
		DeviceType:        "test-dlp",
		Category:          hmenum.DataPointCategoryLock,
		Channels:          []ChannelRoleAssignment{{Channel: 12, Role: ChannelRolePrimary}},
		ScheduleChannelNo: &schedCh,
		Config:            &lockCfg,
	})
	mustReg(Profile{
		Name:       "TestAux",
		DeviceType: "test-dlp",
		Category:   hmenum.DataPointCategorySwitch,
		Channels:   []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		Config:     &auxCfg,
	})
	d := newScheduleTargetDevice(t, "test-dlp", "TESTDLP01", 15, []int{3, 12})
	got := flattenGroups(ScheduleRelevantChannelGroups(d, reg))
	want := []string{"12:p12 "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScheduleRelevantChannelGroups = %v, want %v", got, want)
	}
}

// TestScheduleRelevantChannelGroupsNoCDP verifies that a device without
// any custom DP (HmIP-MIO16-PCB shape) yields no target groups.
func TestScheduleRelevantChannelGroupsNoCDP(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetDevice(t, "HmIP-MIO16-PCB", "MIO16TEST01", 49, nil)
	if got := ScheduleRelevantChannelGroups(d, DefaultRegistry()); got != nil {
		t.Errorf("ScheduleRelevantChannelGroups = %v, want nil", got)
	}
	if got := ScheduleRelevantChannelGroups(nil, DefaultRegistry()); got != nil {
		t.Errorf("ScheduleRelevantChannelGroups(nil) = %v, want nil", got)
	}
}
