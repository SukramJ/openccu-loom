// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestVisFwUndecidedOperationModeLeavesUsageUntouched pins the boundary of
// the CHANNEL_OPERATION_MODE gate.
//
// The firmware defines several input-mode value lists for the same channel
// types, not one. Beyond {INACTIVE, KEY_BEHAVIOR, SWITCH_BEHAVIOR,
// BINARY_BEHAVIOR} it also ships LEVEL_KEY_BEHAVIOR and CONDITIONAL_BEHAVIOR,
// and the CCU's own MULTI_MODE_INPUT_TRANSMITTER dialog offers both to the
// operator whenever the channel's VALUE_LIST carries them
// (../OpenCCU-Base/www/config/easymodes/etc/hmipChannelConfigDialogs.tcl:1008-1035).
// Nothing in the firmware says which VALUES entries such a channel drops, so
// the gate must not decide: an observed mode our table cannot decide leaves
// every data point on its base usage, exactly like a not-yet-observed mode.
// Forcing Ignored there blanks STATE and all four PRESS_* parameters at once
// and reads as a dead device rather than as a rule.
func TestVisFwUndecidedOperationModeLeavesUsageUntouched(t *testing.T) {
	for _, mode := range []string{"LEVEL_KEY_BEHAVIOR", "CONDITIONAL_BEHAVIOR"} {
		t.Run(mode, func(t *testing.T) {
			d := device.New(device.Config{InterfaceID: "iface", Address: "DRDI001", Model: "HmIP-DRDI3"})
			ch := d.AddChannel("DRDI001:1", 1, "MULTI_MODE_INPUT_TRANSMITTER", hmenum.ParamsetKeyValues)

			state := putValuesBoolDP(ch, hmenum.ParameterState)
			press := putValuesBoolDP(ch, hmenum.ParameterPressShort)
			putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, mode)

			visibility.ApplyChannelOperationModeGating(ch)

			if usage, set := state.ForcedUsage(); set {
				t.Errorf("STATE forced usage = %q in mode %s; want untouched — the gate "+
					"must not decide a mode its table does not carry", usage, mode)
			}
			if usage, set := press.ForcedUsage(); set {
				t.Errorf("PRESS_SHORT forced usage = %q in mode %s; want untouched", usage, mode)
			}
		})
	}
}

// TestVisFwDecidedOperationModeStillGates is the negative control for the
// test above: the four modes the table does carry must keep gating exactly as
// before, so "leave it alone" cannot be reached by disabling the gate.
func TestVisFwDecidedOperationModeStillGates(t *testing.T) {
	cases := []struct {
		mode      string
		wantState hmenum.DataPointUsage
		wantPress hmenum.DataPointUsage
	}{
		{"BINARY_BEHAVIOR", hmenum.DataPointUsageDataPoint, hmenum.DataPointUsageIgnored},
		{"KEY_BEHAVIOR", hmenum.DataPointUsageIgnored, hmenum.DataPointUsageDataPoint},
		{"SWITCH_BEHAVIOR", hmenum.DataPointUsageIgnored, hmenum.DataPointUsageDataPoint},
		{"INACTIVE", hmenum.DataPointUsageIgnored, hmenum.DataPointUsageIgnored},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			d := device.New(device.Config{InterfaceID: "iface", Address: "DRDI002", Model: "HmIP-DRDI3"})
			ch := d.AddChannel("DRDI002:1", 1, "MULTI_MODE_INPUT_TRANSMITTER", hmenum.ParamsetKeyValues)

			state := putValuesBoolDP(ch, hmenum.ParameterState)
			press := putValuesBoolDP(ch, hmenum.ParameterPressShort)
			putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, tc.mode)

			visibility.ApplyChannelOperationModeGating(ch)

			if usage, set := state.ForcedUsage(); !set || usage != tc.wantState {
				t.Errorf("STATE forced usage = %q (set=%v) in mode %s, want %q",
					usage, set, tc.mode, tc.wantState)
			}
			if usage, set := press.ForcedUsage(); !set || usage != tc.wantPress {
				t.Errorf("PRESS_SHORT forced usage = %q (set=%v) in mode %s, want %q",
					usage, set, tc.mode, tc.wantPress)
			}
		})
	}
}
