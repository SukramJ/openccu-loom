// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: the colour-capable light variants
// (ColorLight, ColorTempLight, RGBWLight) participate in the Matter
// source surface (ADR 0012). Each shadows the embedded Light's
// MatterDeviceType / MatterClusterServers methods to add a ColorControl
// (0x0300) projection on top of OnOff + LevelControl.
//
// FixedColorLight, EffectLight, DRGDaliLight and SoundPlayerLED stay
// with the inherited Light projection (OnOff + LevelControl). Effect
// dispatch and palette quantisation are MQTT-only per ADR 0012 §5.
var (
	_ interfaces.MatterEndpointSource = (*ColorLight)(nil)
	_ interfaces.MatterEndpointSource = (*ColorTempLight)(nil)
	_ interfaces.MatterEndpointSource = (*RGBWLight)(nil)
)

// Matter ColorControl constants follow the Matter 1.5.1 Application
// Cluster Specification §3.2. Cluster revision + 1.4/1.5 FeatureMap
// bits cross-referenced against the Matter cluster sweep
// (matter.js HEAD packages/model/src/standard/elements/).
const (
	matterDeviceTypeColorTemperatureLight uint16 = 0x010C
	matterDeviceTypeExtendedColorLight    uint16 = 0x010D

	matterClusterColorControl uint32 = 0x0300

	matterAttrColorCurrentHue             uint32 = 0x0000
	matterAttrColorCurrentSaturation      uint32 = 0x0001
	matterAttrColorColorTemperatureMireds uint32 = 0x0007
	matterAttrColorColorMode              uint32 = 0x0008
	// Options (0x000F) and NumberOfPrimaries (0x0010) are mandatory per
	// matter.js packages/model/src/standard/elements/color-control.element.ts.
	// Options bitmap8: 0 = execute command unconditionally.
	// NumberOfPrimaries uint8: 0 = no individually-addressable primaries.
	matterAttrColorOptions                 uint32 = 0x000F // mandatory — bitmap8
	matterAttrColorNumPrimaries            uint32 = 0x0010 // mandatory — uint8, 0 = none
	matterAttrColorEnhancedColorMode       uint32 = 0x4001
	matterAttrColorColorCapabilities       uint32 = 0x400A
	matterAttrColorColorTempPhysicalMinMir uint32 = 0x400B
	matterAttrColorColorTempPhysicalMaxMir uint32 = 0x400C

	matterCmdColorMoveToHue              uint32 = 0x00
	matterCmdColorMoveToSaturation       uint32 = 0x03
	matterCmdColorMoveToHueAndSaturation uint32 = 0x06
	matterCmdColorMoveToColorTemperature uint32 = 0x0A

	// matterColorControlClusterRevision pinned to matter.js HEAD
	// (@matter/model 0.16.11). Matter 1.5 bumped the revision from 7 to 9
	// with EnhancedHue / ColorLoop / XY FeatureMap-gating changes;
	// HM lights still only emit the HS + CT subset, so behaviour is
	// unchanged.
	matterColorControlClusterRevision uint16 = 9

	// ColorMode / EnhancedColorMode enum (spec 3.2.7.7 / 3.2.7.18).
	matterColorModeHueSaturation uint8 = 0
	matterColorModeXY            uint8 = 1
	matterColorModeColorTemp     uint8 = 2

	// ColorCapabilities feature bits (spec 3.2.7.19) — same encoding as
	// FeatureMap.
	matterColorCapHS    uint16 = 1 << 0
	matterColorCapXY    uint16 = 1 << 3
	matterColorCapCT    uint16 = 1 << 4
	matterColorCapEnhHS uint16 = 1 << 1
	matterColorCapLoop  uint16 = 1 << 2

	// ColorControl FeatureMap bits (spec 3.2.4).
	matterColorFeatureHS uint32 = 1 << 0
	matterColorFeatureCT uint32 = 1 << 4

	// matterHueScale matches Matter's CurrentHue range (0..254); the
	// HM hue is in degrees (0..359). Same reasoning as
	// [matterLevelMax] in matter.go: 0xFF is the null sentinel for
	// nullable uint8 attributes.
	matterHueScale = 254.0

	// Matter colour-temperature physical bounds. We use the typical
	// "warm-cool LED" range; per-device-profile narrowing is not yet
	// wired.
	matterMinMireds uint16 = 153 // ≈ 6535 K
	matterMaxMireds uint16 = 500 // ≈ 2000 K
)

// hueToMatter encodes an HM hue (0..359°) into Matter CurrentHue.
func hueToMatter(hue int32) uint8 {
	hue = ((hue % 360) + 360) % 360
	v := float64(hue) * matterHueScale / 360.0
	if v > matterHueScale {
		v = matterHueScale
	}
	return uint8(v + 0.5)
}

// matterHueToHM is the inverse of [hueToMatter].
func matterHueToHM(m uint8) int32 {
	v := float64(m) * 360.0 / matterHueScale
	return int32(v + 0.5)
}

// saturationToMatter encodes an HA-canonical saturation (0..100, as reported
// by [ColorLight.Color]) into Matter CurrentSaturation (0..254).
func saturationToMatter(s float64) uint8 {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return uint8(matterHueScale)
	}
	return uint8(s/100*matterHueScale + 0.5)
}

// matterSaturationToHM is the inverse of [saturationToMatter]: it decodes
// Matter CurrentSaturation (0..254) into the HA-canonical 0..100 value
// [ColorLight.SetColor] expects.
func matterSaturationToHM(m uint8) float64 {
	if float64(m) >= matterHueScale {
		return 100
	}
	return float64(m) / matterHueScale * 100
}

// kelvinToMireds converts Kelvin into Matter's reciprocal mireds.
// Matter spec 3.2.7.10: ColorTemperatureMireds = 1_000_000 / Kelvin.
func kelvinToMireds(k int32) uint16 {
	if k <= 0 {
		return matterMaxMireds
	}
	v := 1_000_000 / k
	if v < int32(matterMinMireds) {
		return matterMinMireds
	}
	if v > int32(matterMaxMireds) {
		return matterMaxMireds
	}
	return uint16(v)
}

// miredsToKelvin is the inverse of [kelvinToMireds].
func miredsToKelvin(m uint16) int32 {
	if m == 0 {
		return 0
	}
	return 1_000_000 / int32(m)
}

// MatterDeviceType for ColorTempLight overrides Light's projection
// (DimmableLight) with ColorTemperatureLight (0x010C).
func (l *ColorTempLight) MatterDeviceType() uint16 {
	return matterDeviceTypeColorTemperatureLight
}

// MatterClusterServers for ColorTempLight returns OnOff + LevelControl
// (inherited via the embedded Light's projection) plus ColorControl in
// CT-only mode.
func (l *ColorTempLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, ctColorServer{l: l})
}

// MatterDeviceType for ColorLight overrides Light's projection with
// ExtendedColorLight (0x010D).
func (l *ColorLight) MatterDeviceType() uint16 {
	return matterDeviceTypeExtendedColorLight
}

// MatterClusterServers for ColorLight returns OnOff + LevelControl +
// ColorControl in HS mode.
func (l *ColorLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, hsColorServer{l: l})
}

// MatterDeviceType for RGBWLight overrides Light's projection with
// ExtendedColorLight (0x010D) — RGBW devices switch between HS and CT
// modes via [RGBWLight.Mode].
func (l *RGBWLight) MatterDeviceType() uint16 {
	return matterDeviceTypeExtendedColorLight
}

// MatterClusterServers for RGBWLight returns OnOff + LevelControl +
// ColorControl in HS+CT mode.
func (l *RGBWLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, rgbwColorServer{l: l})
}

// MatterEligibility marks RGBWLight as partially mappable: OnOff +
// LevelControl + ColorControl (HS+CT) cover the standard light
// surface, but the device's effect playlist (`Effects()` /
// `Effect()`) has no Matter cluster — Matter §3 Color Control covers
// scenes but not arbitrary effect dispatch. Effects stay MQTT-only.
func (l *RGBWLight) MatterEligibility() interfaces.MatterEligibilityVerdict {
	servers := l.MatterClusterServers()
	clusters := make([]uint32, 0, len(servers))
	for _, s := range servers {
		clusters = append(clusters, s.MatterClusterID())
	}
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: matterDeviceTypeExtendedColorLight,
		Clusters:   clusters,
		Reason:     "Effect playlist dispatch is MQTT-only — Matter Color Control has scenes but no general effect surface.",
	}
}

// MatterEligibility marks EffectLight as partially mappable: OnOff +
// LevelControl + ColorControl (HS) cover the regular light surface,
// but the PROGRAM parameter (Slow / Medium / Fast color change,
// Campfire, Waterfall, …) has no Matter cluster equivalent. Effect
// dispatch stays MQTT-only.
func (l *EffectLight) MatterEligibility() interfaces.MatterEligibilityVerdict {
	servers := l.MatterClusterServers()
	clusters := make([]uint32, 0, len(servers))
	for _, s := range servers {
		clusters = append(clusters, s.MatterClusterID())
	}
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: l.MatterDeviceType(),
		Clusters:   clusters,
		Reason:     "PROGRAM-based effect dispatch is MQTT-only — Matter has no effect playlist cluster.",
	}
}

// ctColorServer projects a ColorTempLight onto ColorControl in
// CT-only mode.
type ctColorServer struct{ l *ColorTempLight }

func (s ctColorServer) MatterClusterID() uint32 { return matterClusterColorControl }

func (s ctColorServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrColorColorTemperatureMireds:
		// Value temporarily unavailable (e.g. CCU circuit-breaker open): return
		// (nil, true) so the dispatcher encodes TLV null + Success. See
		// climate/matter.go for the full rationale.
		k, ok := s.l.Kelvin()
		if !ok {
			return nil, true
		}
		return kelvinToMireds(k), true
	case matterAttrColorOptions:
		// Options bitmap8: 0 = execute command unconditionally.
		// matter.js color-control.element.ts Options attribute.
		return uint8(0), true
	case matterAttrColorNumPrimaries:
		// NumberOfPrimaries: 0 = no individually-addressable primaries.
		// matter.js color-control.element.ts NumberOfPrimaries.
		return uint8(0), true
	case matterAttrColorColorMode, matterAttrColorEnhancedColorMode:
		return matterColorModeColorTemp, true
	case matterAttrColorColorCapabilities:
		return matterColorCapCT, true
	case matterAttrColorColorTempPhysicalMinMir:
		return kelvinToMireds(s.l.MaxKelvin), true // higher Kelvin → lower mireds
	case matterAttrColorColorTempPhysicalMaxMir:
		return kelvinToMireds(s.l.MinKelvin), true
	case matterAttrFeatureMap:
		return matterColorFeatureCT, true
	case matterAttrClusterRevision:
		return matterColorControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s ctColorServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s ctColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	if cmdID != matterCmdColorMoveToColorTemperature {
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	mireds, err := extractColorTempMireds(fields)
	if err != nil {
		return nil, err
	}
	if err := s.l.SetKelvin(ctx, miredsToKelvin(mireds), priority); err != nil {
		return nil, err
	}
	s.l.dataVersion.Bump()
	return nil, nil
}

func (s ctColorServer) MatterReportable() []uint32 {
	return []uint32{matterAttrColorColorTemperatureMireds}
}

// MatterAttributes lists every ColorControl (0x0300) attribute the
// CT server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute.
func (s ctColorServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrColorColorTemperatureMireds,
		matterAttrColorOptions,
		matterAttrColorNumPrimaries,
		matterAttrColorColorMode,
		matterAttrColorEnhancedColorMode,
		matterAttrColorColorCapabilities,
		matterAttrColorColorTempPhysicalMinMir,
		matterAttrColorColorTempPhysicalMaxMir,
	}
}

// hsColorServer projects a ColorLight onto ColorControl in HS mode.
type hsColorServer struct{ l *ColorLight }

func (s hsColorServer) MatterClusterID() uint32 { return matterClusterColorControl }

func (s hsColorServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrColorCurrentHue:
		// Value temporarily unavailable — return (nil, true); see ctColorServer.MatterRead.
		hue, _, ok := s.l.Color()
		if !ok {
			return nil, true
		}
		return hueToMatter(hue), true
	case matterAttrColorCurrentSaturation:
		_, sat, ok := s.l.Color()
		if !ok {
			return nil, true
		}
		return saturationToMatter(sat), true
	case matterAttrColorOptions:
		// Options bitmap8: 0 = execute command unconditionally.
		// matter.js color-control.element.ts Options attribute.
		return uint8(0), true
	case matterAttrColorNumPrimaries:
		// NumberOfPrimaries: 0 = no individually-addressable primaries.
		// matter.js color-control.element.ts NumberOfPrimaries.
		return uint8(0), true
	case matterAttrColorColorMode, matterAttrColorEnhancedColorMode:
		return matterColorModeHueSaturation, true
	case matterAttrColorColorCapabilities:
		return matterColorCapHS, true
	case matterAttrFeatureMap:
		return matterColorFeatureHS, true
	case matterAttrClusterRevision:
		return matterColorControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s hsColorServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s hsColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdColorMoveToHue:
		hue, e := extractHueOnly(fields)
		if e != nil {
			return nil, e
		}
		_, sat, _ := s.l.Color()
		err = s.l.SetColor(ctx, matterHueToHM(hue), sat, priority)
	case matterCmdColorMoveToSaturation:
		sat, e := extractSaturationOnly(fields)
		if e != nil {
			return nil, e
		}
		hue, _, _ := s.l.Color()
		err = s.l.SetColor(ctx, hue, matterSaturationToHM(sat), priority)
	case matterCmdColorMoveToHueAndSaturation:
		hue, sat, e := extractHueAndSaturation(fields)
		if e != nil {
			return nil, e
		}
		err = s.l.SetColor(ctx, matterHueToHM(hue), matterSaturationToHM(sat), priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.l.dataVersion.Bump()
	return nil, nil
}

func (s hsColorServer) MatterReportable() []uint32 {
	return []uint32{matterAttrColorCurrentHue, matterAttrColorCurrentSaturation}
}

// MatterAttributes lists every ColorControl (0x0300) attribute the
// HS server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's two-attribute surface.
func (s hsColorServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrColorCurrentHue,
		matterAttrColorCurrentSaturation,
		matterAttrColorOptions,
		matterAttrColorNumPrimaries,
		matterAttrColorColorMode,
		matterAttrColorEnhancedColorMode,
		matterAttrColorColorCapabilities,
	}
}

// rgbwColorServer projects an RGBWLight onto ColorControl in HS+CT
// mode. The active mode is reported via [RGBWLight.Mode] which the
// projection inspects when answering the (Enhanced)ColorMode read.
type rgbwColorServer struct{ l *RGBWLight }

func (s rgbwColorServer) MatterClusterID() uint32 { return matterClusterColorControl }

func (s rgbwColorServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrColorCurrentHue:
		// Value temporarily unavailable — return (nil, true); see ctColorServer.MatterRead.
		hue, _, ok := s.l.CurrentHsColor()
		if !ok {
			return nil, true
		}
		return hueToMatter(hue), true
	case matterAttrColorCurrentSaturation:
		_, sat, ok := s.l.CurrentHsColor()
		if !ok {
			return nil, true
		}
		return saturationToMatter(sat), true
	case matterAttrColorColorTemperatureMireds:
		k, ok := s.l.Kelvin()
		if !ok {
			return nil, true
		}
		return kelvinToMireds(k), true
	case matterAttrColorOptions:
		// Options bitmap8: 0 = execute command unconditionally.
		// matter.js color-control.element.ts Options attribute.
		return uint8(0), true
	case matterAttrColorNumPrimaries:
		// NumberOfPrimaries: 0 = no individually-addressable primaries.
		// matter.js color-control.element.ts NumberOfPrimaries.
		return uint8(0), true
	case matterAttrColorColorMode, matterAttrColorEnhancedColorMode:
		// RGBW switches between HS and CT at runtime per the device-side
		// operating mode. Mapping table:
		//   RGBWModeTunableWhite → CT (CT-only channel sub-set active)
		//   RGBWModeRGB / RGBWModeRGBW → HS (hue/sat is the user-visible
		//     control even when an extra CT channel rides along)
		//   RGBWModePWM / RGBWModeUnknown → HS as a defensive fallback —
		//     the cluster is only materialised for ColorLight-capable
		//     devices, so HS is the safer guess if the mode signal has
		//     not been observed yet.
		// Matter Core Spec §3.2.7.20: ColorMode SHALL indicate which
		// attributes are currently determining the device output. Mirrors
		// the per-mode return from `ctColorServer` (always CT) and
		// `hsColorServer` (always HS).
		switch s.l.Mode() {
		case RGBWModeTunableWhite:
			return matterColorModeColorTemp, true
		default:
			return matterColorModeHueSaturation, true
		}
	case matterAttrColorColorCapabilities:
		return matterColorCapHS | matterColorCapCT, true
	case matterAttrColorColorTempPhysicalMinMir:
		return matterMinMireds, true
	case matterAttrColorColorTempPhysicalMaxMir:
		return matterMaxMireds, true
	case matterAttrFeatureMap:
		return matterColorFeatureHS | matterColorFeatureCT, true
	case matterAttrClusterRevision:
		return matterColorControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s rgbwColorServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s rgbwColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdColorMoveToHueAndSaturation:
		hue, sat, e := extractHueAndSaturation(fields)
		if e != nil {
			return nil, e
		}
		err = s.l.SetColor(ctx, matterHueToHM(hue), matterSaturationToHM(sat), priority)
	case matterCmdColorMoveToColorTemperature:
		mireds, e := extractColorTempMireds(fields)
		if e != nil {
			return nil, e
		}
		err = s.l.SetKelvin(ctx, miredsToKelvin(mireds), priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.l.dataVersion.Bump()
	return nil, nil
}

func (s rgbwColorServer) MatterReportable() []uint32 {
	return []uint32{
		matterAttrColorCurrentHue,
		matterAttrColorCurrentSaturation,
		matterAttrColorColorTemperatureMireds,
	}
}

// MatterAttributes lists every ColorControl (0x0300) attribute the
// RGBW server implements via MatterRead. Apple Home's HAP service
// rebuild reads the full attribute set; without this the dispatcher
// falls back to MatterReportable's three-attribute surface.
func (s rgbwColorServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrColorCurrentHue,
		matterAttrColorCurrentSaturation,
		matterAttrColorColorTemperatureMireds,
		matterAttrColorOptions,
		matterAttrColorNumPrimaries,
		matterAttrColorColorMode,
		matterAttrColorEnhancedColorMode,
		matterAttrColorColorCapabilities,
		matterAttrColorColorTempPhysicalMinMir,
		matterAttrColorColorTempPhysicalMaxMir,
	}
}

// extractHueOnly pulls the hue from a decoded MoveToHue request. The bridge's
// command-fields reader decodes the payload into the typed wire request
// (tag 0 = Hue); the generic tag-keyed map is accepted as a fallback.
func extractHueOnly(fields any) (uint8, error) {
	switch v := fields.(type) {
	case wire.MoveToHueRequest:
		return v.Hue, nil
	case map[uint8]any:
		return colorTagU8(v, 0), nil
	default:
		return 0, fmt.Errorf("%w: MoveToHue got %T", errMatterValueType, fields)
	}
}

// extractSaturationOnly pulls the saturation from a decoded MoveToSaturation
// request (tag 0 = Saturation).
func extractSaturationOnly(fields any) (uint8, error) {
	switch v := fields.(type) {
	case wire.MoveToSaturationRequest:
		return v.Saturation, nil
	case map[uint8]any:
		return colorTagU8(v, 0), nil
	default:
		return 0, fmt.Errorf("%w: MoveToSaturation got %T", errMatterValueType, fields)
	}
}

// extractHueAndSaturation pulls the (hue, saturation) pair from a decoded
// MoveToHueAndSaturation request (tag 0 = Hue, tag 1 = Saturation).
func extractHueAndSaturation(fields any) (hue, sat uint8, err error) {
	switch v := fields.(type) {
	case wire.MoveToHueAndSaturationRequest:
		return v.Hue, v.Saturation, nil
	case map[uint8]any:
		return colorTagU8(v, 0), colorTagU8(v, 1), nil
	default:
		return 0, 0, fmt.Errorf("%w: MoveToHueAndSaturation got %T", errMatterValueType, fields)
	}
}

// colorTagU8 reads a context-tag value from the generic tag-keyed fields map
// produced by the bridge's decodeGenericTagMap (unsigned ints land as uint64),
// clamped to uint8. Returns 0 when the tag is absent.
func colorTagU8(m map[uint8]any, tag uint8) uint8 {
	switch raw := m[tag].(type) {
	case uint64:
		return uint8(raw & 0xFF)
	case uint8:
		return raw
	default:
		return 0
	}
}

// extractColorTempMireds pulls a uint16 colorTempMireds field.
func extractColorTempMireds(fields any) (uint16, error) {
	switch v := fields.(type) {
	case uint16:
		return v, nil
	case map[string]any:
		raw, ok := v["colorTempMireds"]
		if !ok {
			return 0, fmt.Errorf("%w: MoveToColorTemperature missing colorTempMireds", errMatterValueType)
		}
		mireds, ok := raw.(uint16)
		if !ok {
			return 0, fmt.Errorf("%w: MoveToColorTemperature mireds expected uint16, got %T", errMatterValueType, raw)
		}
		return mireds, nil
	default:
		return 0, fmt.Errorf("%w: MoveToColorTemperature expected uint16 or map, got %T", errMatterValueType, fields)
	}
}
