// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"reflect"
	"testing"
)

// TestTargetChannelBitsCarryTheDevicesPositions pins that the TARGET_CHANNELS
// mask is built from the bit each key's channel holds in the device's own
// schedule-relevant list — the value [TargetBitOrder] derives and
// [TargetChannelInfo] carries — and from nothing else. A key whose channel
// the list does not carry contributes no bit and withholds the whole field.
//
// The device is an HmIP-BSL on firmware 2 (receivers 4,5,6 / 8,9,10 /
// 12,13,14; HmIPWeeklyProgram.js:401): under the firmware's positional rule
// key 1_1 (channel 4) is bit 0 and key 2_1 (channel 8) is bit 3. Under the
// two rules that stood here before — "channel number minus one", and the
// 3*(actor-1)+(sub-1) grid — this case reads 1_1 as bit 3 and 2_1 as bit 7,
// respectively bit 3 by coincidence; the mask below tells them apart.
func TestTargetChannelBitsCarryTheDevicesPositions(t *testing.T) {
	t.Parallel()

	order := TargetBitOrder("HmIP-BSL", []TypedChannel{
		{No: 4, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: 5, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: 6, Type: "SWITCH_VIRTUAL_RECEIVER"},
		{No: 7, Type: "SWITCH_WEEK_PROFILE"},
		{No: 8, Type: "DIMMER_VIRTUAL_RECEIVER"},
		{No: 9, Type: "DIMMER_VIRTUAL_RECEIVER"},
		{No: 10, Type: "DIMMER_VIRTUAL_RECEIVER"},
		{No: 11, Type: "DIMMER_WEEK_PROFILE"},
		{No: 12, Type: "DIMMER_VIRTUAL_RECEIVER"},
		{No: 13, Type: "DIMMER_VIRTUAL_RECEIVER"},
		{No: 14, Type: "DIMMER_VIRTUAL_RECEIVER"},
	})
	info := make(map[string]TargetChannelInfo)
	keys := []string{"1_1", "1_2", "1_3", "2_1", "2_2", "2_3", "3_1", "3_2", "3_3"}
	for i, chNo := range []int{4, 5, 6, 8, 9, 10, 12, 13, 14} {
		bit, ok := order[chNo]
		info[keys[i]] = TargetChannelInfo{ChannelNo: chNo, Bit: bit, BitKnown: ok}
	}

	bits := TargetChannelBitsFrom(info)
	want := TargetChannelBits{"1_1": 0, "1_2": 1, "1_3": 2, "2_1": 3, "2_2": 4, "2_3": 5, "3_1": 6, "3_2": 7, "3_3": 8}
	if !reflect.DeepEqual(bits, want) {
		t.Fatalf("TargetChannelBitsFrom = %v, want %v (position in the device's receiver list)", bits, want)
	}

	mask, ok := TargetChannelsListToBitmask([]string{"1_1", "2_1"}, bits)
	if !ok {
		t.Fatal("encoding a fully resolvable selection was withheld")
	}
	if mask != 1<<0|1<<3 {
		t.Errorf("mask for 1_1+2_1 = %#b, want %#b (bits 0 and 3)", mask, 1<<0|1<<3)
	}
	if got := TargetChannelsBitmaskToList(mask, bits); !reflect.DeepEqual(got, []string{"1_1", "2_1"}) {
		t.Errorf("round trip = %v, want [1_1 2_1]", got)
	}

	// A key whose channel the list does not carry has no bit, and the
	// field is withheld rather than partially written.
	info["4_1"] = TargetChannelInfo{ChannelNo: 15}
	bits = TargetChannelBitsFrom(info)
	if _, has := bits["4_1"]; has {
		t.Errorf("an unresolved channel produced a bit: %v", bits["4_1"])
	}
	if _, ok := TargetChannelsListToBitmask([]string{"1_1", "4_1"}, bits); ok {
		t.Error("a selection containing an unresolvable key was encoded; the field must be withheld")
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
	// A channel with no known position cannot become a bit — not even bit 0.
	if bits := TargetChannelBitsFrom(map[string]TargetChannelInfo{"1_1": {ChannelNo: 1}}); bits != nil {
		t.Errorf("a channel without a resolved position produced %v, want nil", bits)
	}
}
