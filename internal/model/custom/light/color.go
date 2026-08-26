// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
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

	// colorIndex is the single COLOR integer the RF colour dimmers carry
	// instead of a HUE / SATURATION pair. It lives on a sibling channel
	// (HM-LC-RGBW-WM: LEVEL on :1, COLOR on :2) which the profile's
	// FieldColor mapping names, so it is resolved by
	// [newColorLightOn] rather than off the light's own channel. Exactly
	// one of (hue+saturation) and colorIndex is populated per device
	// family; when neither is, the light carries no colour axis at all.
	colorIndex *generic.Integer
}

// colorIndexWhite is the COLOR value that means "white": the wire
// encodes the hue circle as 0..199 and reserves 200 for the white
// point. Larger values are undefined and are read back as white for
// robustness. Mirrors `CustomDpColorDimmer.hs_color` (light.py:447-460).
const (
	colorIndexWhite = 200
	colorIndexSpan  = 199
	// colorWhiteSaturationCutoff is the saturation below which a command
	// is treated as a request for white. HA-canonical 0..100, mirroring
	// the reference's `saturation < 0.1` on its 0..1 fraction
	// (light.py:471).
	colorWhiteSaturationCutoff = 10.0
)

// NewColorLight constructs a ColorLight against the channel from cfg.
func NewColorLight(cfg Config) *ColorLight {
	return newColorLightOn(cfg, nil)
}

// newColorLightOn is [NewColorLight] with an explicit COLOR channel for
// the RF colour dimmers, whose colour lives one channel above the
// light's own. Pass nil for the HmIP families, which carry HUE and
// SATURATION on the light channel itself.
func newColorLightOn(cfg Config, colorChannel *device.Channel) *ColorLight {
	cl := &ColorLight{
		Light:      New(cfg),
		hue:        custom.IntegerField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldHue, hmenum.ParameterHue)),
		saturation: custom.FloatField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldSaturation, hmenum.ParameterSaturation)),
	}
	if cl.hue == nil || cl.saturation == nil {
		cl.colorIndex = custom.IntegerField(colorChannel, hmenum.ParameterColor)
	}
	if cl.Float != nil {
		cl.registerColorLightServices()
	}
	if cl.saturation != nil {
		_ = cl.saturation.OnConfirmedUpdate(func(_, _ float64) { cl.dataVersion.Bump() })
	}
	if cl.colorIndex != nil {
		_ = cl.colorIndex.OnConfirmedUpdate(func(_, _ int32) { cl.dataVersion.Bump() })
	}
	// Attach an HSColor combined DP so the aggregate (hue + saturation) is
	// surfaced on the event bus and visible via Channel.CombinedDataPoints.
	// The write path remains ColorLight.SetColor; HSColor is read-side only.
	if cfg.Channel != nil && cl.hue != nil && cl.saturation != nil {
		hs := combined.NewHSColorWithCentral(
			cfg.Channel.CentralName(),
			cfg.Channel.Address,
			cfg.Writer,
			hmenum.ParameterHue,
			hmenum.ParameterSaturation,
		)
		cfg.Channel.AttachCalculatedDataPoint(hs)
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
	// Replay the single-integer COLOR of the RF colour dimmers.
	if l.colorIndex != nil {
		if v, observed := l.colorIndex.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.colorIndex.OnEvent(iv)
			}
		}
	}
	return unsub
}

// SupportsColor reports whether the light carries a colour axis at all —
// either the HUE / SATURATION pair or the single COLOR integer. The HA
// discovery payload declares the `hs` colour mode on this, so a light
// that answers false must not render a colour wheel it cannot serve.
func (l *ColorLight) SupportsColor() bool {
	if l == nil {
		return false
	}
	return (l.hue != nil && l.saturation != nil) || l.colorIndex != nil
}

// Color returns the last observed (hue, saturation, observed) triple.
// observed is true when both underlying data points have been observed.
//
// Saturation is reported HA-canonical 0..100, mirroring
// CeColorDimmer.hs_color reading through _SATURATION_MULTIPLIER=100
// (model/custom/light.py): the wire SATURATION DP carries a 0..1 fraction,
// scaled by 100 here so every north-bound consumer (MQTT/REST/WS ColorHS)
// and the Matter projection share one unit.
func (l *ColorLight) Color() (hue int32, saturation float64, observed bool) {
	if l.hue == nil || l.saturation == nil {
		return l.colorFromIndex()
	}
	h, hOK := l.hue.Value()
	s, sOK := l.saturation.Value()
	return h, s * 100, hOK && sOK
}

// colorFromIndex projects the RF colour dimmers' single COLOR integer
// onto the same (hue, saturation) surface the HUE / SATURATION pair
// produces. Mirrors `CustomDpColorDimmer.hs_color` (light.py:447-460):
// COLOR >= 200 is the white point (saturation 0), everything else walks
// the hue circle in 200 steps at full saturation.
func (l *ColorLight) colorFromIndex() (hue int32, saturation float64, observed bool) {
	if l == nil || l.colorIndex == nil {
		return 0, 0, false
	}
	c, ok := l.colorIndex.Value()
	if !ok {
		return 0, 0, false
	}
	if c >= colorIndexWhite {
		return 0, 0, true
	}
	if c < 0 {
		c = 0
	}
	return int32(float64(c) / colorIndexWhite * 360), 100, true //nolint:gosec // c < 200 keeps the product below 360
}

// SetColor commands a new (hue, saturation) pair. Hue wraps around 360°;
// saturation is HA-canonical 0..100 and is clamped into that range.
//
// The incoming saturation is scaled to the wire's 0..1 SATURATION DP before
// the write, mirroring set_hs_color dividing by
// _SATURATION_MULTIPLIER=100 (model/custom/light.py). [Color] performs the
// inverse on read, so a north-bound round-trip is unit-consistent.
//
// Returns nil without writing when IsStateChangeFull reports no change for the
// given HS color — matches the turn_on guard pattern.
//
// HUE and SATURATION are grouped into one atomic put_paramset: a
// CallParameterCollector is attached to ctx, both dp.Set calls route through
// sendAndObserve which adds them to the collector, and the deferred Send
// dispatches a single put_paramset. Mirrors the CombinedDpHsColor collector
// pattern (hs_color.py:82-91).
func (l *ColorLight) SetColor(
	ctx context.Context, hue int32, saturation float64, priority hmenum.CommandPriority,
) (err error) {
	hue = ((hue % 360) + 360) % 360
	if saturation < 0 {
		saturation = 0
	}
	if saturation > 100 {
		saturation = 100
	}
	// HSColor and the wire SATURATION DP carry the 0..1 fraction; convert the
	// HA-canonical 0..100 input once here.
	wireSat := saturation / 100
	hs := HSColor{Hue: float64(hue), Saturation: wireSat}
	if !l.IsStateChangeFull(StateChangeArgsFull{HSColor: &hs}) {
		return nil
	}
	if l.hue == nil || l.saturation == nil {
		return l.setColorIndex(ctx, hue, saturation, priority)
	}
	if l.hue.Writer == nil {
		return errors.New("colorlight: no writer")
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(l.hue.Writer), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// HUE and SATURATION are staged into the collector, so the
	// put_paramset only happens in the flush: its error is the wire
	// result of the whole colour change and must reach the caller.
	defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	if err = l.hue.Set(ctx, hue, priority); err != nil {
		err = fmt.Errorf("colorlight: HUE: %w", err)
		return err
	}
	if err = l.saturation.Set(ctx, wireSat, priority); err != nil {
		err = fmt.Errorf("colorlight: SATURATION: %w", err)
		return err
	}
	return nil
}

// setColorIndex is the [ColorLight.SetColor] write half for the RF
// colour dimmers, which carry one COLOR integer instead of a HUE /
// SATURATION pair. Mirrors `CustomDpColorDimmer.turn_on`
// (light.py:462-473): a saturation below the white cutoff commands the
// white point, everything else maps the hue circle onto 0..199.
func (l *ColorLight) setColorIndex(
	ctx context.Context, hue int32, saturation float64, priority hmenum.CommandPriority,
) error {
	if l.colorIndex == nil {
		return errors.New("colorlight: channel missing HUE or SATURATION")
	}
	value := int32(colorIndexWhite)
	if saturation >= colorWhiteSaturationCutoff {
		value = int32(math.Round(float64(hue) / 360 * colorIndexSpan))
	}
	if err := l.colorIndex.Set(custom.EnsureContext(ctx), value, priority); err != nil {
		return fmt.Errorf("colorlight: COLOR: %w", err)
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

	// colorLevel is the white-point channel the RF tunable-white dimmers
	// use instead of COLOR_TEMPERATURE, which they do not have: the
	// profile maps COLOR_LEVEL onto the LEVEL of the channel above this
	// one, and the value converts through mireds. Exactly one of kelvin
	// and colorLevel is populated per device family.
	colorLevel *generic.Float
}

// NewColorTempLight constructs a ColorTempLight reading COLOR_TEMPERATURE
// off its own channel — the HmIP shape. Kelvin bounds default to
// 2000 / 6500 when zero or negative values are passed.
func NewColorTempLight(cfg Config, minK, maxK int32) *ColorTempLight {
	return newColorTempLightOn(cfg, minK, maxK, nil)
}

// newColorTempLightOn is [NewColorTempLight] with the white-point channel
// the RF families carry their colour temperature on. A nil whitePoint
// keeps the COLOR_TEMPERATURE-only behaviour.
func newColorTempLightOn(cfg Config, minK, maxK int32, whitePoint *device.Channel) *ColorTempLight {
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
		kelvin:    custom.IntegerField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldColorTemperature, hmenum.ParameterColorTemperature)),
	}
	if ct.kelvin == nil && whitePoint != nil {
		ct.colorLevel = custom.FloatField(whitePoint, hmenum.ParameterLevel)
	}
	if ct.Float != nil {
		ct.registerColorTempLightServices()
	}
	if ct.kelvin != nil {
		_ = ct.kelvin.OnConfirmedUpdate(func(_, _ int32) { ct.dataVersion.Bump() })
	}
	if ct.colorLevel != nil {
		_ = ct.colorLevel.OnConfirmedUpdate(func(_, _ float64) { ct.dataVersion.Bump() })
	}
	return ct
}

// Kelvin returns the last observed colour temperature.
func (l *ColorTempLight) Kelvin() (int32, bool) {
	if l.kelvin != nil {
		return l.kelvin.Value()
	}
	if l.colorLevel == nil {
		return 0, false
	}
	level, observed := l.colorLevel.Value()
	if !observed {
		return 0, false
	}
	return kelvinFromColorLevel(level), true
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
	switch {
	case l.kelvin != nil:
		if err := l.kelvin.Set(custom.EnsureContext(ctx), v, priority); err != nil {
			return fmt.Errorf("colortemp: SET: %w", err)
		}
	case l.colorLevel != nil:
		if err := l.colorLevel.Set(custom.EnsureContext(ctx), colorLevelFromKelvin(v), priority); err != nil {
			return fmt.Errorf("colortemp: SET: %w", err)
		}
	default:
		return errors.New("colortemp: channel carries neither COLOR_TEMPERATURE nor a white-point level")
	}
	return nil
}

// Mireds are the unit the colour temperature of the RF tunable-white
// dimmers is expressed in: the white-point level runs 0..1 across the
// mired range, and kelvin is its reciprocal. The three constants and the
// exact arithmetic — including the truncation — come from the reference
// implementation (model/custom/light.py, CustomDpColorTempDimmer).
const (
	maxKelvinScale = 1_000_000
	maxMireds      = 500
	minMireds      = 153
)

// kelvinFromColorLevel converts a 0..1 white-point level to kelvin.
func kelvinFromColorLevel(level float64) int32 {
	mireds := int(maxMireds - (maxMireds-minMireds)*level)
	if mireds <= 0 {
		return 0
	}
	return int32(maxKelvinScale / mireds) //nolint:gosec // bounded by minMireds: at most 6535
}

// colorLevelFromKelvin is the inverse, clamped to the 0..1 the wire
// parameter accepts.
func colorLevelFromKelvin(kelvin int32) float64 {
	if kelvin <= 0 {
		return 0
	}
	level := float64(maxMireds-maxKelvinScale/int(kelvin)) / float64(maxMireds-minMireds)
	return math.Min(1, math.Max(0, level))
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
	// channelColor is the colour slot the CCU reports as currently
	// active, which can differ from the commanded COLOR during a
	// transition. The profile declares it as the `channel_color` channel
	// field rather than as a wire parameter on this channel, so it is
	// bound by [FixedColorLight.bindChannelColor] and stays nil for a
	// profile that declares no such field. The read-only ENUM resolves to
	// a raw-index Sensor[int32]; the label comes from
	// [custom.EnumLabelValue] on read.
	channelColor *generic.Sensor[int32]
}

// NewFixedColorLight constructs a FixedColorLight.
func NewFixedColorLight(cfg Config) *FixedColorLight {
	fc := &FixedColorLight{
		Light:          New(cfg),
		color:          custom.SelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldColor, hmenum.ParameterColor)),
		colorBehaviour: custom.SelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldColorBehaviour, hmenum.ParameterColorBehaviour)),
	}
	// Signal lights reset the device-side ON_TIME duration on every plain
	// turn_on; RGBW/DALI must not (see Light.resetsOnTimeOnTurnOn).
	fc.resetsOnTimeOnTurnOn = true
	if fc.Float != nil {
		fc.registerFixedColorLightServices()
	}
	if fc.colorBehaviour != nil {
		_ = fc.colorBehaviour.OnConfirmedUpdate(func(_, _ int32) { fc.dataVersion.Bump() })
	}
	return fc
}

// bindChannelColor resolves the profile's `channel_color` field onto the
// [FixedColorLight.channelColor] slot.
//
// The field names a channel *and* a parameter: an HmIP signal light
// carries the writable COLOR on the action channel this data point owns
// and the read-only one on the group's state channel, and the profile
// declares the latter. Resolving the field name as if it were a wire
// parameter of this channel found nothing on any device — no CCU
// description carries a CHANNEL_COLOR parameter — so the slot was nil
// fleet-wide while the HA discovery payload kept advertising an `hs`
// colour mode whose state never arrived.
func (l *FixedColorLight) bindChannelColor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if l == nil || ch == nil {
		return
	}
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldChannelColor]
		if !ok {
			continue
		}
		param, _ := custom.ResolveFieldValue(fv)
		if param == "" {
			continue
		}
		target := ch
		if chNo != custom.AnyChannelOffset && chNo != ch.Number {
			target = siblingChannel(ch, chNo)
		}
		dp := custom.EnumSensorField(target, param)
		if dp == nil {
			continue
		}
		l.channelColor = dp
		_ = dp.OnConfirmedUpdate(func(_, _ int32) { l.dataVersion.Bump() })

		return
	}
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
	// Replay CHANNEL_COLOR current value (raw index; the label is resolved
	// on read via custom.EnumLabelValue).
	if l.channelColor != nil {
		if v, observed := l.channelColor.RawValue(); observed {
			if iv, ok := v.(int32); ok {
				l.channelColor.OnEvent(iv)
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

// SetColorByName commands a colour slot by its CCU enum name (e.g. "BLUE").
// The COLOR descriptor a CCU reports orders its value list by the RGB bit
// pattern, which is not the order [FixedColor] enumerates, so a caller that
// only has the descriptor must address the slot by name rather than by index.
// Returns an error when the name is not one of the eight known colours.
func (l *FixedColorLight) SetColorByName(ctx context.Context, name string, priority hmenum.CommandPriority) error {
	for c, n := range fixedColorNames {
		if n == name {
			return l.SetColor(ctx, c, priority)
		}
	}
	return fmt.Errorf("fixedcolor: unknown color name %q", name)
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
	name, observed := custom.EnumLabelValue(l.channelColor)
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
// slot. Saturation is HA-canonical 0..100; values < 5 collapse to WHITE
// (mirrors hs_color_to_fixed_converter `int(color[1]) < 5`,
// model/custom/light.py). Otherwise the hue is banded into 6 60° segments
// around the colour wheel:
//
// 0..30°       → RED 30..90°       → YELLOW 90..150°      → GREEN 150..210°
// → CYAN 210..270°      → BLUE 270..330°      → MAGENTA 330..360°      → RED
//
// Hue values are normalised into [0, 360) before banding.
func HSToFixedColor(hue int32, saturation float64) FixedColor {
	if saturation < 5 {
		return FixedColorWhite
	}
	h := ((int(hue) % 360) + 360) % 360
	// Exclusive-low / inclusive-high bands mirror
	// hs_color_to_fixed_converter (light.py:824-834) exactly: a hue
	// landing precisely on a boundary (30/90/150/210/270/330) belongs to
	// the band below it, not the one above — e.g. hue=330 is PURPLE, not
	// the next RED wrap. Getting this backwards shifts all six boundary
	// hues one segment clockwise from what the reference reports.
	switch {
	case h > 30 && h <= 90:
		return FixedColorYellow
	case h > 90 && h <= 150:
		return FixedColorGreen
	case h > 150 && h <= 210:
		return FixedColorCyan
	case h > 210 && h <= 270:
		return FixedColorBlue
	case h > 270 && h <= 330:
		return FixedColorMagenta
	default:
		return FixedColorRed
	}
}

// FixedColorToHS maps a FixedColor onto the canonical (hue, saturation)
// representation used by HA-style consumers. Saturation is HA-canonical
// 0..100: every chromatic slot is full saturation (100) and WHITE/BLACK are
// 0, mirroring FIXED_COLOR_TO_HS_CONVERTER with
// _MAX_SATURATION=100.0 / _MIN_SATURATION=0 (model/custom/light.py).
func FixedColorToHS(c FixedColor) (hue int32, saturation float64) {
	switch c { //nolint:exhaustive // FixedColorBlack maps to (0, 0) same as the default return; no dedicated case needed
	case FixedColorWhite:
		return 0, 0
	case FixedColorRed:
		return 0, 100
	case FixedColorYellow:
		return 60, 100
	case FixedColorGreen:
		return 120, 100
	case FixedColorCyan:
		return 180, 100
	case FixedColorBlue:
		return 240, 100
	case FixedColorMagenta:
		return 300, 100
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
	l.recordLastSent(level)
	err := custom.PutOrSet(ctx, w, addr, hmenum.ParamsetKeyValues, params, priority)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	if err = generic.FlushCollector(ctx, coll, err); err != nil {
		l.clearLastSent()
		return err
	}
	return nil
}
