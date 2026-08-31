// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct{}

func (stubWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func levelDP(address string, w cover.Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

// TestCoverSubscribeRoutesDirectionUpdates covers the schema-less shape:
// no profile group, motion on the cover's own channel. The resolver's
// own-channel fallback answers that case, so this test cannot tell a
// schema-resolved binding from an own-channel lookup — which is why it
// stayed green while the two disagreed on every HmIP cover. The
// distinguishing case needs a real device topology and lives in
// tests/contract/cover_motion_channel_test.go.
func TestCoverSubscribeRoutesDirectionUpdates(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch.Put(levelDP(ch.Address, stubWriter{}))

	dirDP := generic.NewIntegerSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "DIRECTION"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(dirDP)

	c := cover.New(cover.Config{Channel: ch, Writer: stubWriter{}, Capabilities: custom.CoverCapabilities{}})
	ch.SetCustomDataPoint(c)

	// Push a DIRECTION=1 (up) update via the channel parameter — the
	// Cover should observe it through Subscribe → OnDirection.
	dirDP.OnEvent(1)

	got, observed := c.Direction()
	if !observed {
		t.Fatalf("Direction should be observed after DIRECTION update")
	}
	if got != cover.DirectionUp {
		t.Fatalf("Direction = %v want DirectionUp(1)", got)
	}
	if !c.IsOpening() {
		t.Fatalf("Cover should report IsOpening after DIRECTION=Up")
	}
}

// TestCoverSubscribeRoutesActivityStateUpdates verifies the HmIP
// fallback: a channel without DIRECTION but with ACTIVITY_STATE (same
// UP/DOWN indices) still feeds Cover.OnDirection through Subscribe, so
// IsOpening / IsClosing work on HmIP actuators too.
func TestCoverSubscribeRoutesActivityStateUpdates(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch.Put(levelDP(ch.Address, stubWriter{}))

	actDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActivityState)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "UP", "DOWN", "STABLE"},
		},
	})
	ch.Put(actDP)

	c := cover.New(cover.Config{Channel: ch, Writer: stubWriter{}, Capabilities: custom.CoverCapabilities{}})
	ch.SetCustomDataPoint(c)

	actDP.OnEvent(2) // DOWN

	got, observed := c.Direction()
	if !observed {
		t.Fatalf("Direction should be observed after ACTIVITY_STATE update")
	}
	if got != cover.DirectionDown {
		t.Fatalf("Direction = %v want DirectionDown(2)", got)
	}
	if !c.IsClosing() {
		t.Fatalf("Cover should report IsClosing after ACTIVITY_STATE=DOWN")
	}
}

func TestCoverSetCustomDataPointReleasesPriorSubscription(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch.Put(levelDP(ch.Address, stubWriter{}))

	dirDP := generic.NewIntegerSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "DIRECTION"},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(dirDP)

	c1 := cover.New(cover.Config{Channel: ch, Writer: stubWriter{}, Capabilities: custom.CoverCapabilities{}})
	ch.SetCustomDataPoint(c1)
	c2 := cover.New(cover.Config{Channel: ch, Writer: stubWriter{}, Capabilities: custom.CoverCapabilities{}})
	ch.SetCustomDataPoint(c2)

	dirDP.OnEvent(2)

	if d, _ := c1.Direction(); d == cover.DirectionDown {
		t.Fatalf("first cover should no longer receive updates after replacement")
	}
	if d, _ := c2.Direction(); d != cover.DirectionDown {
		t.Fatalf("second cover should receive DIRECTION=Down, got %v", d)
	}
}
