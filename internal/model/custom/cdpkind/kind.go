// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package cdpkind resolves a stable, user-facing kind string for any
// custom data point — `light`, `light_color`, `cover_blind`,
// `cover_garage`, `climate_hmip`, `climate_rf`, `climate_simple`, etc.
// The kind drives widget selection in the SPA's CDP-aware Übersicht
// view (see ADR 0016).
//
// The kind is a UI / north-bound concern, not a model concern, so it
// lives outside the per-category packages — that lets a single
// type-switch sit where it can see every concrete Custom-DP type
// without forcing each category package to import every sibling.
package cdpkind

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// Kind names exported as constants so callers (REST handlers, tests,
// future MQTT discovery enrichers) can reference them symbolically
// rather than re-declare string literals.
const (
	KindUnknown = ""

	KindLight           = "light"
	KindLightColor      = "light_color"
	KindLightColorTemp  = "light_color_temp"
	KindLightFixedColor = "light_fixed_color"
	KindLightRGBW       = "light_rgbw"
	KindLightDali       = "light_dali"
	KindLightEffect     = "light_effect"
	KindLightSoundLed   = "light_sound_led"

	KindCover       = "cover"
	KindCoverBlind  = "cover_blind"
	KindCoverGarage = "cover_garage"

	KindClimateSimple = "climate_simple"
	KindClimateRF     = "climate_rf"
	KindClimateHmIP   = "climate_hmip"

	KindLock        = "lock"
	KindSiren       = "siren"
	KindSirenSmoke  = "siren_smoke"
	KindSirenSound  = "siren_sound"
	KindSwitch      = "switch"
	KindTextDisplay = "text_display"
	KindValveIrr    = "valve_irrigation"
	KindValveMod    = "valve_modulating"
)

// Kinds returns every kind token [Of] can return apart from
// [KindUnknown], in a stable order. It is the source the contract
// export (`script/export_schemas.go`) publishes as the open
// `CustomDPKind` vocabulary — the tokens are documented, not closed,
// because KindUnknown is reachable and consumers must keep a
// fallback.
//
// TestKindsCoversEveryKindConstant pins this against the constants
// above.
//
// loom:reachable:reason="sole caller is script/export_schemas.go, the contract-export generator run by 'make export-schemas'; it carries //go:build ignore and the analyzer builds with -tags=!ignore, so no production edge to it can exist — the guards in vocabulary_test.go pin the returned list against the constants"
func Kinds() []string {
	return []string{
		KindLight,
		KindLightColor,
		KindLightColorTemp,
		KindLightFixedColor,
		KindLightRGBW,
		KindLightDali,
		KindLightEffect,
		KindLightSoundLed,
		KindCover,
		KindCoverBlind,
		KindCoverGarage,
		KindClimateSimple,
		KindClimateRF,
		KindClimateHmIP,
		KindLock,
		KindSiren,
		KindSirenSmoke,
		KindSirenSound,
		KindSwitch,
		KindTextDisplay,
		KindValveIrr,
		KindValveMod,
	}
}

// CapabilityFlags returns every key [Capabilities] can put on its
// map, in a stable order. Published alongside [Kinds] as the open
// `CustomDPCapability` vocabulary: a Custom-DP carries only the flags
// of its own category, so a consumer reads absence as "not
// applicable", never as false.
//
// TestCapabilityFlagsCoversEveryHelper pins this against the per-
// category helpers that actually build the maps.
//
// loom:reachable:reason="sole caller is script/export_schemas.go, the contract-export generator run by 'make export-schemas'; it carries //go:build ignore and the analyzer builds with -tags=!ignore, so no production edge to it can exist — the guards in vocabulary_test.go pin the returned list against the helpers that build the maps"
func CapabilityFlags() []string {
	return []string{
		// light
		"dimmable", "color", "color_temp", "effects", "transition",
		// cover
		"position", "tilt", "stop", "vent", "inverted_control",
		// climate
		"boost", "profile", "auto", "heat", "cool", "off", "away", "comfort", "eco",
		// lock
		"open", "child_safe",
		// siren
		"acoustic", "optical", "duration", "soundfiles", "volume_set",
	}
}

// Of returns the stable kind string for dp, or KindUnknown when dp
// is not a known Custom-DP type. The order in the type-switch is
// most-specific-first: subtypes of Light precede *light.Light itself.
func Of(dp device.AttachableDataPoint) string {
	switch v := dp.(type) {
	// --- light variants ---
	// Specific subtypes precede the bare *light.Light embedded base.
	case *light.ColorLight:
		return KindLightColor
	case *light.ColorTempLight:
		return KindLightColorTemp
	case *light.FixedColorLight:
		return KindLightFixedColor
	case *light.RGBWLight:
		return KindLightRGBW
	case *light.DRGDaliLight:
		return KindLightDali
	case *light.EffectLight:
		return KindLightEffect
	case *light.SoundPlayerLED:
		return KindLightSoundLed
	case *light.Light:
		return KindLight

	// --- cover variants ---
	case *cover.Garage:
		return KindCoverGarage
	case *cover.Blind:
		return KindCoverBlind
	case *cover.Cover:
		return KindCover

	// --- climate variants ---
	case *climate.Climate:
		switch v.Kind {
		case climate.KindSimpleRF:
			return KindClimateSimple
		case climate.KindRF:
			return KindClimateRF
		case climate.KindIP:
			return KindClimateHmIP
		}
		return KindUnknown

	// --- single-flavour categories ---
	case *lock.Lock:
		return KindLock
	case *siren.Siren:
		return KindSiren
	case *siren.SmokeSiren:
		return KindSirenSmoke
	case *siren.SoundPlayer:
		return KindSirenSound
	case *switchdev.Switch:
		return KindSwitch
	case *switchdev.AccessPermission:
		// A per-user access permission is a switch on every surface —
		// SWITCH category, `switch` HA component, granted/revoked — so it
		// shares the switch widget rather than carrying a kind of its own.
		return KindSwitch
	case *textdisplay.TextDisplay:
		return KindTextDisplay
	case *valve.Irrigation:
		return KindValveIrr
	case *valve.Modulating:
		return KindValveMod
	}
	return KindUnknown
}

// Capabilities returns a flat string→bool map describing which
// optional features the custom-DP supports. Mirrors the categorical
// Capability struct that lives on the DP itself; flattening to a map
// keeps the REST DTO free of category-specific shapes.
//
// Returns an empty map when the DP does not embed a known
// Capability struct (TextDisplay, Switch, Valve — no per-feature
// flags worth exposing).
func Capabilities(dp device.AttachableDataPoint) map[string]bool {
	switch v := dp.(type) {
	case *light.ColorLight:
		return lightCaps(v.Capabilities)
	case *light.ColorTempLight:
		return lightCaps(v.Capabilities)
	case *light.FixedColorLight:
		return lightCaps(v.Capabilities)
	case *light.RGBWLight:
		// The HmIP-RGBW family advertises colour / colour-temp / effect support
		// per the current DEVICE_OPERATION_MODE (RGB/RGBW → hs, TUNABLE_WHITE →
		// colour temp, every non-PWM mode → effects), mirroring the reference
		// CustomDpIpRGBWLight.has_* mode gating. The static profile flags only
		// carry the mode-independent dimmable/transition pair, so the three
		// mode-gated flags overwrite their static counterparts — building on
		// lightCaps keeps the light flag set defined in exactly one place.
		caps := lightCaps(v.Capabilities)
		caps["color"] = v.HasColor()
		caps["color_temp"] = v.HasColorTempColorMode()
		caps["effects"] = v.HasEffects()
		return caps
	case *light.DRGDaliLight:
		return lightCaps(v.Capabilities)
	case *light.EffectLight:
		return lightCaps(v.Capabilities)
	case *light.SoundPlayerLED:
		return lightCaps(v.Capabilities)
	case *light.Light:
		return lightCaps(v.Capabilities)

	case *cover.Garage:
		return coverCaps(v.Capabilities)
	case *cover.Blind:
		return coverCaps(v.Capabilities)
	case *cover.Cover:
		return coverCaps(v.Capabilities)

	case *climate.Climate:
		return climateCaps(v.Capabilities)

	case *lock.Lock:
		return lockCaps(v.Capabilities)
	case *siren.Siren:
		return sirenCaps(v.Capabilities)
	}
	return map[string]bool{}
}

func lightCaps(c custom.LightCapabilities) map[string]bool {
	return map[string]bool{
		"dimmable":   c.Dimmable,
		"color":      c.SupportsColor,
		"color_temp": c.SupportsColorTemp,
		"effects":    c.SupportsEffects,
		"transition": c.Transition,
	}
}

func coverCaps(c custom.CoverCapabilities) map[string]bool {
	return map[string]bool{
		"position":         c.SupportsPosition,
		"tilt":             c.SupportsTilt,
		"stop":             c.SupportsStop,
		"vent":             c.SupportsVent,
		"inverted_control": c.InvertedControl,
	}
}

func climateCaps(c custom.ClimateCapabilities) map[string]bool {
	return map[string]bool{
		"boost":   c.SupportsBoost,
		"profile": c.SupportsProfile,
		"auto":    c.SupportsAuto,
		"heat":    c.SupportsHeat,
		"cool":    c.SupportsCool,
		"off":     c.SupportsOff,
		"away":    c.SupportsAway,
		"comfort": c.SupportsComfort,
		"eco":     c.SupportsEco,
	}
}

func lockCaps(c custom.LockCapabilities) map[string]bool {
	return map[string]bool{
		"open":       c.SupportsOpen,
		"child_safe": c.SupportsChildSafe,
	}
}

func sirenCaps(c custom.SirenCapabilities) map[string]bool {
	return map[string]bool{
		"acoustic":   c.SupportsAcoustic,
		"optical":    c.SupportsOptical,
		"duration":   c.SupportsDuration,
		"soundfiles": c.SupportsSoundfiles,
		"volume_set": c.SupportsVolumeSet,
	}
}
