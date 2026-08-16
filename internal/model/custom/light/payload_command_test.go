// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putEffectProgram registers a PROGRAM data point that carries a
// VALUE_LIST, which is where [EffectLight.Effects] sources its labels.
func putEffectProgram(ch *device.Channel, address string, w Writer, effects []string) {
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterProgram),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  effects,
		},
		Writer: w,
	}))
}

// findCall returns the last wire write for the given parameter.
func findCall(t *testing.T, w *colorStubWriter, p hmenum.Parameter) any {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.calls) - 1; i >= 0; i-- {
		if w.calls[i].param == p {
			return w.calls[i].value
		}
	}
	t.Fatalf("no wire write for %s (calls=%+v)", p, w.calls)
	return nil
}

// TestSetLevelAppliesHAColor pins that a colour picked in Home Assistant
// reaches the wire. The discovery payload gives every light one
// command_topic pointing at set_level, so HA's JSON-schema light posts
// the whole object — colour included — there; a handler that reads only
// state/brightness silently turns the lamp on and drops the colour.
func TestSetLevelAppliesHAColor(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, Dimmable: true}
	ch := newColorRig(t, "HmIP-RGBW:3", w, caps)
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: caps})

	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state": "ON",
		"color": map[string]any{"h": float64(120), "s": float64(50)},
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if got := findCall(t, w, hmenum.ParameterHue); got != int32(120) {
		t.Errorf("HUE = %v, want 120", got)
	}
	// The wire carries the 0..1 fraction of HA's 0..100 saturation.
	if got := findCall(t, w, hmenum.ParameterSaturation); got != 0.5 {
		t.Errorf("SATURATION = %v, want 0.5", got)
	}
}

// TestSetLevelAppliesHAColorTemp pins the colour-temperature axis of the
// same payload.
func TestSetLevelAppliesHAColorTemp(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	caps := custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}
	ch := newColorTempRig(t, "HmIP-BSL:4", w, caps, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: caps}, 2700, 6500)

	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state":             "ON",
		"color_temp_kelvin": float64(4000),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if got := findCall(t, w, hmenum.ParameterColorTemperature); got != int32(4000) {
		t.Errorf("COLOR_TEMPERATURE = %v, want 4000", got)
	}
}

// TestSetLevelAppliesHAColorTempMireds covers the mired form HA falls
// back to when it does not use the kelvin key.
func TestSetLevelAppliesHAColorTempMireds(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	caps := custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}
	ch := newColorTempRig(t, "HmIP-BSL:4", w, caps, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: caps}, 2700, 6500)

	// 250 mireds = 4000 K.
	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state":      "ON",
		"color_temp": float64(250),
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if got := findCall(t, w, hmenum.ParameterColorTemperature); got != int32(4000) {
		t.Errorf("COLOR_TEMPERATURE = %v, want 4000", got)
	}
}

// TestSetLevelAppliesHAEffect pins the effect axis: HA sends the label it
// read from effect_list.
func TestSetLevelAppliesHAEffect(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, SupportsEffects: true, Dimmable: true}
	effects := []string{"NONE", "SLOW_COLOR_CHANGE", "CAMPFIRE"}
	ch := newColorRig(t, "HM-LC-RGBW-WM:1", w, caps)
	putEffectProgram(ch, "HM-LC-RGBW-WM:1", w, effects)
	l := NewEffectLight(Config{Channel: ch, Writer: w, Capabilities: caps})

	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state":  "ON",
		"effect": "CAMPFIRE",
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if got := findCall(t, w, hmenum.ParameterProgram); got != int32(2) {
		t.Errorf("PROGRAM = %v, want 2 (CAMPFIRE)", got)
	}
}

// TestSetLevelOffIgnoresAttributes pins that a switch-off stays a plain
// switch-off — HA sends {"state":"OFF"} alone, and nothing else may be
// derived from it.
func TestSetLevelOffIgnoresAttributes(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, Dimmable: true}
	ch := newColorRig(t, "HmIP-RGBW:3", w, caps)
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: caps})

	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state": "OFF",
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.calls {
		if c.param == hmenum.ParameterHue || c.param == hmenum.ParameterSaturation {
			t.Fatalf("switch-off wrote a colour parameter: %+v", c)
		}
	}
}

// TestEffectLightSetEffectAcceptsAdvertisedKey pins that set_effect
// accepts the argument key it advertises — as its scalar-arg key and in
// LocalisableSelections — rather than a second, undeclared one.
func TestEffectLightSetEffectAcceptsAdvertisedKey(t *testing.T) {
	t.Parallel()
	effects := []string{"NONE", "SLOW_COLOR_CHANGE", "CAMPFIRE"}
	caps := custom.LightCapabilities{SupportsColor: true, SupportsEffects: true, Dimmable: true}

	cases := []struct {
		name  string
		param any
		want  int32
	}{
		{"label", "CAMPFIRE", 2},
		{"numeric string", "1", 1},
		{"index", float64(1), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &colorStubWriter{}
			ch := newColorRig(t, "HM-LC-RGBW-WM:1", w, caps)
			putEffectProgram(ch, "HM-LC-RGBW-WM:1", w, effects)
			l := NewEffectLight(Config{Channel: ch, Writer: w, Capabilities: caps})

			err := l.Invoke(context.Background(), "set_effect",
				map[string]any{"effect": tc.param}, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("set_effect(%v): %v", tc.param, err)
			}
			if got := findCall(t, w, hmenum.ParameterProgram); got != tc.want {
				t.Errorf("PROGRAM = %v, want %d", got, tc.want)
			}
		})
	}
}

// TestFixedColorLightSetColorAcceptsHS pins the HA path for a discrete
// colour slot: the discovery payload projects the slot onto HA's `hs`
// mode, so the command arrives as a free hue/saturation pair and has to
// snap onto the nearest slot instead of being rejected.
func TestFixedColorLightSetColorAcceptsHS(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BSL:8", 8, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "HmIP-BSL:8", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "HmIP-BSL:8", hmenum.ParameterColor, w, fixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})

	err := l.Invoke(context.Background(), "set_level", map[string]any{
		"state": "ON",
		"color": map[string]any{"h": float64(120), "s": float64(100)},
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if got := findCall(t, w, hmenum.ParameterColor); got != "GREEN" {
		t.Errorf("COLOR = %v, want GREEN", got)
	}
}
