// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// cover_motion_channel_test.go pins the single binding of a Cover's motion
// parameter across the two consumers that read it.
//
// A Cover reads motion twice: the constructor resolves it through the
// profile schema (custom.ResolveSlotOnCarryingChannel) and hangs the Matter
// cluster DataVersion hook off the result, while the state path feeds
// Direction / IsOpening / IsClosing. On the HmIP cover families the schema
// puts the field on the shared transmitter channel — StateChannelOffset -1
// — while the custom data point itself hangs off a virtual-receiver
// channel, so a second, own-channel lookup binds a DIFFERENT data point
// than the schema-resolved one and the two consumers observe different CCU
// events on the same device. This guard asserts they observe the same one.
//
// The device is driven through the real custom.CreateCustomDataPoints
// pipeline so group-number assignment, profile rebasing and the
// SetCustomDataPoint → Subscribe handshake all happen in production code;
// constructing a Cover and calling Subscribe by hand would prove only that
// the collaboration can happen, never that materialisation makes it happen.
package contract

import (
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// coverMotionLevelDP attaches a LEVEL float to the channel.
func coverMotionLevelDP(ch *device.Channel, writable bool) {
	ops := hmenum.OperationsRead | hmenum.OperationsEvent
	if writable {
		ops |= hmenum.OperationsWrite
	}
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: ops},
	}))
}

// coverMotionEnumDP attaches a read-only index-valued ENUM sensor and
// returns it so the test can push wire events through it.
func coverMotionEnumDP(ch *device.Channel, param hmenum.Parameter, values []string) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  values,
		},
	})
	ch.Put(dp)
	return dp
}

// coverMotionActivityStateValues is the HmIP ACTIVITY_STATE VALUE_LIST;
// index 1 is UP and index 2 is DOWN.
var coverMotionActivityStateValues = []string{"UNKNOWN", "UP", "DOWN", "STABLE"}

// coverMotionBrollDevice builds an HmIP-BROLL-shaped device: channel 3 is
// the SHUTTER_TRANSMITTER (the IPCover schema's state channel, offset -1
// from the group), channels 4/5/6 are the SHUTTER_VIRTUAL_RECEIVERs that
// carry the Cover. Both the transmitter and every receiver carry
// ACTIVITY_STATE, exactly as the shipped model snapshot describes — which
// is what makes the two resolutions distinguishable at all.
//
// Returns the materialised Cover on channel 4 plus the ACTIVITY_STATE
// sensors of the transmitter and of channel 4.
func coverMotionBrollDevice(t *testing.T) (cov *cover.Cover, transmitter, receiver *generic.Sensor[int32]) {
	t.Helper()

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "COVERMOTION",
		Model:       "hmip-broll",
	})
	dev.AddChannel("COVERMOTION:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	dev.AddChannel("COVERMOTION:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dev.AddChannel("COVERMOTION:2", 2, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	tx := dev.AddChannel("COVERMOTION:3", 3, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)
	coverMotionLevelDP(tx, false)
	transmitter = coverMotionEnumDP(tx, hmenum.ParameterActivityState, coverMotionActivityStateValues)

	var rx4 *device.Channel
	for no := 4; no <= 6; no++ {
		rx := dev.AddChannel("COVERMOTION:"+strconv.Itoa(no), no, "SHUTTER_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
		coverMotionLevelDP(rx, true)
		dp := coverMotionEnumDP(rx, hmenum.ParameterActivityState, coverMotionActivityStateValues)
		if no == 4 {
			rx4, receiver = rx, dp
		}
	}

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}
	got, ok := rx4.CustomDataPoint().(*cover.Cover)
	if !ok || got == nil {
		t.Fatalf("channel 4 carries %T, want *cover.Cover — the IPCover profile no longer materialises here", rx4.CustomDataPoint())
	}
	return got, transmitter, receiver
}

// TestCoverMotionBindsTheSchemaResolvedChannel asserts that the state path
// (Direction / IsOpening) and the Matter DataVersion path observe the SAME
// motion data point — the one the profile schema names.
func TestCoverMotionBindsTheSchemaResolvedChannel(t *testing.T) {
	t.Run("transmitter push drives both consumers", func(t *testing.T) {
		cov, transmitter, _ := coverMotionBrollDevice(t)

		// Snapshot immediately before the motion push: LEVEL updates bump
		// the same counter, so anything in between would make this arm noise.
		before := cov.MatterDataVersion()
		transmitter.OnEvent(1) // UP

		got, observed := cov.Direction()
		if !observed {
			t.Fatalf("Direction not observed after transmitter ACTIVITY_STATE push — the state path is bound to a different data point than the schema-resolved one")
		}
		if got != cover.DirectionUp {
			t.Fatalf("Direction = %v, want DirectionUp", got)
		}
		if !cov.IsOpening() {
			t.Fatalf("IsOpening = false after transmitter ACTIVITY_STATE=UP")
		}
		if cov.MatterDataVersion() == before {
			t.Fatalf("MatterDataVersion unchanged (%d) after transmitter ACTIVITY_STATE push — the Matter path did not observe the same event as the state path", before)
		}
	})

	// The deliberate consequence of binding the schema-resolved channel:
	// a receiver-channel push is NOT the cover's motion source. Asserting
	// that both consumers stay silent together is a tautology (silence
	// always agrees), so this arm states the contract explicitly.
	t.Run("receiver push drives neither consumer", func(t *testing.T) {
		cov, _, receiver := coverMotionBrollDevice(t)

		before := cov.MatterDataVersion()
		receiver.OnEvent(2) // DOWN on the cover's OWN channel

		if got, observed := cov.Direction(); observed {
			t.Fatalf("Direction observed as %v after a receiver-channel ACTIVITY_STATE push — motion must come from the schema-resolved transmitter channel only", got)
		}
		if cov.IsClosing() {
			t.Fatalf("IsClosing = true after a receiver-channel ACTIVITY_STATE push")
		}
		if cov.MatterDataVersion() != before {
			t.Fatalf("MatterDataVersion moved from %d to %d on a receiver-channel push", before, cov.MatterDataVersion())
		}
	})

	// Negative control: the classic-RF profile maps DIRECTION under
	// `Fields`, i.e. onto the cover's own channel with no state-channel
	// offset. The schema resolution and the own-channel lookup agree
	// there, so this arm must stay green through any change to the
	// binding — if it goes red, the schema-less / own-channel path broke,
	// not the schema path.
	t.Run("classic RF cover keeps its own channel", func(t *testing.T) {
		dev := device.New(device.Config{
			InterfaceID: "BidCos-RF",
			Interface:   hmenum.InterfaceBidCosRF,
			Address:     "COVERMOTIONRF",
			Model:       "hm-lc-bl1-fm",
		})
		dev.AddChannel("COVERMOTIONRF:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
		ch := dev.AddChannel("COVERMOTIONRF:1", 1, "BLIND", hmenum.ParamsetKeyValues)
		coverMotionLevelDP(ch, true)
		dirDP := coverMotionEnumDP(ch, hmenum.ParameterDirection, []string{"NONE", "UP", "DOWN", "UNDEFINED"})

		if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
			t.Fatalf("CreateCustomDataPoints: %v", err)
		}
		cov, ok := ch.CustomDataPoint().(*cover.Cover)
		if !ok || cov == nil {
			t.Fatalf("channel 1 carries %T, want *cover.Cover", ch.CustomDataPoint())
		}

		dirDP.OnEvent(2) // DOWN

		got, observed := cov.Direction()
		if !observed {
			t.Fatalf("Direction not observed after own-channel DIRECTION push on a classic-RF cover")
		}
		if got != cover.DirectionDown || !cov.IsClosing() {
			t.Fatalf("Direction = %v / IsClosing = %v, want DirectionDown / true", got, cov.IsClosing())
		}
	})
}
