// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package valve_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
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

// wsmFixturePath is the VALUES paramset description of an HmIP-WSM
// WATER_SWITCH_VIRTUAL_RECEIVER channel, read from a live CCU over JSON-RPC
// (Interface.getParamsetDescription) and kept verbatim.
const wsmFixturePath = "../../../../notes/parity/fixtures/hmip-wsm-water-switch-values.json"

// wsmDurationUnits reads DURATION_UNIT's VALUE_LIST from the captured HmIP-WSM
// description. JSON-RPC carries it as one space-separated string.
func wsmDurationUnits(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(wsmFixturePath)
	if err != nil {
		t.Fatalf("read %s: %v", wsmFixturePath, err)
	}
	var desc []struct {
		ID        string `json:"ID"`
		Type      string `json:"TYPE"`
		ValueList string `json:"VALUE_LIST"`
	}
	if err := json.Unmarshal(raw, &desc); err != nil {
		t.Fatalf("decode %s: %v", wsmFixturePath, err)
	}
	for _, p := range desc {
		if p.ID == string(hmenum.ParameterDurationUnit) && p.Type == "ENUM" {
			return strings.Fields(p.ValueList)
		}
	}
	t.Fatalf("%s carries no ENUM DURATION_UNIT", wsmFixturePath)
	return nil
}

// TestIrrigationOpenEncodesByTheChannelsUnitList drives Irrigation.Open
// through the real registry: the valve hands the list its channel declares to
// the encoder — 10MS on the HmIP-WSM, whose captured DURATION_UNIT carries it,
// whole seconds rounded half-up and never below one step on a list without
// it, and the seconds unit where the channel declares no list at all.
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
			name: "the HmIP-WSM's captured list",
			channel: func(t *testing.T, w custom.Writer) *device.Channel {
				t.Helper()
				return newWSMValveWithUnitList(t, w, wsmDurationUnits(t))
			},
			wantValue: 50, wantUnit: int32(slices.Index(wsmDurationUnits(t), "10MS")), //nolint:gosec // a four-entry list
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
