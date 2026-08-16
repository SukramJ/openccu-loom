// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cdpkind

import (
	"testing"

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

func TestOf_LightVariants(t *testing.T) {
	t.Parallel()
	// Typed-nil pointers exercise the type-switch dispatch without
	// needing fully-wired DPs — Of() never dereferences them for
	// these arms.
	cases := []struct {
		name string
		dp   device.AttachableDataPoint
		want string
	}{
		{"ColorLight", (*light.ColorLight)(nil), KindLightColor},
		{"ColorTempLight", (*light.ColorTempLight)(nil), KindLightColorTemp},
		{"FixedColorLight", (*light.FixedColorLight)(nil), KindLightFixedColor},
		{"RGBWLight", (*light.RGBWLight)(nil), KindLightRGBW},
		{"DRGDaliLight", (*light.DRGDaliLight)(nil), KindLightDali},
		{"EffectLight", (*light.EffectLight)(nil), KindLightEffect},
		{"SoundPlayerLED", (*light.SoundPlayerLED)(nil), KindLightSoundLed},
		{"BareLight", (*light.Light)(nil), KindLight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Of(tc.dp); got != tc.want {
				t.Fatalf("Of(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestOf_CoverVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dp   device.AttachableDataPoint
		want string
	}{
		{"Garage", (*cover.Garage)(nil), KindCoverGarage},
		{"Blind", (*cover.Blind)(nil), KindCoverBlind},
		{"BareCover", (*cover.Cover)(nil), KindCover},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Of(tc.dp); got != tc.want {
				t.Fatalf("Of(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestOf_ClimateVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind climate.Kind
		want string
	}{
		{"SimpleRF", climate.KindSimpleRF, KindClimateSimple},
		{"RF", climate.KindRF, KindClimateRF},
		{"IP", climate.KindIP, KindClimateHmIP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := &climate.Climate{Kind: tc.kind}
			if got := Of(dp); got != tc.want {
				t.Fatalf("Of(Climate{Kind=%v}) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

func TestOf_SingleFlavourCategories(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dp   device.AttachableDataPoint
		want string
	}{
		{"Lock", (*lock.Lock)(nil), KindLock},
		{"Siren", (*siren.Siren)(nil), KindSiren},
		{"Switch", (*switchdev.Switch)(nil), KindSwitch},
		// A per-user access permission shares the switch widget: an
		// unresolved kind renders it as an unknown tile in the SPA.
		{"AccessPermission", (*switchdev.AccessPermission)(nil), KindSwitch},
		{"TextDisplay", (*textdisplay.TextDisplay)(nil), KindTextDisplay},
		{"ValveIrrigation", (*valve.Irrigation)(nil), KindValveIrr},
		{"ValveModulating", (*valve.Modulating)(nil), KindValveMod},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Of(tc.dp); got != tc.want {
				t.Fatalf("Of(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestOf_UnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := Of(nil); got != KindUnknown {
		t.Fatalf("Of(nil) = %q, want KindUnknown", got)
	}
}

func TestCapabilities_Light(t *testing.T) {
	t.Parallel()
	caps := custom.LightCapabilities{
		Dimmable:          true,
		SupportsColor:     true,
		SupportsColorTemp: false,
		SupportsEffects:   true,
		Transition:        true,
	}
	want := map[string]bool{
		"dimmable":   true,
		"color":      true,
		"color_temp": false,
		"effects":    true,
		"transition": true,
	}
	// Cover every light variant — they share the lightCaps helper
	// but the type-switch arm must dispatch them all correctly.
	base := &light.Light{Capabilities: caps}
	colorBase := &light.ColorLight{Light: base}
	colorTempBase := &light.ColorTempLight{Light: base}
	fixedBase := &light.FixedColorLight{Light: base}
	checks := []struct {
		name string
		dp   device.AttachableDataPoint
		want map[string]bool
	}{
		{"ColorLight", colorBase, want},
		{"ColorTempLight", colorTempBase, want},
		{"FixedColorLight", fixedBase, want},
		// The RGBW family derives colour / colour-temp / effects from the
		// current DEVICE_OPERATION_MODE, not the static struct flags. With no
		// mode observed it defaults to RGBW (hs colour, no colour temp) and,
		// with no EFFECT data point bound, reports no effects.
		{"RGBWLight", &light.RGBWLight{ColorLight: colorBase}, map[string]bool{
			"dimmable": true, "transition": true, "color": true, "color_temp": false, "effects": false,
		}},
		{"DRGDaliLight", &light.DRGDaliLight{ColorTempLight: colorTempBase}, want},
		{"EffectLight", &light.EffectLight{ColorLight: colorBase}, want},
		{"SoundPlayerLED", &light.SoundPlayerLED{FixedColorLight: fixedBase}, want},
		{"BareLight", base, want},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Capabilities(tc.dp)
			assertCapsEqual(t, got, tc.want)
		})
	}
}

func TestCapabilities_Cover(t *testing.T) {
	t.Parallel()
	caps := custom.CoverCapabilities{
		SupportsPosition: true,
		SupportsTilt:     true,
		SupportsStop:     true,
		SupportsVent:     false,
		InvertedControl:  true,
	}
	want := map[string]bool{
		"position":         true,
		"tilt":             true,
		"stop":             true,
		"vent":             false,
		"inverted_control": true,
	}
	checks := []struct {
		name string
		dp   device.AttachableDataPoint
	}{
		{"Garage", &cover.Garage{Capabilities: caps}},
		{"Blind", &cover.Blind{Cover: &cover.Cover{Capabilities: caps}}},
		{"BareCover", &cover.Cover{Capabilities: caps}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Capabilities(tc.dp)
			assertCapsEqual(t, got, want)
		})
	}
}

func TestCapabilities_Climate(t *testing.T) {
	t.Parallel()
	caps := custom.ClimateCapabilities{
		SupportsBoost:   true,
		SupportsProfile: true,
		SupportsAuto:    true,
		SupportsHeat:    true,
		SupportsCool:    false,
		SupportsOff:     true,
		SupportsAway:    true,
		SupportsComfort: true,
		SupportsEco:     true,
	}
	got := Capabilities(&climate.Climate{Kind: climate.KindIP, Capabilities: caps})
	assertCapsEqual(t, got, map[string]bool{
		"boost":   true,
		"profile": true,
		"auto":    true,
		"heat":    true,
		"cool":    false,
		"off":     true,
		"away":    true,
		"comfort": true,
		"eco":     true,
	})
}

func TestCapabilities_Lock(t *testing.T) {
	t.Parallel()
	got := Capabilities(&lock.Lock{Capabilities: custom.LockCapabilities{
		SupportsOpen:      true,
		SupportsChildSafe: false,
	}})
	assertCapsEqual(t, got, map[string]bool{
		"open":       true,
		"child_safe": false,
	})
}

func TestCapabilities_Siren(t *testing.T) {
	t.Parallel()
	got := Capabilities(&siren.Siren{Capabilities: custom.SirenCapabilities{
		SupportsAcoustic:   true,
		SupportsOptical:    true,
		SupportsDuration:   true,
		SupportsSoundfiles: false,
		SupportsVolumeSet:  true,
	}})
	assertCapsEqual(t, got, map[string]bool{
		"acoustic":   true,
		"optical":    true,
		"duration":   true,
		"soundfiles": false,
		"volume_set": true,
	})
}

func TestCapabilities_NonCapDPsReturnEmpty(t *testing.T) {
	t.Parallel()
	// Switch / TextDisplay / Valve hit the default arm — they
	// don't embed a Capability struct.
	dps := []device.AttachableDataPoint{
		(*switchdev.Switch)(nil),
		(*textdisplay.TextDisplay)(nil),
		(*valve.Irrigation)(nil),
		(*valve.Modulating)(nil),
	}
	for _, dp := range dps {
		got := Capabilities(dp)
		if len(got) != 0 {
			t.Errorf("expected empty caps for %T, got %v", dp, got)
		}
	}
}

func assertCapsEqual(t *testing.T, got, want map[string]bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d (got=%v, want=%v)", len(got), len(want), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("caps[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestCapabilities_UnknownReturnsEmptyMap(t *testing.T) {
	t.Parallel()
	got := Capabilities(nil)
	if got == nil {
		t.Fatal("Capabilities must return a non-nil map")
	}
	if len(got) != 0 {
		t.Fatalf("unknown DP must yield empty map, got %v", got)
	}
}
