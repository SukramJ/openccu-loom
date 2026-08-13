// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ---

// newFixedColorLightRig builds a channel with LEVEL + COLOR and returns a
// FixedColorLight. Modelled on newColorRig from color_test.go.
func newFixedColorLightRig(t *testing.T, address string, w Writer) *FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "FIXED_COLOR", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterColor, w)
	return NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
}

// newEffectLightRig builds a channel with LEVEL + HUE + SATURATION + PROGRAM
// (with a value list) and returns an EffectLight.
func newEffectLightRig(t *testing.T, address string, w Writer) *EffectLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "COLOR_DIMMER_EFFECT", hmenum.ParamsetKeyValues)

	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)

	// PROGRAM carries a ValueList so that Effect() can resolve labels.
	prog := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterProgram),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"NONE", "SLOW_COLOR_CHANGE", "FAST_COLOR_CHANGE"},
		},
		Writer: w,
	})
	ch.Put(prog)

	return NewEffectLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsEffects: true},
	})
}

// newRGBWLightRig creates a full RGBW channel and returns an RGBWLight.
func newRGBWLightRig(t *testing.T, address string, w Writer) *RGBWLight {
	t.Helper()
	ch := newRGBWRig(t, address, w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	return NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})
}

// --- 1. Light: default state OFF before any observation ---

func TestLightStatePayload_DefaultStateOff(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})

	p, _ := l.State().(*payload.LightState)
	if p == nil {
		t.Fatalf("StatePayload missing 'state' key; payload=%v", p)
	}
	if p.State != "OFF" {
		t.Errorf("unobserved light: state = %q, want OFF", p.State)
	}
	if p.Brightness != nil {
		t.Error("'brightness' must not appear before first observation")
	}
}

// --- 2. Light: ON and brightness; 0%-edge-case ---

func TestLightStatePayload_OnAndBrightness(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// 50 % level → state ON, brightness 128 (int(0.5*255+0.5) = 128)
	level.OnEvent(0.5)
	p, _ := l.State().(*payload.LightState)

	if p == nil || p.State != "ON" {
		state := ""
		if p != nil {
			state = p.State
		}
		t.Errorf("level=0.5 → state %q, want ON", state)
	}
	if p == nil || p.Brightness == nil || *p.Brightness != 128 {
		var brightness any
		if p != nil {
			brightness = p.Brightness
		}
		t.Errorf("level=0.5 → brightness %v, want 128", brightness)
	}

	// 0 % level → state OFF
	level.OnEvent(0)
	p2, _ := l.State().(*payload.LightState)
	if p2 == nil || p2.State != "OFF" {
		state := ""
		if p2 != nil {
			state = p2.State
		}
		t.Errorf("level=0 → state %q, want OFF", state)
	}
}

// --- 3. Pin: old format is_on / brightness_pct must NOT appear ---
//
// With typed structs the JSON tags are fixed at compile time, so is_on and
// brightness_pct simply cannot appear — they are not fields on LightState.
// The test remains as a compile-time / structural guard that we still return
// a *payload.LightState (not a free-form map that might include extra keys).
func TestLightStatePayload_NoIsOnNoBrightnessPctKey(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	level.OnEvent(0.75)

	p, ok := l.State().(*payload.LightState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return *payload.LightState")
	}
	// Typed struct: no extra keys can exist — guard is satisfied by type system.
}

// --- 4. ColorLight: hs emitted after hue + saturation observed ---

func TestColorLightStatePayload_HSEmitted(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cl, level, hue, sat := newColorLightRig(t, "HmIP-BSL:3", w)

	level.OnEvent(0.8)
	hue.OnEvent(int32(120))
	sat.OnEvent(0.9)

	p, _ := cl.State().(*payload.ColorLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.ColorLightState")
	}

	if p.ColorMode != "hs" {
		t.Errorf("color_mode = %q, want hs", p.ColorMode)
	}
	if p.Color == nil {
		t.Fatalf("'color' key missing or wrong type; got nil")
	}
	if p.Color.H != 120 {
		t.Errorf("color.h = %v, want 120", p.Color.H)
	}
	if p.Color.S != 90 {
		t.Errorf("color.s = %v, want 90", p.Color.S)
	}
}

// --- 5. ColorTempLight: kelvin emitted after observation ---

func TestColorTempLightStatePayload_KelvinEmitted(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ctl, level, kelvin := newColorTempLightRig(t, "HmIP-SCTH230:3", w, 2000, 6500)

	level.OnEvent(1.0)
	kelvin.OnEvent(int32(4000))

	p, _ := ctl.State().(*payload.ColorTempLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.ColorTempLightState")
	}

	if p.ColorMode != "color_temp" {
		t.Errorf("color_mode = %q, want color_temp", p.ColorMode)
	}
	if p.ColorTempKelvin == nil || *p.ColorTempKelvin != 4000 {
		t.Errorf("color_temp_kelvin = %v, want 4000", p.ColorTempKelvin)
	}
}

// --- 6. FixedColorLight: no 'color' key in StatePayload ---

func TestFixedColorLightStatePayload_NoColorKey(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	l := newFixedColorLightRig(t, "HmIP-LSC:1", w)

	p, _ := l.State().(*payload.FixedColorLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.FixedColorLightState")
	}
	// No CHANNEL_COLOR DP installed in rig, so Color must be nil.
	if p.Color != nil {
		t.Error("FixedColorLight StatePayload must not contain a 'color' key")
	}
	if p.State == "" {
		t.Error("FixedColorLight StatePayload must contain 'state'")
	}
}

// --- 7. EffectLight: effect label emitted after program observed ---

func TestEffectLightStatePayload_EffectLabelEmitted(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	el := newEffectLightRig(t, "HmIP-MP3P:2", w)

	if el.Float == nil {
		t.Fatal("EffectLight has no LEVEL DP")
	}
	el.OnEvent(0.5)
	el.hue.OnEvent(int32(60))
	el.saturation.OnEvent(0.8)
	// PROGRAM index 1 → "SLOW_COLOR_CHANGE"
	el.program.OnEvent(int32(1))

	p, _ := el.State().(*payload.EffectLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.EffectLightState")
	}

	if p.Effect != "SLOW_COLOR_CHANGE" {
		t.Errorf("effect = %q, want SLOW_COLOR_CHANGE", p.Effect)
	}
	if p.ColorMode != "hs" {
		t.Errorf("color_mode = %q, want hs alongside effect", p.ColorMode)
	}
}

// --- 8. RGBWLight: TUNABLE_WHITE drops 'color', emits kelvin ---

func TestRGBWLightStatePayload_TunableWhiteDropsColor(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	r := newRGBWLightRig(t, "HmIP-RGBW:1", w)
	r.recordMode("2_TUNABLE_WHITE")

	r.OnEvent(0.7)
	r.hue.OnEvent(int32(200))
	r.saturation.OnEvent(0.5)
	r.kelvin.OnEvent(int32(3000))

	p, _ := r.State().(*payload.RGBWLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.RGBWLightState")
	}

	if p.Color != nil {
		t.Error("TUNABLE_WHITE mode: 'color' key must be absent")
	}
	if p.ColorMode != "color_temp" {
		t.Errorf("color_mode = %q, want color_temp", p.ColorMode)
	}
	if p.ColorTempKelvin == nil || *p.ColorTempKelvin != 3000 {
		t.Errorf("color_temp_kelvin = %v, want 3000", p.ColorTempKelvin)
	}
}

// --- 9. RGBWLight: PWM sets brightness mode, no color key ---

func TestRGBWLightStatePayload_PWMSetsBrightnessMode(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	r := newRGBWLightRig(t, "HmIP-RGBW:1", w)
	r.recordMode("4_PWM")

	r.OnEvent(0.5)

	p, _ := r.State().(*payload.RGBWLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.RGBWLightState")
	}

	if p.ColorMode != "brightness" {
		t.Errorf("PWM mode: color_mode = %q, want brightness", p.ColorMode)
	}
	if p.Color != nil {
		t.Error("PWM mode: 'color' key must be absent")
	}
}

// --- 10. RGBWLight: RGBW keeps both hs color and color_temp_kelvin ---

func TestRGBWLightStatePayload_RGBWKeepsHSAndKelvin(t *testing.T) {
	t.Parallel()
	w := &colorStubWriter{}
	r := newRGBWLightRig(t, "HmIP-RGBW:1", w)
	r.recordMode("RGBW")

	r.OnEvent(1.0)
	r.hue.OnEvent(int32(30))
	r.saturation.OnEvent(0.6)
	r.kelvin.OnEvent(int32(5000))

	p, _ := r.State().(*payload.RGBWLightState)
	if p == nil {
		t.Fatal("StatePayload must return *payload.RGBWLightState")
	}

	// HS color must be present (from ColorLight base).
	if p.Color == nil {
		t.Fatalf("RGBW mode: 'color' key missing or wrong type; payload=%v", p)
	}
	if p.Color.H != 30 {
		t.Errorf("color.h = %v, want 30", p.Color.H)
	}
	// Kelvin must also be present (RGBW surfaces both).
	if p.ColorTempKelvin == nil || *p.ColorTempKelvin != 5000 {
		t.Errorf("color_temp_kelvin = %v, want 5000", p.ColorTempKelvin)
	}
}
