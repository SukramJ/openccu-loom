// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"reflect"
	"testing"
)

// TestTargetChannelBitsFollowTheDeviceNotAFormula pins the rule the CCU
// actually applies: a target channel's bit is the device's own channel number
// minus one.
//
// The cases are the firmware's own device lists, read from its weekly-program
// editor (www/config/easymodes/js/HmIPWeeklyProgram.js:394-418). They exist
// because a formula stood here instead — 3*(actor-1) + (sub-1) — which is
// right only for a device whose virtual receivers run contiguously from
// channel 1. Every gapped device below was silently mis-addressed by it, and
// nothing in this repository noticed for as long as it stood: no test asserted
// a TARGET_CHANNELS value at all, in either direction.
//
// That is the point of this file. A round-trip through an encoder nobody
// checks the output of proves the two halves agree, not that either is right.
func TestTargetChannelBitsFollowTheDeviceNotAFormula(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		channels []int
		want     []uint
	}{
		{
			// The layout the old formula happened to match, kept so a
			// regression toward it is visible as "only this case still passes".
			name:     "contiguous from 1",
			channels: []int{1, 2, 3},
			want:     []uint{0, 1, 2},
		},
		{
			// HmIP-BSL on firmware 2: channels 7 and 11 are not receivers, so
			// the numbering has holes the formula cannot express.
			name:     "HmIP-BSL fw2, gapped",
			channels: []int{4, 5, 6, 8, 9, 10, 12, 13, 14},
			want:     []uint{3, 4, 5, 7, 8, 9, 11, 12, 13},
		},
		{
			// HmIP-WKP: odd channels only.
			name:     "HmIP-WKP, odd only",
			channels: []int{1, 3, 5, 7, 9, 11, 13, 15},
			want:     []uint{0, 2, 4, 6, 8, 10, 12, 14},
		},
		{
			// A window drive exposes a single receiver, and it is channel 2 —
			// so even the one-channel case is not bit 0.
			name:     "window drive, single non-first channel",
			channels: []int{2},
			want:     []uint{1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info := make(map[string]TargetChannelInfo, len(tc.channels))
			keys := make([]string, 0, len(tc.channels))
			for i, chNo := range tc.channels {
				key := targetKey(i)
				keys = append(keys, key)
				info[key] = TargetChannelInfo{ChannelNo: chNo}
			}
			bits := TargetChannelBitsFrom(info)
			if bits == nil {
				t.Fatal("TargetChannelBitsFrom returned nil for a resolvable device")
			}
			for i, key := range keys {
				if got := bits[key]; got != tc.want[i] {
					t.Errorf("%s -> bit %d, want %d (device channel %d; the bit is the "+
						"channel number minus one, not a position computed from the key)",
						key, got, tc.want[i], tc.channels[i])
				}
			}

			// The mask and the list must be two spellings of one fact.
			mask, ok := TargetChannelsListToBitmask(keys, bits)
			if !ok {
				t.Fatal("encoding a fully resolvable selection was withheld")
			}
			var wantMask int
			for _, b := range tc.want {
				wantMask |= 1 << b
			}
			if mask != wantMask {
				t.Errorf("mask = %#b, want %#b", mask, wantMask)
			}
			if got := TargetChannelsBitmaskToList(mask, bits); !reflect.DeepEqual(got, keys) {
				t.Errorf("round trip = %v, want %v", got, keys)
			}
		})
	}
}

// TestTargetChannelsAreWithheldRatherThanGuessed pins the other half of the
// decision: when the device cannot be resolved, nothing is written.
//
// An optional field left unwritten leaves the device holding what it had. A
// guessed one switches a channel the operator did not select — on real
// hardware, silently. The two are not close calls.
func TestTargetChannelsAreWithheldRatherThanGuessed(t *testing.T) {
	t.Parallel()

	if _, ok := TargetChannelsListToBitmask([]string{"1_1"}, nil); ok {
		t.Error("an unresolved device produced a bitmask; it must withhold the field instead")
	}
	if got := TargetChannelsBitmaskToList(0b101, nil); got != nil {
		t.Errorf("decoding without a device returned %v, want nil", got)
	}
	// One unknown key withholds the whole field rather than contributing a
	// partial mask: a partial mask is a different selection, not a smaller one.
	bits := TargetChannelBits{"1_1": 0}
	if _, ok := TargetChannelsListToBitmask([]string{"1_1", "9_9"}, bits); ok {
		t.Error("an unresolvable key was dropped from the mask; the field must be withheld")
	}
	if bits := TargetChannelBitsFrom(map[string]TargetChannelInfo{}); bits != nil {
		t.Errorf("an empty device map produced %v, want nil", bits)
	}
	// A channel number below 1 cannot be a bit position and must not become one.
	if bits := TargetChannelBitsFrom(map[string]TargetChannelInfo{"1_1": {ChannelNo: 0}}); bits != nil {
		t.Errorf("channel 0 produced %v, want nil", bits)
	}
}

// targetKey spells the nth resolved target channel the way the daemon keys
// them (actor-major, three per actor). The key is only a label here: what the
// bit is derived from is the channel number the map carries, which is the
// whole point of the test above.
func targetKey(i int) string {
	return string(rune('1'+i/3)) + "_" + string(rune('1'+i%3))
}
