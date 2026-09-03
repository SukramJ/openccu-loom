// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmSchProfilePointerBaseComesFromTheParameterNotTheGoType pins which
// parameter is 0-based and which is 1-based, and pins it against the
// parameter name rather than against the Go type the wire value happens to
// arrive as.
//
// WEEK_PROGRAM_POINTER is declared TYPE=ENUM with MIN 0, VALUE_LIST
// ["WEEK PROGRAM 1", "WEEK PROGRAM 2", "WEEK PROGRAM 3"], and the CCU hands
// out the VALUE_LIST index as an integer: HSSLogicalTypeOption::GetDescription
// sets MIN=int(0) / MAX=int(size-1) and EnforceConstraints replaces an
// incoming label with `(int)i`
// (../OpenCCU-Base/src/libhsscomm/HSSLogicalTypeOption.cpp:72-118;
// ../OpenCCU-Base/src/devicetypes/rftypes/rf_tc_it_wm-w-eu.xml:381-401).
// ACTIVE_PROFILE is an INTEGER with min 1, default 1
// (HMIPServer de.eq3.cbcs.devicedescription.channelspecification.stateparameter.ClimateStateParameterFactory#createActiveProfileParameter).
//
// So the RF pointer reaches us as an int too, and "an int means 1-based" maps
// the device's first week program onto P1's neighbour.
func TestHmSchProfilePointerBaseComesFromTheParameterNotTheGoType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		param hmenum.Parameter
		raw   any
		want  string
	}{
		{"RF pointer 0 is the first program", hmenum.ParameterWeekProgramPointer, int32(0), "P1"},
		{"RF pointer 1 is the second program", hmenum.ParameterWeekProgramPointer, int32(1), "P2"},
		{"RF pointer 2 is the third program", hmenum.ParameterWeekProgramPointer, int32(2), "P3"},
		{"RF pointer as plain int", hmenum.ParameterWeekProgramPointer, 1, "P2"},
		{"RF pointer as float", hmenum.ParameterWeekProgramPointer, float64(2), "P3"},
		{"RF pointer as numeric string", hmenum.ParameterWeekProgramPointer, "1", "P2"},
		{"IP profile 1 is the first profile", hmenum.ParameterActiveProfile, int32(1), "P1"},
		{"IP profile 3", hmenum.ParameterActiveProfile, 3, "P3"},
		{"IP profile as float", hmenum.ParameterActiveProfile, float64(2), "P2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := NewProfileDataPoint(ProfileDataPointConfig{
				ScheduleType: ScheduleTypeClimate,
				ProfileCount: 6,
			})
			if err := dp.SyncProfilePointerFor(tc.param, tc.raw); err != nil {
				t.Fatalf("SyncProfilePointerFor(%s, %v): %v", tc.param, tc.raw, err)
			}
			if got := dp.CurrentProfile(); got != tc.want {
				t.Errorf("SyncProfilePointerFor(%s, %v) = %q, want %q",
					tc.param, tc.raw, got, tc.want)
			}
		})
	}
}

// TestHmSchProfilePointerRejectsWhatTheParameterCannotMean is the other half:
// a value outside the profile range and an unrelated parameter leave the
// current profile alone rather than resolving to a neighbouring program.
func TestHmSchProfilePointerRejectsWhatTheParameterCannotMean(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		param hmenum.Parameter
		raw   any
	}{
		{"IP profile 0 is below the declared minimum", hmenum.ParameterActiveProfile, int32(0)},
		{"RF pointer 6 is past the sixth program", hmenum.ParameterWeekProgramPointer, int32(6)},
		{"a parameter that is not a profile pointer", hmenum.ParameterLevel, int32(2)},
		{"a value that is not a number", hmenum.ParameterActiveProfile, "auto"},
		{"nil", hmenum.ParameterActiveProfile, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := NewProfileDataPoint(ProfileDataPointConfig{
				ScheduleType:   ScheduleTypeClimate,
				ProfileCount:   6,
				InitialProfile: "P4",
			})
			if err := dp.SyncProfilePointerFor(tc.param, tc.raw); err != nil {
				t.Fatalf("SyncProfilePointerFor(%s, %v): %v", tc.param, tc.raw, err)
			}
			if got := dp.CurrentProfile(); got != "P4" {
				t.Errorf("SyncProfilePointerFor(%s, %v) moved the profile to %q; an "+
					"unusable value must leave it untouched", tc.param, tc.raw, got)
			}
		})
	}
}
