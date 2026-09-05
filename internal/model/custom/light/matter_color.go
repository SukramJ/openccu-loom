// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"fmt"

	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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
	// The colour variants shadow the embedded Light's OnMatterValueChanged
	// (see below) so a colour change — hue/saturation, the RF COLOR
	// integer, or colour temperature — dirty-marks the ColorControl
	// attributes in addition to the inherited on/off + brightness
	// propagation, exactly as Climate.OnMatterValueChanged fans out its
	// own value-bearing DPs.
	_ interfaces.MatterChangeNotifier = (*ColorLight)(nil)
	_ interfaces.MatterChangeNotifier = (*ColorTempLight)(nil)
	_ interfaces.MatterChangeNotifier = (*RGBWLight)(nil)
)

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *Light's. A colour light's ColorControl cluster
// depends on HUE/SATURATION or the RF colour dimmers' single COLOR
// integer — DPs the embedded Light knows nothing about — so without this
// override a colour changed at the wall remote, in the CCU WebUI, or from
// a CCU program never dirty-marks the cluster and a subscribed controller
// keeps showing the stale colour until brightness or on/off happens to
// change too. Exactly one of (hue+saturation) and colorIndex is non-nil
// per device family; each embedded DP's own OnMatterValueChanged guards a
// nil receiver, so the unused one contributes a no-op unsubscribe.
func (l *ColorLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.Light.OnMatterValueChanged(cb),
		l.hue.OnMatterValueChanged(cb),
		l.saturation.OnMatterValueChanged(cb),
		l.colorIndex.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *Light's — see [ColorLight.OnMatterValueChanged]
// for the rationale. Exactly one of kelvin and colorLevel is non-nil per
// device family (HmIP COLOR_TEMPERATURE vs. the RF tunable-white
// COLOR_LEVEL channel); the unused one's nil-guarded
// OnMatterValueChanged contributes a no-op unsubscribe.
func (l *ColorTempLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.Light.OnMatterValueChanged(cb),
		l.kelvin.OnMatterValueChanged(cb),
		l.colorLevel.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *ColorLight's — see
// [ColorLight.OnMatterValueChanged] for the rationale. RGBWLight adds its
// own kelvin (COLOR_TEMPERATURE) axis on top of the embedded ColorLight's
// hue/saturation/colorIndex, so both must fan into the callback.
func (l *RGBWLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.ColorLight.OnMatterValueChanged(cb),
		l.kelvin.OnMatterValueChanged(cb),
	)
}

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
	// CoupleColorTempToLevelMinMireds (0x400D, "R V") and
	// StartUpColorTemperatureMireds (0x4010, "RW VM", nullable X, persistent
	// N) are both mandatory once the CT feature + ColorTemperatureMireds are
	// advertised (color-control.element.ts:183-190, conformance
	// "CT & ColorTemperatureMireds"). Missing them makes a cert read return
	// UNSUPPORTED_ATTRIBUTE.
	matterAttrColorCoupleColorTempToLevelMinMir uint32 = 0x400D
	matterAttrColorStartUpColorTempMireds       uint32 = 0x4010

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

	// Matter colour-temperature bounds. Per-device narrowing IS wired:
	// both ColorControl projections report ColorTempPhysicalMin/MaxMireds
	// from the light's own Kelvin bounds, which come from the
	// COLOR_TEMPERATURE descriptor (see [kelvinBoundsFromChannel]).
	//
	// This pair is what [kelvinToMireds] clamps into — the typical
	// "warm-cool LED" window. The clamp is loom-local and its width is
	// **unverified**: matter.js color-control.element.ts constrains the
	// attribute to the full uint16 range, not to this window, and no
	// device descriptor in the fleet names 2000/6535 K. A device
	// declaring a wider range therefore reaches Home Assistant unclamped
	// (payload.go derives min/max mireds straight from the bounds) and
	// Matter clamped. What would settle the width is a matter.js or
	// firmware statement about the advertised range; until then the
	// window stays as shipped rather than being widened on a guess.
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

// colorOptionsAllowExecution mirrors matter.js
// ColorControlServer.ts:1733 (#optionsAllowExecution): a ColorControl
// move-to command silently no-ops while the device is off unless the
// effective ExecuteIfOff option (bit 0) is set. The Options attribute
// (0x000F) is a constant 0 on every projection in this file, so the
// effective option reduces to "mask bit set AND override bit set" —
// matter.js's #calculateEffectiveOptions with options.executeIfOff
// always false. Mirrors [lightLevelServer.levelOptionsAllowExecution]
// in matter.go for the analogous LevelControl gate.
func colorOptionsAllowExecution(on bool, optionsMask, optionsOverride uint8) bool {
	const executeIfOffBit = 0x01
	if optionsMask&executeIfOffBit != 0 && optionsOverride&executeIfOffBit != 0 {
		return true
	}
	return on
}

// extractColorOptions pulls OptionsMask / OptionsOverride out of a
// decoded ColorControl move-to command. The typed wire request structs
// already carry both fields (decoded by the bridge's TLV codec); the
// map[uint8]any fallback reads the command-specific tag pair — the tag
// numbers vary per command (color-control.element.ts:197-198 MoveToHue
// tags 3/4, :205-206 MoveToSaturation tags 2/3, :288-289
// MoveToColorTemperature tags 2/3). Absent fields default to 0,
// matching the spec default for an omitted optional bitmap.
func extractColorOptions(fields any, maskTag, overrideTag uint8) (mask, override uint8) {
	switch v := fields.(type) {
	case wire.MoveToHueRequest:
		return v.OptionsMask, v.OptionsOverride
	case wire.MoveToSaturationRequest:
		return v.OptionsMask, v.OptionsOverride
	case wire.MoveToHueAndSaturationRequest:
		return v.OptionsMask, v.OptionsOverride
	case wire.MoveToColorTemperatureRequest:
		return v.OptionsMask, v.OptionsOverride
	case map[uint8]any:
		if raw, ok := v[maskTag]; ok {
			if m, mok := wireUint8(raw); mok {
				mask = m
			}
		}
		if raw, ok := v[overrideTag]; ok {
			if o, ook := wireUint8(raw); ook {
				override = o
			}
		}
	}
	return mask, override
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
	case matterAttrColorCoupleColorTempToLevelMinMir:
		// Without the CoupleColorTempToLevel feature the spec floors this
		// at ColorTempPhysicalMinMireds (§3.2.6.4.5).
		return kelvinToMireds(s.l.MaxKelvin), true
	case matterAttrColorStartUpColorTempMireds:
		// Nullable (quality X); the bridge stores no start-up colour
		// temperature, so it reports null.
		return nil, true
	case matterAttrFeatureMap:
		return matterColorFeatureCT, true
	case matterAttrClusterRevision:
		return matterColorControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s ctColorServer) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s ctColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	switch cmdID {
	case matterCmdColorMoveToColorTemperature:
		mireds, err := extractColorTempMireds(fields)
		if err != nil {
			return nil, err
		}
		mask, override := extractColorOptions(fields, 2, 3)
		on, _ := s.l.IsOn()
		if !colorOptionsAllowExecution(on, mask, override) {
			// matter.js ColorControlServer.ts:956-957: silent no-op while off
			// and ExecuteIfOff is not effective.
			return nil, nil
		}
		if err := s.l.SetKelvin(ctx, miredsToKelvin(mireds), matterDispatchPriority); err != nil {
			return nil, err
		}
		s.l.dataVersion.Bump()
		return nil, nil
	case wire.ColorCtrlCmdMoveColorTemperature, wire.ColorCtrlCmdStepColorTemperature, wire.ColorCtrlCmdStopMoveStep:
		// HM lights have no continuous-rate colour-temperature sweep;
		// accept the mandatory Move/Step/Stop commands as no-ops so the
		// cluster advertises + honours its full CT command set.
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
}

func (s ctColorServer) MatterReportable() []uint32 {
	return []uint32{matterAttrColorColorTemperatureMireds}
}

// MatterAcceptedCommands lists the ColorControl commands mandatory with
// the CT feature (color-control.element.ts: MoveToColorTemperature,
// MoveColorTemperature, StepColorTemperature all "CT"; StopMoveStep "O").
// Without this the dispatcher advertises an empty AcceptedCommandList and
// a conformance controller rejects the CT cluster.
func (s ctColorServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdColorMoveToColorTemperature,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	}
}

// MatterGeneratedCommands returns nil — ColorControl commands carry no
// response payload.
func (s ctColorServer) MatterGeneratedCommands() []uint32 { return nil }

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
		matterAttrColorCoupleColorTempToLevelMinMir,
		matterAttrColorStartUpColorTempMireds,
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

func (s hsColorServer) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s hsColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	on, _ := s.l.IsOn()
	var err error
	switch cmdID {
	case matterCmdColorMoveToHue:
		hue, e := extractHueOnly(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 3, 4)
		if !colorOptionsAllowExecution(on, mask, override) {
			// matter.js ColorControlServer.ts:409-411: silent no-op while
			// off and ExecuteIfOff is not effective.
			return nil, nil
		}
		_, sat, _ := s.l.Color()
		err = s.l.SetColor(ctx, matterHueToHM(hue), sat, matterDispatchPriority)
	case matterCmdColorMoveToSaturation:
		sat, e := extractSaturationOnly(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 2, 3)
		if !colorOptionsAllowExecution(on, mask, override) {
			return nil, nil
		}
		hue, _, _ := s.l.Color()
		err = s.l.SetColor(ctx, hue, matterSaturationToHM(sat), matterDispatchPriority)
	case matterCmdColorMoveToHueAndSaturation:
		hue, sat, e := extractHueAndSaturation(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 3, 4)
		if !colorOptionsAllowExecution(on, mask, override) {
			return nil, nil
		}
		err = s.l.SetColor(ctx, matterHueToHM(hue), matterSaturationToHM(sat), matterDispatchPriority)
	case wire.ColorCtrlCmdMoveHue, wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation, wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdStopMoveStep:
		// HM has no continuous-rate hue/saturation sweep; accept the
		// mandatory Move/Step/Stop commands as no-ops.
		return nil, nil
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

// MatterAcceptedCommands lists the ColorControl commands mandatory with
// the HS feature (color-control.element.ts: MoveToHue, MoveHue, StepHue,
// MoveToSaturation, MoveSaturation, StepSaturation, MoveToHueAndSaturation
// all "HS"; StopMoveStep "O").
func (s hsColorServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdColorMoveToHue,
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		matterCmdColorMoveToSaturation,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		matterCmdColorMoveToHueAndSaturation,
		wire.ColorCtrlCmdStopMoveStep,
	}
}

// MatterGeneratedCommands returns nil — ColorControl commands carry no
// response payload.
func (s hsColorServer) MatterGeneratedCommands() []uint32 { return nil }

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
		if s.l.colorTempCombined {
			// HmIP-LSC runs HS and CT simultaneously; the active axis is the
			// one currently carrying a value (see colorTempKelvinActive).
			if s.l.colorTempKelvinActive() {
				return matterColorModeColorTemp, true
			}
			return matterColorModeHueSaturation, true
		}
		switch s.l.Mode() {
		case RGBWModeTunableWhite:
			return matterColorModeColorTemp, true
		default:
			return matterColorModeHueSaturation, true
		}
	case matterAttrColorColorCapabilities:
		return matterColorCapHS | matterColorCapCT, true
	case matterAttrColorColorTempPhysicalMinMir:
		// Same rule as ctColorServer: the light's own Kelvin bounds,
		// reciprocated. One datum, one decision site.
		return kelvinToMireds(s.l.MaxKelvin), true // higher Kelvin → lower mireds
	case matterAttrColorColorTempPhysicalMaxMir:
		return kelvinToMireds(s.l.MinKelvin), true
	case matterAttrColorCoupleColorTempToLevelMinMir:
		// No CoupleColorTempToLevel feature → floors at PhysicalMinMireds.
		return kelvinToMireds(s.l.MaxKelvin), true
	case matterAttrColorStartUpColorTempMireds:
		// Nullable; no stored start-up colour temperature.
		return nil, true
	case matterAttrFeatureMap:
		return matterColorFeatureHS | matterColorFeatureCT, true
	case matterAttrClusterRevision:
		return matterColorControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s rgbwColorServer) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s rgbwColorServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	on, _ := s.l.IsOn()
	var err error
	switch cmdID {
	case matterCmdColorMoveToHue:
		hue, e := extractHueOnly(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 3, 4)
		if !colorOptionsAllowExecution(on, mask, override) {
			// matter.js ColorControlServer.ts:409-411: silent no-op while
			// off and ExecuteIfOff is not effective.
			return nil, nil
		}
		_, sat, _ := s.l.Color()
		err = s.l.SetColor(ctx, matterHueToHM(hue), sat, matterDispatchPriority)
	case matterCmdColorMoveToSaturation:
		sat, e := extractSaturationOnly(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 2, 3)
		if !colorOptionsAllowExecution(on, mask, override) {
			return nil, nil
		}
		hue, _, _ := s.l.Color()
		err = s.l.SetColor(ctx, hue, matterSaturationToHM(sat), matterDispatchPriority)
	case matterCmdColorMoveToHueAndSaturation:
		hue, sat, e := extractHueAndSaturation(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 3, 4)
		if !colorOptionsAllowExecution(on, mask, override) {
			return nil, nil
		}
		err = s.l.SetColor(ctx, matterHueToHM(hue), matterSaturationToHM(sat), matterDispatchPriority)
	case matterCmdColorMoveToColorTemperature:
		mireds, e := extractColorTempMireds(fields)
		if e != nil {
			return nil, e
		}
		mask, override := extractColorOptions(fields, 2, 3)
		if !colorOptionsAllowExecution(on, mask, override) {
			return nil, nil
		}
		err = s.l.SetKelvin(ctx, miredsToKelvin(mireds), matterDispatchPriority)
	case wire.ColorCtrlCmdMoveHue, wire.ColorCtrlCmdStepHue,
		wire.ColorCtrlCmdMoveSaturation, wire.ColorCtrlCmdStepSaturation,
		wire.ColorCtrlCmdMoveColorTemperature, wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep:
		// HM has no continuous-rate hue/saturation/CT sweep; accept the
		// mandatory Move/Step/Stop commands as no-ops.
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.l.dataVersion.Bump()
	return nil, nil
}

// MatterAcceptedCommands lists the ColorControl commands mandatory with the
// HS + CT features combined (color-control.element.ts).
func (s rgbwColorServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdColorMoveToHue,
		wire.ColorCtrlCmdMoveHue,
		wire.ColorCtrlCmdStepHue,
		matterCmdColorMoveToSaturation,
		wire.ColorCtrlCmdMoveSaturation,
		wire.ColorCtrlCmdStepSaturation,
		matterCmdColorMoveToHueAndSaturation,
		matterCmdColorMoveToColorTemperature,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
	}
}

// MatterGeneratedCommands returns nil — ColorControl commands carry no
// response payload.
func (s rgbwColorServer) MatterGeneratedCommands() []uint32 { return nil }

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
		matterAttrColorCoupleColorTempToLevelMinMir,
		matterAttrColorStartUpColorTempMireds,
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

// extractColorTempMireds pulls the ColorTemperatureMireds field (context
// tag 0) out of a MoveToColorTemperature request. The bridge decodes
// this command to a typed [wire.MoveToColorTemperatureRequest] (see
// decodeMoveToColorTemperatureFields in
// internal/north/matter/bridge/fields_reader.go), so that is the real
// wire shape; the map[uint8]any / uint16 / string-keyed cases keep the
// helper usable from the generic-decode fallback and the in-package
// tests.
func extractColorTempMireds(fields any) (uint16, error) {
	switch v := fields.(type) {
	case wire.MoveToColorTemperatureRequest:
		return v.ColorTemperatureMireds, nil
	case uint16:
		return v, nil
	case map[uint8]any:
		raw, ok := v[0]
		if !ok {
			return 0, fmt.Errorf("%w: MoveToColorTemperature missing colorTempMireds (tag 0)", errMatterValueType)
		}
		mireds, ok := colorTagU16(raw)
		if !ok {
			return 0, fmt.Errorf("%w: MoveToColorTemperature mireds expected integer, got %T", errMatterValueType, raw)
		}
		return mireds, nil
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
		return 0, fmt.Errorf("%w: MoveToColorTemperature expected typed request or map, got %T", errMatterValueType, fields)
	}
}

// colorTagU16 reads an unsigned 16-bit context-tag value from the generic
// tag-keyed fields map (decodeGenericTagMap stores unsigned ints as
// uint64). The narrower Go-type cases keep the helper usable from tests.
func colorTagU16(raw any) (uint16, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint16(n & 0xFFFF), true
	case uint16:
		return n, true
	case uint8:
		return uint16(n), true
	default:
		return 0, false
	}
}
