// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- ColorLight: full HSV (HUE + SATURATION + LEVEL) ---

// ColorLight extends [Light] with HUE and SATURATION generic
// primitives. HUE / SATURATION are typed references to the channel's
// existing data points — no duplicate instances.
type ColorLight struct {
	*Light

	hue        *generic.Integer
	saturation *generic.Float
}

// NewColorLight constructs a ColorLight against the channel from cfg.
func NewColorLight(cfg Config) *ColorLight {
	cl := &ColorLight{
		Light:      New(cfg),
		hue:        custom.IntegerField(cfg.Channel, hmenum.ParameterHue),
		saturation: custom.FloatField(cfg.Channel, hmenum.ParameterSaturation),
	}
	if cl.Float != nil {
		cl.registerColorLightServices()
	}
	if cl.saturation != nil {
		_ = cl.saturation.OnConfirmedUpdate(func(_, _ float64) { cl.dataVersion.Bump() })
	}
	return cl
}

// NamePostfix overrides [Light.NamePostfix] with the "color" suffix
func (l *ColorLight) NamePostfix() string { return "color" }

// Subscribe overrides [Light.Subscribe] to also replay HUE and SATURATION
// when they already carry observed values. Without the replay, a reconnect
// that runs Subscribe after the initial CCU push would leave the color cache
// stale until the next push event.
func (l *ColorLight) Subscribe(ch *device.Channel) func() {
	unsub := l.Light.Subscribe(ch)
	// Replay HUE current value.
	if l.hue != nil {
		if v, observed := l.hue.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.hue.OnEvent(iv)
			}
		}
	}
	// Replay SATURATION current value.
	if l.saturation != nil {
		if v, observed := l.saturation.RawValue(); observed {
			if fv, ok := v.(float64); ok {
				l.saturation.OnEvent(fv)
			}
		}
	}
	return unsub
}

// Color returns the last observed (hue, saturation, observed) triple.
// observed is true when both underlying data points have been observed.
func (l *ColorLight) Color() (hue int32, saturation float64, observed bool) {
	if l.hue == nil || l.saturation == nil {
		return 0, 0, false
	}
	h, hOK := l.hue.Value()
	s, sOK := l.saturation.Value()
	return h, s, hOK && sOK
}

// SetColor commands a new (hue, saturation) pair. Hue wraps around 360°;
// saturation is clamped into [0, 1].
//
// Returns nil without writing when IsStateChangeFull reports no change for the
// given HS color — matches the turn_on guard pattern.
//
// HUE and SATURATION are grouped into one atomic put_paramset: a
// CallParameterCollector is attached to ctx, both dp.Set calls route through
// sendAndObserve which adds them to the collector, and the deferred Send
// dispatches a single put_paramset. Mirrors the CombinedDpHsColor collector
// pattern (hs_color.py:82-91).
func (l *ColorLight) SetColor(ctx context.Context, hue int32, saturation float64, priority hmenum.CommandPriority) error {
	hue = ((hue % 360) + 360) % 360
	if saturation < 0 {
		saturation = 0
	}
	if saturation > 1 {
		saturation = 1
	}
	hs := HSColor{Hue: float64(hue), Saturation: saturation}
	if !l.IsStateChangeFull(StateChangeArgsFull{HSColor: &hs}) {
		return nil
	}
	if l.hue == nil || l.saturation == nil {
		return errors.New("colorlight: channel missing HUE or SATURATION")
	}
	if l.hue.Writer == nil {
		return errors.New("colorlight: no writer")
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(l.hue.Writer), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	defer func() { _ = coll.Send(ctx) }()
	if err := l.hue.Set(ctx, hue, priority); err != nil {
		return fmt.Errorf("colorlight: HUE: %w", err)
	}
	if err := l.saturation.Set(ctx, saturation, priority); err != nil {
		return fmt.Errorf("colorlight: SATURATION: %w", err)
	}
	return nil
}

// --- ColorTempLight: COLOR_TEMPERATURE sliders ---

// ColorTempLight is a dimmable light with variable colour temperature.
// KELVIN is a typed reference to the channel's COLOR_TEMPERATURE DP.
type ColorTempLight struct {
	*Light

	MinKelvin int32
	MaxKelvin int32

	kelvin *generic.Integer
}

// NewColorTempLight constructs a ColorTempLight. Kelvin bounds default
// to 2000 / 6500 when zero or negative values are passed.
func NewColorTempLight(cfg Config, minK, maxK int32) *ColorTempLight {
	if minK <= 0 {
		minK = 2000
	}
	if maxK <= 0 {
		maxK = 6500
	}
	ct := &ColorTempLight{
		Light:     New(cfg),
		MinKelvin: minK,
		MaxKelvin: maxK,
		kelvin:    custom.IntegerField(cfg.Channel, hmenum.ParameterColorTemperature),
	}
	if ct.Float != nil {
		ct.registerColorTempLightServices()
	}
	if ct.kelvin != nil {
		_ = ct.kelvin.OnConfirmedUpdate(func(_, _ int32) { ct.dataVersion.Bump() })
	}
	return ct
}

// Kelvin returns the last observed colour temperature.
func (l *ColorTempLight) Kelvin() (int32, bool) {
	if l.kelvin == nil {
		return 0, false
	}
	return l.kelvin.Value()
}

// NamePostfix overrides [Light.NamePostfix] with the "color_temp" suffix.
func (l *ColorTempLight) NamePostfix() string { return "color_temp" }

// Subscribe overrides [Light.Subscribe] to replay the COLOR_TEMPERATURE DP
// when it already carries an observed value. Without the replay a reconnect
// that runs Subscribe after the initial CCU push would leave the kelvin cache
// stale until the next push event, causing the MQTT color_temp attribute to
// surface "unknown" on restart.
func (l *ColorTempLight) Subscribe(ch *device.Channel) func() {
	unsub := l.Light.Subscribe(ch)
	if l.kelvin != nil {
		if v, observed := l.kelvin.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.kelvin.OnEvent(iv)
			}
		}
	}
	return unsub
}

// SetKelvin commands a new colour temperature. Values are clamped to
// the [MinKelvin, MaxKelvin] range.
//
// Returns nil without writing when IsStateChangeFull reports no change for the
// given colour temperature — matches the turn_on guard pattern.
func (l *ColorTempLight) SetKelvin(ctx context.Context, v int32, priority hmenum.CommandPriority) error {
	if v < l.MinKelvin {
		v = l.MinKelvin
	}
	if v > l.MaxKelvin {
		v = l.MaxKelvin
	}
	// Guard the uint16 narrowing with a dominating bounds check so the
	// conversion is reached only for in-range values. The
	// [MinKelvin, MaxKelvin] clamp above already keeps v in range for any
	// sane configuration; expressing the bound as a guard around the
	// conversion (rather than a clamp-by-reassignment) is the form the
	// static analyser recognises as safe.
	var kelvinU16 uint16
	if v >= 0 && v <= math.MaxUint16 {
		kelvinU16 = uint16(v)
	}
	if !l.IsStateChangeFull(StateChangeArgsFull{ColorTempKelvin: &kelvinU16}) {
		return nil
	}
	if l.kelvin == nil {
		return errors.New("colortemp: channel missing COLOR_TEMPERATURE")
	}
	if err := l.kelvin.Set(custom.EnsureContext(ctx), v, priority); err != nil {
		return fmt.Errorf("colortemp: SET: %w", err)
	}
	return nil
}

// --- ColorBehaviour: post-on colour restore ---

// ColorBehaviour enumerates the values the COLOR_BEHAVIOUR parameter accepts.
// It controls what the device does to its colour channel when the light
// transitions from off to on or during a programmed sequence.
type ColorBehaviour string

// ColorBehaviour values.
const (
	// ColorBehaviourDoNotCare leaves the colour channel as-is.
	ColorBehaviourDoNotCare ColorBehaviour = "DO_NOT_CARE"
	// ColorBehaviourOff turns the colour off on activation.
	ColorBehaviourOff ColorBehaviour = "OFF"
	// ColorBehaviourOldValue restores the last observed colour.
	ColorBehaviourOldValue ColorBehaviour = "OLD_VALUE"
	// ColorBehaviourOn activates the colour on turn-on.
	ColorBehaviourOn ColorBehaviour = "ON"
)

// --- FixedColorLight: enum-valued COLOR ---

// FixedColor enumerates the indices a fixed-colour light accepts.
type FixedColor int32

// FixedColor values.
const (
	FixedColorBlack   FixedColor = 0
	FixedColorRed     FixedColor = 1
	FixedColorGreen   FixedColor = 2
	FixedColorYellow  FixedColor = 3
	FixedColorBlue    FixedColor = 4
	FixedColorMagenta FixedColor = 5
	FixedColorCyan    FixedColor = 6
	FixedColorWhite   FixedColor = 7
)

// FixedColorLight is a light with a COLOR parameter that is an enum
// rather than HSV. LEVEL still drives brightness.
type FixedColorLight struct {
	*Light

	color          *generic.Select
	colorBehaviour *generic.Select
	channelColor   *generic.Sensor[string]
}

// NewFixedColorLight constructs a FixedColorLight.
func NewFixedColorLight(cfg Config) *FixedColorLight {
	fc := &FixedColorLight{
		Light:          New(cfg),
		color:          custom.SelectField(cfg.Channel, hmenum.ParameterColor),
		colorBehaviour: custom.SelectField(cfg.Channel, hmenum.ParameterColorBehaviour),
		channelColor:   custom.StringSensorField(cfg.Channel, hmenum.ParameterChannelColor),
	}
	// Signal lights reset the device-side ON_TIME duration on every plain
	// turn_on; RGBW/DALI must not (see Light.resetsOnTimeOnTurnOn).
	fc.resetsOnTimeOnTurnOn = true
	if fc.Float != nil {
		fc.registerFixedColorLightServices()
	}
	if fc.channelColor != nil {
		_ = fc.channelColor.OnConfirmedUpdate(func(_, _ string) { fc.dataVersion.Bump() })
	}
	if fc.colorBehaviour != nil {
		_ = fc.colorBehaviour.OnConfirmedUpdate(func(_, _ int32) { fc.dataVersion.Bump() })
	}
	return fc
}

// NamePostfix overrides [Light.NamePostfix] with the "color" suffix
func (l *FixedColorLight) NamePostfix() string { return "color" }

// Subscribe overrides [Light.Subscribe] to also replay the COLOR,
// COLOR_BEHAVIOUR and CHANNEL_COLOR DPs when they already carry observed
// values. Without the replay a reconnect that runs Subscribe after the
// initial CCU push would leave the fixed-color cache stale until the
// next push event.
func (l *FixedColorLight) Subscribe(ch *device.Channel) func() {
	unsub := l.Light.Subscribe(ch)
	// Replay COLOR current value.
	if l.color != nil {
		if v, observed := l.color.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.color.OnEvent(iv)
			}
		}
	}
	// Replay COLOR_BEHAVIOUR current value.
	if l.colorBehaviour != nil {
		if v, observed := l.colorBehaviour.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.colorBehaviour.OnEvent(iv)
			}
		}
	}
	// Replay CHANNEL_COLOR current value.
	if l.channelColor != nil {
		if v, observed := l.channelColor.RawValue(); observed {
			if sv, ok := v.(string); ok {
				l.channelColor.OnEvent(sv)
			}
		}
	}
	return unsub
}

// Color returns the last observed colour slot.
func (l *FixedColorLight) Color() (FixedColor, bool) {
	if l.color == nil {
		return 0, false
	}
	v, ok := l.color.Value()
	return FixedColor(v), ok
}

// SetColor commands a new colour slot. The wire value is the string label
// (e.g. "WHITE", "RED") from [fixedColorNames] — the CCU's DpSelect type
// expects the enum string directly for IP devices. After the wire write
// the local index is updated optimistically so Color() / ColorName() reflect
// the new value immediately, before the CCU echo arrives.
func (l *FixedColorLight) SetColor(ctx context.Context, c FixedColor, priority hmenum.CommandPriority) error {
	if l.color == nil {
		return errors.New("fixedcolor: channel missing COLOR")
	}
	if l.Writer == nil {
		return errors.New("fixedcolor: no writer configured")
	}
	name, ok := fixedColorNames[c]
	if !ok {
		return fmt.Errorf("fixedcolor: unknown color index %d", c)
	}
	addr := l.color.DataPointKey().ChannelAddress
	if err := l.Writer.SetValue(custom.EnsureContext(ctx), addr, hmenum.ParameterColor, name, priority); err != nil {
		return fmt.Errorf("fixedcolor: SET: %w", err)
	}
	// Optimistic local update so Color() / ColorName() reflect the new slot
	// immediately, before the CCU confirms the write.
	l.color.OnEvent(int32(c))
	return nil
}

// fixedColorNames maps [FixedColor] integer values to their CCU string
// names, mirroring Python's StrEnum values (light.py:63-75).
var fixedColorNames = map[FixedColor]string{
	FixedColorBlack:   "BLACK",
	FixedColorRed:     "RED",
	FixedColorGreen:   "GREEN",
	FixedColorYellow:  "YELLOW",
	FixedColorBlue:    "BLUE",
	FixedColorMagenta: "PURPLE",
	FixedColorCyan:    "TURQUOISE",
	FixedColorWhite:   "WHITE",
}

// ColorName returns the string name of the currently observed COLOR parameter
// (e.g. "WHITE", "RED"). Returns ("", false) when the value has never been
// observed.
func (l *FixedColorLight) ColorName() (string, bool) {
	c, ok := l.Color()
	if !ok {
		return "", false
	}
	if name, found := fixedColorNames[c]; found {
		return name, true
	}
	return "", false
}

// CurrentColorBehaviour returns the last observed COLOR_BEHAVIOUR label
// (e.g. "ON", "OLD_VALUE", "BLINKING_LONG"). Returns ("", false) when the
// parameter has never been observed or the channel does not carry it.
func (l *FixedColorLight) CurrentColorBehaviour() (string, bool) {
	if l.colorBehaviour == nil {
		return "", false
	}
	return l.colorBehaviour.Label()
}

// SetColorBehaviour writes a new COLOR_BEHAVIOUR value by label.
func (l *FixedColorLight) SetColorBehaviour(ctx context.Context, behaviour ColorBehaviour, priority hmenum.CommandPriority) error {
	if l.colorBehaviour == nil {
		return errors.New("fixedcolor: channel missing COLOR_BEHAVIOUR")
	}
	if err := l.colorBehaviour.SetLabel(custom.EnsureContext(ctx), string(behaviour), priority); err != nil {
		return fmt.Errorf("fixedcolor: SET COLOR_BEHAVIOUR: %w", err)
	}
	return nil
}

// ChannelHsColor returns the (hue, saturation) pair derived from the
// CHANNEL_COLOR parameter, which reflects the currently active colour slot as
// reported by the CCU (may differ from COLOR during transitions). Returns (0,
// 0, false) when not observed.
func (l *FixedColorLight) ChannelHsColor() (hue int32, saturation float64, ok bool) {
	if l.channelColor == nil {
		return 0, 0, false
	}
	name, observed := l.channelColor.Value()
	if !observed {
		return 0, 0, false
	}
	// Map the CCU string name to a FixedColor index then to HS.
	for fc, n := range fixedColorNames {
		if n == name {
			h, s := FixedColorToHS(fc)
			return h, s, true
		}
	}
	// Unknown name: return minimum hue/saturation (mirrors Python's fallback
	// of (_MIN_HUE, _MIN_SATURATION) in FIXED_COLOR_TO_HS_CONVERTER.get).
	return 0, 0, true
}

// HSToFixedColor maps an (hue, saturation) pair onto the closest FixedColor
// slot. Saturation < 0.05 collapses to WHITE; otherwise the hue is banded
// into 6 60° segments around the colour wheel:
//
// 0..30°       → RED 30..90°       → YELLOW 90..150°      → GREEN 150..210°
// → CYAN 210..270°      → BLUE 270..330°      → MAGENTA 330..360°      → RED
//
// Hue values are normalised into [0, 360) before banding.
func HSToFixedColor(hue int32, saturation float64) FixedColor {
	if saturation < 0.05 {
		return FixedColorWhite
	}
	h := ((int(hue) % 360) + 360) % 360
	switch {
	case h < 30 || h >= 330:
		return FixedColorRed
	case h < 90:
		return FixedColorYellow
	case h < 150:
		return FixedColorGreen
	case h < 210:
		return FixedColorCyan
	case h < 270:
		return FixedColorBlue
	default:
		return FixedColorMagenta
	}
}

// FixedColorToHS maps a FixedColor onto the canonical (hue, saturation)
// representation used by HA-style consumers.
func FixedColorToHS(c FixedColor) (hue int32, saturation float64) {
	switch c { //nolint:exhaustive // FixedColorBlack maps to (0, 0) same as the default return; no dedicated case needed
	case FixedColorWhite:
		return 0, 0
	case FixedColorRed:
		return 0, 1
	case FixedColorYellow:
		return 60, 1
	case FixedColorGreen:
		return 120, 1
	case FixedColorCyan:
		return 180, 1
	case FixedColorBlue:
		return 240, 1
	case FixedColorMagenta:
		return 300, 1
	}
	return 0, 0
}

// FixedColorOnConfig bundles the optional parameters
// [FixedColorLight.TurnOnFixedColor] understands.
type FixedColorOnConfig struct {
	Color          FixedColor
	HasColor       bool
	ColorBehaviour string         // "ON", "BLINKING_LONG", "BLINKING_SHORT", …
	Duration       *time.Duration // optional ON_TIME_UNIT/VALUE timer
	Brightness     *float64       // optional LEVEL override (default LastLevel())
}

// TurnOnFixedColor sends the full COLOR + COLOR_BEHAVIOUR + ON_TIME_* +
// LEVEL bundle as one atomic put_paramset. HmIP-BSL expects ON_TIME_VALUE /
// ON_TIME_UNIT (not DURATION_*) for its timer channel.
//
// Falls back to per-parameter SetValue when the writer has no PutParamset
// support. The address used is the embedded LEVEL data point's channel
// address.
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (l *FixedColorLight) TurnOnFixedColor(ctx context.Context, cfg FixedColorOnConfig, priority hmenum.CommandPriority) error {
	if l.Float == nil {
		return errors.New("fixedcolor: channel missing LEVEL")
	}
	addr := l.DataPointKey().ChannelAddress
	w := l.Writer
	if w == nil {
		return errors.New("fixedcolor: no writer configured")
	}
	level := l.turnOnLevel()
	if cfg.Brightness != nil {
		level = *cfg.Brightness
	}
	params := map[hmenum.Parameter]any{
		hmenum.ParameterLevel: level,
	}
	if cfg.HasColor {
		if name, ok := fixedColorNames[cfg.Color]; ok {
			params[hmenum.ParameterColor] = name
		} else {
			params[hmenum.ParameterColor] = fixedColorNames[FixedColorWhite]
		}
	}
	// COLOR_BEHAVIOUR fallback: when the caller leaves the field empty but a
	// colour has been set, the CCU expects "ON" so the LED actually lights up
	// rather than waiting for a behaviour (BLINKING_*, BILLOW_*) selection.
	if cfg.ColorBehaviour == "" && cfg.HasColor {
		params[hmenum.ParameterColorBehaviour] = "ON"
	} else if cfg.ColorBehaviour != "" {
		params[hmenum.ParameterColorBehaviour] = cfg.ColorBehaviour
	}
	if cfg.Duration != nil {
		v, u := custom.EncodeTimerDuration(*cfg.Duration)
		params[hmenum.ParameterOnTimeUnit] = u
		params[hmenum.ParameterOnTimeValue] = v
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(w), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	defer func() { _ = coll.Send(ctx) }()
	l.recordLastSent(level)
	if err := custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, params, priority); err != nil {
		l.clearLastSent()
		return err
	}
	return nil
}
