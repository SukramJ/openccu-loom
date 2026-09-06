// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"reflect"
	"testing"
)

// TestTargetBitOrderIsThePositionInTheFirmwaresRelevantChannelList pins the
// rule the CCU's weekly-program editor applies, taken from the editor
// itself rather than from any one device:
//
//	valCheckBox = Math.pow(2, index)
//	    www/config/easymodes/js/HmIPWeeklyProgram.js:2899 and :2926
//	    src/webui/www_source/ise/js/iseHmIPWeeklyProgram.js:357
//
// where `index` walks the device's schedule-relevant channels — the
// CHANNEL_TYPEs getRelevantChannels accepts (iseHmIPWeeklyProgram.js:517-555)
// — in channel order. The cases are the editor's own per-family lists
// (HmIPWeeklyProgram.js:394-418), each stated as the channel types a real
// device carries, so the expectation is derived from the firmware's rule
// and its lists, not from a formula this package once carried.
//
// Two rules stood here before and both were wrong for ordinary devices:
// 3*(actor-1)+(sub-1) held only for receivers running contiguously from
// channel 1, and "channel number minus one" — the DALI branch at :2918,
// applied to every device — set bit 3 for an HmIP-BSL slot aimed at channel
// 4, which is channel 8. The cases below fail under either.
func TestTargetBitOrderIsThePositionInTheFirmwaresRelevantChannelList(t *testing.T) {
	t.Parallel()

	receivers := func(typ string, nos ...int) []TypedChannel {
		out := make([]TypedChannel, 0, len(nos))
		for _, no := range nos {
			out = append(out, TypedChannel{No: no, Type: typ})
		}
		return out
	}
	cases := []struct {
		name     string
		model    string
		channels []TypedChannel
		want     map[int]uint
	}{
		{
			// HmIP-BSL on firmware 2: switch receivers 4-6, dimmer receivers
			// 8-10 and 12-14; 7 and 11 are the week-profile channels
			// (HmIPWeeklyProgram.js:401). Position, not number-1.
			name:  "HmIP-BSL fw2",
			model: "HmIP-BSL",
			channels: append(append(
				receivers("SWITCH_VIRTUAL_RECEIVER", 4, 5, 6),
				TypedChannel{No: 7, Type: "SWITCH_WEEK_PROFILE"},
			),
				append(receivers("DIMMER_VIRTUAL_RECEIVER", 8, 9, 10),
					append([]TypedChannel{{No: 11, Type: "DIMMER_WEEK_PROFILE"}},
						receivers("DIMMER_VIRTUAL_RECEIVER", 12, 13, 14)...)...)...),
			want: map[int]uint{4: 0, 5: 1, 6: 2, 8: 3, 9: 4, 10: 5, 12: 6, 13: 7, 14: 8},
		},
		{
			// HmIP-PS / HmIP-PSM: one switch with receivers on 3, 4, 5.
			name:     "HmIP-PSM",
			model:    "HmIP-PSM",
			channels: append([]TypedChannel{{No: 1, Type: "SWITCH_TRANSMITTER"}, {No: 2, Type: "SWITCH_TRANSMITTER"}}, receivers("SWITCH_VIRTUAL_RECEIVER", 3, 4, 5)...),
			want:     map[int]uint{3: 0, 4: 1, 5: 2},
		},
		{
			// HmIP-SMO230: receivers 10-12 (HmIPWeeklyProgram.js:414).
			name:     "HmIP-SMO230",
			model:    "HmIP-SMO230",
			channels: append(receivers("MOTION_DETECTOR", 1, 2, 3), receivers("SWITCH_VIRTUAL_RECEIVER", 10, 11, 12)...),
			want:     map[int]uint{10: 0, 11: 1, 12: 2},
		},
		{
			// HmIP-WKP: odd channels only (HmIPWeeklyProgram.js:406).
			name:     "HmIP-WKP",
			model:    "HmIP-WKP",
			channels: append(receivers("ACCESS_TRANSCEIVER", 1, 3, 5, 7, 9, 11, 13, 15), receivers("KEY_TRANSCEIVER", 2, 4, 6, 8, 10, 12, 14, 16)...),
			want:     map[int]uint{1: 0, 3: 1, 5: 2, 7: 3, 9: 4, 11: 5, 13: 6, 15: 7},
		},
		{
			// HmIP-MOD-WD-VK: a single receiver, and it is channel 2 — so
			// even the one-channel case is bit 0, not bit 1.
			name:     "window drive",
			model:    "HmIP-MOD-WD-VK",
			channels: append([]TypedChannel{{No: 1, Type: "WINDOW_DRIVE_TRANSMITTER"}}, receivers("WINDOW_DRIVE_RECEIVER_VIRTUAL_RECEIVER", 2)...),
			want:     map[int]uint{2: 0},
		},
		{
			// HmIP-DLP: eight permission channels, then the door lock on 12
			// and auto-relock on 13 — the lock is bit 8, not bit 0
			// (iseHmIPWeeklyProgram_AccessReceiver.js:300-313).
			name:  "HmIP-DLP",
			model: "HmIP-DLP",
			channels: append(append(receivers("PERMISSION_TRANSCEIVER", 1, 2, 3, 4, 5, 6, 7, 8),
				receivers("ACCESS_WEEK_PROFILE", 9, 10, 11)...),
				TypedChannel{No: 12, Type: "DOOR_LOCK_TRANSCEIVER"}, TypedChannel{No: 13, Type: "AUTO_RELOCK_TRANSCEIVER"}),
			want: map[int]uint{1: 0, 2: 1, 3: 2, 4: 3, 5: 4, 6: 5, 7: 6, 8: 7, 12: 8, 13: 9},
		},
		{
			// HmIP-RGBW: four UNIVERSAL_LIGHT_RECEIVER channels; the fourth
			// is bit 3, which no 8x3 grid could name.
			name:     "HmIP-RGBW",
			model:    "HmIP-RGBW",
			channels: receivers("UNIVERSAL_LIGHT_RECEIVER", 1, 2, 3, 4),
			want:     map[int]uint{1: 0, 2: 1, 3: 2, 4: 3},
		},
		{
			// HmIP-FWI: the eight access channels take bits 3..10, the three
			// switch receivers bits 0..2 (valHmIP_FWI, HmIPWeeklyProgram.js:2893).
			name:  "HmIP-FWI",
			model: "HmIP-FWI",
			channels: append(append(receivers("ACCESS_TRANSCEIVER", 1, 2, 3, 4, 5, 6, 7, 8),
				TypedChannel{No: 9, Type: "ACCESS_WEEK_PROFILE"}),
				receivers("SWITCH_VIRTUAL_RECEIVER", 10, 11, 12)...),
			want: map[int]uint{1: 3, 2: 4, 3: 5, 4: 6, 5: 7, 6: 8, 7: 9, 8: 10, 10: 0, 11: 1, 12: 2},
		},
		{
			// HmIP-DRG-DALI: the one family where the bit IS the channel
			// number minus one (HmIPWeeklyProgram.js:2918); its list is
			// sparse, so a position would not survive a device with fewer
			// lamps.
			name:     "HmIP-DRG-DALI",
			model:    "HmIP-DRG-DALI",
			channels: receivers("UNIVERSAL_LIGHT_RECEIVER", 1, 2, 5, 33, 34, 48),
			want:     map[int]uint{1: 0, 2: 1, 5: 4, 33: 32, 34: 33, 48: 47},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TargetBitOrder(tc.model, tc.channels)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TargetBitOrder(%s) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

// TestTargetBitOrderIgnoresChannelsTheEditorDoesNotList — channel order is
// what counts, not the order the caller hands the channels in, and channels
// outside the editor's type set (maintenance, transmitters, the schedule
// channel itself, the device root) take no position.
func TestTargetBitOrderIgnoresChannelsTheEditorDoesNotList(t *testing.T) {
	t.Parallel()
	got := TargetBitOrder("HmIP-BSM", []TypedChannel{
		{No: 6, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: 0, Type: "MAINTENANCE"},
		{No: 4, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: -1, Type: ""},
		{No: 7, Type: "SWITCH_WEEK_PROFILE"},
		{No: 5, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: 1, Type: "SWITCH_TRANSMITTER"},
	})
	want := map[int]uint{4: 0, 5: 1, 6: 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TargetBitOrder = %v, want %v", got, want)
	}
	if got := TargetBitOrder("HmIP-eTRV", []TypedChannel{{No: 1, Type: "HEATING_CLIMATECONTROL_TRANSCEIVER"}}); got != nil {
		t.Errorf("a device with no schedule-relevant channel yielded %v, want nil", got)
	}
}
