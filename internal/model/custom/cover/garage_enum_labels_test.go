// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"slices"
	"testing"
)

// TestDoorEnumLabelsMatchReportedValueList pins the DoorState and
// DoorCommand constants against the VALUE_LISTs the CCU reports for
// DOOR_STATE and DOOR_COMMAND on the HmIP-MOD-HO / HmIP-MOD-TM garage
// drives.
//
// Measured from the device descriptions the simulator embeds
// (godevccu internal/embed/data/paramset_descriptions/HmIP-MOD-HO.json
// and HmIP-MOD-TM.json, channel 1, VALUES): DOOR_STATE reports
// ["CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"] and
// DOOR_COMMAND accepts ["NOP", "OPEN", "STOP", "CLOSE", "PARTIAL_OPEN"].
// Both files agree label for label.
//
// The read path resolves the wire index to its label before anything
// compares it to a constant ([Garage.Subscribe] via
// custom.EnumLabelValue), so a constant spelled differently from the
// VALUE_LIST never matches a real report — the comparison silently falls
// through and the state is treated as unrecognised. That is a defect no
// behavioural test can catch while nothing in production compares
// against the constant, which is why this guard pins the declarations
// themselves.
//
// The comparison is by membership in both directions, not by position:
// the declaration order of the Go constants carries no wire meaning and
// must not be asserted as if it did.
func TestDoorEnumLabelsMatchReportedValueList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		valueList []string
		constants []string
	}{
		{
			name:      "DOOR_STATE",
			valueList: []string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"},
			constants: []string{
				string(DoorStateUnknown),
				string(DoorStateOpen),
				string(DoorStateClosed),
				string(DoorStateVentilation),
			},
		},
		{
			name:      "DOOR_COMMAND",
			valueList: []string{"NOP", "OPEN", "STOP", "CLOSE", "PARTIAL_OPEN"},
			constants: []string{
				string(DoorCommandOpen),
				string(DoorCommandClose),
				string(DoorCommandStop),
				string(DoorCommandPartialOpen),
				string(DoorCommandNop),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, c := range tc.constants {
				if !slices.Contains(tc.valueList, c) {
					t.Errorf("%s: constant %q is not a label the CCU reports; VALUE_LIST is %v",
						tc.name, c, tc.valueList)
				}
			}
			for _, label := range tc.valueList {
				if !slices.Contains(tc.constants, label) {
					t.Errorf("%s: reported label %q has no constant; declared are %v",
						tc.name, label, tc.constants)
				}
			}
		})
	}
}
