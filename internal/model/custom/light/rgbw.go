// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// RGBWMode enumerates the operating modes a HmIP-RGBW (and friends) channel
// can run in. The DEVICE_OPERATION_MODE parameter selects which sub-set of
// inputs is honoured.
type RGBWMode int

// RGBWMode values.
const (
	RGBWModeUnknown      RGBWMode = iota
	RGBWModePWM                   // simple LEVEL only
	RGBWModeRGB                   // HUE + SATURATION + LEVEL
	RGBWModeRGBW                  // RGB + COLOR_TEMPERATURE
	RGBWModeTunableWhite          // COLOR_TEMPERATURE + LEVEL
)

// RGBWUsage discriminates how a [RGBWLight] channel should be treated by
// north-bound adapters (MQTT discovery, REST). It mirrors the Python
// `DataPointUsage` values that `CustomDpIpRGBWLight.usage` returns
// (model/custom/light.py:531-542):
//
// - RGBWUsagePrimary — the channel is active for its current mode and
// should be exposed as a full device entity.
// - RGBWUsageSecondary — the channel carries a secondary function in
// the current mode (e.g. channels 2-4 in RGB/RGBW mode or channels
// 3-4 in tunable-white mode) and should be hidden from discovery.
// - RGBWUsageUnknown — operating mode has not yet been observed; the
// caller must re-check after the first Subscribe callback.
type RGBWUsage int

// RGBWUsage values.
const (
	RGBWUsageUnknown   RGBWUsage = iota
	RGBWUsagePrimary             // CDP_PRIMARY — expose as entity
	RGBWUsageSecondary           // NO_CREATE  — hide from discovery
)

// RGBWLight is a HmIP-RGBW / HmIP-LSC / HmIP-DRDI3 multi-mode light.
// It composes [ColorLight] (HUE + SATURATION) and [ColorTempLight]
// (KELVIN) under a Mode-aware façade: TurnOn / SetColor / SetKelvin
// / SetEffect dispatch on the current Mode.
type RGBWLight struct {
	*ColorLight

	kelvin    *generic.Integer
	mode      *generic.Sensor[string]
	effect    *generic.ActionSelect
	MinKelvin int32
	MaxKelvin int32

	// channelNo is the integer channel number from device.Channel.Number,
	// stored at construction time so Usage() can apply the Python
	// suppression logic without holding a live channel reference.
	channelNo int

	muMode  sync.RWMutex
	hasMode bool
	current RGBWMode

	effects []string
}

// NewRGBWLight constructs an RGBW light. Mode is "unknown" until the
// CCU emits a DEVICE_OPERATION_MODE update — TurnOn before that point
// behaves as if PWM mode were active (LEVEL only).
func NewRGBWLight(cfg Config) *RGBWLight {
	cl := NewColorLight(cfg)
	chNo := 0
	if cfg.Channel != nil {
		chNo = cfg.Channel.Number
	}
	r := &RGBWLight{
		ColorLight: cl,
		kelvin:     custom.IntegerField(cfg.Channel, hmenum.ParameterColorTemperature),
		mode:       custom.StringSensorField(cfg.Channel, hmenum.ParameterDeviceOperationMode),
		effect:     custom.ActionSelectField(cfg.Channel, hmenum.ParameterEffect),
		MinKelvin:  2000,
		MaxKelvin:  6500,
		channelNo:  chNo,
	}
	if cfg.Channel != nil {
		if dp := cfg.Channel.Parameter(hmenum.ParameterEffect); dp != nil {
			r.effects = append([]string(nil), dp.ParameterData().ValueList...)
		}
	}
	if r.Float != nil {
		r.registerRGBWLightServices()
	}
	if r.mode != nil {
		_ = r.mode.OnConfirmedUpdate(func(_, _ string) { r.dataVersion.Bump() })
	}
	if r.kelvin != nil {
		_ = r.kelvin.OnConfirmedUpdate(func(_, _ int32) { r.dataVersion.Bump() })
	}
	return r
}

// NamePostfix returns the suffix appended in
// pipeline. RGBW lights surface as "hs" / "color_temp" / "effect"
// depending on the current operating mode (light.py:531-542). When
// the mode is not yet known, returns an empty postfix so the base
// device name passes through.
func (r *RGBWLight) NamePostfix() string {
	switch r.Mode() {
	case RGBWModeRGB, RGBWModeRGBW:
		return "hs"
	case RGBWModeTunableWhite:
		return "color_temp"
	default:
		return ""
	}
}

// Mode returns the last observed operating mode, or RGBWModeUnknown
// when no DEVICE_OPERATION_MODE update has been seen.
func (r *RGBWLight) Mode() RGBWMode {
	r.muMode.RLock()
	defer r.muMode.RUnlock()
	if !r.hasMode {
		return RGBWModeUnknown
	}
	return r.current
}

// HasMode reports whether the operating mode has been observed.
func (r *RGBWLight) HasMode() bool {
	r.muMode.RLock()
	defer r.muMode.RUnlock()
	return r.hasMode
}

// effectiveMode returns the operating mode used for capability and usage
// decisions, defaulting to RGBW when DEVICE_OPERATION_MODE has not been
// observed yet. Mirrors the reference CustomDpIpRGBWLight._device_operation_mode,
// which falls back to RGBW on an unset/unexpected value so the light advertises
// its richest surface from boot rather than collapsing to brightness-only.
func (r *RGBWLight) effectiveMode() RGBWMode {
	if m := r.Mode(); m != RGBWModeUnknown {
		return m
	}
	return RGBWModeRGBW
}

// Usage returns the [RGBWUsage] discriminator that north-bound adapters
// (MQTT discovery, REST) should use to decide whether to expose this
// Channel as an entity. It mirrors
// `CustomDpIpRGBWLight.usage` property (light.py:531-542):
//
// - RGB / RGBW mode: channels 2, 3, 4 return [RGBWUsageSecondary].
// - TunableWhite mode: channels 3, 4 return [RGBWUsageSecondary].
// - All other channels / modes return [RGBWUsagePrimary].
//
// Uses [effectiveMode] (RGBW fallback when unobserved), mirroring the
// reference usage property, which reads _device_operation_mode (defaults
// RGBW) — so secondary channels are folded away from boot rather than
// surfacing until the mode is first observed.
func (r *RGBWLight) Usage() RGBWUsage {
	m := r.effectiveMode()
	no := r.channelNo
	if (m == RGBWModeRGB || m == RGBWModeRGBW) && (no == 2 || no == 3 || no == 4) {
		return RGBWUsageSecondary
	}
	if m == RGBWModeTunableWhite && (no == 3 || no == 4) {
		return RGBWUsageSecondary
	}
	return RGBWUsagePrimary
}

// HiddenByOperationMode reports whether this channel is a secondary channel in
// the current operating mode and must not surface as a standalone light entity.
// Mirrors the NO_CREATE branch of the reference CustomDpIpRGBWLight.usage:
// channels 2-4 (RGB/RGBW) and 3-4 (TUNABLE_WHITE) are folded into the primary
// channel's aggregate, so north-bound adapters skip them.
func (r *RGBWLight) HiddenByOperationMode() bool {
	return r.Usage() == RGBWUsageSecondary
}

// Subscribe wires the channel's DEVICE_OPERATION_MODE parameter so
// Mode reflects the CCU's reported state. Replays the wire DP's
// currently observed value through the same handler so the
// hot-path-cached `r.current` mode lands in sync with the CCU at
// boot, not only on the next push. Implements
// [device.SubscribingDataPoint].
func (r *RGBWLight) Subscribe(ch *device.Channel) func() {
	subs := []func(){r.Light.Subscribe(ch)}
	applyMode := func(next any) {
		if s, ok := next.(string); ok {
			r.recordMode(s)
		}
	}
	if ch != nil {
		// DEVICE_OPERATION_MODE lives on MASTER for the HmIP-RGBW
		// family — fall back to MASTER if VALUES does not carry it.
		if dp := custom.ParamFromAnyParamset(ch, hmenum.ParameterDeviceOperationMode); dp != nil {
			subs = append(subs, dp.OnAnyUpdate(func(_, next any) {
				applyMode(next)
			}))
			custom.ReplayCurrentValue(dp, applyMode)
		}
	}
	return func() {
		for _, u := range subs {
			if u != nil {
				u()
			}
		}
	}
}

func (r *RGBWLight) recordMode(s string) {
	r.muMode.Lock()
	defer r.muMode.Unlock()
	r.hasMode = true
	switch s {
	case "PWM":
		r.current = RGBWModePWM
	case "RGB":
		r.current = RGBWModeRGB
	case "RGBW":
		r.current = RGBWModeRGBW
	case "TUNABLE_WHITE":
		r.current = RGBWModeTunableWhite
	default:
		r.current = RGBWModeUnknown
	}
}

// HasColor reports whether the current mode honours HUE / SATURATION.
func (r *RGBWLight) HasColor() bool {
	m := r.effectiveMode()
	return m == RGBWModeRGB || m == RGBWModeRGBW
}

// HasHsColor reports whether the current mode provides HS (hue-saturation)
// colour — identical to [HasColor].
func (r *RGBWLight) HasHsColor() bool { return r.HasColor() }

// CurrentHsColor returns the last observed (hue, saturation) pair from the
// channel's HUE and SATURATION wire data points, and whether both have been
// observed. Returns (0, 0, false) when the current mode does not support HS
// colour ([HasHsColor] == false) or when either DP has not been observed yet.
//
// @state_property def hs_color(self) -> tuple[float, float] | None: return
// self._dp_hs_color.value
//
// The underlying wire fields are the HUE and SATURATION DPs held by the
// embedded [ColorLight]. Audit
func (r *RGBWLight) CurrentHsColor() (hue int32, sat float64, ok bool) {
	if !r.HasHsColor() {
		return 0, 0, false
	}
	return r.Color()
}

// HasColorTemperature reports whether the current mode honours the KELVIN
// wire field — TUNABLE_WHITE or RGBW. This is the wire/Matter capability:
// the RGBW mode's relevant data points include COLOR_TEMPERATURE, so a
// Matter MoveToColorTemperature and the colour-temp state both apply there.
// For the mutually-exclusive HA colour-mode capability use
// [HasColorTempColorMode] instead.
func (r *RGBWLight) HasColorTemperature() bool {
	m := r.effectiveMode()
	return m == RGBWModeTunableWhite || m == RGBWModeRGBW
}

// HasColorTempColorMode reports whether the light advertises colour
// temperature as its HA colour mode. Mirrors the reference
// CustomDpIpRGBWLight.has_color_temperature: only TUNABLE_WHITE — HA colour
// modes are mutually exclusive, so RGBW mode advertises hs colour even though
// the wire profile also carries a KELVIN field.
func (r *RGBWLight) HasColorTempColorMode() bool {
	return r.effectiveMode() == RGBWModeTunableWhite
}

// HasEffects reports whether the current mode honours EFFECT.
func (r *RGBWLight) HasEffects() bool {
	return r.effectiveMode() != RGBWModePWM && len(r.effects) > 0
}

// Kelvin returns the last observed colour temperature.
func (r *RGBWLight) Kelvin() (int32, bool) {
	if r.kelvin == nil {
		return 0, false
	}
	return r.kelvin.Value()
}

// SetKelvin commands a new colour temperature. Returns an error when
// the current mode does not support colour-temperature input.
func (r *RGBWLight) SetKelvin(ctx context.Context, v int32, priority hmenum.CommandPriority) error {
	if !r.HasColorTemperature() {
		return errors.New("rgbw: current mode does not support colour temperature")
	}
	if r.kelvin == nil {
		return errors.New("rgbw: channel missing COLOR_TEMPERATURE")
	}
	if v < r.MinKelvin {
		v = r.MinKelvin
	}
	if v > r.MaxKelvin {
		v = r.MaxKelvin
	}
	if err := r.kelvin.Set(custom.EnsureContext(ctx), v, priority); err != nil {
		return fmt.Errorf("rgbw: SET KELVIN: %w", err)
	}
	return nil
}

// Effects returns the labels of all effects this light supports.
func (r *RGBWLight) Effects() []string {
	if !r.HasEffects() {
		return nil
	}
	return append([]string(nil), r.effects...)
}

// SetEffect selects an effect by its string label (e.g. "BLINKING_SLOW").
// The label is sent directly to the CCU — the wire type is DpActionSelect
// which expects the string value for HmIP devices.
// Returns an error when the current mode does not support effects or the
// label is not in the available effects list.
func (r *RGBWLight) SetEffect(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	if !r.HasEffects() {
		return errors.New("rgbw: current mode does not support effects")
	}
	if r.effect == nil {
		return errors.New("rgbw: channel missing EFFECT")
	}
	if err := r.effect.TriggerLabel(custom.EnsureContext(ctx), label, priority); err != nil {
		return fmt.Errorf("rgbw: SET EFFECT: %w", err)
	}
	return nil
}

// SetColor commands a new HSV colour, dispatching to the embedded
// [ColorLight] only when the current mode honours colour input.
func (r *RGBWLight) SetColor(ctx context.Context, hue int32, saturation float64, priority hmenum.CommandPriority) error {
	if !r.HasColor() {
		return errors.New("rgbw: current mode does not support HSV colour")
	}
	return r.ColorLight.SetColor(ctx, hue, saturation, priority)
}
