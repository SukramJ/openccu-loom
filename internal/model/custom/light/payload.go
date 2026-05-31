// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantees that all top-level light types satisfy the
// universal Source contract and the HA-Discovery payload builder
// contract (ADR 0010). ADR-0007 step 5.
//
// All types inherit ServiceRegistry from the *generic.Float chain:
//
//	Light → *generic.Float
//	ColorLight → *Light → *generic.Float
//	ColorTempLight → *Light → *generic.Float
//	FixedColorLight → *Light → *generic.Float
//	EffectLight → *ColorLight → *Light → *generic.Float
//	DRGDaliLight → *ColorTempLight → *Light → *generic.Float
//	RGBWLight → *ColorLight → *Light → *generic.Float
var (
	_ payload.Source                    = (*Light)(nil)
	_ payload.Source                    = (*ColorLight)(nil)
	_ payload.Source                    = (*ColorTempLight)(nil)
	_ payload.Source                    = (*FixedColorLight)(nil)
	_ payload.Source                    = (*EffectLight)(nil)
	_ payload.Source                    = (*DRGDaliLight)(nil)
	_ payload.Source                    = (*RGBWLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Light)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*ColorLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*ColorTempLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*FixedColorLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*EffectLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*DRGDaliLight)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*RGBWLight)(nil)
)

// --- Light ---

// Info returns identity-level fields for a Light.
func (l *Light) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	info := &payload.LightInfo{
		Category: "light",
		Dimmable: l.Capabilities.Dimmable,
	}
	if l.Float != nil {
		info.Address = l.Address()
		info.Key = l.DataPointKey().String()
	}
	return info
}

// Config returns the light capability configuration.
func (l *Light) Config() payload.ConfigPayload {
	if l == nil {
		return nil
	}
	return &payload.LightConfig{
		Dimmable:          l.Capabilities.Dimmable,
		SupportsColor:     l.Capabilities.SupportsColor,
		SupportsColorTemp: l.Capabilities.SupportsColorTemp,
		SupportsEffects:   l.Capabilities.SupportsEffects,
	}
}

// State returns the live light state in the canonical HA MQTT Light
// JSON-Schema shape: `state` carries "ON"/"OFF", `brightness` is the
// integer 0..255 scaled from the CCU's 0..1 LEVEL float. Both keys are
// always present so the schema_json parser never hits a missing key on
// an unobserved channel — pre-observation state is "OFF".
//
// Mirrors HA's mqtt/light/schema_json.py expectations.
func (l *Light) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	state := &payload.LightState{}
	if on, ok := l.IsOn(); ok {
		state.State = onOff(on)
	} else {
		state.State = "OFF"
	}
	if b, ok := l.Brightness(); ok {
		v := int(b.Level()*255 + 0.5)
		state.Brightness = &v
	}
	return state
}

// onOff maps a boolean on/off flag to the HA-canonical literal.
func onOff(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// registerLightServices registers the base light service methods (turn_on,
// turn_off, set_level) onto the ServiceRegistry inherited from
// *generic.Float.
func (l *Light) registerLightServices() {
	l.RegisterService("turn_on", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return l.TurnOn(ctx, priority)
	})
	l.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return l.TurnOff(ctx, priority)
	})
	// toFloat64 coerces JSON-decoded numerics to float64. JSON
	// numbers always decode to float64, but callers (REST handlers,
	// test fakes) may pass int / int32 / int64 directly, so be
	// generous in what we accept.
	toFloat64 := func(v any) (float64, error) {
		switch x := v.(type) {
		case float64:
			return x, nil
		case float32:
			return float64(x), nil
		case int:
			return float64(x), nil
		case int32:
			return float64(x), nil
		case int64:
			return float64(x), nil
		}
		return 0, payload.ErrServiceInvalidParam
	}
	l.RegisterServiceWithArg("set_level", "level", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		// `set_level` accepts three payload shapes — the HA-Discovery builder
		// advertises this method as the Light's `command_topic`, so HA's
		// `mqtt-light schema=json` component sends the rich form:
		//
		// {"state":"ON","brightness":<0-255>}  — turn on at brightness
		// {"state":"OFF"}                      — turn off {"brightness":<0-255>}
		// — set brightness only {"level":<0-1>}                      — legacy
		// scalar form
		if state, ok := params["state"]; ok {
			s, _ := state.(string)
			switch s {
			case "OFF", "off", "Off":
				return l.TurnOff(ctx, priority)
			case "ON", "on", "On":
				if br, hasBr := params["brightness"]; hasBr {
					if f, err := toFloat64(br); err == nil {
						return l.SetLevel(ctx, f/255.0, priority)
					}
				}
				return l.TurnOn(ctx, priority)
			}
		}
		if br, hasBr := params["brightness"]; hasBr {
			if f, err := toFloat64(br); err == nil {
				return l.SetLevel(ctx, f/255.0, priority)
			}
		}
		// Legacy scalar form: {"level": 0.5}.
		v, err := payload.ParamFloat64(params, "level")
		if err != nil {
			return err
		}
		return l.SetLevel(ctx, v, priority)
	})
	l.RegisterService("set_timer_on_time", func(_ context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		d, err := payload.ParamFloat64(params, "seconds")
		if err != nil {
			return err
		}
		l.SetTimerOnTime(time.Duration(d * float64(time.Second)))
		return nil
	})
}

// --- ColorLight ---

// Info returns identity-level fields for a ColorLight.
func (l *ColorLight) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.Info().(*payload.LightInfo)
	if base == nil {
		base = &payload.LightInfo{}
	}
	return &payload.ColorLightInfo{
		LightInfo: *base,
		Kind:      "color",
	}
}

// State returns the live ColorLight state in HA JSON-Schema shape:
// `color: {h, s}` plus `color_mode: "hs"` when the colour has been
// observed. Brightness/state are inherited from the base.
func (l *ColorLight) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.State().(*payload.LightState)
	if base == nil {
		base = &payload.LightState{State: "OFF"}
	}
	out := &payload.ColorLightState{LightState: *base}
	if hue, sat, ok := l.Color(); ok {
		out.ColorMode = "hs"
		out.Color = &payload.ColorHS{H: float64(hue), S: sat}
	}
	return out
}

// registerColorLightServices registers the HSV color operations on top
// of the base light service methods.
func (l *ColorLight) registerColorLightServices() {
	// set_color expects a JSON object {hue, saturation} in practice;
	// "color" is the scalar-arg key for bare-scalar fallback (matching
	// the original serviceMethodScalarArg table).
	l.RegisterServiceWithArg("set_color", "color", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		hue, err := payload.ParamInt32(params, "hue")
		if err != nil {
			return err
		}
		sat, err := payload.ParamFloat64(params, "saturation")
		if err != nil {
			return err
		}
		return l.SetColor(ctx, hue, sat, priority)
	})
}

// --- ColorTempLight ---

// Info returns identity-level fields for a ColorTempLight.
func (l *ColorTempLight) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.Info().(*payload.LightInfo)
	if base == nil {
		base = &payload.LightInfo{}
	}
	return &payload.ColorTempLightInfo{
		LightInfo: *base,
		Kind:      "color_temp",
		MinKelvin: l.MinKelvin,
		MaxKelvin: l.MaxKelvin,
	}
}

// Config returns the color temperature light configuration.
func (l *ColorTempLight) Config() payload.ConfigPayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.Config().(*payload.LightConfig)
	if base == nil {
		base = &payload.LightConfig{}
	}
	return &payload.ColorTempLightConfig{
		LightConfig: *base,
		MinKelvin:   l.MinKelvin,
		MaxKelvin:   l.MaxKelvin,
	}
}

// State returns the live ColorTempLight state in HA JSON-Schema shape:
// `color_temp_kelvin` plus `color_mode: "color_temp"` when the kelvin
// value has been observed. Brightness/state are inherited.
func (l *ColorTempLight) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.State().(*payload.LightState)
	if base == nil {
		base = &payload.LightState{State: "OFF"}
	}
	out := &payload.ColorTempLightState{LightState: *base}
	if k, ok := l.Kelvin(); ok {
		out.ColorMode = "color_temp"
		kv := int(k)
		out.ColorTempKelvin = &kv
	}
	return out
}

// registerColorTempLightServices registers the kelvin operation on
// top of the base light service methods.
func (l *ColorTempLight) registerColorTempLightServices() {
	l.RegisterServiceWithArg("set_kelvin", "kelvin", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamInt32(params, "kelvin")
		if err != nil {
			return err
		}
		return l.SetKelvin(ctx, v, priority)
	})
}

// --- FixedColorLight ---

// Info returns identity-level fields for a FixedColorLight.
func (l *FixedColorLight) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.Info().(*payload.LightInfo)
	if base == nil {
		base = &payload.LightInfo{}
	}
	return &payload.FixedColorLightInfo{
		LightInfo: *base,
		Kind:      "fixed_color",
	}
}

// State returns the live FixedColorLight state. The discrete fixed-colour
// slot is projected onto HA's `hs` colour mode through [FixedColorToHS];
// the raw colour name lands in `fixed_color` for the SPA swatch list.
func (l *FixedColorLight) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	base, _ := l.Light.State().(*payload.LightState)
	if base == nil {
		base = &payload.LightState{State: "OFF"}
	}
	out := &payload.FixedColorLightState{LightState: *base}
	if hue, sat, ok := l.ChannelHsColor(); ok {
		out.Color = &payload.ColorHS{H: float64(hue), S: sat * 100}
		out.ColorMode = "hs"
	}
	if name, ok := l.ColorName(); ok {
		out.FixedColor = name
	}
	return out
}

// registerFixedColorLightServices registers the fixed-color operation
// on top of the base light service methods.
func (l *FixedColorLight) registerFixedColorLightServices() {
	l.RegisterServiceWithArg("set_color", "color", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamInt32(params, "color")
		if err != nil {
			return err
		}
		return l.SetColor(ctx, FixedColor(v), priority)
	})
}

// --- EffectLight ---

// Info returns identity-level fields for an EffectLight.
func (l *EffectLight) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	base, _ := l.ColorLight.Info().(*payload.ColorLightInfo)
	if base == nil {
		base = &payload.ColorLightInfo{}
	}
	info := &payload.EffectLightInfo{
		ColorLightInfo: *base,
	}
	info.Kind = "effect"
	return info
}

// Config returns the effect light configuration.
func (l *EffectLight) Config() payload.ConfigPayload {
	if l == nil {
		return nil
	}
	base, _ := l.ColorLight.Config().(*payload.LightConfig)
	if base == nil {
		base = &payload.LightConfig{}
	}
	out := &payload.EffectLightConfig{
		LightConfig: *base,
	}
	if effects := l.Effects(); len(effects) > 0 {
		out.Effects = effects
	}
	return out
}

// State returns the live EffectLight state in HA JSON-Schema shape:
// inherits state/brightness/color/color_mode from ColorLight and adds
// `effect: <label>` when an effect has been observed.
func (l *EffectLight) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	base, _ := l.ColorLight.State().(*payload.ColorLightState)
	if base == nil {
		base = &payload.ColorLightState{LightState: payload.LightState{State: "OFF"}}
	}
	out := &payload.EffectLightState{ColorLightState: *base}
	if _, label, ok := l.Effect(); ok {
		out.Effect = label
	}
	return out
}

// registerEffectLightServices registers the effect operation on top of the
// color light service methods.
func (l *EffectLight) registerEffectLightServices() {
	l.RegisterServiceWithArg("set_effect", "effect", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamInt32(params, "effect_index")
		if err != nil {
			return err
		}
		return l.SetEffect(ctx, v, priority)
	})
}

// --- DRGDaliLight ---

// Info returns identity-level fields for a DRGDaliLight.
func (l *DRGDaliLight) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	base, _ := l.ColorTempLight.Info().(*payload.ColorTempLightInfo)
	if base == nil {
		base = &payload.ColorTempLightInfo{}
	}
	info := &payload.DRGDaliLightInfo{
		ColorTempLightInfo: *base,
	}
	info.Kind = "dali"
	return info
}

// --- RGBWLight ---

// Info returns identity-level fields for an RGBWLight.
func (r *RGBWLight) Info() payload.InfoPayload {
	if r == nil {
		return nil
	}
	base, _ := r.ColorLight.Info().(*payload.ColorLightInfo)
	if base == nil {
		base = &payload.ColorLightInfo{}
	}
	// Override kind from ColorLight's "color" to "rgbw".
	info := &payload.RGBWLightInfo{
		ColorLightInfo: *base,
		Mode:           rgbwModeName(r.Mode()),
	}
	info.Kind = "rgbw"
	return info
}

// Config returns the RGBW light configuration.
func (r *RGBWLight) Config() payload.ConfigPayload {
	if r == nil {
		return nil
	}
	base, _ := r.ColorLight.Config().(*payload.LightConfig)
	if base == nil {
		base = &payload.LightConfig{}
	}
	out := &payload.RGBWLightConfig{
		LightConfig: *base,
		MinKelvin:   r.MinKelvin,
		MaxKelvin:   r.MaxKelvin,
	}
	if effects := r.Effects(); len(effects) > 0 {
		out.Effects = effects
	}
	return out
}

// State returns the live RGBWLight state in HA JSON-Schema shape.
// The operating mode dictates which colour fields are emitted:
//   - RGB / RGBW          → hs colour from ColorLight (color_mode "hs")
//   - TunableWhite        → kelvin only, hs is dropped (color_mode "color_temp")
//   - PWM / unknown       → no colour (color_mode "brightness")
//
// In RGBW mode both hs and kelvin are valid; we keep the more recent
// reading and let HA pick one via color_mode.
func (r *RGBWLight) State() payload.StatePayload {
	if r == nil {
		return nil
	}
	base, _ := r.ColorLight.State().(*payload.ColorLightState)
	if base == nil {
		base = &payload.ColorLightState{LightState: payload.LightState{State: "OFF"}}
	}
	out := &payload.RGBWLightState{LightState: base.LightState}
	switch r.Mode() { //nolint:exhaustive // Unknown and RGB modes fall through to the base ColorLight payload unchanged
	case RGBWModeTunableWhite:
		// Pure white-temperature: hs is meaningless — omit Color.
		if k, ok := r.Kelvin(); ok {
			out.ColorMode = "color_temp"
			kv := int(k)
			out.ColorTempKelvin = &kv
		}
	case RGBWModeRGBW:
		// hs already set by ColorLight; surface kelvin too so HA can
		// switch color_mode dynamically when the user picks the white channel.
		out.ColorMode = base.ColorMode
		out.Color = base.Color
		if k, ok := r.Kelvin(); ok {
			kv := int(k)
			out.ColorTempKelvin = &kv
		}
	case RGBWModePWM:
		// Brightness only — no colour fields.
		out.ColorMode = "brightness"
	default:
		// RGB or unknown: carry through ColorLight's hs colour unchanged.
		out.ColorMode = base.ColorMode
		out.Color = base.Color
	}
	return out
}

// registerRGBWLightServices registers the RGBW-specific service methods on
// top of the color light service methods.
func (r *RGBWLight) registerRGBWLightServices() {
	r.RegisterServiceWithArg("set_kelvin", "kelvin", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := payload.ParamInt32(params, "kelvin")
		if err != nil {
			return err
		}
		return r.SetKelvin(ctx, v, priority)
	})
	r.RegisterServiceWithArg("set_effect", "effect", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		label, err := payload.ParamString(params, "effect")
		if err == nil {
			return r.SetEffect(ctx, label, priority)
		}
		// Index-based fallback for callers that pass effect_index.
		idx, idxErr := payload.ParamInt32(params, "effect_index")
		if idxErr != nil {
			return err
		}
		effects := r.Effects()
		if idx < 0 || int(idx) >= len(effects) {
			return fmt.Errorf("set_effect: effect index %d out of range", idx)
		}
		return r.SetEffect(ctx, effects[idx], priority)
	})
}

// --- HADiscoveryPayload implementations ---
//
// All types use the HA MQTT Light JSON-Schema mode (schema: "json").
// In this mode both state and command topics carry JSON objects with
// HA's canonical keys: state ("ON"/"OFF"), brightness (0..255),
// color {h,s} or {r,g,b} or {x,y}, color_temp / color_temp_kelvin,
// effect, color_mode. StatePayload emits this shape directly — no
// templates are needed since HA parses the JSON natively.

// haBaseBody builds the common JSON-Schema discovery fields shared by
// every light type. Callers extend the returned map before returning.
//
// `schema: "json"` switches HA's MQTT light platform into the parser
// at `homeassistant/components/mqtt/light/schema_json.py`, which reads
// the state-topic JSON object directly — no `state_value_template` is
// supported in this mode (the JSON keys `state`, `brightness`, `color`,
// `color_temp`/`color_temp_kelvin`, `effect`, `color_mode` are parsed
// natively). StatePayload emits exactly that shape.
func haBaseBody(stateTopic, cmdTopic string) map[string]any {
	return map[string]any{
		"schema":        "json",
		"state_topic":   stateTopic,
		"command_topic": cmdTopic,
		"optimistic":    false,
		"flash":         false,
	}
}

// HADiscoveryPayload returns the HA Light-platform-specific payload
// skeleton for a plain dimmable or on/off Light.
//
// JSON-Schema mode: single command_topic carries JSON objects with
// "state", "brightness" fields. State topic emits StatePayload JSON
// directly in HA's expected shape (state, brightness, color_mode).
//
// Supported_color_modes follows
// logic: "brightness" for dimmable, "onoff" for non-dimmable.
func (l *Light) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()
	cmdTopic := ctx.ServiceMethodCommandTopic("set_level")
	body = haBaseBody(stateTopic, cmdTopic)

	if l.Capabilities.Dimmable {
		body["supported_color_modes"] = []string{"brightness"}
		body["brightness"] = true
		// HA JSON-Schema brightness scale is 0-255 — StatePayload pre-scales
		// the raw 0..1 LEVEL float to that range.
		body["brightness_scale"] = 255
		if l.Capabilities.Transition {
			body["transition"] = true
		}
	} else {
		body["supported_color_modes"] = []string{"onoff"}
	}

	return "light", body
}

// HADiscoveryPayload returns the HA Light payload for a ColorLight —
// extends Light with HS colour.
//
// In JSON-Schema mode HA expects the command topic to receive a JSON
// object with "color": {"h": H, "s": S}. The single command_topic
// handles all light operations (on/off, brightness, color).
// supported_color_modes: ["hs"].
func (l *ColorLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	_, body = l.Light.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}
	// Override supported_color_modes to "hs" — ColorLight adds HSV.
	body["supported_color_modes"] = []string{"hs"}
	// HS flag enables the color picker in HA JSON-Schema mode.
	body["hs"] = true
	return "light", body
}

// HADiscoveryPayload returns the HA Light payload for a ColorTempLight —
// extends Light with colour temperature.
//
// supported_color_modes: ["color_temp"].
// min_kelvin / max_kelvin carry the hardware limits.
// min_mireds / max_mireds are derived from kelvin limits:
//
//	min_mireds = 1_000_000 / MaxKelvin  (HA uses integer mireds)
//	max_mireds = 1_000_000 / MinKelvin
//
// Fallback to Python constants _MIN_MIREDS=153 / _MAX_MIREDS=500
// when kelvin limits are zero / unknown.
func (l *ColorTempLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	_, body = l.Light.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}
	body["supported_color_modes"] = []string{"color_temp"}
	body["color_temp_kelvin"] = true
	body["min_kelvin"] = l.MinKelvin
	body["max_kelvin"] = l.MaxKelvin

	// Derive mireds from kelvin hardware limits.
	// Python fallbacks: _MIN_MIREDS=153, _MAX_MIREDS=500 (light.py:26-27).
	const (
		pythonMinMireds = 153
		pythonMaxMireds = 500
	)
	minMireds := pythonMinMireds
	maxMireds := pythonMaxMireds
	if l.MaxKelvin > 0 {
		minMireds = int(1e6 / float64(l.MaxKelvin))
	}
	if l.MinKelvin > 0 {
		maxMireds = int(1e6 / float64(l.MinKelvin))
	}
	body["min_mireds"] = minMireds
	body["max_mireds"] = maxMireds

	return "light", body
}

// HADiscoveryPayload returns the HA Light payload for a FixedColorLight —
// extends Light with the discrete colour slot projected onto HA's
// `hs` color mode. HA renders a colour picker; the daemon snaps the
// chosen hue/saturation onto the nearest discrete slot (set_color
// service) on the way down via [HSToFixedColor].
//
// supported_color_modes: ["hs"]. hs:true enables the picker in
// JSON-Schema mode.
func (l *FixedColorLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	_, body = l.Light.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}
	body["supported_color_modes"] = []string{"hs"}
	body["hs"] = true
	return "light", body
}

// HADiscoveryPayload returns the HA Light payload for an EffectLight —
// extends ColorLight with effect selection.
//
// supported_color_modes: ["hs"] (inherited from ColorLight).
// effect: true enables the HA effect picker.
func (l *EffectLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	_, body = l.ColorLight.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}
	body["effect"] = true
	effects := l.Effects()
	if len(effects) == 0 {
		effects = []string{"NONE", "SLOW_COLOR_CHANGE", "MEDIUM_COLOR_CHANGE", "FAST_COLOR_CHANGE"}
	}
	body["effect_list"] = effects
	return "light", body
}

// HADiscoveryPayload returns the HA Light payload for a DRGDaliLight —
// extends ColorTempLight. DALI does not carry RGB so HS is absent.
//
// supported_color_modes: ["color_temp"] (inherited from ColorTempLight).
func (l *DRGDaliLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	// DRGDaliLight composes ColorTempLight which already includes colour-temp fields.
	return l.ColorTempLight.HADiscoveryPayload(ctx)
}

// HADiscoveryPayload returns the HA Light payload for an RGBWLight —
// extends ColorLight with colour temperature and optional effects.
//
// supported_color_modes follows the operating mode:
//   - RGB / RGBW mode → ["hs"] (or ["hs", "color_temp"] for RGBW)
//   - TunableWhite    → ["color_temp"]
//   - PWM / unknown   → ["brightness"]
//
// When HasColorTemperature() is true, color_temp_kelvin is added.
// When HasEffects() is true, effect + effect_list are added.
func (r *RGBWLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if r == nil || ctx == nil {
		return "", nil
	}
	_, body = r.ColorLight.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}

	// Compute supported_color_modes from the current operating mode.
	switch r.Mode() {
	case RGBWModeRGBW:
		body["supported_color_modes"] = []string{"hs", "color_temp"}
	case RGBWModeTunableWhite:
		body["supported_color_modes"] = []string{"color_temp"}
		// Remove hs flag set by ColorLight — not applicable in tunable white mode.
		delete(body, "hs")
	case RGBWModeRGB:
		body["supported_color_modes"] = []string{"hs"}
	default:
		// PWM or unknown: brightness only.
		body["supported_color_modes"] = []string{"brightness"}
		delete(body, "hs")
	}

	if r.HasColorTemperature() {
		body["color_temp_kelvin"] = true
		body["min_kelvin"] = r.MinKelvin
		body["max_kelvin"] = r.MaxKelvin
	}
	effects := r.Effects()
	if len(effects) > 0 {
		body["effect"] = true
		body["effect_list"] = effects
	}
	return "light", body
}

// rgbwModeName returns a wire-stable string label for the operating mode.
func rgbwModeName(m RGBWMode) string {
	switch m { //nolint:exhaustive // Unknown mode falls through to the "unknown" default return
	case RGBWModePWM:
		return "pwm"
	case RGBWModeRGB:
		return "rgb"
	case RGBWModeRGBW:
		return "rgbw"
	case RGBWModeTunableWhite:
		return "tunable_white"
	}
	return "unknown"
}
