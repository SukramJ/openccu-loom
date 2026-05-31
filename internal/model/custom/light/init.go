// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

// init registers the Constructor functions for every light DeviceProfile
// onto the global custom.DefaultRegistry. This file is the D.12 delivery
// for the light sub-package.
//
// Profile → Constructor mapping:
//
// - "IPDimmer" → dimmable Light (IP)
// - "RfDimmer" → dimmable Light (RF)
// - "RfDimmerWithVirtChannel" → dimmable Light (RF, virtual channel)
// - "IPDRGDALI" → DRGDaliLight (HmIP DALI bus driver)
// - "IPFixedColorLight" → FixedColorLight (HmIP colour select)
// - "IPSimpleFixedColorLightWired" → FixedColorLight (wired variant)
// - "IPSimpleFixedColorLight" → FixedColorLight (wireless variant)
// - "IPRGBW" → RGBWLight (HmIP-RGBW / LSC / DRDI3)
// - "RfDimmer_Color" → ColorLight (HSV RF dimmer)
// - "RfDimmer_Color_Fixed" → FixedColorLight (fixed-colour RF)
// - "RfDimmer_Color_Temp" → ColorTempLight (colour-temp RF)
// - "IPSoundPlayerLed" → SoundPlayerLED (HmIP-MP3P channel 6 LED strip)
//
// The registry is the process-wide DefaultRegistry; sub-packages call
// MustRegisterConstructor in init() — a panic here means a compile-time
// invariant was violated (two constructors for the same profile).

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	r := custom.DefaultRegistry()

	// IP Dimmer (HmIP-BDT / HmIP-DRDI3 / …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPDimmer"), newDimmerConstructor)

	// RF Dimmer (HM-LC-Dim1L-FM / …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfDimmer"), newDimmerConstructor)

	// RF Dimmer with virtual channel (HM-LC-Dim1T-FM / …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfDimmerWithVirtChannel"), newDimmerConstructor)

	// IP DRG-DALI 48-channel DALI bus driver.
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPDRGDALI"), newDaliConstructor)

	// IP Fixed-colour light (HmIP-BSL / HmIP-MP3P channel 2–5).
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPFixedColorLight"), newFixedColorConstructor)

	// IP Simple fixed-colour light wired (HmIPW-WGC, …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPSimpleFixedColorLightWired"), newFixedColorConstructor)

	// IP Simple fixed-colour light wireless (HmIP-LED-WW / HmIP-MP3P chan 2-5,
	// …). Same field set as the wired variant — COLOR + LEVEL + timer/ramp.
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPSimpleFixedColorLight"), newFixedColorConstructor)

	// IP RGBW multi-mode light (HmIP-RGBW / LSC / DRDI3 colour mode).
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPRGBW"), newRGBWConstructor)

	// RF dimmer with HSV colour and programmable effects (HM-LC-RGBW-WM / …).
	// The PROGRAM parameter carries 7 effect presets; EffectLight surfaces them.
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfDimmer_Color"), newEffectLightConstructor)

	// RF dimmer with fixed colour (HM-LC-RGBW-WM / …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfDimmer_Color_Fixed"), newFixedColorConstructor)

	// RF dimmer with colour temperature (HM-LC-DW-WM / …).
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfDimmer_Color_Temp"), newColorTempConstructor)

	// HmIP-MP3P channel 6 LED strip. Categorised as light
	r.MustRegisterConstructor(hmenum.DeviceProfileIPSoundPlayerLed, newSoundPlayerLEDConstructor)

	payload.RegisterGlobalScalarArgKey("set_level", "level")
	payload.RegisterGlobalScalarArgKey("set_color", "color")
	payload.RegisterGlobalScalarArgKey("set_kelvin", "kelvin")
	payload.RegisterGlobalScalarArgKey("set_effect", "effect")
}

// Predefined capability presets mirror
// capabilities/light.py — exported so north-bound adapters and tests can
// reference them by name rather than reconstructing the struct literal.

// SimpleLightCapabilities is the simplest light capability set: brightness
// only (no transitions, no colour, no colour temp, no effects). Used for
// on/off-only channel types.
var SimpleLightCapabilities = custom.LightCapabilities{
	Dimmable: true,
}

// DimmerCapabilities mirrors
// brightness + ramp-time transition.
// Mirrors capabilities/light.py:55.
var DimmerCapabilities = custom.LightCapabilities{
	Dimmable:   true,
	Transition: true,
}

// ColorLightCapabilities mirrors
// brightness + transition + HSV colour.
// Mirrors capabilities/light.py:60.
var ColorLightCapabilities = custom.LightCapabilities{
	Dimmable:      true,
	Transition:    true,
	SupportsColor: true,
}

// ColorTempLightCapabilities mirrors
// brightness + transition + colour temperature.
// Mirrors capabilities/light.py:66.
var ColorTempLightCapabilities = custom.LightCapabilities{
	Dimmable:          true,
	Transition:        true,
	SupportsColorTemp: true,
}

// FixedColorLightCapabilities mirrors
// brightness + transition (fixed colour — no hs_color/color_temp).
// Mirrors capabilities/light.py:72.
var FixedColorLightCapabilities = custom.LightCapabilities{
	Dimmable:   true,
	Transition: true,
}

// RGBWLightCapabilities mirrors
// brightness + transition (colour/colour-temp/effects are dynamic via has_*).
// Mirrors capabilities/light.py:81.
var RGBWLightCapabilities = custom.LightCapabilities{
	Dimmable:   true,
	Transition: true,
}

// Python-exact sentinel names — exported aliases matching
// module-level constant names for parity and north-bound adapter use.

// DIMMER_CAPABILITIES is the Python-parity alias for [DimmerCapabilities].
var DIMMER_CAPABILITIES = DimmerCapabilities //nolint:revive // Python-exact name required for parity

// COLOR_DIMMER_CAPABILITIES is the Python-parity alias for [ColorLightCapabilities].
var COLOR_DIMMER_CAPABILITIES = ColorLightCapabilities //nolint:revive // Python-exact name required for parity

// COLOR_TEMP_DIMMER_CAPABILITIES is the Python-parity alias for [ColorTempLightCapabilities].
var COLOR_TEMP_DIMMER_CAPABILITIES = ColorTempLightCapabilities //nolint:revive // Python-exact name required for parity

// FIXED_COLOR_LIGHT_CAPABILITIES is the Python-parity alias for [FixedColorLightCapabilities].
var FIXED_COLOR_LIGHT_CAPABILITIES = FixedColorLightCapabilities //nolint:revive // Python-exact name required for parity

// RGBW_LIGHT_CAPABILITIES is the Python-parity alias for [RGBWLightCapabilities].
var RGBW_LIGHT_CAPABILITIES = RGBWLightCapabilities //nolint:revive // Python-exact name required for parity

// writerFromChannel extracts the generic.Writer that the channel's LEVEL
// data point was built with. When no LEVEL DP exists the writer is nil;
// callers fall back to a nil Writer, which causes commands to fail cleanly
// (mirrors the behaviour of unhydrated channels in tests).
func writerFromChannel(ch *device.Channel) Writer {
	if ch == nil {
		return nil
	}
	if dp := custom.FloatField(ch, hmenum.ParameterLevel); dp != nil {
		return dp.Writer
	}
	return nil
}

// configFromChannel builds the shared Config that every light constructor
// needs. Capabilities are set at the profile level by the caller.
func configFromChannel(ch *device.Channel, caps custom.LightCapabilities) Config {
	return Config{
		Channel:      ch,
		Writer:       writerFromChannel(ch),
		Capabilities: caps,
	}
}

// applyGroupLevel resolves the profile's `FieldGroupLevel` mapping (when
// present) on the primary channel and binds the corresponding LEVEL DP onto
// the Light via [Light.SetGroupLevel].
func applyGroupLevel(l *Light, ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if l == nil || ch == nil {
		return
	}
	fv, ok := rebased.Fields[hmenum.FieldGroupLevel]
	if !ok {
		return
	}
	param, _ := custom.ResolveFieldValue(fv)
	if param == "" {
		return
	}
	if dp := custom.FloatField(ch, param); dp != nil {
		l.SetGroupLevel(dp)
	}
}

// newDimmerConstructor builds a plain dimmable Light.
func newDimmerConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	l := New(configFromChannel(ch, custom.LightCapabilities{Dimmable: true}))
	applyGroupLevel(l, ch, rebased)
	return l, nil
}

// newDaliConstructor builds a DRGDaliLight for the HmIP-DRG-DALI.
// Kelvin bounds match
func newDaliConstructor(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewDRGDaliLight(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}),
		2000, 6500,
	), nil
}

// newFixedColorConstructor builds a FixedColorLight (enum-valued COLOR
// parameter). Used by IPFixedColorLight, IPSimpleFixedColorLightWired,
// and RfDimmer_Color_Fixed.
func newFixedColorConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	fcl := NewFixedColorLight(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColor: true}),
	)
	applyGroupLevel(fcl.Light, ch, rebased)
	return fcl, nil
}

// newRGBWConstructor builds an RGBWLight for multi-mode HmIP-RGBW / LSC
// DRDI3 devices. The operating mode is read from DEVICE_OPERATION_MODE at
// runtime, so the capability flags cover all possible modes.
func newRGBWConstructor(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewRGBWLight(
		configFromChannel(ch, custom.LightCapabilities{
			Dimmable:          true,
			SupportsColor:     true,
			SupportsColorTemp: true,
			SupportsEffects:   true,
		}),
	), nil
}

// newEffectLightConstructor builds an EffectLight for RF colour dimmers that
// additionally carry a PROGRAM parameter exposing a set of named effects
// (e.g. "Slow color change", "Campfire"). The effect list is sourced from
// the PROGRAM VALUE_LIST at construction time.
func newEffectLightConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	el := NewEffectLight(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColor: true}),
	)
	applyGroupLevel(el.Light, ch, rebased)
	return el, nil
}

// newColorTempConstructor builds a ColorTempLight for RF dimmers with
// tunable white (COLOR_TEMPERATURE + LEVEL). Kelvin bounds are read from
// the COLOR_TEMPERATURE parameter descriptor when available; the fallback
// is 2000–6500 K.
func newColorTempConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	minK, maxK := kelvinBoundsFromChannel(ch)
	ctl := NewColorTempLight(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}),
		minK, maxK,
	)
	applyGroupLevel(ctl.Light, ch, rebased)
	return ctl, nil
}

// kelvinBoundsFromChannel reads the COLOR_TEMPERATURE parameter descriptor
// MIN / MAX bounds as Kelvin integers. Falls back to (0, 0) when absent so
// [NewColorTempLight] applies its own defaults (2000 / 6500).
func kelvinBoundsFromChannel(ch *device.Channel) (minK, maxK int32) {
	if ch == nil {
		return 0, 0
	}
	dp := ch.Parameter(hmenum.ParameterColorTemperature)
	if dp == nil {
		return 0, 0
	}
	desc := dp.ParameterData()
	var lo, hi float64
	if len(desc.Min) > 0 {
		_ = json.Unmarshal(desc.Min, &lo) //nolint:errcheck // fallback to 0 on parse failure
	}
	if len(desc.Max) > 0 {
		_ = json.Unmarshal(desc.Max, &hi) //nolint:errcheck // fallback to 0 on parse failure
	}
	return int32(lo), int32(hi) //nolint:gosec // CCU colour-temp values fit int32
}

// newSoundPlayerLEDConstructor builds a [SoundPlayerLED] for the
// HmIP-MP3P channel-6 RGB LED strip. Categorised as light
func newSoundPlayerLEDConstructor(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewSoundPlayerLED(configFromChannel(ch, custom.LightCapabilities{
		Dimmable:      true,
		Transition:    true,
		SupportsColor: true,
	})), nil
}
