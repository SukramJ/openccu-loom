// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// hmAdpAssertTargetKeysAreWritable fails for every key deriveTargetChannels
// published that the WEEK_PROGRAM_CHANNEL_LOCKS table cannot resolve. Such a
// key reaches the operator as a schedule switch that can never be written:
// the REST lock handler's 404 gate passes (the key IS registered on the DP),
// and SetScheduleEnabled then fails with `weekprofile: unknown channel key`.
func hmAdpAssertTargetKeysAreWritable(t *testing.T, targets map[string]weekprofile.TargetChannelInfo) {
	t.Helper()
	bad := make([]string, 0, len(targets))
	for k, info := range targets {
		if !info.BitKnown {
			bad = append(bad, k)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("deriveTargetChannels published %d key(s) with no WEEK_PROGRAM_CHANNEL_LOCKS bit: %v", len(bad), bad)
	}
}

// TestHmAdpDerivedTargetKeysAllResolveToALockBit couples the two halves of the
// channel-lock surface: every key the adapter mints carries the bit the
// firmware addresses its channel with, derived from the device's own
// schedule-relevant channel list ([weekprofile.TargetBitOrder]). A key
// whose channel that list does not carry must not be published rather
// than be published unwritable.
//
// HmIP-RGBW is the shipped case: profile IPRGBW carries PrimaryChannel 0 with
// SecondaryChannels {1,2,3} (internal/model/custom/profile_configs.go), so a
// group based at channel 1 has four members and the fourth mints "1_4" — the
// firmware's bit 3, which a fixed 8x3 grid had no entry for.
func TestHmAdpDerivedTargetKeysAllResolveToALockBit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		model       string
		address     string
		maxChannel  int
		schedule    int
		cdpChannels []int
	}{
		{"rgbw four member group", "HmIP-RGBW", "0058E3C0002EC3", 5, 5, []int{1}},
		{"lsc four member group", "HmIP-LSC", "0058E3C0002EC4", 5, 5, []int{1}},
		{"irrigation valve three member group", "ELV-SH-WSM", "0052E3C0002EC3", 7, 7, []int{4}},
		{"wall remote eight actors", "HmIP-WRC6-230", "0018226998783B", 18, 19, []int{9, 12, 13, 14, 15, 16, 17, 18}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := newScheduleTargetsDevice(t, tc.model, tc.address, tc.maxChannel, tc.schedule, tc.cdpChannels)
			targets := deriveTargetChannels(d)
			if len(targets) == 0 {
				t.Fatalf("%s: no targets derived — the fixture no longer exercises the key generator", tc.model)
			}
			hmAdpAssertTargetKeysAreWritable(t, targets)
		})
	}
}

// TestHmAdpRGBWPublishesAllFourUniversalLightReceivers records that the
// fourth UNIVERSAL_LIGHT_RECEIVER is addressable: the CCU editor gives it
// `Math.pow(2, 3)` (position 3 in the device's list), and the derived map
// carries exactly that bit.
func TestHmAdpRGBWPublishesAllFourUniversalLightReceivers(t *testing.T) {
	t.Parallel()

	d := newScheduleTargetsDevice(t, "HmIP-RGBW", "0058E3C0002EC3", 5, 5, []int{1})
	got := targetKeySummary(deriveTargetChannels(d))
	want := []string{"1_1=ch1/primary", "1_2=ch2/secondary", "1_3=ch3/secondary", "1_4=ch4/secondary"}
	if len(got) != len(want) {
		t.Fatalf("deriveTargetChannels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deriveTargetChannels = %v, want %v", got, want)
		}
	}
	targets := deriveTargetChannels(d)
	if info := targets["1_4"]; !info.BitKnown || info.Bit != 3 {
		t.Fatalf("1_4 bit = %+v, want position 3 (HmIPWeeklyProgram.js:2926 over the four UNIVERSAL_LIGHT_RECEIVER channels)", info)
	}
}
