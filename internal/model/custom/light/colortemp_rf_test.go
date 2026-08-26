// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"math"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The RF tunable-white dimmers (HM-LC-DW-WM, HM-DW-WM) have no
// COLOR_TEMPERATURE parameter — that one exists only on the HmIP
// families. They express the white point as a second dimmer channel:
// the profile maps COLOR_LEVEL onto LEVEL of the channel above the
// light's own, and the value is converted through mireds. The reference
// implementation does the same (model/custom/light.py,
// CustomDpColorTempDimmer.color_temp_kelvin).

// newRFColorTempRig builds an HM-LC-DW-WM: LEVEL on the light's channel
// (brightness) and LEVEL on the next one (white point).
func newRFColorTempRig(t *testing.T) (*ColorTempLight, *generic.Float, *recordingWriter) {
	t.Helper()
	w := &recordingWriter{}
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF", Interface: hmenum.InterfaceBidCosRF,
		Address: "LEQ0000115", Model: "HM-LC-DW-WM", Name: "Deckenlampe",
	})
	level := func(addr string, no int) *generic.Float {
		ch := dev.AddChannel(addr, no, "DIMMER", hmenum.ParamsetKeyValues)
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterLevel),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
		ch.Put(dp)
		return dp
	}
	_ = level("LEQ0000115:1", 1)
	colorLevel := level("LEQ0000115:2", 2)

	ch := dev.Channel("LEQ0000115:1")
	// The profile maps COLOR_LEVEL to LEVEL of channel 1 relative to the
	// light's base — channel 2 here.
	rebased := custom.RebasedChannelGroupConfig{
		ChannelFields: map[int]map[hmenum.Field]custom.FieldValue{
			2: {hmenum.FieldColorLevel: custom.Bare(hmenum.ParameterLevel)},
		},
	}
	dp, err := newColorTempConstructor(ch, rebased)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	ct, ok := dp.(*ColorTempLight)
	if !ok {
		t.Fatalf("constructor returned %T, want *ColorTempLight", dp)
	}
	return ct, colorLevel, w
}

// TestRFColorTempDimmerReadsKelvinFromItsWhitePointChannel is the
// reproducer: these devices have no COLOR_TEMPERATURE at all, so a light
// that only looks for that parameter reports no colour temperature on
// any of them — in either direction.
func TestRFColorTempDimmerReadsKelvinFromItsWhitePointChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		colorLevel float64
		wantKelvin int32
	}{
		// kelvin = 1e6 / (500 - (500-153) * level), truncated — the
		// reference's exact arithmetic.
		{name: "warmest", colorLevel: 0, wantKelvin: 2000},
		{name: "coldest", colorLevel: 1, wantKelvin: 6535},
		{name: "midpoint", colorLevel: 0.5, wantKelvin: 3067},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ct, colorLevel, _ := newRFColorTempRig(t)

			if _, observed := ct.Kelvin(); observed {
				t.Error("Kelvin() must not report a value before the white-point channel has one")
			}
			colorLevel.OnEvent(tc.colorLevel)

			got, observed := ct.Kelvin()
			if !observed {
				t.Fatalf("Kelvin() is not observed after the white-point channel reported %v — these "+
					"devices carry no COLOR_TEMPERATURE parameter, so a light that only reads that one "+
					"reports no colour temperature at all", tc.colorLevel)
			}
			if got != tc.wantKelvin {
				t.Errorf("Kelvin() = %d for level %v, want %d", got, tc.colorLevel, tc.wantKelvin)
			}
		})
	}
}

// TestRFColorTempDimmerWritesKelvinToItsWhitePointChannel pins the write
// direction: setting a colour temperature has to reach the white-point
// channel as a level, and come back as (very nearly) the same kelvin.
func TestRFColorTempDimmerWritesKelvinToItsWhitePointChannel(t *testing.T) {
	t.Parallel()

	for _, wantKelvin := range []int32{2200, 3000, 4000, 6000} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			ct, colorLevel, w := newRFColorTempRig(t)

			if err := ct.SetKelvin(context.Background(), wantKelvin, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("SetKelvin(%d): %v", wantKelvin, err)
			}
			sent, ok := w.lastFor(hmenum.ParameterLevel, colorLevel.Key.ChannelAddress)
			if !ok {
				t.Fatalf("SetKelvin(%d) wrote nothing to the white-point channel — the device has no "+
					"other way to receive a colour temperature", wantKelvin)
			}
			level, isFloat := sent.(float64)
			if !isFloat {
				t.Fatalf("white-point write carried %T, want a float level", sent)
			}
			if level < 0 || level > 1 {
				t.Errorf("white-point level %v is outside 0..1 and would be rejected", level)
			}
			// Feeding the written level back must land on the requested
			// temperature, within the resolution mireds allow.
			colorLevel.OnEvent(level)
			got, _ := ct.Kelvin()
			if math.Abs(float64(got-wantKelvin)) > float64(wantKelvin)/100 {
				t.Errorf("round trip of %dK came back as %dK (level %v)", wantKelvin, got, level)
			}
		})
	}
}

// recordingWriter captures the values a data point sends.
type recordingWriter struct {
	calls []recordedCall
}

type recordedCall struct {
	address string
	param   hmenum.Parameter
	value   any
}

func (w *recordingWriter) SetValue(
	_ context.Context, address string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.calls = append(w.calls, recordedCall{address: address, param: p, value: v})
	return nil
}

func (w *recordingWriter) lastFor(p hmenum.Parameter, address string) (any, bool) {
	for i := len(w.calls) - 1; i >= 0; i-- {
		if w.calls[i].param == p && w.calls[i].address == address {
			return w.calls[i].value, true
		}
	}
	return nil, false
}
