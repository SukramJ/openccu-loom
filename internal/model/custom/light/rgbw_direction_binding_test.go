// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// putEnumSensorDP attaches a read-only, index-valued ENUM wire data point
// to ch — the shape ACTIVITY_STATE reports.
func putEnumSensorDP(ch *device.Channel, param hmenum.Parameter, valueList []string) *generic.Sensor[int32] {
	dp := generic.NewSensor[int32](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	})
	ch.Put(dp)
	return dp
}

// activityStateValueList mirrors the CCU's ACTIVITY_STATE VALUE_LIST.
var activityStateValueList = []string{"UNKNOWN", "UP", "DOWN", "STABLE"}

// newIPRGBWDevice builds a HmIP-RGBW-shaped device through the real
// registry: LEVEL, HUE, SATURATION and ACTIVITY_STATE on the primary
// channel, matching the IPRGBW schema's Fields map (all Bare, all on the
// light's own channel).
func newIPRGBWDevice(t *testing.T) (*RGBWLight, *device.Channel) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU2345678",
		Model:        "HmIP-RGBW",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel("VCU2345678:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloatDP(ch, hmenum.ParameterLevel)
	putEnumSensorDP(ch, hmenum.ParameterActivityState, activityStateValueList)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}

	cdp := ch.CustomDataPoint()
	r, ok := cdp.(*RGBWLight)
	if !ok {
		t.Fatalf("custom data point is %T, want *RGBWLight", cdp)
	}
	return r, ch
}

// putWritableFloatDP attaches a writable FLOAT wire data point to ch.
func putWritableFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// TestRGBWLightBindsDirection is the regression guard for FieldDirection on
// the IPRGBW schema (ACTIVITY_STATE): the profile maps it directly onto the
// light's own channel, the device carries it, and RGBWLight held no pointer
// to it at all.
func TestRGBWLightBindsDirection(t *testing.T) {
	t.Parallel()
	r, ch := newIPRGBWDevice(t)

	activity, ok := ch.Parameter(hmenum.ParameterActivityState).(*generic.Sensor[int32])
	if !ok {
		t.Fatal("ACTIVITY_STATE is not an enum sensor data point")
	}
	activity.OnEvent(1) // "UP"

	label, ok := r.ActivityState()
	if !ok {
		t.Fatal("ActivityState() reported nothing after ACTIVITY_STATE was fed — the slot is unbound")
	}
	if label != "UP" {
		t.Errorf("ActivityState() = %q, want %q", label, "UP")
	}
}
