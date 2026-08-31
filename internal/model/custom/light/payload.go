// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// toNumber coerces JSON-decoded numerics to float64. JSON numbers always
// decode to float64, but callers (REST handlers, test fakes) may pass
// int / int32 / int64 directly, so be generous in what we accept.
//
// json.Number and numeric strings are accepted as well: this handler is
// the single definition behind both the MQTT command topic and the
// REST/WS custom-DP invoke plane, and the latter's third-party callers
// have always been able to send `{"brightness":"128"}`. Narrowing here
// would silently break them. It also lines the coercion up with
// [payload.ParamFloat64], which accepts strings too.
func toNumber(v any) (float64, error) {
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
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, fmt.Errorf("%w: %q", payload.ErrServiceInvalidParam, x.String())
		}
		return f, nil
	case string:
		// strconv.ParseFloat rejects trailing garbage such as "42xyz"
		// instead of silently truncating it.
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q", payload.ErrServiceInvalidParam, x)
		}
		return f, nil
	}
	return 0, payload.ErrServiceInvalidParam
}

// hasHALightAttributes reports whether the HA JSON-schema light payload
// carries any attribute beyond the on/off + brightness axis.
func hasHALightAttributes(params map[string]any) bool {
	for _, k := range []string{"color", "color_temp_kelvin", "color_temp", "effect"} {
		if _, ok := params[k]; ok {
			return true
		}
	}
	return false
}

// haColorHS extracts HA's canonical hue/saturation pair from the `color`
// object of a JSON-schema light command. HA emits `{"h":0-360,"s":0-100}`
// for every light that advertises the `hs` colour mode, which is the only
// colour mode any light type here advertises.
func haColorHS(v any) (hue int32, saturation float64, ok bool) {
	obj, isObj := v.(map[string]any)
	if !isObj {
		return 0, 0, false
	}
	h, hErr := toNumber(obj["h"])
	s, sErr := toNumber(obj["s"])
	if hErr != nil || sErr != nil {
		return 0, 0, false
	}
	return int32(h), s, true
}

// haColorTempKelvin resolves the colour temperature of a JSON-schema
// light command to kelvin. The discovery payload sets
// `color_temp_kelvin: true`, so HA sends kelvin; the mired form is
// accepted as well because HA falls back to it for a light that was
// discovered before that flag existed.
func haColorTempKelvin(params map[string]any) (int32, bool) {
	if v, ok := params["color_temp_kelvin"]; ok {
		if k, err := toNumber(v); err == nil && k > 0 {
			return int32(k), true
		}
	}
	if v, ok := params["color_temp"]; ok {
		if mireds, err := toNumber(v); err == nil && mireds > 0 {
			return int32(1e6 / mireds), true
		}
	}
	return 0, false
}

// applyHALightAttributes routes the colour / colour-temperature / effect
// keys of an HA JSON-schema light command to the service method that owns
// each one.
//
// Routing rather than re-implementing is what makes this correct for
// every light type: the concrete type (ColorLight, FixedColorLight,
// ColorTempLight, RGBWLight, EffectLight) registers its own set_color /
// set_kelvin / set_effect on the *same* ServiceRegistry this base type
// carries, so the pointer-embedded chain resolves the semantics the
// device actually has — a discrete colour slot for a FixedColorLight, a
// HUE/SATURATION pair for a ColorLight.
//
// An attribute HA sends to a light whose type never registered the
// matching method is an error, not a silent drop: the discovery payload
// only advertises the axes the type supports, so such a command means
// the two sides disagree and the operator has to see it.
func (l *Light) applyHALightAttributes(
	ctx context.Context, params map[string]any, priority hmenum.CommandPriority,
) error {
	if c, ok := params["color"]; ok {
		hue, sat, valid := haColorHS(c)
		if !valid {
			return fmt.Errorf("%w: color must be {\"h\":…,\"s\":…}", payload.ErrServiceInvalidParam)
		}
		if err := l.Invoke(ctx, "set_color", map[string]any{
			"hue":        hue,
			"saturation": sat,
		}, priority); err != nil {
			return err
		}
	}
	return l.applyHAColorTempAndEffect(ctx, params, priority)
}

// applyHAColorTempAndEffect routes the non-colour half of an HA
// JSON-schema light command. Split out from [Light.applyHALightAttributes]
// because a light whose turn-on writes COLOR atomically (the HmIP-MP3P
// status LED) has already consumed the `color` key by the time the
// remaining attributes have to be routed.
func (l *Light) applyHAColorTempAndEffect(
	ctx context.Context, params map[string]any, priority hmenum.CommandPriority,
) error {
	if kelvin, ok := haColorTempKelvin(params); ok {
		if err := l.Invoke(ctx, "set_kelvin", map[string]any{"kelvin": kelvin}, priority); err != nil {
			return err
		}
	}
	if e, ok := params["effect"]; ok {
		if err := l.Invoke(ctx, "set_effect", map[string]any{"effect": e}, priority); err != nil {
			return err
		}
	}
	return nil
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
	l.RegisterServiceWithArg("set_level", "level", l.applyHASetLevel)
	l.RegisterService("set_timer_on_time", func(_ context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		d, err := payload.ParamFloat64(params, "seconds")
		if err != nil {
			return err
		}
		l.SetTimerOnTime(time.Duration(d * float64(time.Second)))
		return nil
	})
}

// applyHASetLevel is the single definition of what an HA JSON-schema
// light command means, shared by every plane that can send one: it backs
// the `set_level` service method (the command_topic HA itself posts to)
// and the REST / WebSocket / MQTT custom-DP invoke plane reaches the same
// code through [payload.ServiceRegistry.Invoke]. Two copies of this
// ladder used to exist and had drifted — the second one dropped colour,
// colour temperature and effect on the floor.
//
// `set_level` accepts several payload shapes, one object per user action:
//
//	{"state":"ON","brightness":<0-255>}     — turn on at brightness
//	{"state":"OFF"}                         — turn off
//	{"brightness":<0-255>}                  — set brightness only
//	{"state":"ON","color":{"h":H,"s":S}}    — pick a colour
//	{"state":"ON","color_temp_kelvin":K}    — pick a colour temperature
//	{"state":"ON","effect":"<label>"}       — pick an effect
//	{"level":<0-1>}                         — legacy scalar form
//
// The colour / colour-temperature / effect keys travel on the same topic
// as on/off and brightness, so this handler has to apply them as well:
// dropping them makes a colour pick silently do nothing but toggle the
// lamp.
//
// The state literal is matched case-insensitively because that is what
// the REST/WS plane has always accepted; narrowing it would reject
// payloads that work today.
func (l *Light) applyHASetLevel(
	ctx context.Context, params map[string]any, priority hmenum.CommandPriority,
) error {
	if state, ok := params["state"]; ok {
		s, _ := state.(string)
		switch strings.ToUpper(s) {
		case "OFF":
			// Colour / effect keys are irrelevant for a switch-off.
			return l.TurnOff(ctx, priority)
		case "ON":
			if br, hasBr := params["brightness"]; hasBr {
				if f, err := toNumber(br); err == nil {
					if err := l.SetLevel(ctx, f/255.0, priority); err != nil {
						return err
					}
					return l.applyHALightAttributes(ctx, params, priority)
				}
			}
			if err := l.TurnOn(ctx, priority); err != nil {
				return err
			}
			return l.applyHALightAttributes(ctx, params, priority)
		}
	}
	if br, hasBr := params["brightness"]; hasBr {
		if f, err := toNumber(br); err == nil {
			if err := l.SetLevel(ctx, f/255.0, priority); err != nil {
				return err
			}
			return l.applyHALightAttributes(ctx, params, priority)
		}
	}
	if hasHALightAttributes(params) {
		return l.applyHALightAttributes(ctx, params, priority)
	}
	// Legacy scalar form: {"level": 0.5}.
	v, err := payload.ParamFloat64(params, "level")
	if err != nil {
		return err
	}
	// LEVEL is a 0..1 wire fraction. An out-of-range value is a caller
	// bug the device cannot honour, so it is rejected at the boundary
	// rather than forwarded to the wire.
	if v < 0 || v > 1 {
		return fmt.Errorf("%w: %q value %v out of range [0, 1]", payload.ErrServiceInvalidParam, "level", v)
	}
	return l.SetLevel(ctx, v, priority)
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
		// Color() reports HA-canonical 0..100 saturation (the wire 0..1
		// fraction scaled by 100), matching the ColorHS contract and the
		// reference stack's hs_color — emit verbatim.
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
		// ChannelHsColor already reports HA-canonical 0..100 (FixedColorToHS),
		// matching ColorLight.Color() — emit verbatim.
		out.Color = &payload.ColorHS{H: float64(hue), S: sat}
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
		// The HA JSON-schema form carries a free hue/saturation pair
		// because the discovery payload projects the discrete colour slot
		// onto HA's `hs` colour mode; snap it onto the nearest slot the
		// hardware actually has. The index form stays for REST/SPA
		// callers that pick a slot directly.
		if _, hasHue := params["hue"]; hasHue {
			hue, err := payload.ParamInt32(params, "hue")
			if err != nil {
				return err
			}
			sat, err := payload.ParamFloat64(params, "saturation")
			if err != nil {
				return err
			}
			return l.SetColor(ctx, HSToFixedColor(hue, sat), priority)
		}
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
		// "effect" is the key this method advertises — as its scalar-arg
		// key and in [EffectLight.LocalisableSelections] — so it has to be
		// the key it accepts. Home Assistant picks an effect by label, the
		// bare-scalar MQTT form arrives as a numeric string, and REST/SPA
		// callers pass the index; all three resolve here.
		if raw, ok := params["effect"]; ok {
			if label, isStr := raw.(string); isStr {
				if slices.Contains(l.Effects(), label) {
					return l.SetEffectByLabel(ctx, label, priority)
				}
				idx, err := strconv.ParseInt(label, 10, 32)
				if err != nil {
					return fmt.Errorf("effectlight: unknown effect label %q", label)
				}
				return l.SetEffect(ctx, int32(idx), priority)
			}
			v, err := payload.ParamInt32(params, "effect")
			if err != nil {
				return err
			}
			return l.SetEffect(ctx, v, priority)
		}
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
	if r.colorTempCombined {
		// HmIP-LSC: hs and colour temperature coexist; report the active axis
		// (the inactive one carries an empty wire value). Mirrors the reference
		// CustomDpIpRGBWColorTempLight.has_color_temperature.
		if r.colorTempKelvinActive() {
			out.ColorMode = "color_temp"
			if k, ok := r.Kelvin(); ok {
				kv := int(k)
				out.ColorTempKelvin = &kv
			}
		} else {
			out.ColorMode = "hs"
			out.Color = base.Color
		}
		return out
	}
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
	if !l.SupportsColor() {
		// No HUE / SATURATION pair and no COLOR integer: declaring the
		// hs mode would render a colour wheel whose every command is
		// refused, and the state payload would never carry a colour.
		return "light", body
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
	effects := l.Effects()
	if len(effects) == 0 {
		// No vocabulary, no picker. A substituted list is worse than
		// none: Home Assistant renders the dropdown, and every entry it
		// offers is refused on the way back because no lookup resolves
		// it.
		return "light", body
	}
	body["effect"] = true
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
// supported_color_modes follows the operating mode (HA colour modes are
// mutually exclusive):
//   - RGB / RGBW mode → ["hs"]
//   - TunableWhite    → ["color_temp"]
//   - PWM             → ["brightness"]
//   - unknown         → defaults to RGBW (["hs"]) via effectiveMode
//
// When HasColorTempColorMode() is true (TUNABLE_WHITE), color_temp_kelvin is
// added. When HasEffects() is true, effect + effect_list are added.
func (r *RGBWLight) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if r == nil || ctx == nil {
		return "", nil
	}
	_, body = r.ColorLight.HADiscoveryPayload(ctx)
	if body == nil {
		body = map[string]any{}
	}

	if r.colorTempCombined {
		// HmIP-LSC: RGBW hardware without DEVICE_OPERATION_MODE advertises hs
		// AND colour temperature at once; HA picks the active one via
		// color_mode. Mirrors the reference CustomDpIpRGBWColorTempLight
		// (_compute_capabilities sets hs_color and color_temperature both true).
		body["supported_color_modes"] = []string{"color_temp", "hs"}
		body["hs"] = true
		body["color_temp_kelvin"] = true
		body["min_kelvin"] = r.MinKelvin
		body["max_kelvin"] = r.MaxKelvin
		if effects := r.Effects(); len(effects) > 0 {
			body["effect"] = true
			body["effect_list"] = effects
		}
		return "light", body
	}

	// Compute supported_color_modes from the current operating mode. HA colour
	// modes are mutually exclusive, so RGBW advertises hs only (its KELVIN wire
	// field is still writable, but not as an HA colour mode). Unknown defaults
	// to RGBW via effectiveMode, mirroring the reference fallback.
	switch r.effectiveMode() {
	case RGBWModeRGBW, RGBWModeRGB:
		body["supported_color_modes"] = []string{"hs"}
	case RGBWModeTunableWhite:
		body["supported_color_modes"] = []string{"color_temp"}
		// Remove hs flag set by ColorLight — not applicable in tunable white mode.
		delete(body, "hs")
	default:
		// PWM: brightness only.
		body["supported_color_modes"] = []string{"brightness"}
		delete(body, "hs")
	}

	if r.HasColorTempColorMode() {
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

// LocalisableSelections implements [payload.LocalisableSelections]: the
// effect list is the VALUE_LIST of PROGRAM, and Home Assistant returns
// the operator's pick as `effect`. A device whose PROGRAM carries no
// VALUE_LIST falls back to the actuator's own vocabulary, whose labels
// are already human-readable and simply miss the translation lookup.
func (l *EffectLight) LocalisableSelections() []payload.LocalisableSelection {
	if l == nil || len(l.Effects()) == 0 {
		return nil
	}
	return []payload.LocalisableSelection{{
		BodyKey:   "effect_list",
		ArgKey:    "effect",
		Parameter: string(hmenum.ParameterProgram),
	}}
}
