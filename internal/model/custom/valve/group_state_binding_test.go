// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve_test

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putValveWireDP attaches one STATE-shaped wire data point to ch, in the
// shape the CCU describes it. writable=false mirrors the read-only
// status-mirror channel most HmIP irrigation valves carry; writable=true
// mirrors the valve's own channel.
func putValveWireDP(t *testing.T, ch *device.Channel, param hmenum.Parameter, writable bool) {
	t.Helper()
	ops := hmenum.OperationsRead | hmenum.OperationsEvent
	if writable {
		ops |= hmenum.OperationsWrite
	}
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: ops,
		},
	}
	if writable {
		ch.Put(generic.NewSwitch(spec))
	} else {
		ch.Put(generic.NewBinarySensor(spec))
	}
}

// newIPIrrigationValveDevice builds an ELV-SH-WSM irrigation valve through
// the real registry: the valve sits on channel 4 (StateChannelOffset -1
// puts the profile's group_state field on channel 3), matching the
// "elv-sh-wsm" registration in profiles.go.
func newIPIrrigationValveDevice(t *testing.T, groupStateWritable bool) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "000A1BE9957783",
		Model:        "ELV-SH-WSM",
		ProductGroup: hmenum.ProductGroupUnknown,
	})
	valveCh := dev.AddChannel("000A1BE9957783:4", 4, "IRRIGATION_ACTUATOR_CHANNEL", hmenum.ParamsetKeyValues)
	groupCh := dev.AddChannel("000A1BE9957783:3", 3, "IRRIGATION_TRANSMITTER", hmenum.ParamsetKeyValues)

	putValveWireDP(t, valveCh, hmenum.ParameterState, true)
	putValveWireDP(t, groupCh, hmenum.ParameterState, groupStateWritable)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return valveCh
}

func irrigationOn(t *testing.T, ch *device.Channel) *valve.Irrigation {
	t.Helper()
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		t.Fatalf("no custom data point on %s", ch.Address)
	}
	v, ok := cdp.(*valve.Irrigation)
	if !ok {
		t.Fatalf("custom data point on %s is %T, want *valve.Irrigation", ch.Address, cdp)
	}
	return v
}

// TestIPIrrigationValveBindsGroupStateFromTheChannelTheSchemaResolvesItTo
// is the regression guard for ELV-SH-WSM: the IPIrrigationValve profile
// maps FieldGroupState onto STATE at ChannelFields[-1] (one channel below
// the valve), and nothing on [valve.Irrigation] held a pointer to the
// resolved parameter — the value was silently unreachable, exactly the
// shape TestEverySchemaFieldTheDeviceCarriesIsBound (tests/integration)
// exists to catch.
//
// Both wire shapes are exercised: the read-only BinarySensor and the
// writable Switch.
func TestIPIrrigationValveBindsGroupStateFromTheChannelTheSchemaResolvesItTo(t *testing.T) {
	t.Parallel()

	for _, writable := range []bool{false, true} {
		t.Run(fmt.Sprintf("writable=%v", writable), func(t *testing.T) {
			t.Parallel()

			valveCh := newIPIrrigationValveDevice(t, writable)
			v := irrigationOn(t, valveCh)

			if _, observed := v.GroupStateValue(); observed {
				t.Fatal("GroupStateValue() observed before any event, want unobserved")
			}

			var groupDP device.ParameterDataPoint
			for _, ch := range valveCh.Device().Channels() {
				if ch.Number == 3 {
					groupDP = ch.Parameter(hmenum.ParameterState)
				}
			}
			switch dp := groupDP.(type) {
			case *generic.Switch:
				dp.OnEvent(true)
			case *generic.BinarySensor:
				dp.OnEvent(true)
			default:
				t.Fatalf("channel 3 STATE has unexpected type %T", groupDP)
			}

			on, observed := v.GroupStateValue()
			if !observed {
				t.Fatal("GroupStateValue() not observed after feeding channel 3's STATE")
			}
			if !on {
				t.Error("GroupStateValue() = false, want true")
			}

			// The group indicator must never overwrite the valve's own
			// STATE: the two are different slots with different
			// meanings.
			if _, valveObserved := v.IsOpen(); valveObserved {
				t.Error("IsOpen() observed after only the group channel fired — group_state leaked into the valve's own state")
			}
		})
	}
}
