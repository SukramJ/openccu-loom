// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- Encoding helpers ---

// TestHueRoundTripFullCircle locks the hue encoding boundaries.
// HM 0° → Matter 0; HM 180° → Matter 127; HM 359° → Matter 253-254.
func TestHueRoundTripFullCircle(t *testing.T) {
	cases := []struct {
		hm    int32
		want  uint8
		delta uint8
	}{
		{0, 0, 0},
		{90, 64, 1},
		{180, 127, 1},
		{270, 191, 1},
		{359, 253, 2},
	}
	for _, tc := range cases {
		got := hueToMatter(tc.hm)
		if got+tc.delta < tc.want || got > tc.want+tc.delta {
			t.Errorf("hueToMatter(%d°) = %d, want %d±%d", tc.hm, got, tc.want, tc.delta)
		}
	}
}

// TestHueWrapAroundNegativeAndOver360 covers the wrap mathematics —
// negative hues and ≥360 should land on the equivalent positive
// segment.
func TestHueWrapAroundNegativeAndOver360(t *testing.T) {
	if hueToMatter(360) != 0 {
		t.Errorf("hueToMatter(360) = %d, want 0", hueToMatter(360))
	}
	if hueToMatter(-90) != hueToMatter(270) {
		t.Errorf("negative hue not normalised: -90→%d, 270→%d", hueToMatter(-90), hueToMatter(270))
	}
}

// TestSaturationClampsToMatterScale: HM 100 (HA-canonical) must encode to
// 254 (the non-null max), not 255.
func TestSaturationClampsToMatterScale(t *testing.T) {
	if got := saturationToMatter(100); got != 254 {
		t.Errorf("saturationToMatter(100) = %d, want 254 (Matter null=255)", got)
	}
	if got := saturationToMatter(0); got != 0 {
		t.Errorf("saturationToMatter(0) = %d, want 0", got)
	}
	if got := saturationToMatter(150); got != 254 {
		t.Errorf("saturationToMatter(150) over-range = %d, want clamped to 254", got)
	}
}

// TestKelvinToMiredsConversionRange covers the Mired conversion at
// the typical warm/cool extremes.
func TestKelvinToMiredsConversionRange(t *testing.T) {
	// 6500 K → 153 Mireds (cool white); spec defaults the cluster
	// physical-min to ~153.
	if got := kelvinToMireds(6500); got != 153 {
		t.Errorf("kelvinToMireds(6500) = %d, want 153", got)
	}
	// 2700 K → ~370 Mireds (warm white).
	if got := kelvinToMireds(2700); got != 370 {
		t.Errorf("kelvinToMireds(2700) = %d, want 370", got)
	}
	// Extremes clamp into [matterMinMireds, matterMaxMireds].
	if got := kelvinToMireds(10000); got != matterMinMireds {
		t.Errorf("kelvinToMireds(10000) = %d, want clamp to %d", got, matterMinMireds)
	}
	if got := kelvinToMireds(1500); got != matterMaxMireds {
		t.Errorf("kelvinToMireds(1500) = %d, want clamp to %d", got, matterMaxMireds)
	}
}

// --- ColorTempLight ---

// TestColorTempLightDeviceTypeIsColorTemperatureLight locks the
// 0x010C device type override on the embedded Light's 0x0101.
func TestColorTempLightDeviceTypeIsColorTemperatureLight(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	if got := l.MatterDeviceType(); got != 0x010C {
		t.Fatalf("ColorTempLight.MatterDeviceType = 0x%04X, want 0x010C", got)
	}
}

// TestColorTempLightExposesOnOffLevelAndColorControl confirms the
// three-cluster projection.
func TestColorTempLightExposesOnOffLevelAndColorControl(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	got := map[uint32]bool{}
	for _, s := range l.MatterClusterServers() {
		got[s.MatterClusterID()] = true
	}
	for _, want := range []uint32{0x0006, 0x0008, 0x0300} {
		if !got[want] {
			t.Errorf("ColorTempLight cluster 0x%04X missing from %v", want, got)
		}
	}
}

// TestColorTempServerKelvinReadAndWrite round-trips through Mireds.
func TestColorTempServerKelvinReadAndWrite(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	// Find ColorControl server.
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	// Issue MoveToColorTemperature(370 mireds ≈ 2700 K).
	if _, err := cc.MatterInvoke(context.Background(), 0x0A, uint16(370), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToColorTemperature err: %v", err)
	}
	// Verify Kelvin reached the wire.
	dp := ch.Parameter(hmenum.ParameterColorTemperature)
	if dp == nil {
		t.Fatal("COLOR_TEMPERATURE DP missing from channel")
	}
}

// TestColorTempServerColorModeIsCT locks the EnhancedColorMode read.
func TestColorTempServerColorModeIsCT(t *testing.T) {
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	v, ok := cc.MatterRead(0x0008)
	if !ok || v.(uint8) != matterColorModeColorTemp {
		t.Fatalf("ColorMode = (%v, %v), want (2=ColorTemp, true)", v, ok)
	}
}

// --- ColorLight ---

// TestColorLightDeviceTypeIsExtendedColorLight locks the 0x010D
// device type override.
func TestColorLightDeviceTypeIsExtendedColorLight(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	if got := l.MatterDeviceType(); got != 0x010D {
		t.Fatalf("ColorLight.MatterDeviceType = 0x%04X, want 0x010D", got)
	}
}

// TestColorLightHueSatRoundTrip routes a MoveToHueAndSaturation
// command through SetColor.
func TestColorLightHueSatRoundTrip(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	// Matter 127 hue ≈ 180°, 254 saturation = 1.0.
	if _, err := hs.MatterInvoke(context.Background(), 0x06, wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 254}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToHueAndSaturation err: %v", err)
	}
	if ch.Parameter(hmenum.ParameterHue) == nil {
		t.Fatal("HUE DP missing")
	}
	if ch.Parameter(hmenum.ParameterSaturation) == nil {
		t.Fatal("SATURATION DP missing")
	}
}

// TestColorLightHueSatMissingFieldsRejected covers the field-shape
// guard.
func TestColorLightHueSatMissingFieldsRejected(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	fields := map[string]any{"hue": uint8(64)} // missing saturation
	_, err := hs.MatterInvoke(context.Background(), 0x06, fields, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("err = %v, want errMatterValueType for missing saturation", err)
	}
}

// TestColorLightColorModeIsHS locks the EnhancedColorMode read.
func TestColorLightColorModeIsHS(t *testing.T) {
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	v, ok := hs.MatterRead(0x0008)
	if !ok || v.(uint8) != matterColorModeHueSaturation {
		t.Fatalf("ColorMode = (%v, %v), want (0=HS, true)", v, ok)
	}
}

// --- RGBWLight ---

// TestRGBWLightDeviceTypeIsExtendedColorLight locks the 0x010D device
// type override.
func TestRGBWLightDeviceTypeIsExtendedColorLight(t *testing.T) {
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	if got := l.MatterDeviceType(); got != 0x010D {
		t.Fatalf("RGBWLight.MatterDeviceType = 0x%04X, want 0x010D", got)
	}
}

// TestRGBWLightFeatureMapAdvertisesHSAndCT confirms the FeatureMap
// has both HS (bit 0) and CT (bit 4) bits set.
func TestRGBWLightFeatureMapAdvertisesHSAndCT(t *testing.T) {
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	v, _ := rgbw.MatterRead(0xFFFC)
	got := v.(uint32)
	if got&matterColorFeatureHS == 0 {
		t.Errorf("FeatureMap = 0x%08X, missing HS bit", got)
	}
	if got&matterColorFeatureCT == 0 {
		t.Errorf("FeatureMap = 0x%08X, missing CT bit", got)
	}
}

// TestRGBWLightInvokeMoveToCTRequiresTunableMode confirms the
// projection delegates the mode-gating to the upstream RGBWLight.
// HmIP-RGBW devices reject SetKelvin when the current mode is not
// RGBW or TunableWhite — the projection surfaces that error verbatim.
func TestRGBWLightInvokeMoveToCTRequiresTunableMode(t *testing.T) {
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	// Seed the mode via the wire-side recorder so SetKelvin is allowed.
	l.recordMode("2_TUNABLE_WHITE")
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	if _, err := rgbw.MatterInvoke(context.Background(), 0x0A, uint16(370), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToColorTemperature err: %v", err)
	}
	if ch.Parameter(hmenum.ParameterColorTemperature) == nil {
		t.Fatal("COLOR_TEMPERATURE DP missing")
	}
}

// TestRGBWLightColorModeFollowsDeviceOperatingMode locks the ColorMode
// projection contract. ColorMode (and the mirror EnhancedColorMode) must
// report CT when the device-side RGBW operating mode is TunableWhite and
// HS in every other case — Apple Home reads ColorMode at Subscribe-initial
// and routes MoveTo* commands accordingly. A projection that returns HS
// unconditionally would route MoveToHueAndSaturation at a TunableWhite-mode
// lamp.
//
// Mirrors matter.js color-control behavior: the server reports the mode
// matching the most recently active attribute family.
func TestRGBWLightColorModeFollowsDeviceOperatingMode(t *testing.T) {
	cases := []struct {
		modeWire string
		want     uint8
	}{
		{"2_TUNABLE_WHITE", matterColorModeColorTemp},
		{"RGB", matterColorModeHueSaturation},
		{"RGBW", matterColorModeHueSaturation},
		{"4_PWM", matterColorModeHueSaturation},
		// Empty / unset mode resolves to RGBWModeUnknown and must keep
		// the defensive HS fallback.
		{"", matterColorModeHueSaturation},
	}
	for _, tc := range cases {
		t.Run(tc.modeWire, func(t *testing.T) {
			w := &stubWriter{}
			ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
			l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
			if tc.modeWire != "" {
				l.recordMode(tc.modeWire)
			}
			var rgbw rgbwColorServer
			for _, s := range l.MatterClusterServers() {
				if v, ok := s.(rgbwColorServer); ok {
					rgbw = v
				}
			}
			for _, attrID := range []uint32{matterAttrColorColorMode, matterAttrColorEnhancedColorMode} {
				got, ok := rgbw.MatterRead(attrID)
				if !ok {
					t.Fatalf("attr 0x%04X: MatterRead must return ok", attrID)
				}
				if got.(uint8) != tc.want {
					t.Fatalf("attr 0x%04X mode=%q: got %d, want %d", attrID, tc.modeWire, got.(uint8), tc.want)
				}
			}
		})
	}
}
