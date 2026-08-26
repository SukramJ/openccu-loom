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

// buildFixedColorLightRig constructs a FixedColorLight with COLOR and
// CHANNEL_COLOR DPs so Subscribe replay has data to re-emit.
func buildFixedColorLightRig(t *testing.T) (*FixedColorLight, *generic.Select, *generic.Sensor[string]) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0020"})
	ch := d.AddChannel("VCU0020:1", 1, "RGBW_COLOR", hmenum.ParamsetKeyValues)

	levelDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0020:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(levelDP)

	colorDP := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0020:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColor),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE"},
		},
	})
	ch.Put(colorDP)

	chanColorDP := generic.NewSensor[string](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0020:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterChannelColor),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(chanColorDP)

	cfg := Config{Channel: ch, Capabilities: custom.LightCapabilities{}}
	fc := NewFixedColorLight(cfg)
	return fc, colorDP, chanColorDP
}

// TestFixedColorLightSubscribeReplaysColor verifies that calling Subscribe
// when COLOR and CHANNEL_COLOR already carry observed values replays those
// values so the cache is not stale after a reconnect.
func TestFixedColorLightSubscribeReplaysColor(t *testing.T) {
	t.Parallel()
	fc, colorDP, chanColorDP := buildFixedColorLightRig(t)

	// Pre-observe values before Subscribe.
	colorDP.OnEvent(int32(FixedColorBlue)) // index 4
	chanColorDP.OnEvent("BLUE")

	// Subscribe — should replay COLOR and CHANNEL_COLOR.
	if unsub := fc.Subscribe(nil); unsub != nil {
		defer unsub()
	}

	// COLOR replay.
	c, ok := fc.Color()
	if !ok {
		t.Fatal("expected Color observed after Subscribe replay")
	}
	if c != FixedColorBlue {
		t.Fatalf("expected FixedColorBlue (%d), got %d", FixedColorBlue, c)
	}

	// CHANNEL_COLOR replay.
	name, ok := fc.ColorName()
	if !ok {
		t.Fatal("expected ColorName observed after Subscribe replay")
	}
	if name != "BLUE" {
		t.Fatalf("expected ColorName=%q, got %q", "BLUE", name)
	}
}

// TestFixedColorLightSubscribeUnobservedIsNoop verifies that Subscribe on a
// FixedColorLight with no pre-observed values does not panic and the accessor
// returns (_, false).
func TestFixedColorLightSubscribeUnobservedIsNoop(t *testing.T) {
	t.Parallel()
	fc, _, _ := buildFixedColorLightRig(t)

	if unsub := fc.Subscribe(nil); unsub != nil {
		defer unsub()
	}

	if _, ok := fc.Color(); ok {
		t.Error("expected Color unobserved when no DPs have been observed before Subscribe")
	}
}
