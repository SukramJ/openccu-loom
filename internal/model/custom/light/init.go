// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// DimmerCapabilities describes brightness + ramp-time transition.
// Mirrors capabilities/light.py:55.
var DimmerCapabilities = custom.LightCapabilities{
	Dimmable:   true,
	Transition: true,
}

// ColorLightCapabilities describes brightness + transition + HSV colour.
// Mirrors capabilities/light.py:60.
var ColorLightCapabilities = custom.LightCapabilities{
	Dimmable:      true,
	Transition:    true,
	SupportsColor: true,
}

// ColorTempLightCapabilities describes brightness + transition +
// colour temperature. Mirrors capabilities/light.py:66.
var ColorTempLightCapabilities = custom.LightCapabilities{
	Dimmable:          true,
	Transition:        true,
	SupportsColorTemp: true,
}

// FixedColorLightCapabilities describes brightness + transition (fixed
// colour — no hs_color/color_temp). Mirrors capabilities/light.py:72.
var FixedColorLightCapabilities = custom.LightCapabilities{
	Dimmable:   true,
	Transition: true,
}

// RGBWLightCapabilities describes brightness + transition (colour /
// colour-temp / effects are dynamic via has_*). Mirrors capabilities/light.py:81.
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
// needs. Capabilities are set at the profile level by the caller; the schema
// travels with the config so each composed field can resolve through it.
func configFromChannel(ch *device.Channel, caps custom.LightCapabilities, rebased custom.RebasedChannelGroupConfig) Config {
	return Config{
		Channel:      ch,
		Writer:       writerFromChannel(ch),
		Capabilities: caps,
		Group:        rebased,
	}
}

// applyGroupLevel resolves the profile's `FieldGroupLevel` mapping (when
// present) on the primary channel and binds the corresponding LEVEL DP onto
// the Light via [Light.SetGroupLevel].
func applyGroupLevel(l *Light, ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if l == nil || ch == nil {
		return
	}
	// The RF families declare GROUP_LEVEL group-wide, mapped to
	// LEVEL_REAL on this very channel; the HmIP families declare it per
	// channel, mapped to LEVEL on the group's state channel. Reading only
	// the group-wide block missed every HmIP device, and both wire
	// parameters are read-only where they are declared, so asking for a
	// writable float missed the rest.
	if fv, ok := rebased.Fields[hmenum.FieldGroupLevel]; ok {
		if param, _ := custom.ResolveFieldValue(fv); param != "" {
			if dp := custom.GroupLevelField(ch, param); dp != nil {
				l.SetGroupLevel(dp)
				return
			}
		}
	}
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldGroupLevel]
		if !ok {
			continue
		}
		param, _ := custom.ResolveFieldValue(fv)
		if param == "" {
			continue
		}
		groupCh := siblingChannel(ch, chNo)
		if groupCh == nil {
			continue
		}
		if dp := custom.GroupLevelField(groupCh, param); dp != nil {
			l.SetGroupLevel(dp)
			return
		}
	}
}

// siblingChannel returns the channel of ch's device carrying number no.
func siblingChannel(ch *device.Channel, no int) *device.Channel {
	dev := ch.Device()
	if dev == nil {
		return nil
	}
	for _, sibling := range dev.Channels() {
		if sibling.Number == no {
			return sibling
		}
	}
	return nil
}

// newDimmerConstructor builds a plain dimmable Light.
func newDimmerConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	l := New(configFromChannel(ch, custom.LightCapabilities{Dimmable: true}, rebased))
	applyGroupLevel(l, ch, rebased)
	return l, nil
}

// newDaliConstructor builds a DRGDaliLight for the HmIP-DRG-DALI.
// Kelvin bounds are the device's own, read from the COLOR_TEMPERATURE
// descriptor (HmIP-DRG-DALI declares 1000-10200 K); the
// [defaultMinKelvin] / [defaultMaxKelvin] pair applies only when the
// channel carries no bounds.
func newDaliConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	// The reference CustomDpIpDrgDaliLight declares HUE+SATURATION, COLOR_TEMPERATURE
	// and EFFECT fields, so it supports hs colour, colour temperature AND effects
	// (has_hs_color / has_color_temperature / has_effects all resolve true).
	minK, maxK := kelvinBoundsFromChannel(ch)
	return NewDRGDaliLight(
		configFromChannel(ch, custom.LightCapabilities{
			Dimmable:          true,
			SupportsColor:     true,
			SupportsColorTemp: true,
			SupportsEffects:   true,
		}, rebased),
		minK, maxK,
	), nil
}

// newFixedColorConstructor builds a FixedColorLight (enum-valued COLOR
// parameter). Used by IPFixedColorLight, IPSimpleFixedColorLightWired,
// and RfDimmer_Color_Fixed.
func newFixedColorConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	// The reference CustomDpIpFixedColorLight declares a COLOR_BEHAVIOUR effect
	// field, so its has_effects resolves true (the effect list is the
	// COLOR_BEHAVIOUR value list).
	fcl := NewFixedColorLight(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsEffects: true}, rebased),
	)
	applyGroupLevel(fcl.Light, ch, rebased)
	fcl.bindChannelColor(ch, rebased)
	return fcl, nil
}

// newRGBWConstructor builds an RGBWLight for multi-mode HmIP-RGBW / LSC
// DRDI3 devices. The operating mode is read from DEVICE_OPERATION_MODE at
// runtime, so the capability flags cover all possible modes.
func newRGBWConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewRGBWLight(
		configFromChannel(ch, custom.LightCapabilities{
			Dimmable:          true,
			SupportsColor:     true,
			SupportsColorTemp: true,
			SupportsEffects:   true,
		}, rebased),
	), nil
}

// newEffectLightConstructor builds an EffectLight for RF colour dimmers that
// additionally carry a PROGRAM parameter exposing a set of named effects
// (e.g. "Slow color change", "Campfire"). The effect list is sourced from
// the PROGRAM VALUE_LIST at construction time.
func newEffectLightConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	// The reference CustomDpColorDimmerEffect carries a PROGRAM effect field, so
	// its has_effects resolves true (the effect list is the PROGRAM value list).
	el := newEffectLightOn(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsEffects: true}, rebased),
		programChannel(ch, rebased),
		colorChannel(ch, rebased),
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
	ctl := newColorTempLightOn(
		configFromChannel(ch, custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}, rebased),
		minK, maxK,
		whitePointChannel(ch, rebased),
	)
	applyGroupLevel(ctl.Light, ch, rebased)
	return ctl, nil
}

// kelvinBoundsFromChannel reads the COLOR_TEMPERATURE parameter descriptor
// MIN / MAX bounds as Kelvin integers. Falls back to (0, 0) when absent so
// the caller applies [defaultMinKelvin] / [defaultMaxKelvin].
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
	return int32(lo), int32(hi) //nolint:gosec // CCU colour-temp values fit int32; see #20
}

// newSoundPlayerLEDConstructor builds a [SoundPlayerLED] for the
// HmIP-MP3P channel-6 RGB LED strip. Categorised as light
func newSoundPlayerLEDConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewSoundPlayerLED(configFromChannel(ch, custom.LightCapabilities{
		Dimmable:      true,
		Transition:    true,
		SupportsColor: true,
	}, rebased)), nil
}

// programChannel resolves the channel the profile maps PROGRAM onto. The
// RF colour dimmers declare it two channels above the light's own, so
// looking on the light's channel found nothing and the effect list came
// out empty on every device.
func programChannel(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) *device.Channel {
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldProgram]
		if !ok {
			continue
		}
		if param, _ := custom.ResolveFieldValue(fv); param != hmenum.ParameterProgram {
			continue
		}
		if sibling := siblingChannel(ch, chNo); sibling != nil {
			return sibling
		}
	}
	return ch
}

// colorChannel resolves the channel the profile maps COLOR onto. The RF
// colour dimmers carry a single COLOR integer one channel above the
// light's own and no HUE / SATURATION anywhere, so a light that only
// looks at its own channel finds no colour axis at all — while the
// discovery payload still advertises the hs colour mode and every colour
// command comes back as "channel missing HUE or SATURATION".
func colorChannel(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) *device.Channel {
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldColor]
		if !ok {
			continue
		}
		if param, _ := custom.ResolveFieldValue(fv); param != hmenum.ParameterColor {
			continue
		}
		if sibling := siblingChannel(ch, chNo); sibling != nil {
			return sibling
		}
	}
	return nil
}

// whitePointChannel resolves the channel the profile maps COLOR_LEVEL
// onto. The RF tunable-white dimmers express their colour temperature as
// the LEVEL of the channel above the light's own — they carry no
// COLOR_TEMPERATURE parameter at all — so a light that only looks at its
// own channel reports no colour temperature on any of them.
func whitePointChannel(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) *device.Channel {
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldColorLevel]
		if !ok {
			continue
		}
		if param, _ := custom.ResolveFieldValue(fv); param != hmenum.ParameterLevel {
			continue
		}
		if sibling := siblingChannel(ch, chNo); sibling != nil {
			return sibling
		}
	}
	return nil
}
