// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2CstRFClimateWithPointerRange builds a KindRF Climate whose channel
// carries a WEEK_PROGRAM_POINTER declaring MIN..MAX, the descriptor bound
// [Climate.numWeekPrograms] reads. Pass ok=false to omit the pointer data
// point entirely — the shape a thermostat without week programs has.
func w2CstRFClimateWithPointerRange(t *testing.T, lo, hi int, present bool) *Climate {
	t.Helper()

	const addr = "OEQ0000001:4"
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "OEQ0000001"})
	ch := d.AddChannel(addr, 4, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)

	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))

	if present {
		mustJSON := func(v int) json.RawMessage {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal %d: %v", v, err)
			}
			return b
		}
		ch.Put(generic.NewInteger(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterWeekProgramPointer),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeInteger,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
				Min:        mustJSON(lo),
				Max:        mustJSON(hi),
			},
		}))
	}

	return New(Config{
		Channel: ch,
		Writer:  &stubWriter{},
		Kind:    KindRF,
		Capabilities: custom.ClimateCapabilities{
			SupportsProfile: true,
			SupportsAuto:    true,
		},
	})
}

// w2CstWeekProgramsIn counts the week-program slots in a profile list.
func w2CstWeekProgramsIn(profiles []Profile) int {
	n := 0
	for _, p := range profiles {
		if _, ok := profileWeekIndex(p); ok {
			n++
		}
	}
	return n
}

// TestW2CstScheduleProfileNosMatchesThePublishedSlots pins the exported
// count against the slots the device actually gets.
//
// [Climate.ScheduleProfileNos] documents itself as counting "the week-program
// slots that would appear in Profiles()", and Profiles() derives that count
// from [Climate.numWeekPrograms], which reads the pointer descriptor's
// MIN/MAX off the wire. The exported method used instead to walk six literal
// Profile constants, so it answered 6 for every thermostat with
// SupportsProfile set — including one whose descriptor declares three slots
// and one that carries no pointer data point at all.
//
// The static answer is what a future caller would adopt: the count is the
// number of presets a north-bound surface would offer, and offering
// week_program_4..6 on a thermostat that has three of them produces commands
// the device cannot execute.
func TestW2CstScheduleProfileNosMatchesThePublishedSlots(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		lo, hi  int
		present bool
	}{
		{"three_slot_pointer", 0, 2, true},
		{"six_slot_pointer", 0, 5, true},
		{"no_pointer_data_point", 0, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := w2CstRFClimateWithPointerRange(t, tc.lo, tc.hi, tc.present)
			published := w2CstWeekProgramsIn(c.Profiles())
			if got := c.ScheduleProfileNos(); got != published {
				t.Errorf("ScheduleProfileNos() = %d but Profiles() carries %d week-program slot(s) — the two answer the same question by different methods, and the exported one is the static answer",
					got, published)
			}
		})
	}
}

// TestW2CstScheduleProfileNosStaysZeroWithoutProfileSupport keeps the fix
// from being satisfiable by deleting the capability gate.
func TestW2CstScheduleProfileNosStaysZeroWithoutProfileSupport(t *testing.T) {
	t.Parallel()

	c := w2CstRFClimateWithPointerRange(t, 0, 5, true)
	c.Capabilities.SupportsProfile = false
	if got := c.ScheduleProfileNos(); got != 0 {
		t.Errorf("ScheduleProfileNos() = %d on a profile that does not support week programs, want 0", got)
	}
}
