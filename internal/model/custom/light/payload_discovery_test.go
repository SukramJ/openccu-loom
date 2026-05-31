// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// discoveryCtx is a minimal stub for payload.HADiscoveryContext used in
// payload-builder smoke tests.
type discoveryCtx struct{}

func (discoveryCtx) AggregatedStateTopic() string { return "test/state" }
func (discoveryCtx) CustomDPStateTopic() string   { return "test/custom/state" }
func (discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + parameter
}

var _ payload.HADiscoveryContext = discoveryCtx{}

// --- Light ---

func TestLightHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var l *Light
	comp, body := l.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestLightHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	comp, body := l.HADiscoveryPayload(discoveryCtx{})
	if comp != "light" {
		t.Fatalf("component = %q, want %q", comp, "light")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

// TestLightDimmableHASchemaJSON pins the JSON-Schema mode fields for a
// plain dimmable light: schema, state/command topics, supported_color_modes,
// brightness_scale, optimistic, flash.
func TestLightDimmableHASchemaJSON(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	ctx := discoveryCtx{}
	comp, body := l.HADiscoveryPayload(ctx)

	if comp != "light" {
		t.Fatalf("component = %q, want %q", comp, "light")
	}
	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	wantCmd := ctx.ServiceMethodCommandTopic("set_level")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q", v, wantCmd)
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "brightness" {
		t.Errorf("supported_color_modes = %v, want [brightness]", modes)
	}
	if v, _ := body["brightness"].(bool); !v {
		t.Error("brightness must be true for dimmable light")
	}
	if v, _ := body["brightness_scale"].(int); v != 255 {
		t.Errorf("brightness_scale = %v, want 255", v)
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
	// Old per-attribute topics must not appear in JSON-Schema mode.
	for _, forbidden := range []string{"brightness_state_topic", "brightness_command_topic"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("JSON-Schema mode must not expose old-style key %q", forbidden)
		}
	}
}

// TestLightDimmableTransitionHASchemaJSON verifies that transition: true
// appears when Capabilities.Transition is set.
func TestLightDimmableTransitionHASchemaJSON(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true, Transition: true})
	_, body := l.HADiscoveryPayload(discoveryCtx{})
	if v, _ := body["transition"].(bool); !v {
		t.Error("transition must be true when Capabilities.Transition is set")
	}
}

// TestLightDimmableNoTransitionHASchemaJSON verifies that transition is
// absent when not supported.
func TestLightDimmableNoTransitionHASchemaJSON(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	_, body := l.HADiscoveryPayload(discoveryCtx{})
	if _, ok := body["transition"]; ok {
		t.Error("transition must be absent when Capabilities.Transition is false")
	}
}

// TestLightNonDimmableHASchemaJSON pins JSON-Schema fields for a non-dimmable light.
func TestLightNonDimmableHASchemaJSON(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{Dimmable: false})
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	for _, key := range []string{"state_topic", "command_topic"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required non-dimmable key %q", key)
		}
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "onoff" {
		t.Errorf("supported_color_modes = %v, want [onoff]", modes)
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
	// brightness must not appear for non-dimmable.
	if _, ok := body["brightness"]; ok {
		t.Error("non-dimmable light must not expose brightness")
	}
}

// --- ColorLight ---

func TestColorLightHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var l *ColorLight
	comp, body := l.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestColorLightHASchemaJSON pins JSON-Schema fields for a ColorLight.
func TestColorLightHASchemaJSON(t *testing.T) {
	t.Parallel()
	ch := newColorRig(t, "HmIP-RGBW:3", &colorStubWriter{}, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true},
	})
	ctx := discoveryCtx{}
	comp, body := l.HADiscoveryPayload(ctx)
	if comp != "light" {
		t.Fatalf("component = %q, want %q", comp, "light")
	}
	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "hs" {
		t.Errorf("supported_color_modes = %v, want [hs]", modes)
	}
	if v, _ := body["hs"].(bool); !v {
		t.Error("hs must be true for ColorLight")
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
	// Old per-attribute HS topics must not appear.
	for _, forbidden := range []string{"hs_state_topic", "hs_command_topic", "hs_value_template"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("JSON-Schema mode must not expose old-style key %q", forbidden)
		}
	}
	// Single command_topic must point to set_level (JSON-Schema: all commands go there).
	wantCmd := ctx.ServiceMethodCommandTopic("set_level")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q", v, wantCmd)
	}
}

// --- ColorTempLight ---

// TestColorTempLightHASchemaJSON pins JSON-Schema fields for a ColorTempLight.
func TestColorTempLightHASchemaJSON(t *testing.T) {
	t.Parallel()
	ch := newColorTempRig(t, "HmIP-SCTH230:1", &stubWriter{}, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{
		Channel:      ch,
		Writer:       &stubWriter{},
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true},
	}, 2700, 6500)
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "color_temp" {
		t.Errorf("supported_color_modes = %v, want [color_temp]", modes)
	}
	if v, _ := body["color_temp_kelvin"].(bool); !v {
		t.Error("color_temp_kelvin must be true")
	}
	if v, _ := body["min_kelvin"].(int32); v != 2700 {
		t.Errorf("min_kelvin = %v, want 2700", v)
	}
	if v, _ := body["max_kelvin"].(int32); v != 6500 {
		t.Errorf("max_kelvin = %v, want 6500", v)
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
	// Old per-attribute color-temp topics must not appear.
	for _, forbidden := range []string{"color_temp_state_topic", "color_temp_command_topic", "color_temp_value_template"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("JSON-Schema mode must not expose old-style key %q", forbidden)
		}
	}
}

// TestColorTempLightMinMaxMireds verifies that HADiscoveryPayload emits
// min_mireds and max_mireds derived from kelvin hardware limits.
func TestColorTempLightMinMaxMireds(t *testing.T) {
	t.Parallel()
	// min_kelvin=2000 → max_mireds=500, max_kelvin=6536 → min_mireds=152
	ch := newColorTempRig(t, "HmIP-SCTH230:1", &stubWriter{}, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}, 2000, 6536)
	l := NewColorTempLight(Config{
		Channel:      ch,
		Writer:       &stubWriter{},
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true},
	}, 2000, 6536)
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	minMireds, ok := body["min_mireds"].(int)
	if !ok {
		t.Fatalf("min_mireds missing or wrong type: %T=%v", body["min_mireds"], body["min_mireds"])
	}
	maxMireds, ok := body["max_mireds"].(int)
	if !ok {
		t.Fatalf("max_mireds missing or wrong type: %T=%v", body["max_mireds"], body["max_mireds"])
	}
	// min_mireds = 1e6 / max_kelvin (6536K) = 152
	if minMireds != 152 {
		t.Errorf("min_mireds = %d, want 152 (1e6/6536)", minMireds)
	}
	// max_mireds = 1e6 / min_kelvin (2000K) = 500
	if maxMireds != 500 {
		t.Errorf("max_mireds = %d, want 500 (1e6/2000)", maxMireds)
	}
}

// TestColorTempLightMinMaxMireds_FallbackWhenZeroKelvin verifies Python
// constant fallback (153/500) when kelvin limits are zero.
func TestColorTempLightMinMaxMireds_FallbackWhenZeroKelvin(t *testing.T) {
	t.Parallel()
	l := &ColorTempLight{} // zero MinKelvin/MaxKelvin
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	minMireds, ok := body["min_mireds"].(int)
	if !ok {
		t.Fatalf("min_mireds missing: %v", body["min_mireds"])
	}
	maxMireds, ok := body["max_mireds"].(int)
	if !ok {
		t.Fatalf("max_mireds missing: %v", body["max_mireds"])
	}
	if minMireds != 153 {
		t.Errorf("min_mireds fallback = %d, want 153", minMireds)
	}
	if maxMireds != 500 {
		t.Errorf("max_mireds fallback = %d, want 500", maxMireds)
	}
}

// --- EffectLight ---

// TestEffectLightHASchemaJSON pins JSON-Schema fields for an EffectLight.
func TestEffectLightHASchemaJSON(t *testing.T) {
	t.Parallel()
	ch := newEffectRig(t, "HmIP-MP3P:2", &colorStubWriter{}, custom.LightCapabilities{SupportsColor: true, SupportsEffects: true, Dimmable: true})
	l := NewEffectLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsEffects: true, Dimmable: true},
	})
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "hs" {
		t.Errorf("supported_color_modes = %v, want [hs]", modes)
	}
	if v, _ := body["effect"].(bool); !v {
		t.Error("effect must be true for EffectLight")
	}
	if _, ok := body["effect_list"]; !ok {
		t.Error("effect_list must be present for EffectLight")
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
	// Old per-attribute effect topics must not appear.
	for _, forbidden := range []string{"effect_state_topic", "effect_command_topic", "effect_value_template"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("JSON-Schema mode must not expose old-style key %q", forbidden)
		}
	}
}

// --- DRGDaliLight ---

// TestDRGDaliLightHASchemaJSON pins JSON-Schema fields for a DRGDaliLight.
func TestDRGDaliLightHASchemaJSON(t *testing.T) {
	t.Parallel()
	ch := newColorTempRig(t, "HmIP-DRG-DALI:1", &stubWriter{}, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}, 2700, 6500)
	l := NewDRGDaliLight(Config{
		Channel:      ch,
		Writer:       &stubWriter{},
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true},
	}, 2700, 6500)
	_, body := l.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "color_temp" {
		t.Errorf("supported_color_modes = %v, want [color_temp]", modes)
	}
	if v, _ := body["color_temp_kelvin"].(bool); !v {
		t.Error("color_temp_kelvin must be true for DRGDaliLight")
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
}

// --- RGBWLight ---

// TestRGBWLightHASchemaJSON_RGBMode pins JSON-Schema fields when RGBWLight
// is in RGB mode.
func TestRGBWLightHASchemaJSON_RGBMode(t *testing.T) {
	t.Parallel()
	ch := newRGBWRig(t, "HmIP-RGBW:1", &colorStubWriter{}, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true},
	})
	r.recordMode("RGB")
	_, body := r.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["schema"].(string); v != "json" {
		t.Errorf("schema = %q, want %q", v, "json")
	}
	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "hs" {
		t.Errorf("RGB mode: supported_color_modes = %v, want [hs]", modes)
	}
	if v, _ := body["optimistic"].(bool); v {
		t.Error("optimistic must be false")
	}
	if v, _ := body["flash"].(bool); v {
		t.Error("flash must be false")
	}
}

// TestRGBWLightHASchemaJSON_RGBWMode pins supported_color_modes for RGBW mode.
func TestRGBWLightHASchemaJSON_RGBWMode(t *testing.T) {
	t.Parallel()
	ch := newRGBWRig(t, "HmIP-RGBW:1", &colorStubWriter{}, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})
	r.recordMode("RGBW")
	_, body := r.HADiscoveryPayload(discoveryCtx{})

	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 2 {
		t.Fatalf("RGBW mode: supported_color_modes = %v, want [hs color_temp]", modes)
	}
	hasHS, hasCT := false, false
	for _, m := range modes {
		if m == "hs" {
			hasHS = true
		}
		if m == "color_temp" {
			hasCT = true
		}
	}
	if !hasHS || !hasCT {
		t.Errorf("RGBW mode: supported_color_modes = %v, want both hs and color_temp", modes)
	}
	if v, _ := body["color_temp_kelvin"].(bool); !v {
		t.Error("color_temp_kelvin must be true in RGBW mode")
	}
}

// TestRGBWLightHASchemaJSON_TunableWhiteMode pins supported_color_modes for
// tunable white mode.
func TestRGBWLightHASchemaJSON_TunableWhiteMode(t *testing.T) {
	t.Parallel()
	ch := newRGBWRig(t, "HmIP-RGBW:1", &colorStubWriter{}, custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true})
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true},
	})
	r.recordMode("TUNABLE_WHITE")
	_, body := r.HADiscoveryPayload(discoveryCtx{})

	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "color_temp" {
		t.Errorf("tunable white: supported_color_modes = %v, want [color_temp]", modes)
	}
	// hs must be removed in tunable white mode.
	if _, ok := body["hs"]; ok {
		t.Error("hs flag must not be present in tunable white mode")
	}
}

// TestRGBWLightHASchemaJSON_PWMMode pins supported_color_modes for PWM mode.
func TestRGBWLightHASchemaJSON_PWMMode(t *testing.T) {
	t.Parallel()
	ch := newRGBWRig(t, "HmIP-RGBW:1", &colorStubWriter{}, custom.LightCapabilities{Dimmable: true})
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       &colorStubWriter{},
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})
	r.recordMode("PWM")
	_, body := r.HADiscoveryPayload(discoveryCtx{})

	modes, _ := body["supported_color_modes"].([]string)
	if len(modes) != 1 || modes[0] != "brightness" {
		t.Errorf("PWM mode: supported_color_modes = %v, want [brightness]", modes)
	}
}
