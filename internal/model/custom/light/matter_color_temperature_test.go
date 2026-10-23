// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	colorcontrolspec "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestColorLightAdvertisesTheColourTemperatureItsDeviceTypeMandates pins
// the FeatureMap of a hue/saturation light: ExtendedColorLight mandates
// XY and CT (matter.js extended-color-light.ts), so the light without a
// colour-temperature axis serves CT as well, mapped onto its colour.
func TestColorLightAdvertisesTheColourTemperatureItsDeviceTypeMandates(t *testing.T) {
	l := newHSLight(t, &colorStubWriter{})
	srv := colorServer(t, l)
	v, _ := srv.MatterRead(matterAttrFeatureMap)
	want := uint32(colorcontrolspec.FeatureHueSaturation | colorcontrolspec.FeatureXy | colorcontrolspec.FeatureColorTemperature)
	if v != want {
		t.Fatalf("FeatureMap = %v, want 0x%X (HS|XY|CT)", v, want)
	}
}

// TestColorLightMoveToColorTemperatureWritesTheNearestWhite pins the
// route of a colour temperature on a light without a colour-temperature
// axis: the mireds become the hue and saturation of that white through
// matter.js's miredsToHsv and reach the device as one colour write, and
// the server reports the CT mode. Without the writer the server stores
// the temperature and nothing reaches the CCU.
func TestColorLightMoveToColorTemperatureWritesTheNearestWhite(t *testing.T) {
	stub := &colorStubWriter{}
	l := newHSLight(t, stub)
	l.OnLevel(onLevel)
	l.hue.OnEvent(int32(180))
	l.saturation.OnEvent(0.5)
	srv := colorServer(t, l)

	const mireds = 370 // ≈ 2700 K, a warm white
	req := colorcontrolspec.MoveToColorTemperatureRequest{ColorTemperatureMireds: mireds}
	if _, err := srv.MatterInvoke(context.Background(), colorcontrolspec.CmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("MoveToColorTemperature: %v", err)
	}
	want, ok := whiteOf(mireds)
	if !ok {
		t.Fatalf("%d mireds has no white", mireds)
	}
	got := writtenValues(stub)
	if hue, _ := got[hmenum.ParameterHue].(int32); hue != matterHueToHM(want.Hue) {
		t.Fatalf("HUE written = %v, want %d (the white of %d mireds)", got[hmenum.ParameterHue], matterHueToHM(want.Hue), mireds)
	}
	if sat, _ := got[hmenum.ParameterSaturation].(float64); absDiffU8(saturationToMatter(sat*100), want.Saturation) > 1 {
		t.Fatalf("SATURATION written = %v, want %v (the white of %d mireds)", sat, matterSaturationToHM(want.Saturation), mireds)
	}
	if sat, _ := got[hmenum.ParameterSaturation].(float64); sat >= 1 {
		t.Fatalf("SATURATION written = %v: a white must not be a fully saturated colour", sat)
	}
	if m := readU8(t, srv, colorcontrolspec.AttrColorMode); m != uint8(colorModeCT) {
		t.Fatalf("ColorMode after MoveToColorTemperature = %d, want 2 (CT)", m)
	}
}

// TestColorLightConfirmedWhiteKeepsTheServersCTMode pins the echo rule of
// the white write: the device confirms the HUE / SATURATION the white
// became, and that confirmation must not move the server to HS — it is
// the write the server just carried out.
func TestColorLightConfirmedWhiteKeepsTheServersCTMode(t *testing.T) {
	stub := &colorStubWriter{}
	l := newHSLight(t, stub)
	l.OnLevel(onLevel)
	l.hue.OnEvent(int32(180))
	l.saturation.OnEvent(0.5)
	srv := colorServer(t, l)

	const mireds = 370
	req := colorcontrolspec.MoveToColorTemperatureRequest{ColorTemperatureMireds: mireds}
	if _, err := srv.MatterInvoke(context.Background(), colorcontrolspec.CmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("MoveToColorTemperature: %v", err)
	}
	want, _ := whiteOf(mireds)
	l.hue.OnEvent(matterHueToHM(want.Hue))
	l.saturation.OnEvent(matterSaturationToHM(want.Saturation) / 100)
	if m := readU8(t, srv, colorcontrolspec.AttrColorMode); m != uint8(colorModeCT) {
		t.Fatalf("ColorMode after the device confirmed the white = %d, want 2 (CT kept)", m)
	}
	l.hue.OnEvent(int32(120))
	l.saturation.OnEvent(1.0)
	if m := readU8(t, srv, colorcontrolspec.AttrColorMode); m != uint8(colorModeHS) {
		t.Fatalf("ColorMode after the device reported another colour = %d, want 0 (HS)", m)
	}
}
