// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestProfilePointerIsReadAgainstItsOwnParameter pins that the profile pointer
// is decoded by the parameter it came from, not by the Go type of its value.
//
// ACTIVE_PROFILE is declared 1-based and WEEK_PROGRAM_POINTER 0-based, so the
// same wire number means different profiles on the two parameters. The
// subscription used to call the parameter-less SyncProfilePointer, which
// guesses: a string is read as WEEK_PROGRAM_POINTER, everything else as
// ACTIVE_PROFILE. An RF pointer arriving as an integer — the ordinary shape —
// was therefore read 1-based and resolved one profile too low.
func TestProfilePointerIsReadAgainstItsOwnParameter(t *testing.T) {
	t.Parallel()

	// Through the production subscription, not through the method: a test
	// calling SyncProfilePointerFor directly passes whether or not
	// subscribeProfilePointer hands the parameter along, which is the defect.
	for _, tc := range []struct {
		param hmenum.Parameter
		raw   int32
		want  string
	}{
		{hmenum.ParameterWeekProgramPointer, 0, "P1"},
		{hmenum.ParameterWeekProgramPointer, 2, "P3"},
		{hmenum.ParameterActiveProfile, 1, "P1"},
		{hmenum.ParameterActiveProfile, 3, "P3"},
	} {
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
		ch := d.AddChannel("ABC0001:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
		ch.Put(intDP("ABC0001:1", tc.param, nil))
		wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
			ChannelAddress: "ABC0001:1", ProfileCount: 6, ScheduleType: weekprofile.ScheduleTypeClimate,
		})

		subscribeProfilePointer(d, wp)
		dp := ch.Parameter(tc.param)
		if dp == nil {
			t.Fatalf("%s: data point missing", tc.param)
		}
		setter, ok := dp.(interface{ OnWireValue(any) bool })
		if !ok {
			t.Fatalf("%s: data point takes no wire value", tc.param)
		}
		setter.OnWireValue(tc.raw)

		if got := wp.CurrentProfile(); got != tc.want {
			t.Errorf("%s=%v: current profile = %q, want %q", tc.param, tc.raw, got, tc.want)
		}
	}
}
