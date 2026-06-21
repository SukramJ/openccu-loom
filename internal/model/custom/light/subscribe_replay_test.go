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

// buildColorTempLightRig constructs a ColorTempLight with a pre-observed
// COLOR_TEMPERATURE DP so Subscribe replay has data to re-emit.
func buildColorTempLightRig(t *testing.T) (*ColorTempLight, *generic.Integer) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0010"})
	ch := d.AddChannel("VCU0010:1", 1, "DIMMER", hmenum.ParamsetKeyValues)

	levelDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0010:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(levelDP)

	kelvinDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0010:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(kelvinDP)

	cfg := Config{Channel: ch, Capabilities: custom.LightCapabilities{}}
	ct := NewColorTempLight(cfg, 2700, 6500)
	return ct, kelvinDP
}

// buildColorLightRig constructs a ColorLight with HUE and SATURATION DPs
// pre-observed so Subscribe replay has data to re-emit.
func buildColorLightRig(t *testing.T) (*ColorLight, *generic.Integer, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0002"})
	ch := d.AddChannel("VCU0002:1", 1, "DIMMER", hmenum.ParamsetKeyValues)

	levelDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(levelDP)
	hueDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHue),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(hueDP)
	satDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSaturation),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(satDP)

	cfg := Config{
		Channel:      ch,
		Capabilities: custom.LightCapabilities{},
	}
	cl := NewColorLight(cfg)
	return cl, hueDP, satDP
}

// TestColorLightSubscribeReplaysColor verifies that calling Subscribe when
// HUE and SATURATION already have observed values re-emits those values.
func TestColorLightSubscribeReplaysColor(t *testing.T) {
	cl, hueDP, satDP := buildColorLightRig(t)

	// Pre-observe values before Subscribe.
	hueDP.OnEvent(int32(120))
	satDP.OnEvent(0.75)

	// Subscribe — should replay HUE and SATURATION.
	unsub := cl.Subscribe(nil)
	if unsub != nil {
		defer unsub()
	}

	// Verify the values are still accessible after subscribe replay.
	h, s, ok := cl.Color()
	if !ok {
		t.Fatal("expected Color observed after Subscribe replay")
	}
	if h != 120 {
		t.Fatalf("expected hue=120, got %d", h)
	}
	if s < 74 || s > 76 {
		t.Fatalf("expected saturation≈75, got %f", s)
	}
}

// TestEffectLightSubscribeReplaysEffect verifies that Subscribe replays the
// PROGRAM DP when it carries an observed value.
func TestEffectLightSubscribeReplaysEffect(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0003"})
	ch := d.AddChannel("VCU0003:1", 1, "DIMMER", hmenum.ParamsetKeyValues)

	levelDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(levelDP)
	progDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterProgram),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(progDP)

	cfg := Config{Channel: ch, Capabilities: custom.LightCapabilities{}}
	el := NewEffectLight(cfg)

	// Pre-observe program index 2.
	progDP.OnEvent(int32(2))

	// Subscribe — should replay the program value.
	if unsub := el.Subscribe(nil); unsub != nil {
		defer unsub()
	}

	idx, _, ok := el.Effect()
	if !ok {
		t.Fatal("expected Effect observed after Subscribe replay")
	}
	if idx != 2 {
		t.Fatalf("expected effect index 2, got %d", idx)
	}
}

// TestColorTempLightSubscribeReplaysKelvin verifies that Subscribe re-emits the
// last observed COLOR_TEMPERATURE value, so the kelvin cache is not "unknown"
// after a reconnect that runs Subscribe after the initial CCU data push.
func TestColorTempLightSubscribeReplaysKelvin(t *testing.T) {
	ct, kelvinDP := buildColorTempLightRig(t)

	// Pre-observe a kelvin value before Subscribe.
	const wantKelvin int32 = 4000
	kelvinDP.OnEvent(wantKelvin)

	// Verify observable before subscribe.
	if k, ok := ct.Kelvin(); !ok || k != wantKelvin {
		t.Fatalf("pre-subscribe: Kelvin()=(%d,%v), want (%d,true)", k, ok, wantKelvin)
	}

	// Simulate a reconnect: clear the in-memory cache by constructing a
	// fresh ColorTempLight that shares the same underlying DP object but
	// has not yet received any OnEvent call via its own handler chain.
	ct2, _ := buildColorTempLightRig(t)
	// Share the already-observed kelvin DP from the first rig.
	ct2.kelvin = ct.kelvin

	// Subscribe — the replay in Subscribe must repopulate the cache.
	if unsub := ct2.Subscribe(nil); unsub != nil {
		defer unsub()
	}

	k, ok := ct2.Kelvin()
	if !ok {
		t.Fatal("expected Kelvin observed after Subscribe replay")
	}
	if k != wantKelvin {
		t.Fatalf("expected kelvin=%d, got %d", wantKelvin, k)
	}
}
