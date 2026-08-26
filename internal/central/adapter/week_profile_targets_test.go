// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// targetsFakeCDP satisfies [device.AttachableDataPoint] so
// deriveTargetChannels can resolve the producing profile from the
// generated registry through the DataPointKey's channel address.
type targetsFakeCDP struct {
	key hmtypes.DataPointKey
}

func (f *targetsFakeCDP) DataPointKey() hmtypes.DataPointKey { return f.key }

// putIntegerDP installs an INTEGER VALUES data point on ch.
func putIntegerDP(ch *device.Channel, param hmenum.Parameter) *generic.Integer {
	dp := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "Test-HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	})
	ch.Put(dp)
	return dp
}

// newScheduleTargetsDevice builds a device of the given model with
// channels 0..maxChannel, a fake custom DP (plus group bookkeeping) on
// every channel in cdpChannels, and the WEEK_PROGRAM_CHANNEL_LOCKS
// parameter on scheduleChannel.
func newScheduleTargetsDevice(t *testing.T, model, address string, maxChannel, scheduleChannel int, cdpChannels []int) *device.Device {
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
		ch.AssignGroupNumber(no)
		d.AddChannelToGroup(no, no)
		ch.SetCustomDataPoint(&targetsFakeCDP{
			key: hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "STATE"},
		})
	}
	if schedCh := d.Channel(fmt.Sprintf("%s:%d", address, scheduleChannel)); schedCh != nil {
		putIntegerDP(schedCh, hmenum.ParameterWeekProgramChannelLocks)
	}
	return d
}

// targetKeySummary renders the derived map as "key=ch<no>/<type>"
// entries sorted by key for compact assertions.
func targetKeySummary(targets map[string]weekprofile.TargetChannelInfo) []string {
	out := make([]string, 0, len(targets))
	for k, info := range targets {
		out = append(out, fmt.Sprintf("%s=ch%d/%s", k, info.ChannelNo, info.ChannelType))
	}
	sort.Strings(out)
	return out
}

// TestDeriveTargetChannelsIrrigationValve pins the ELV-SH-WSM shape:
// one valve group (primary ch4, secondaries ch5+ch6) → keys 1_1..1_3.
// The legacy *_VIRTUAL_RECEIVER type heuristic produced no targets at
// all here (WATER_SWITCH_VIRTUAL_RECEIVER), leaving the device without
// a week-profile attach.
func TestDeriveTargetChannelsIrrigationValve(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetsDevice(t, "ELV-SH-WSM", "0052E3C0002EC3", 7, 7, []int{4})
	got := targetKeySummary(deriveTargetChannels(d))
	want := []string{"1_1=ch4/primary", "1_2=ch5/secondary", "1_3=ch6/secondary"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deriveTargetChannels = %v, want %v", got, want)
	}
}

// TestDeriveTargetChannelsSoundPlayer pins the HmIP-MP3P shape: two
// single-channel groups (sound player ch2, LED ch6) → keys 1_1 + 2_1.
func TestDeriveTargetChannelsSoundPlayer(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetsDevice(t, "HmIP-MP3P", "0015226998783B", 9, 9, []int{2, 6})
	got := targetKeySummary(deriveTargetChannels(d))
	want := []string{"1_1=ch2/primary", "2_1=ch6/primary"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deriveTargetChannels = %v, want %v", got, want)
	}
}

// TestDeriveTargetChannelsWallRemote pins the HmIP-WRC6-230 shape: the
// IPSwitch group (ch9 + virtual receivers ch10/ch11) is actor 1, the
// seven LED channels 12..18 are single-channel actors 2..8.
func TestDeriveTargetChannelsWallRemote(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetsDevice(t, "HmIP-WRC6-230", "00536409A5E82D", 20, 19,
		[]int{9, 12, 13, 14, 15, 16, 17, 18})
	got := targetKeySummary(deriveTargetChannels(d))
	want := []string{
		"1_1=ch9/primary", "1_2=ch10/secondary", "1_3=ch11/secondary",
		"2_1=ch12/primary", "3_1=ch13/primary", "4_1=ch14/primary",
		"5_1=ch15/primary", "6_1=ch16/primary", "7_1=ch17/primary",
		"8_1=ch18/primary",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deriveTargetChannels = %v, want %v", got, want)
	}
}

// TestAttachNonClimateWeekProfileAttachesForCDPGroups runs the full
// attach path for the three previously-broken models and asserts the
// week profile lands on the schedule channel with the expected
// schedule-switch keys.
func TestAttachNonClimateWeekProfileAttachesForCDPGroups(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model       string
		address     string
		maxChannel  int
		scheduleCh  int
		cdpChannels []int
		wantKeys    []string
	}{
		{"ELV-SH-WSM", "0052E3C0002EC3", 7, 7, []int{4}, []string{"1_1", "1_2", "1_3"}},
		{"HmIP-MP3P", "0015226998783B", 9, 9, []int{2, 6}, []string{"1_1", "2_1"}},
		{
			"HmIP-WRC6-230", "00536409A5E82D", 20, 19,
			[]int{9, 12, 13, 14, 15, 16, 17, 18},
			[]string{"1_1", "1_2", "1_3", "2_1", "3_1", "4_1", "5_1", "6_1", "7_1", "8_1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			d := newScheduleTargetsDevice(t, tc.model, tc.address, tc.maxChannel, tc.scheduleCh, tc.cdpChannels)
			attachNonClimateWeekProfileToDevice(d, "TestCentral")

			schedCh := d.Channel(fmt.Sprintf("%s:%d", tc.address, tc.scheduleCh))
			wp := schedCh.WeekProfile()
			if wp == nil {
				t.Fatalf("%s: no week profile attached to schedule channel %d", tc.model, tc.scheduleCh)
			}
			if wp.ScheduleType() != weekprofile.ScheduleTypeDefault {
				t.Errorf("ScheduleType = %v, want %v", wp.ScheduleType(), weekprofile.ScheduleTypeDefault)
			}
			gotTargets := make([]string, 0)
			for k := range wp.AvailableTargetChannels() {
				gotTargets = append(gotTargets, k)
			}
			sort.Strings(gotTargets)
			wantSorted := append([]string(nil), tc.wantKeys...)
			sort.Strings(wantSorted)
			if !reflect.DeepEqual(gotTargets, wantSorted) {
				t.Errorf("AvailableTargetChannels keys = %v, want %v", gotTargets, wantSorted)
			}
			gotSwitches := make([]string, 0)
			for _, cs := range d.ScheduleChannelSwitches() {
				gotSwitches = append(gotSwitches, cs.ChannelKey())
			}
			sort.Strings(gotSwitches)
			if !reflect.DeepEqual(gotSwitches, wantSorted) {
				t.Errorf("ScheduleChannelSwitches keys = %v, want %v", gotSwitches, wantSorted)
			}
		})
	}
}

// TestAttachNonClimateWeekProfileNoCDPSuppressesTargetLocksOnly pins
// the CDP-less schedule device (HmIP-MIO16-PCB shape): no week profile
// and no switches are created, the redundant target-lock DPs are still
// force-hidden, and WEEK_PROGRAM_CHANNEL_LOCKS keeps its default usage
// so it can surface as a sensor like in the reference stack.
func TestAttachNonClimateWeekProfileNoCDPSuppressesTargetLocksOnly(t *testing.T) {
	t.Parallel()
	d := newScheduleTargetsDevice(t, "HmIP-MIO16-PCB", "MIO16TEST01", 49, 49, nil)
	schedCh := d.Channel("MIO16TEST01:49")
	lockDP := putIntegerDP(schedCh, "WEEK_PROGRAM_TARGET_CHANNEL_LOCK")
	locksDP := putIntegerDP(schedCh, "WEEK_PROGRAM_TARGET_CHANNEL_LOCKS")
	channelLocksDP, ok := schedCh.Parameter(hmenum.ParameterWeekProgramChannelLocks).(*generic.Integer)
	if !ok {
		t.Fatal("WEEK_PROGRAM_CHANNEL_LOCKS DP missing on schedule channel")
	}

	attachNonClimateWeekProfileToDevice(d, "TestCentral")

	if schedCh.WeekProfile() != nil {
		t.Error("CDP-less device must not get a week profile attached")
	}
	if n := len(d.ScheduleChannelSwitches()); n != 0 {
		t.Errorf("ScheduleChannelSwitches = %d, want 0", n)
	}
	for name, dp := range map[string]*generic.Integer{
		"WEEK_PROGRAM_TARGET_CHANNEL_LOCK":  lockDP,
		"WEEK_PROGRAM_TARGET_CHANNEL_LOCKS": locksDP,
	} {
		usage, forced := dp.ForcedUsage()
		if !forced || usage != hmenum.DataPointUsageNoCreate {
			t.Errorf("%s: ForcedUsage = (%v, %v), want (no_create, true)", name, usage, forced)
		}
	}
	if usage, forced := channelLocksDP.ForcedUsage(); forced {
		t.Errorf("WEEK_PROGRAM_CHANNEL_LOCKS must keep its default usage, got forced %v", usage)
	}
}
