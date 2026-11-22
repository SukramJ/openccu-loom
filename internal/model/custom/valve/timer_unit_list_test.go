// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package valve_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newWSMValveWithUnitList is newWSMValve with DURATION_UNIT materialised as
// the write-only ENUM over unitList, so the valve's encoder sees a declared
// list.
func newWSMValveWithUnitList(t *testing.T, w custom.Writer, unitList []string) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "000A1BE9957799",
		Model:        "HmIP-WSM",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	const addr = "000A1BE9957799:4"
	ch := dev.AddChannel(addr, 4, "WATER_SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)}
	}
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: key(hmenum.ParameterState),
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	ch.Put(generic.NewInteger(generic.Spec{
		Key:        key(hmenum.ParameterDurationValue),
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsWrite},
		Writer:     w,
	}))
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: key(hmenum.ParameterDurationUnit),
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsWrite, ValueList: unitList,
		},
		Writer: w,
	}))
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return ch
}

// TestIrrigationOpenEncodesByTheChannelsUnitList drives Irrigation.Open
// through the real registry. The HmIP-WSM's own DURATION_UNIT VALUE_LIST is
// not captured in this repository or in godevccu, so the cases do not claim
// one: they pin that the valve hands whatever list its channel declares to
// the encoder — 10MS where the list carries it, whole seconds rounded
// half-up and never below one step where it does not, and the seconds unit
// where the channel declares no list at all.
func TestIrrigationOpenEncodesByTheChannelsUnitList(t *testing.T) {
	t.Parallel()
	const (
		value = string(hmenum.ParameterDurationValue)
		unit  = string(hmenum.ParameterDurationUnit)
	)
	tests := []struct {
		name      string
		channel   func(t *testing.T, w custom.Writer) *device.Channel
		wantValue int32
		wantUnit  int32
	}{
		{
			name: "list with 10MS",
			channel: func(t *testing.T, w custom.Writer) *device.Channel {
				t.Helper()
				return newWSMValveWithUnitList(t, w, []string{"S", "M", "H", "10MS"})
			},
			wantValue: 50, wantUnit: 3, // "10MS" is the fourth entry of that list
		},
		{
			name: "list without 10MS",
			channel: func(t *testing.T, w custom.Writer) *device.Channel {
				t.Helper()
				return newWSMValveWithUnitList(t, w, []string{"S", "M", "H"})
			},
			wantValue: 1, wantUnit: 0, // "S" is the first entry of that list
		},
		{
			name: "no declared list",
			channel: func(t *testing.T, w custom.Writer) *device.Channel {
				t.Helper()
				return newWSMValve(t, w, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
			},
			wantValue: 1, wantUnit: int32(hmenum.TimerUnitSeconds),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &recordingParamsetWriter{}
			v := irrigationOn(t, tt.channel(t, w))
			if err := v.Open(context.Background(), 500*time.Millisecond, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("Open: %v", err)
			}
			if len(w.puts) != 1 {
				t.Fatalf("want one atomic paramset write, got puts=%v sets=%v", w.puts, w.sets)
			}
			if got := [2]any{w.puts[0][value], w.puts[0][unit]}; got != [2]any{tt.wantValue, tt.wantUnit} {
				t.Errorf("Open(500ms) wrote %s/%s = %v, want (%d, %d)", value, unit, got, tt.wantValue, tt.wantUnit)
			}
		})
	}
}
