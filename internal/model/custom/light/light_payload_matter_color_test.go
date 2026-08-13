// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for payload assembly (Info/Config/State) across all light types and
// for the color cluster servers in matter_color.go (ctColorServer,
// hsColorServer, rgbwColorServer).

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── payload.go: InfoPayload / ConfigPayload / StatePayload ─────────────────

// TestLightInfoPayload verifies InfoPayload returns expected keys.
func TestLightInfoPayload(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	info, _ := l.Info().(*payload.LightInfo)
	if info == nil {
		t.Fatal("Info() must not return nil")
	}
	if info.Category != "light" {
		t.Errorf("InfoPayload category=%q, want %q", info.Category, "light")
	}
	if info.Address == "" {
		t.Error("InfoPayload must include address")
	}
	if !info.Dimmable {
		t.Errorf("InfoPayload dimmable=%v, want true", info.Dimmable)
	}
}

// TestLightInfoPayloadNilFloat verifies InfoPayload works when Float is nil.
func TestLightInfoPayloadNilFloat(t *testing.T) {
	l := &Light{Capabilities: custom.LightCapabilities{Dimmable: false}}
	info, _ := l.Info().(*payload.LightInfo)
	if info == nil {
		t.Fatal("Info() with nil Float must not return nil")
	}
	if info.Address != "" {
		t.Error("InfoPayload with nil Float must not include address")
	}
}

// TestLightConfigPayload verifies ConfigPayload returns capability keys.
func TestLightConfigPayload(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, SupportsColor: true})
	cfg, _ := l.Config().(*payload.LightConfig)
	if cfg == nil {
		t.Fatal("Config() must not return nil")
	}
	if !cfg.Dimmable {
		t.Errorf("ConfigPayload dimmable=%v, want true", cfg.Dimmable)
	}
	if !cfg.SupportsColor {
		t.Errorf("ConfigPayload supports_color=%v, want true", cfg.SupportsColor)
	}
}

// TestColorLightInfoPayload verifies ColorLight.InfoPayload includes kind.
func TestColorLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, _, _ := newColorLightRig(t, "X:1", w)
	info, _ := cl.Info().(*payload.ColorLightInfo)
	if info == nil {
		t.Fatal("ColorLight.Info() must not return nil")
	}
	if info.Kind != "color" {
		t.Errorf("ColorLight.InfoPayload kind=%q, want %q", info.Kind, "color")
	}
}

// TestColorLightStatePayload verifies ColorLight.StatePayload emits hs color.
func TestColorLightStatePayload(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, hue, sat := newColorLightRig(t, "X:1", w)

	// No colour observed yet.
	state, _ := cl.State().(*payload.ColorLightState)
	if state != nil && state.Color != nil {
		t.Error("StatePayload before color event must not include color")
	}

	// Observe colour.
	hue.OnEvent(int32(120))
	sat.OnEvent(0.8)
	state, _ = cl.State().(*payload.ColorLightState)
	if state == nil {
		t.Fatal("StatePayload must not return nil")
	}
	if state.ColorMode != "hs" {
		t.Errorf("StatePayload color_mode=%q, want %q", state.ColorMode, "hs")
	}
	if state.Color == nil {
		t.Error("StatePayload after color event must include color")
	}
}

// TestColorTempLightInfoPayload verifies ColorTempLight.InfoPayload includes kelvin bounds.
func TestColorTempLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	info, _ := ctl.Info().(*payload.ColorTempLightInfo)
	if info == nil {
		t.Fatal("ColorTempLight.Info() must not return nil")
	}
	if info.Kind != "color_temp" {
		t.Errorf("kind=%q, want color_temp", info.Kind)
	}
	if info.MinKelvin != 2700 {
		t.Errorf("min_kelvin=%v, want 2700", info.MinKelvin)
	}
	if info.MaxKelvin != 6500 {
		t.Errorf("max_kelvin=%v, want 6500", info.MaxKelvin)
	}
}

// TestColorTempLightConfigPayload verifies ConfigPayload includes kelvin bounds.
func TestColorTempLightConfigPayload(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	cfg, _ := ctl.Config().(*payload.ColorTempLightConfig)
	if cfg == nil {
		t.Fatal("Config() must not return nil")
	}
	if cfg.MinKelvin != int32(2700) {
		t.Errorf("min_kelvin=%v, want 2700", cfg.MinKelvin)
	}
	if cfg.MaxKelvin != int32(6500) {
		t.Errorf("max_kelvin=%v, want 6500", cfg.MaxKelvin)
	}
}

// TestColorTempLightStatePayload verifies StatePayload emits color_temp_kelvin.
func TestColorTempLightStatePayload(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, kelvin := newColorTempLightRig(t, "X:1", w, 2700, 6500)

	// No kelvin observed.
	state, _ := ctl.State().(*payload.ColorTempLightState)
	if state != nil && state.ColorTempKelvin != nil {
		t.Error("StatePayload before kelvin event must not include color_temp_kelvin")
	}

	// Observe kelvin.
	kelvin.OnEvent(int32(4000))
	state, _ = ctl.State().(*payload.ColorTempLightState)
	if state == nil {
		t.Fatal("StatePayload must not return nil")
	}
	if state.ColorMode != "color_temp" {
		t.Errorf("color_mode=%q, want color_temp", state.ColorMode)
	}
	if state.ColorTempKelvin == nil || *state.ColorTempKelvin != 4000 {
		t.Errorf("color_temp_kelvin=%v, want 4000", state.ColorTempKelvin)
	}
}

// TestFixedColorLightInfoPayload verifies InfoPayload returns kind=fixed_color.
func TestFixedColorLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	fcl := newFixedColorLightRig(t, "X:1", w)
	info, _ := fcl.Info().(*payload.FixedColorLightInfo)
	if info == nil {
		t.Fatal("FixedColorLight.Info() must not return nil")
	}
	if info.Kind != "fixed_color" {
		t.Errorf("kind=%q, want fixed_color", info.Kind)
	}
}

// TestFixedColorLightConfigPayload verifies FixedColorLight ConfigPayload.
func TestFixedColorLightConfigPayload(t *testing.T) {
	// FixedColorLight inherits ConfigPayload from Light.
	w := &colorStubWriter{}
	fcl := newFixedColorLightRig(t, "X:1", w)
	cfg, _ := fcl.Config().(*payload.LightConfig)
	if cfg == nil {
		t.Fatal("Config() must not return nil")
	}
	// FixedColorLight inherits LightConfig from Light; field access verifies presence.
	_ = cfg.Dimmable
}

// TestEffectLightInfoPayload verifies EffectLight.InfoPayload includes kind=effect.
func TestEffectLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRig(t, "X:1", w)
	info, _ := el.Info().(*payload.EffectLightInfo)
	if info == nil {
		t.Fatal("EffectLight.Info() must not return nil")
	}
	if info.Kind != "effect" {
		t.Errorf("kind=%q, want effect", info.Kind)
	}
}

// TestEffectLightConfigPayload verifies ConfigPayload includes effects list.
func TestEffectLightConfigPayload(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRig(t, "X:1", w)
	cfg, _ := el.Config().(*payload.EffectLightConfig)
	if cfg == nil {
		t.Fatal("Config() must not return nil")
	}
	if len(cfg.Effects) == 0 {
		t.Error("ConfigPayload must include effects when ValueList is non-empty")
	}
}

// TestDRGDaliLightInfoPayload verifies InfoPayload includes kind=dali.
func TestDRGDaliLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	dali := &DRGDaliLight{ColorTempLight: ctl}
	info, _ := dali.Info().(*payload.DRGDaliLightInfo)
	if info == nil {
		t.Fatal("DRGDaliLight.Info() must not return nil")
	}
	if info.Kind != "dali" {
		t.Errorf("kind=%q, want dali", info.Kind)
	}
}

// TestRGBWLightInfoPayload verifies RGBWLight.InfoPayload includes kind=rgbw.
func TestRGBWLightInfoPayload(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	info, _ := r.Info().(*payload.RGBWLightInfo)
	if info == nil {
		t.Fatal("RGBWLight.Info() must not return nil")
	}
	if info.Kind != "rgbw" {
		t.Errorf("kind=%q, want rgbw", info.Kind)
	}
}

// TestRGBWLightConfigPayload verifies RGBWLight.ConfigPayload includes kelvin/effects.
func TestRGBWLightConfigPayload(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	r.recordMode("RGB")
	cfg, _ := r.Config().(*payload.RGBWLightConfig)
	if cfg == nil {
		t.Fatal("Config() must not return nil")
	}
	if cfg.MinKelvin != int32(2000) {
		t.Errorf("min_kelvin=%v, want 2000", cfg.MinKelvin)
	}
}

// TestRGBWLightStatePayloadModes verifies StatePayload mode branching.
func TestRGBWLightStatePayloadModes(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)

	// PWM: color must be absent, color_mode=brightness.
	r.recordMode("4_PWM")
	state, _ := r.State().(*payload.RGBWLightState)
	if state == nil {
		t.Fatal("StatePayload must not return nil")
	}
	if state.Color != nil {
		t.Error("StatePayload in PWM must not include color")
	}
	if state.ColorMode != "brightness" {
		t.Errorf("color_mode in PWM=%q, want brightness", state.ColorMode)
	}

	// TunableWhite: inject kelvin, expect color_temp_kelvin.
	r.recordMode("2_TUNABLE_WHITE")
	r.kelvin.OnEvent(int32(3000))
	state, _ = r.State().(*payload.RGBWLightState)
	if state == nil {
		t.Fatal("StatePayload must not return nil")
	}
	if state.ColorMode != "color_temp" {
		t.Errorf("color_mode in TunableWhite=%q, want color_temp", state.ColorMode)
	}
	if state.Color != nil {
		t.Error("StatePayload in TunableWhite must not include color")
	}

	// RGBW: kelvin should be added as color_temp_kelvin.
	r.recordMode("RGBW")
	state, _ = r.State().(*payload.RGBWLightState)
	if state == nil {
		t.Fatal("StatePayload must not return nil")
	}
	if state.ColorTempKelvin == nil {
		t.Error("StatePayload in RGBW must include color_temp_kelvin when kelvin observed")
	}
}

// TestRGBWModeName exercises rgbwModeName through InfoPayload.
func TestRGBWModeName(t *testing.T) {
	// rgbwModeName is called by RGBWLight.InfoPayload.
	w := &colorStubWriter{}
	for _, mode := range []string{"4_PWM", "RGB", "RGBW", "TUNABLE_WHITE"} {
		r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
		r.recordMode(mode)
		info, _ := r.Info().(*payload.RGBWLightInfo)
		if info == nil || info.Mode == "" {
			t.Errorf("InfoPayload mode=nil for mode string %q", mode)
		}
	}
	// Unknown mode ("unknown" is the fallback string from rgbwModeName).
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	info, _ := r.Info().(*payload.RGBWLightInfo)
	if info == nil || info.Mode == "" {
		t.Error("InfoPayload mode=nil for unknown mode")
	}
}

// ─── matter_color.go: color server tests ────────────────────────────────────

// TestCTColorServerRead verifies ctColorServer.MatterRead for all attributes.
func TestCTColorServerRead(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, kelvin := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	s := ctColorServer{l: ctl}

	// Unobserved kelvin → (nil, true).
	v, ok := s.MatterRead(matterAttrColorColorTemperatureMireds)
	if !ok || v != nil {
		t.Errorf("ctColorServer.MatterRead(CT) unobserved = (%v, %v), want (nil, true)", v, ok)
	}

	// Observed kelvin.
	kelvin.OnEvent(int32(4000))
	v, ok = s.MatterRead(matterAttrColorColorTemperatureMireds)
	if !ok || v == nil {
		t.Errorf("ctColorServer.MatterRead(CT) observed = (%v, %v)", v, ok)
	}

	// Options.
	v, ok = s.MatterRead(matterAttrColorOptions)
	if !ok || v.(uint8) != 0 {
		t.Errorf("Options = (%v, %v)", v, ok)
	}

	// NumPrimaries.
	v, ok = s.MatterRead(matterAttrColorNumPrimaries)
	if !ok || v.(uint8) != 0 {
		t.Errorf("NumPrimaries = (%v, %v)", v, ok)
	}

	// ColorMode / EnhancedColorMode.
	v, ok = s.MatterRead(matterAttrColorColorMode)
	if !ok || v.(uint8) != matterColorModeColorTemp {
		t.Errorf("ColorMode = (%v, %v)", v, ok)
	}
	_, ok = s.MatterRead(matterAttrColorEnhancedColorMode)
	if !ok {
		t.Errorf("EnhancedColorMode: ok=false")
	}

	// ColorCapabilities.
	_, ok = s.MatterRead(matterAttrColorColorCapabilities)
	if !ok {
		t.Errorf("ColorCapabilities: ok=false")
	}

	// Physical min/max mireds.
	_, ok = s.MatterRead(matterAttrColorColorTempPhysicalMinMir)
	if !ok {
		t.Error("PhysicalMinMir: ok=false")
	}
	_, ok = s.MatterRead(matterAttrColorColorTempPhysicalMaxMir)
	if !ok {
		t.Error("PhysicalMaxMir: ok=false")
	}

	// FeatureMap.
	_, ok = s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Error("FeatureMap: ok=false")
	}

	// ClusterRevision.
	_, ok = s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Error("ClusterRevision: ok=false")
	}

	// Unknown.
	_, ok = s.MatterRead(0xFFFF)
	if ok {
		t.Error("Unknown attr must return ok=false")
	}
}

// TestCTColorServerWriteAndInvoke verifies ctColorServer write and invoke.
func TestCTColorServerWriteAndInvoke(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	s := ctColorServer{l: ctl}

	// Write always returns unknown-attr error.
	if err := s.MatterWrite(context.Background(), matterAttrColorColorTemperatureMireds, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("ctColorServer.MatterWrite must always return error")
	}

	// Invoke with MoveToColorTemperature bare uint16.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, uint16(300), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(CT): %v", err)
	}

	// Invoke with map.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, map[string]any{"colorTempMireds": uint16(250)}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(CT/map): %v", err)
	}

	// Unknown command.
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Unknown cmd must return error")
	}
}

// TestCTColorServerMatterReportableAndAttributes verifies list methods.
func TestCTColorServerMatterReportableAndAttributes(t *testing.T) {
	s := ctColorServer{l: &ColorTempLight{}}
	if r := s.MatterReportable(); len(r) == 0 {
		t.Error("MatterReportable must return at least one attribute")
	}
	if a := s.MatterAttributes(); len(a) == 0 {
		t.Error("MatterAttributes must return at least one attribute")
	}
}

// TestHSColorServerRead verifies hsColorServer.MatterRead for all attributes.
func TestHSColorServerRead(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, hue, sat := newColorLightRig(t, "X:1", w)
	s := hsColorServer{l: cl}

	// Unobserved → (nil, true).
	v, ok := s.MatterRead(matterAttrColorCurrentHue)
	if !ok || v != nil {
		t.Errorf("MatterRead(Hue) unobserved = (%v, %v)", v, ok)
	}
	v, ok = s.MatterRead(matterAttrColorCurrentSaturation)
	if !ok || v != nil {
		t.Errorf("MatterRead(Sat) unobserved = (%v, %v)", v, ok)
	}

	// Observed.
	hue.OnEvent(int32(120))
	sat.OnEvent(0.8)
	v, ok = s.MatterRead(matterAttrColorCurrentHue)
	if !ok || v == nil {
		t.Errorf("MatterRead(Hue) observed = (%v, %v)", v, ok)
	}
	v, ok = s.MatterRead(matterAttrColorCurrentSaturation)
	if !ok || v == nil {
		t.Errorf("MatterRead(Sat) observed = (%v, %v)", v, ok)
	}

	// Options.
	v, ok = s.MatterRead(matterAttrColorOptions)
	if !ok || v.(uint8) != 0 {
		t.Errorf("Options = (%v, %v)", v, ok)
	}

	// NumPrimaries.
	_, ok = s.MatterRead(matterAttrColorNumPrimaries)
	if !ok {
		t.Errorf("NumPrimaries: ok=false")
	}

	// ColorMode.
	v, ok = s.MatterRead(matterAttrColorColorMode)
	if !ok || v.(uint8) != matterColorModeHueSaturation {
		t.Errorf("ColorMode = (%v, %v)", v, ok)
	}
	_, ok = s.MatterRead(matterAttrColorEnhancedColorMode)
	if !ok {
		t.Error("EnhancedColorMode: ok=false")
	}

	// ColorCapabilities.
	_, ok = s.MatterRead(matterAttrColorColorCapabilities)
	if !ok {
		t.Error("ColorCapabilities: ok=false")
	}

	// FeatureMap.
	_, ok = s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Error("FeatureMap: ok=false")
	}

	// ClusterRevision.
	_, ok = s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Error("ClusterRevision: ok=false")
	}

	// Unknown.
	_, ok = s.MatterRead(0xFFFF)
	if ok {
		t.Error("Unknown attr must return ok=false")
	}
}

// TestHSColorServerWriteAndInvoke verifies hsColorServer write and invoke.
func TestHSColorServerWriteAndInvoke(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, hue, sat := newColorLightRig(t, "X:1", w)
	s := hsColorServer{l: cl}

	// Write always returns error.
	if err := s.MatterWrite(context.Background(), matterAttrColorCurrentHue, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("hsColorServer.MatterWrite must always return error")
	}

	// Seed colour so SetColor has a baseline.
	hue.OnEvent(int32(0))
	sat.OnEvent(0.0)

	// MoveToHue wire struct.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToHue, wire.MoveToHueRequest{Hue: 127}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToHue/wire): %v", err)
	}

	// MoveToHue wire struct variant.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToHue, wire.MoveToHueRequest{Hue: 64}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToHue/wire2): %v", err)
	}

	// MoveToSaturation wire struct.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToSaturation, wire.MoveToSaturationRequest{Saturation: 200}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToSat/wire): %v", err)
	}

	// MoveToSaturation wire struct variant.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToSaturation, wire.MoveToSaturationRequest{Saturation: 100}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToSat/wire2): %v", err)
	}

	// MoveToHueAndSaturation wire struct.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, wire.MoveToHueAndSaturationRequest{Hue: 60, Saturation: 200}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToHueSat): %v", err)
	}

	// Unknown command.
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Unknown cmd must return error")
	}
}

// TestHSColorServerMatterReportableAndAttributes verifies list methods.
func TestHSColorServerMatterReportableAndAttributes(t *testing.T) {
	s := hsColorServer{l: &ColorLight{}}
	if r := s.MatterReportable(); len(r) == 0 {
		t.Error("MatterReportable must return at least one attribute")
	}
	if a := s.MatterAttributes(); len(a) == 0 {
		t.Error("MatterAttributes must return at least one attribute")
	}
}

// TestRGBWColorServerRead verifies rgbwColorServer.MatterRead.
func TestRGBWColorServerRead(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	s := rgbwColorServer{l: r}

	r.recordMode("RGB")

	// Hue unobserved.
	v, ok := s.MatterRead(matterAttrColorCurrentHue)
	if !ok || v != nil {
		t.Errorf("MatterRead(Hue) unobserved = (%v, %v)", v, ok)
	}
	// Saturation unobserved.
	v, ok = s.MatterRead(matterAttrColorCurrentSaturation)
	if !ok || v != nil {
		t.Errorf("MatterRead(Sat) unobserved = (%v, %v)", v, ok)
	}
	// ColorTemp unobserved.
	v, ok = s.MatterRead(matterAttrColorColorTemperatureMireds)
	if !ok || v != nil {
		t.Errorf("MatterRead(CT) unobserved = (%v, %v)", v, ok)
	}

	// Options.
	v, ok = s.MatterRead(matterAttrColorOptions)
	if !ok || v.(uint8) != 0 {
		t.Errorf("Options = (%v, %v)", v, ok)
	}

	// NumPrimaries.
	_, ok = s.MatterRead(matterAttrColorNumPrimaries)
	if !ok {
		t.Error("NumPrimaries: ok=false")
	}

	// ColorMode.
	_, ok = s.MatterRead(matterAttrColorColorMode)
	if !ok {
		t.Error("ColorMode: ok=false")
	}
	_, ok = s.MatterRead(matterAttrColorEnhancedColorMode)
	if !ok {
		t.Error("EnhancedColorMode: ok=false")
	}

	// ColorCapabilities.
	_, ok = s.MatterRead(matterAttrColorColorCapabilities)
	if !ok {
		t.Error("ColorCapabilities: ok=false")
	}

	// Physical min/max mireds.
	_, ok = s.MatterRead(matterAttrColorColorTempPhysicalMinMir)
	if !ok {
		t.Error("PhysicalMinMir: ok=false")
	}
	_, ok = s.MatterRead(matterAttrColorColorTempPhysicalMaxMir)
	if !ok {
		t.Error("PhysicalMaxMir: ok=false")
	}

	// FeatureMap.
	_, ok = s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Error("FeatureMap: ok=false")
	}

	// ClusterRevision.
	_, ok = s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Error("ClusterRevision: ok=false")
	}

	// Unknown.
	_, ok = s.MatterRead(0xFFFF)
	if ok {
		t.Error("Unknown attr must return ok=false")
	}
}

// TestRGBWColorServerWriteAndInvoke verifies rgbwColorServer write and invoke.
func TestRGBWColorServerWriteAndInvoke(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	r.recordMode("RGBW")
	s := rgbwColorServer{l: r}

	// Write always returns error.
	if err := s.MatterWrite(context.Background(), matterAttrColorCurrentHue, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("rgbwColorServer.MatterWrite must always return error")
	}

	// MoveToHueAndSaturation wire struct.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, wire.MoveToHueAndSaturationRequest{Hue: 60, Saturation: 200}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToHueSat): %v", err)
	}

	// MoveToColorTemperature bare uint16.
	if _, err := s.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, uint16(300), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(CT): %v", err)
	}

	// Unknown command.
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Unknown cmd must return error")
	}
}

// TestRGBWColorServerMatterReportableAndAttributes verifies list methods.
func TestRGBWColorServerMatterReportableAndAttributes(t *testing.T) {
	s := rgbwColorServer{l: &RGBWLight{}}
	if r := s.MatterReportable(); len(r) == 0 {
		t.Error("MatterReportable must return at least one attribute")
	}
	if a := s.MatterAttributes(); len(a) == 0 {
		t.Error("MatterAttributes must return at least one attribute")
	}
}

// TestMatterEligibilityBranches verifies MatterEligibility for RGBW and Effect.
func TestMatterEligibilityBranches(t *testing.T) {
	w := &colorStubWriter{}

	// RGBWLight.
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	verdict := r.MatterEligibility()
	if len(verdict.Clusters) == 0 {
		t.Error("RGBWLight.MatterEligibility Clusters must not be empty")
	}

	// EffectLight.
	el := newEffectLightRig(t, "X:1", w)
	verdict = el.MatterEligibility()
	if len(verdict.Clusters) == 0 {
		t.Error("EffectLight.MatterEligibility Clusters must not be empty")
	}
}

// TestColorLightMatterDeviceType verifies ColorLight.MatterDeviceType.
func TestColorLightMatterDeviceType(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, _, _ := newColorLightRig(t, "X:1", w)
	if got := cl.MatterDeviceType(); got != matterDeviceTypeExtendedColorLight {
		t.Errorf("ColorLight.MatterDeviceType() = 0x%04X, want 0x%04X", got, matterDeviceTypeExtendedColorLight)
	}
}

// TestColorTempLightMatterDeviceType verifies ColorTempLight.MatterDeviceType.
func TestColorTempLightMatterDeviceType(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	if got := ctl.MatterDeviceType(); got != matterDeviceTypeColorTemperatureLight {
		t.Errorf("ColorTempLight.MatterDeviceType() = 0x%04X, want 0x%04X", got, matterDeviceTypeColorTemperatureLight)
	}
}

// TestRGBWLightMatterDeviceType verifies RGBWLight.MatterDeviceType.
func TestRGBWLightMatterDeviceType(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	if got := r.MatterDeviceType(); got != matterDeviceTypeExtendedColorLight {
		t.Errorf("RGBWLight.MatterDeviceType() = 0x%04X, want 0x%04X", got, matterDeviceTypeExtendedColorLight)
	}
}

// TestColorLightMatterClusterServers verifies ColorLight adds ColorControl.
func TestColorLightMatterClusterServers(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, _, _ := newColorLightRig(t, "X:1", w)
	servers := cl.MatterClusterServers()
	// Expects: OnOff, LevelControl, Groups, ScenesManagement, ColorControl (5 total).
	if len(servers) < 5 {
		t.Errorf("ColorLight.MatterClusterServers() len=%d, want ≥5", len(servers))
	}
}

// TestColorTempLightMatterClusterServers verifies ColorTempLight adds CT ColorControl.
func TestColorTempLightMatterClusterServers(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2700, 6500)
	servers := ctl.MatterClusterServers()
	if len(servers) < 5 {
		t.Errorf("ColorTempLight.MatterClusterServers() len=%d, want ≥5", len(servers))
	}
}

// TestRGBWLightMatterClusterServers verifies RGBWLight adds RGBW ColorControl.
func TestRGBWLightMatterClusterServers(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	servers := r.MatterClusterServers()
	if len(servers) < 5 {
		t.Errorf("RGBWLight.MatterClusterServers() len=%d, want ≥5", len(servers))
	}
}

// ─── matter_color.go: extract* helper coverage ──────────────────────────────

// TestExtractHueOnlyBranches verifies all extractHueOnly branches.
func TestExtractHueOnlyBranches(t *testing.T) {
	// Wire-struct primary path.
	v, err := extractHueOnly(wire.MoveToHueRequest{Hue: 42})
	if err != nil || v != 42 {
		t.Errorf("extractHueOnly(wire struct) = (%d, %v), want (42, nil)", v, err)
	}

	// map[uint8]any fallback: tag 0 = Hue as uint64.
	v, err = extractHueOnly(map[uint8]any{0: uint64(10)})
	if err != nil || v != 10 {
		t.Errorf("extractHueOnly(map fallback) = (%d, %v), want (10, nil)", v, err)
	}

	// Unsupported type must return errMatterValueType.
	_, err = extractHueOnly("not-valid")
	if err == nil {
		t.Error("extractHueOnly(string) must return error")
	}
}

// TestExtractSaturationOnlyBranches verifies all extractSaturationOnly branches.
func TestExtractSaturationOnlyBranches(t *testing.T) {
	// Wire-struct primary path.
	v, err := extractSaturationOnly(wire.MoveToSaturationRequest{Saturation: 200})
	if err != nil || v != 200 {
		t.Errorf("extractSaturationOnly(wire struct) = (%d, %v), want (200, nil)", v, err)
	}

	// map[uint8]any fallback: tag 0 = Saturation as uint64.
	v, err = extractSaturationOnly(map[uint8]any{0: uint64(100)})
	if err != nil || v != 100 {
		t.Errorf("extractSaturationOnly(map fallback) = (%d, %v), want (100, nil)", v, err)
	}

	// Unsupported type must return errMatterValueType.
	_, err = extractSaturationOnly("bad")
	if err == nil {
		t.Error("extractSaturationOnly(string) must return error")
	}
}

// TestExtractHueAndSaturationBranches verifies all extractHueAndSaturation branches.
func TestExtractHueAndSaturationBranches(t *testing.T) {
	// Wire-struct primary path.
	h, s, err := extractHueAndSaturation(wire.MoveToHueAndSaturationRequest{Hue: 60, Saturation: 200})
	if err != nil || h != 60 || s != 200 {
		t.Errorf("extractHueAndSaturation(wire struct) = (%d, %d, %v)", h, s, err)
	}

	// map[uint8]any fallback: tag 0 = Hue, tag 1 = Saturation as uint64.
	h, s, err = extractHueAndSaturation(map[uint8]any{0: uint64(30), 1: uint64(120)})
	if err != nil || h != 30 || s != 120 {
		t.Errorf("extractHueAndSaturation(map fallback) = (%d, %d, %v)", h, s, err)
	}

	// Unsupported type must return errMatterValueType.
	_, _, err = extractHueAndSaturation("bad")
	if err == nil {
		t.Error("extractHueAndSaturation(string) must return error")
	}
}

// TestExtractColorTempMiredsBranches verifies all extractColorTempMireds branches.
func TestExtractColorTempMiredsBranches(t *testing.T) {
	// Bare uint16.
	v, err := extractColorTempMireds(uint16(300))
	if err != nil || v != 300 {
		t.Errorf("extractColorTempMireds(uint16) = (%d, %v)", v, err)
	}

	// Map with colorTempMireds.
	v, err = extractColorTempMireds(map[string]any{"colorTempMireds": uint16(200)})
	if err != nil || v != 200 {
		t.Errorf("extractColorTempMireds(map) = (%d, %v)", v, err)
	}

	// Map missing key.
	_, err = extractColorTempMireds(map[string]any{})
	if err == nil {
		t.Error("extractColorTempMireds(map{}) must return error")
	}

	// Map wrong type.
	_, err = extractColorTempMireds(map[string]any{"colorTempMireds": "bad"})
	if err == nil {
		t.Error("extractColorTempMireds(map{bad type}) must return error")
	}

	// Unsupported type.
	_, err = extractColorTempMireds("bad")
	if err == nil {
		t.Error("extractColorTempMireds(string) must return error")
	}
}

// TestKelvinToMiredsAndBack verifies kelvinToMireds and miredsToKelvin conversion.
func TestKelvinToMiredsAndBack(t *testing.T) {
	// Normal value.
	m := kelvinToMireds(4000)
	if m == 0 {
		t.Error("kelvinToMireds(4000) must not be 0")
	}
	k := miredsToKelvin(m)
	if k == 0 {
		t.Error("miredsToKelvin after conversion must not be 0")
	}

	// Zero kelvin → matterMaxMireds.
	if got := kelvinToMireds(0); got != matterMaxMireds {
		t.Errorf("kelvinToMireds(0) = %d, want %d", got, matterMaxMireds)
	}

	// Very high kelvin → clamped to matterMinMireds.
	if got := kelvinToMireds(100000); got != matterMinMireds {
		t.Errorf("kelvinToMireds(100000) = %d, want %d", got, matterMinMireds)
	}

	// Very low kelvin → clamped to matterMaxMireds.
	if got := kelvinToMireds(100); got != matterMaxMireds {
		t.Errorf("kelvinToMireds(100) = %d, want %d", got, matterMaxMireds)
	}

	// miredsToKelvin(0) → 0.
	if got := miredsToKelvin(0); got != 0 {
		t.Errorf("miredsToKelvin(0) = %d, want 0", got)
	}
}

// TestSaturationToMatterAndBack verifies saturationToMatter / matterSaturationToHM.
func TestSaturationToMatterAndBack(t *testing.T) {
	// Negative → 0.
	if got := saturationToMatter(-1); got != 0 {
		t.Errorf("saturationToMatter(-1) = %d, want 0", got)
	}
	// >100 → max (over-range clamp).
	if got := saturationToMatter(150); got != uint8(matterHueScale) {
		t.Errorf("saturationToMatter(150) = %d, want %d", got, uint8(matterHueScale))
	}
	// Normal: 50 (out of 100) produces a non-zero mid-range value.
	got := saturationToMatter(50)
	if got == 0 || got > uint8(matterHueScale) {
		t.Errorf("saturationToMatter(50) = %d out of range", got)
	}

	// matterSaturationToHM at ceiling → 100 (HA-canonical 0..100).
	if v := matterSaturationToHM(uint8(matterHueScale)); v != 100.0 {
		t.Errorf("matterSaturationToHM(max) = %.4f, want 100.0", v)
	}
	// matterSaturationToHM at 0.
	if v := matterSaturationToHM(0); v != 0.0 {
		t.Errorf("matterSaturationToHM(0) = %.4f, want 0.0", v)
	}
}

// TestHueToMatterBranches verifies hueToMatter over/under-range clamping.
func TestHueToMatterBranches(t *testing.T) {
	// 0° → 0.
	if got := hueToMatter(0); got != 0 {
		t.Errorf("hueToMatter(0) = %d, want 0", got)
	}
	// 360° wraps to 0° → 0.
	if got := hueToMatter(360); got != 0 {
		t.Errorf("hueToMatter(360) = %d, want 0", got)
	}
	// 180° → mid-range.
	got := hueToMatter(180)
	if got == 0 || got > uint8(matterHueScale) {
		t.Errorf("hueToMatter(180) = %d out of range", got)
	}
}
