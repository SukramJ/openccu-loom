// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The colour-temperature range the CCU itself declares for the three
// devices whose profiles carry COLOR_TEMPERATURE on the light channel.
//
// Source: the simulated-fleet paramset descriptions,
// ../godevccu/internal/embed/data/paramset_descriptions/HmIP-RGBW.json
// (VCU5629873:1..:4 VALUES COLOR_TEMPERATURE {MIN:1000, MAX:10200,
// UNIT:"K"}), identically HmIP-DRG-DALI.json (VCU7338277:1..:48) and
// HmIP-LSC.json (VCU7603954:1). The bounds are read from the descriptor
// rather than assumed, so this fixture carries device data instead of
// the convention it is meant to check.
const (
	hmLgtDeclaredMinKelvin int32 = 1000
	hmLgtDeclaredMaxKelvin int32 = 10200
)

// hmLgtRGBWChannelWithDeclaredKelvin builds an RGBW light channel whose
// COLOR_TEMPERATURE descriptor carries the MIN/MAX the CCU declares.
func hmLgtRGBWChannelWithDeclaredKelvin(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterEffect, w)
	hmLgtPutDeclaredColorTemperature(ch, address, w)
	return ch
}

// hmLgtPutDeclaredColorTemperature puts a COLOR_TEMPERATURE data point
// whose descriptor carries the CCU-declared MIN/MAX, so a fixture cannot
// silently agree with a hard-coded range by omitting the bounds.
func hmLgtPutDeclaredColorTemperature(ch *device.Channel, address string, w Writer) {
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Unit:       "K",
			Min:        json.RawMessage("1000"),
			Max:        json.RawMessage("10200"),
		},
		Writer: w,
	}))
}

// An RGBW light's advertised colour-temperature range must be the range
// its own COLOR_TEMPERATURE descriptor declares — the same rule
// [kelvinBoundsFromChannel] already applies for ColorTempLight. A
// hard-coded pair makes the daemon publish min_kelvin/max_kelvin the
// device contradicts and clamps user requests the hardware accepts.
func TestHmLgtRGBWKelvinBoundsComeFromTheDescriptor(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := hmLgtRGBWChannelWithDeclaredKelvin(t, "HmIP-RGBW:1", w)
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})

	if r.MinKelvin != hmLgtDeclaredMinKelvin || r.MaxKelvin != hmLgtDeclaredMaxKelvin {
		t.Fatalf("RGBW bounds = %d..%d K, but the COLOR_TEMPERATURE descriptor declares %d..%d K",
			r.MinKelvin, r.MaxKelvin, hmLgtDeclaredMinKelvin, hmLgtDeclaredMaxKelvin)
	}

	// A request inside the declared range must reach the wire unclamped.
	// An unobserved DEVICE_OPERATION_MODE resolves to RGBW, which carries
	// colour temperature.
	if err := r.SetKelvin(context.Background(), 1500, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetKelvin(1500): %v", err)
	}
	var wrote int32 = -1
	for _, c := range w.calls {
		if c.param == hmenum.ParameterColorTemperature {
			if v, ok := c.value.(int32); ok {
				wrote = v
			}
		}
	}
	if wrote != 1500 {
		t.Fatalf("SetKelvin(1500) wrote %d K — the device declares a %d K minimum", wrote, hmLgtDeclaredMinKelvin)
	}
}

// The DALI constructor resolves the same datum, so it must read the same
// source rather than pass literals.
func TestHmLgtDaliKelvinBoundsComeFromTheDescriptor(t *testing.T) {
	t.Parallel()

	w := &colorStubWriter{}
	ch := hmLgtRGBWChannelWithDeclaredKelvin(t, "HmIP-DRG-DALI:1", w)
	dp, err := newDaliConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("newDaliConstructor: %v", err)
	}
	l, ok := dp.(*DRGDaliLight)
	if !ok {
		t.Fatalf("constructor returned %T, want *DRGDaliLight", dp)
	}
	if l.MinKelvin != hmLgtDeclaredMinKelvin || l.MaxKelvin != hmLgtDeclaredMaxKelvin {
		t.Fatalf("DALI bounds = %d..%d K, but the COLOR_TEMPERATURE descriptor declares %d..%d K",
			l.MinKelvin, l.MaxKelvin, hmLgtDeclaredMinKelvin, hmLgtDeclaredMaxKelvin)
	}
}
