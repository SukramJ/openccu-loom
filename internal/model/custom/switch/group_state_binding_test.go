// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev_test

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putSwitchWireDP attaches one STATE-shaped wire data point to ch, in the
// shape the CCU describes it. writable=false mirrors the read-only
// SWITCH_TRANSMITTER status-mirror channel most HmIP switches carry;
// writable=true mirrors the relay's own SWITCH_VIRTUAL_RECEIVER channel.
func putSwitchWireDP(t *testing.T, ch *device.Channel, param hmenum.Parameter, writable bool) {
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

// newIPSwitchDevice builds an HmIP switch through the real registry,
// mirroring the HMIP-PS channel layout: the relay sits on channel 3
// (StateChannelOffset -1 puts the profile's group_state field on channel
// 2), matching the "hmip-ps" registration in profiles.go.
//
// groupStateWritable selects the wire shape of channel 2's STATE: most
// HmIP switches report it read-only (*generic.BinarySensor); a minority
// keep it writable (*generic.Switch). Both must resolve.
func newIPSwitchDevice(t *testing.T, groupStateWritable bool) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "000A1BE9957782",
		Model:        "HMIP-PS",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	relayCh := dev.AddChannel("000A1BE9957782:3", 3, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	groupCh := dev.AddChannel("000A1BE9957782:2", 2, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)

	putSwitchWireDP(t, relayCh, hmenum.ParameterState, true)
	putSwitchWireDP(t, groupCh, hmenum.ParameterState, groupStateWritable)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return relayCh
}

func switchOn(t *testing.T, ch *device.Channel) *switchdev.Switch {
	t.Helper()
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		t.Fatalf("no custom data point on %s", ch.Address)
	}
	sw, ok := cdp.(*switchdev.Switch)
	if !ok {
		t.Fatalf("custom data point on %s is %T, want *switchdev.Switch", ch.Address, cdp)
	}
	return sw
}

// TestIPSwitchBindsGroupStateFromTheChannelTheSchemaResolvesItTo is the
// regression guard for HmIP-PS-family switches whose group-level STATE
// indicator (SWITCH_TRANSMITTER, one channel below the relay) never
// reached the custom data point: the IPSwitch profile maps
// FieldGroupState onto STATE at ChannelFields[-1], and nothing on
// [switchdev.Switch] held a pointer to the resolved parameter — the value
// was silently unreachable, exactly the shape
// TestEverySchemaFieldTheDeviceCarriesIsBound (tests/integration) exists
// to catch.
//
// Both wire shapes are exercised: the read-only BinarySensor most HmIP
// switches use, and the writable Switch a minority expose.
func TestIPSwitchBindsGroupStateFromTheChannelTheSchemaResolvesItTo(t *testing.T) {
	t.Parallel()

	for _, writable := range []bool{false, true} {
		t.Run(fmt.Sprintf("writable=%v", writable), func(t *testing.T) {
			t.Parallel()

			relayCh := newIPSwitchDevice(t, writable)
			sw := switchOn(t, relayCh)

			if _, observed := sw.GroupStateValue(); observed {
				t.Fatal("GroupStateValue() observed before any event, want unobserved")
			}

			groupCh := relayCh.Device().Channels()
			var groupDP device.ParameterDataPoint
			for _, ch := range groupCh {
				if ch.Number == 2 {
					groupDP = ch.Parameter(hmenum.ParameterState)
				}
			}
			switch dp := groupDP.(type) {
			case *generic.Switch:
				dp.OnEvent(true)
			case *generic.BinarySensor:
				dp.OnEvent(true)
			default:
				t.Fatalf("channel 2 STATE has unexpected type %T", groupDP)
			}

			on, observed := sw.GroupStateValue()
			if !observed {
				t.Fatal("GroupStateValue() not observed after feeding channel 2's STATE")
			}
			if !on {
				t.Error("GroupStateValue() = false, want true")
			}

			// The group indicator must never overwrite the relay's own
			// STATE: the two are different slots with different
			// meanings.
			if _, relayObserved := sw.IsOn(); relayObserved {
				t.Error("IsOn() observed after only the group channel fired — group_state leaked into the relay's own state")
			}
		})
	}
}
