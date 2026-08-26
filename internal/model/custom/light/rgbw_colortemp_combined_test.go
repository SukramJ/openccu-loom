// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the HmIP-LSC colorTempCombined branch of RGBWLight: hs colour
// and colour temperature are both wired at once (no DEVICE_OPERATION_MODE),
// and the active HA/Matter colour mode follows whichever axis currently
// carries an observed, non-zero KELVIN value.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newLSCLightRig builds an HmIP-LSC channel — same wire shape as
// newRGBWLightRigCh (LEVEL/HUE/SATURATION/COLOR_TEMPERATURE/EFFECT) but with
// Model set to "HmIP-LSC" and no DEVICE_OPERATION_MODE parameter, since the
// LSC hardware never carries one.
func newLSCLightRig(t *testing.T, w Writer) *RGBWLight {
	t.Helper()
	address := "LSC0001:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LSC00001", Model: "HmIP-LSC"})
	ch := d.AddChannel(address, 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterColorTemperature, w)
	putWritableIntegerWithValueList(ch, address, hmenum.ParameterEffect, w, []string{"OFF", "EFFECT1", "EFFECT2"})
	r := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsColorTemp: true}})
	unsub := r.Subscribe(ch)
	t.Cleanup(unsub)
	return r
}

// TestLSCColorTempCombinedFlagSet verifies the model-keyed flag is set only
// for the HmIP-LSC rig and not for a plain HmIP-RGBW (no Model set).
func TestLSCColorTempCombinedFlagSet(t *testing.T) {
	w := &colorStubWriter{}
	lsc := newLSCLightRig(t, w)
	if !lsc.colorTempCombined {
		t.Error("colorTempCombined must be true for an HmIP-LSC device")
	}

	rgbw := newRGBWLightRigCh(t, "RGBW0001:1", w, 1)
	if rgbw.colorTempCombined {
		t.Error("colorTempCombined must be false for a plain RGBW device")
	}
}

// TestLSCHasColorAndColorTemperatureAlwaysTrue verifies HasColor,
// HasHsColor and HasColorTemperature are unconditionally true on the
// combined light, both before and after a kelvin value is observed.
func TestLSCHasColorAndColorTemperatureAlwaysTrue(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	if !r.HasColor() {
		t.Error("HasColor() must be true before kelvin is observed")
	}
	if !r.HasHsColor() {
		t.Error("HasHsColor() must be true before kelvin is observed")
	}
	if !r.HasColorTemperature() {
		t.Error("HasColorTemperature() must be true before kelvin is observed")
	}

	r.kelvin.OnEvent(int32(4000))

	if !r.HasColor() {
		t.Error("HasColor() must be true after kelvin is observed")
	}
	if !r.HasHsColor() {
		t.Error("HasHsColor() must be true after kelvin is observed")
	}
	if !r.HasColorTemperature() {
		t.Error("HasColorTemperature() must be true after kelvin is observed")
	}
}

// TestLSCHasColorTempColorModeFollowsKelvin verifies HasColorTempColorMode
// tracks colorTempKelvinActive: false while KELVIN is unobserved, true once
// a non-zero value lands.
func TestLSCHasColorTempColorModeFollowsKelvin(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	if r.HasColorTempColorMode() {
		t.Error("HasColorTempColorMode() must be false before any KELVIN value is observed")
	}

	r.kelvin.OnEvent(int32(4000))

	if !r.HasColorTempColorMode() {
		t.Error("HasColorTempColorMode() must be true once KELVIN is observed and non-zero")
	}
}

// TestLSCHADiscoveryAdvertisesColorTempAndHs verifies the HA-Discovery body
// advertises both hs and color_temp simultaneously, with the hardware
// kelvin bounds carried through.
func TestLSCHADiscoveryAdvertisesColorTempAndHs(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	_, body := r.HADiscoveryPayload(discoveryCtx{})
	if body == nil {
		t.Fatal("HADiscoveryPayload body must not be nil")
	}

	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 2 || modes[0] != "color_temp" || modes[1] != "hs" {
		t.Errorf("supported_color_modes = %v, want [color_temp hs]", modes)
	}
	if v, _ := body["hs"].(bool); !v {
		t.Error("hs must be true")
	}
	if v, _ := body["color_temp_kelvin"].(bool); !v {
		t.Error("color_temp_kelvin must be true")
	}
	if v, _ := body["min_kelvin"].(int32); v != r.MinKelvin {
		t.Errorf("min_kelvin = %v, want %v", v, r.MinKelvin)
	}
	if v, _ := body["max_kelvin"].(int32); v != r.MaxKelvin {
		t.Errorf("max_kelvin = %v, want %v", v, r.MaxKelvin)
	}
	if r.MinKelvin != 2000 {
		t.Errorf("MinKelvin = %v, want 2000", r.MinKelvin)
	}
	if r.MaxKelvin != 6500 {
		t.Errorf("MaxKelvin = %v, want 6500", r.MaxKelvin)
	}
}

// TestLSCStateColorModeFollowsKelvin verifies State().ColorMode reports "hs"
// while KELVIN is unobserved and "color_temp" (with ColorTempKelvin set)
// once a non-zero KELVIN value lands.
func TestLSCStateColorModeFollowsKelvin(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	state, _ := r.State().(*payload.RGBWLightState)
	if state == nil {
		t.Fatal("State() must return *payload.RGBWLightState")
	}
	if state.ColorMode != "hs" {
		t.Errorf("ColorMode before kelvin = %q, want hs", state.ColorMode)
	}
	if state.ColorTempKelvin != nil {
		t.Error("ColorTempKelvin must not be set before kelvin is observed")
	}

	r.kelvin.OnEvent(int32(4000))

	state, _ = r.State().(*payload.RGBWLightState)
	if state == nil {
		t.Fatal("State() must return *payload.RGBWLightState")
	}
	if state.ColorMode != "color_temp" {
		t.Errorf("ColorMode after kelvin = %q, want color_temp", state.ColorMode)
	}
	if state.ColorTempKelvin == nil || *state.ColorTempKelvin != 4000 {
		t.Errorf("ColorTempKelvin = %v, want 4000", state.ColorTempKelvin)
	}
}

// TestLSCMatterColorModeFollowsKelvin verifies the Matter ColorControl
// server's ColorMode attribute reports HueSaturation while KELVIN is
// unobserved and ColorTemp once a non-zero KELVIN value lands.
func TestLSCMatterColorModeFollowsKelvin(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	var rgbw rgbwColorServer
	for _, s := range r.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	if rgbw.l == nil {
		t.Fatal("rgbwColorServer not found among MatterClusterServers()")
	}

	got, ok := rgbw.MatterRead(matterAttrColorColorMode)
	if !ok {
		t.Fatal("MatterRead(ColorMode) must return ok=true")
	}
	if got.(uint8) != matterColorModeHueSaturation {
		t.Errorf("ColorMode before kelvin = %d, want %d (HueSaturation)", got.(uint8), matterColorModeHueSaturation)
	}

	r.kelvin.OnEvent(int32(4000))

	got, ok = rgbw.MatterRead(matterAttrColorColorMode)
	if !ok {
		t.Fatal("MatterRead(ColorMode) must return ok=true")
	}
	if got.(uint8) != matterColorModeColorTemp {
		t.Errorf("ColorMode after kelvin = %d, want %d (ColorTemp)", got.(uint8), matterColorModeColorTemp)
	}
}

// writtenParams collects the distinct parameters w observed a write for.
// Regression guards below only care whether a parameter was touched at all
// — not the write count or the wire value — so they check membership in
// this set rather than walking w.calls directly.
func writtenParams(w *colorStubWriter) map[hmenum.Parameter]bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[hmenum.Parameter]bool, len(w.calls))
	for _, c := range w.calls {
		out[c.param] = true
	}
	return out
}

// TestLSCSetKelvinWritesOnlyColorTemperature guards the HmIP-LSC combined
// light against the reference-stack bug where a colour-temperature command
// bundled HUE/SATURATION into the same write and stomped the device's
// active mode: SetKelvin must write COLOR_TEMPERATURE only.
func TestLSCSetKelvinWritesOnlyColorTemperature(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	if err := r.SetKelvin(context.Background(), 3300, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	got := writtenParams(w)
	if !got[hmenum.ParameterColorTemperature] {
		t.Error("expected a write to COLOR_TEMPERATURE")
	}
	if got[hmenum.ParameterHue] {
		t.Error("SetKelvin must not write HUE")
	}
	if got[hmenum.ParameterSaturation] {
		t.Error("SetKelvin must not write SATURATION")
	}
}

// TestLSCSetColorWritesOnlyHueSaturation is the counterpart of
// TestLSCSetKelvinWritesOnlyColorTemperature: an hs-colour command must
// write HUE + SATURATION only, never dragging COLOR_TEMPERATURE along and
// silently switching the device out of its active colour-temp mode.
func TestLSCSetColorWritesOnlyHueSaturation(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	if err := r.SetColor(context.Background(), 180, 50, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	got := writtenParams(w)
	if !got[hmenum.ParameterHue] {
		t.Error("expected a write to HUE")
	}
	if !got[hmenum.ParameterSaturation] {
		t.Error("expected a write to SATURATION")
	}
	if got[hmenum.ParameterColorTemperature] {
		t.Error("SetColor must not write COLOR_TEMPERATURE")
	}
}

// TestLSCTurnOnWritesOnlyLevelPreservingActiveMode verifies a plain TurnOn
// on a fresh (off) HmIP-LSC combined light writes LEVEL only. A bare
// on-command never carries colour intent, so it must not bundle HUE,
// SATURATION or COLOR_TEMPERATURE — doing so would silently flip whichever
// colour mode the device was last left in.
func TestLSCTurnOnWritesOnlyLevelPreservingActiveMode(t *testing.T) {
	w := &colorStubWriter{}
	r := newLSCLightRig(t, w)

	if err := r.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	got := writtenParams(w)
	if !got[hmenum.ParameterLevel] {
		t.Error("expected a write to LEVEL")
	}
	if got[hmenum.ParameterHue] {
		t.Error("TurnOn must not write HUE")
	}
	if got[hmenum.ParameterSaturation] {
		t.Error("TurnOn must not write SATURATION")
	}
	if got[hmenum.ParameterColorTemperature] {
		t.Error("TurnOn must not write COLOR_TEMPERATURE")
	}
}
