// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Firmware-grounding guards for the visibility rule tables. Each test here
// pins a value against what the device itself declares, not against the
// shape our own table happens to have.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// visFwChannelSetEqual reports whether got holds exactly the listed channel
// numbers. Written out rather than compared with maps.Equal so a failure can
// name the channel that differs.
func visFwChannelSetEqual(got map[int]struct{}, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for _, n := range want {
		if _, ok := got[n]; !ok {
			return false
		}
	}
	return true
}

// TestVisFwDRDI3MasterChannelsMatchTheDeclaredChannels pins the HmIP-DRDI3
// row against the device's own paramset description.
//
// The recorded descriptors for this model declare VCU6948166:1..3 as
// MULTI_MODE_INPUT_TRANSMITTER and :4/:8/:12 as DIMMER_TRANSMITTER, and
// declare MASTER CHANNEL_OPERATION_MODE on all six, OPERATIONS=3 FLAGS=1 —
// on 1/2/3 with VALUE_LIST [INACTIVE, KEY_BEHAVIOR, SWITCH_BEHAVIOR,
// BINARY_BEHAVIOR], on 4/8/12 with the channel-enable VALUE_LIST [OFF, ON].
func TestVisFwDRDI3MasterChannelsMatchTheDeclaredChannels(t *testing.T) {
	t.Parallel()
	entry, ok := relevantMasterParamsetsByDevice["HmIP-DRDI3"]
	if !ok {
		t.Fatal("relevantMasterParamsetsByDevice must carry an HmIP-DRDI3 entry")
	}
	if !visFwChannelSetEqual(entry.Channels, 1, 2, 3, 4, 8, 12) {
		t.Errorf("HmIP-DRDI3 channels = %v, want {1,2,3,4,8,12} — the descriptor "+
			"declares MASTER CHANNEL_OPERATION_MODE on VCU6948166:1,:2,:3,:4,:8,:12",
			entry.Channels)
	}
}

// TestVisFwDRDI3DimmerChannelsAreNotMasterSkipped is the behaviour half of
// the row above: the decider must not default-skip CHANNEL_OPERATION_MODE on
// the three DIMMER_TRANSMITTER channels the device declares it on.
//
// The negative control rides along: channel 5 is not a declared
// CHANNEL_OPERATION_MODE channel on this device and must stay skipped, so a
// table that simply allowed every channel would fail this test too.
func TestVisFwDRDI3DimmerChannelsAreNotMasterSkipped(t *testing.T) {
	t.Parallel()
	for _, ch := range []int{1, 2, 3, 4, 8, 12} {
		if checkMasterParameterIgnored(ch, hmenum.ParameterChannelOperationMode, "HmIP-DRDI3") {
			t.Errorf("HmIP-DRDI3 channel %d: CHANNEL_OPERATION_MODE must not be "+
				"MASTER-skipped — the device declares it there", ch)
		}
	}
	if !checkMasterParameterIgnored(5, hmenum.ParameterChannelOperationMode, "HmIP-DRDI3") {
		t.Error("HmIP-DRDI3 channel 5: CHANNEL_OPERATION_MODE must stay MASTER-skipped — " +
			"the device does not declare it there")
	}
}

// TestVisFwHandlePrefixKeepsTheOperatorFacingHandleParameters pins the
// carve-out for the only two HANDLE_* parameters any device in the descriptor
// corpus declares.
//
// HM-ReSC-Win-PCB-xx declares VCU0000208:1 VALUES HANDLE_LOCK (BOOL) and
// HANDLE_LED_MODE (ENUM [OFF, DIMMED_ON, FULL_ON]) in the recorded
// device-descriptor corpus, both OPERATIONS=7 FLAGS=1 — readable, writable,
// event-carrying, and neither INTERNAL nor SERVICE. The CCU labels both for
// the operator at ../OpenCCU-Base/src/webui/www/config/stringtable_de.txt:95-99
// (ACTOR_WINDOW|HANDLE_LED_MODE=..., ACTOR_WINDOW|HANDLE_LOCK=...).
func TestVisFwHandlePrefixKeepsTheOperatorFacingHandleParameters(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	for _, name := range []string{"HANDLE_LOCK", "HANDLE_LED_MODE"} {
		if d.IsParameterIgnored("HM-ReSC-Win-PCB-xx", "ACTOR_WINDOW", 1,
			hmenum.ParamsetKeyValues, hmenum.Parameter(name)) {
			t.Errorf("%s on HM-ReSC-Win-PCB-xx must not be ignored — the device "+
				"declares it OPERATIONS=7 FLAGS=1 and the CCU labels it for the operator", name)
		}
	}

	// Negative control: the HANDLE_ prefix rule itself must still bite, both
	// for an unwitnessed name on the same device and for the same names on a
	// device that does not declare them.
	if !d.IsParameterIgnored("HM-ReSC-Win-PCB-xx", "ACTOR_WINDOW", 1,
		hmenum.ParamsetKeyValues, hmenum.Parameter("HANDLE_SOMETHING")) {
		t.Error("HANDLE_SOMETHING must stay ignored — the carve-out is per parameter, not per prefix")
	}
	if !d.IsParameterIgnored("HmIP-BROLL", "BLIND_TRANSMITTER", 1,
		hmenum.ParamsetKeyValues, hmenum.Parameter("HANDLE_LOCK")) {
		t.Error("HANDLE_LOCK must stay ignored on a device that does not declare it")
	}
}

// TestVisFwChannelRestrictionDoesNotReachTheHmIPBatteryName pins the reach of
// acceptParameterOnlyOnChannel.
//
// The map keys the BidCos spelling LOWBAT. Across the 399-model descriptor
// corpus the sets {declares LOWBAT} and {declares LOW_BAT} are disjoint, and
// no HmIP model declares LOWBAT — so the restriction has no HmIP effect and
// must not acquire one by someone adding the HmIP spelling to the same map.
func TestVisFwChannelRestrictionDoesNotReachTheHmIPBatteryName(t *testing.T) {
	t.Parallel()
	if IsAcceptedOnlyOnChannel("LOW_BAT", 1) {
		t.Error("LOW_BAT must carry no channel restriction — the rule is a BidCos-only " +
			"de-duplication policy and HmIP devices declare LOW_BAT, not LOWBAT")
	}
	// Negative control: the map still restricts the name it is written for.
	if !IsAcceptedOnlyOnChannel("LOWBAT", 1) {
		t.Error("LOWBAT off channel 0 must still be restricted")
	}
}
