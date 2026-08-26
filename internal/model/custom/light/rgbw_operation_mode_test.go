// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The wire facts, read off the embedded HmIP-RGBW description:
//
//	DEVICE_OPERATION_MODE  channel 0  MASTER  TYPE=ENUM  OPERATIONS=3
//	VALUE_LIST = [RGBW, RGB, 2_TUNABLE_WHITE, 4_PWM]
//
// Three of those facts each broke the mode detection on their own: the
// parameter lives on channel 0 while the light sits on channels 1-4, a
// read+write ENUM resolves to a *generic.Select whose wire value is the
// 0-based index rather than the label, and the labels the CCU uses are
// "4_PWM" and "2_TUNABLE_WHITE" — not the bare words. The reference
// implementation spells them the same way
// (model/custom/light.py:_DeviceOperationMode).
var rgbwOperationModes = []string{"RGBW", "RGB", "2_TUNABLE_WHITE", "4_PWM"}

// newRGBWDevice builds an HmIP-RGBW: DEVICE_OPERATION_MODE on the
// device's channel 0 (MASTER), a light channel carrying LEVEL.
func newRGBWDevice(t *testing.T, lightChannel int) (*device.Device, *device.Channel, *generic.Select) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "RGBW0001", Model: "HmIP-RGBW", Name: "Lichtband",
	})
	ch0 := dev.AddChannel("RGBW0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyMaster)
	mode := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch0.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterDeviceOperationMode),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			ValueList:  rgbwOperationModes,
		},
	})
	ch0.PutMaster(mode)

	addr := "RGBW0001:" + string(rune('0'+lightChannel))
	ch := dev.AddChannel(addr, lightChannel, "RGBW", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	return dev, ch, mode
}

// TestRGBWLightReadsTheOperatingModeFromChannelZero pins the mode
// detection against the real device layout.
//
// Everything downstream hangs off it: which colour capabilities the
// light advertises, and whether channels 2-4 are folded into the primary
// channel or surface as their own entities. With the mode never
// observed, the light falls back to "behaves as if PWM were active",
// which is wrong for every RGBW device in RGB or tunable-white mode.
func TestRGBWLightReadsTheOperatingModeFromChannelZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		wireLabel string
		wantColor bool
	}{
		{name: "RGBW", wireLabel: "RGBW", wantColor: true},
		{name: "RGB", wireLabel: "RGB", wantColor: true},
		{name: "tunable white", wireLabel: "2_TUNABLE_WHITE", wantColor: false},
		{name: "PWM", wireLabel: "4_PWM", wantColor: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev, ch, mode := newRGBWDevice(t, 1)
			_ = dev

			r := NewRGBWLight(Config{Channel: ch})
			unsubscribe := r.Subscribe(ch)
			defer unsubscribe()

			// The CCU pushes the 0-based index, not the label.
			idx, ok := indexOfMode(tc.wireLabel)
			if !ok {
				t.Fatalf("%q is not in the fixture's VALUE_LIST", tc.wireLabel)
			}
			mode.OnEvent(idx)

			if got := r.Usage(); got == RGBWUsageUnknown {
				t.Fatalf("mode %q on channel 0 was not observed — Usage() is still unknown, so the light "+
					"advertises the PWM fallback whatever the device is actually configured as",
					tc.wireLabel)
			}
			if got := r.HasColor(); got != tc.wantColor {
				t.Errorf("HasColor() = %v for mode %q, want %v", got, tc.wireLabel, tc.wantColor)
			}
		})
	}
}

func indexOfMode(label string) (int32, bool) {
	for i, v := range rgbwOperationModes {
		if v == label {
			return int32(i), true //nolint:gosec // fixture list length
		}
	}
	return 0, false
}
