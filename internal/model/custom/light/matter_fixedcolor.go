// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"

	ccdef "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// The fixed-colour lights (HmIP-BSL, the HmIP-MP3P signal channels, the
// wired HmIPW-WGC) carry a COLOR enum of eight slots instead of a
// hue/saturation pair. ADR 0012's mapping table bridges them as an
// ExtendedColorLight whose ColorControl is palette-quantised: a
// controller sees a hue/saturation light, every colour it writes snaps to
// the nearest slot, and the slot the CCU confirms is reported back as
// that slot's reference colour. The snapping ([HSToFixedColor]) and the
// reference colours ([FixedColorToHS]) are the ones the MQTT plane's `hs`
// colour mode already uses, which are the Home Assistant integration's
// for the same devices (the reference `light.py`:
// `hs_color_to_fixed_converter`, `FIXED_COLOR_TO_HS_CONVERTER`), so a
// colour picked in Apple Home and one picked in Home Assistant land on
// the same slot.
var (
	_ interfaces.MatterDeviceSource   = (*FixedColorLight)(nil)
	_ interfaces.MatterChangeNotifier = (*FixedColorLight)(nil)
	_ interfaces.MatterDeviceSource   = (*SoundPlayerLED)(nil)
	_ interfaces.MatterChangeNotifier = (*SoundPlayerLED)(nil)
)

// hsColor is the server-side colour of the slot the light reports, in
// the HS mode; unobserved until the CCU reported a COLOR.
func (l *FixedColorLight) hsColor() (ccdef.Color, bool) {
	c, observed := l.Color()
	if !observed {
		return ccdef.Color{}, false
	}
	hue, sat := FixedColorToHS(c)
	return ccdef.Color{
		Mode:       colorModeHS,
		Hue:        hueToMatter(hue),
		Saturation: saturationToMatter(sat),
	}, true
}

// fixedColorWriter is the [ccdef.ColorWriter] of a fixed-colour
// light: every colour the server applies — hue/saturation, or x/y
// converted through go-fabric's matter.js conversion — snaps to a slot
// and becomes one COLOR write at [matterDispatchPriority]. The CCU's
// COLOR echo then reaches the server through Set as the slot's reference
// colour, which is how a controller learns where its colour landed: a
// MoveToHue 100° answers SUCCESS and the attributes settle on 120°.
type fixedColorWriter struct{ l *FixedColorLight }

// SetColor implements [ccdef.ColorWriter]. A colour in the CT mode
// cannot occur on a server without the CT feature; it is ignored rather
// than written as a slot.
func (w fixedColorWriter) SetColor(ctx context.Context, c ccdef.Color) error {
	var hue int32
	var sat float64
	switch c.Mode {
	case colorModeCT:
		return nil
	case colorModeXY:
		hue, sat = xyToHM(c.X, c.Y)
	default:
		hue, sat = matterHueToHM(c.Hue), matterSaturationToHM(c.Saturation)
	}
	return w.l.SetColor(ctx, HSToFixedColor(hue, sat), matterDispatchPriority)
}

// matterColorServer builds the fixed-colour light's ColorControl server
// once; see [ColorLight.matterColorServer] for the shape. The COLOR enum
// (and the CHANNEL_COLOR the device reports while a transition runs)
// feed the confirmed slot back as its reference colour.
func (l *FixedColorLight) colorPlan() *colorPlan {
	mode := colorModeHS
	initial := ccdef.Patch{EnhancedColorMode: &mode}
	if c, observed := l.hsColor(); observed {
		hue, sat := c.Hue, c.Saturation
		x, y := hmToXY(matterHueToHM(c.Hue), matterSaturationToHM(c.Saturation))
		initial.CurrentHue, initial.CurrentSaturation = &hue, &sat
		initial.CurrentX, initial.CurrentY = &x, &y
	}
	minMireds, maxMireds, mireds := matterMinMireds, matterMaxMireds, matterInitialMireds
	initial.ColorTempPhysicalMinMireds, initial.ColorTempPhysicalMaxMireds = &minMireds, &maxMireds
	initial.ColorTemperatureMireds = &mireds
	colour := fixedColorWriter{l: l}
	return &colorPlan{
		declare: ccdef.Declare{
			Features:               ccdef.FeatureHueSaturation | ccdef.FeatureXy | ccdef.FeatureColorTemperature,
			Initial:                initial,
			ColorWriter:            colour,
			ColorTemperatureWriter: ctViaHS{colour: colour},
		},
		bind: func(srv colorInbound) {
			// The slot the device snaps a white to is rarely the exact
			// conversion, so the confirmed slot moves the server to HS;
			// [confirmsCT] keeps the CT mode only for an exact echo.
			resync := func() {
				if c, observed := l.hsColor(); observed && !confirmsXY(srv.Color(), c) && !confirmsCT(srv.Color(), c) {
					srv.Set(c)
				}
			}
			if l.color != nil {
				_ = l.color.OnConfirmedUpdate(func(_, _ int32) { resync() })
			}
			if l.channelColor != nil {
				_ = l.channelColor.OnConfirmedUpdate(func(_, _ int32) { resync() })
			}
			resync()
		},
	}
}

// MatterDeviceType for FixedColorLight overrides Light's projection with
// ExtendedColorLight (0x010D), as ADR 0012's mapping table states.
func (l *FixedColorLight) MatterDeviceType() uint16 {
	return matterDeviceTypeExtendedColorLight
}

// MatterDevice for FixedColorLight is an ExtendedColorLight: OnOff and
// LevelControl from the embedded Light's declarations plus the
// palette-quantised ColorControl with the HS and XY features, and the CT
// feature the device type mandates mapped onto the palette ([ctViaHS]).
func (l *FixedColorLight) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return l.matterLightDevice(l, matterDeviceTypeExtendedColorLight, opts, l.colorPlan())
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *Light's so a confirmed COLOR or CHANNEL_COLOR
// dirty-marks the endpoint like the on/off and brightness changes do.
func (l *FixedColorLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.Light.OnMatterValueChanged(cb),
		l.color.OnMatterValueChanged(cb),
		l.channelColor.OnMatterValueChanged(cb),
	)
}

// MatterDeviceType for SoundPlayerLED is DimmableLight (0x0101): ADR 0012
// bridges the HmIP-MP3P's LED as OnOff + LevelControl and leaves its
// colour to MQTT and REST, so the FixedColorLight it embeds does not pull
// the ColorControl in. The LED channel always carries LEVEL, so the
// constant is what the embedded Light would advertise.
func (l *SoundPlayerLED) MatterDeviceType() uint16 {
	return matterDeviceTypeDimmableLight
}

// MatterDevice for SoundPlayerLED is a DimmableLight: OnOff and
// LevelControl, no colour (see [SoundPlayerLED.MatterDeviceType]).
func (l *SoundPlayerLED) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return l.matterLightDevice(l, matterDeviceTypeDimmableLight, opts, nil)
}

// OnMatterValueChanged for SoundPlayerLED notifies on the on/off and
// brightness changes the endpoint serves, not on a colour it does not.
func (l *SoundPlayerLED) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return l.Light.OnMatterValueChanged(cb)
}
