// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putReadOnlyFloatDP attaches a read-only (sensor-shaped) FLOAT wire data
// point to ch — the shape the group/state channel's LEVEL and LEVEL_2
// report on the HmIP blind families (read+write on the action channel,
// read-only on the state channel).
func putReadOnlyFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// newIPBlindDeviceWithGroupChannel builds a HmIP-BROLL-shaped device
// through the real registry: a MAINTENANCE channel, the IPCover profile's
// state channel (offset -1 from the primary), and the primary action
// channel carrying LEVEL + LEVEL_2. The primary channel is registered
// under "hmip-broll" in profiles.go with base channel 4, so the state
// channel lands on 3.
func newIPBlindDeviceWithGroupChannel(t *testing.T) (blind *cover.Blind, stateCh *device.Channel) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU1234567",
		Model:        "HmIP-BROLL",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	dev.AddChannel("VCU1234567:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	state := dev.AddChannel("VCU1234567:3", 3, "SHUTTER_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	putReadOnlyFloatDP(state, hmenum.ParameterLevel)
	putReadOnlyFloatDP(state, hmenum.ParameterLevel2)
	action := dev.AddChannel("VCU1234567:4", 4, "SHUTTER_CONTACT", hmenum.ParamsetKeyValues)
	putCoverFloatDP(action, hmenum.ParameterLevel)
	putCoverFloatDP(action, hmenum.ParameterLevel2)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}

	cdp := action.CustomDataPoint()
	b, ok := cdp.(*cover.Blind)
	if !ok {
		t.Fatalf("custom data point is %T, want *cover.Blind", cdp)
	}
	return b, state
}

// TestBlindTiltFollowsGroupChannelLevel2 is the regression guard for
// GROUP_LEVEL_2: the IPCover schema maps it onto the group/state channel's
// LEVEL_2, mirroring GROUP_LEVEL for the position axis — but nothing bound
// the tilt half of that mirror. With the toggle on (the default),
// TiltPosition must read from the state channel's LEVEL_2 instead of the
// blind's own channel.
func TestBlindTiltFollowsGroupChannelLevel2(t *testing.T) {
	t.Parallel()
	b, state := newIPBlindDeviceWithGroupChannel(t)

	stateLevel2, castOK := state.Parameter(hmenum.ParameterLevel2).(*generic.Float)
	if !castOK {
		t.Fatal("state channel LEVEL_2 is not a float data point")
	}
	stateLevel2.OnEvent(0.42)

	tilt, ok := b.TiltPosition()
	if !ok {
		t.Fatal("TiltPosition() reported nothing after the group channel's LEVEL_2 was fed — GROUP_LEVEL_2 is unbound")
	}
	if got := tilt.Level(); got != 0.42 {
		t.Errorf("TiltPosition().Level() = %v, want 0.42 (from the group channel)", got)
	}
}
