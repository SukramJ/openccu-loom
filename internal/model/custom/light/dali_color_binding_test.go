// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putWritableIntegerDP attaches a writable INTEGER wire data point to ch.
func putWritableIntegerDP(ch *device.Channel, param hmenum.Parameter) *generic.Integer {
	dp := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// newIPDRGDALIDevice builds a HmIP-DRG-DALI channel through the real
// registry: LEVEL, HUE and SATURATION, matching the IPDRGDALI schema's
// Fields map — both colour axes are Bare-mapped onto the light's own
// channel, and the wire fleet carries them (UNIVERSAL_LIGHT_RECEIVER
// reports HUE and SATURATION on every DALI channel, contradicting the
// package's own "DALI does not carry RGB" assumption — see the reference
// CustomDpIpDrgDaliLight, which binds the same pair).
func newIPDRGDALIDevice(t *testing.T) (*DRGDaliLight, *device.Channel) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU4567890",
		Model:        "HmIP-DRG-DALI",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel("VCU4567890:1", 1, "UNIVERSAL_LIGHT_RECEIVER", hmenum.ParamsetKeyValues)
	putWritableFloatDP(ch, hmenum.ParameterLevel)
	putWritableIntegerDP(ch, hmenum.ParameterHue)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSaturation),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}

	cdp := ch.CustomDataPoint()
	dali, ok := cdp.(*DRGDaliLight)
	if !ok {
		t.Fatalf("custom data point is %T, want *DRGDaliLight", cdp)
	}
	return dali, ch
}

// TestDRGDaliLightBindsHueAndSaturation is the regression guard for
// FieldHue / FieldSaturation on the IPDRGDALI schema: the profile maps both
// directly onto the light's own channel, HmIP-DRG-DALI carries HUE and
// SATURATION on every channel, and DRGDaliLight held no pointer to either —
// it composed only ColorTempLight, on the assumption DALI has no RGB axis.
func TestDRGDaliLightBindsHueAndSaturation(t *testing.T) {
	t.Parallel()
	dali, ch := newIPDRGDALIDevice(t)

	hueDP, ok := ch.Parameter(hmenum.ParameterHue).(*generic.Integer)
	if !ok {
		t.Fatal("HUE is not an integer data point")
	}
	hueDP.OnEvent(120)

	satDP, ok := ch.Parameter(hmenum.ParameterSaturation).(*generic.Float)
	if !ok {
		t.Fatal("SATURATION is not a float data point")
	}
	satDP.OnEvent(0.75)

	hue, sat, ok := dali.HSColor()
	if !ok {
		t.Fatal("HSColor() reported nothing after HUE/SATURATION were fed — the slots are unbound")
	}
	if hue != 120 {
		t.Errorf("HSColor() hue = %v, want 120", hue)
	}
	if sat != 0.75 {
		t.Errorf("HSColor() saturation = %v, want 0.75", sat)
	}
}
