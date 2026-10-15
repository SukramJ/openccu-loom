// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	colorcontrolspec "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"
	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newFixedColorLightForMatter builds a fixed-colour light on a channel
// carrying LEVEL and the HmIP-BSL's COLOR value list, with w as the CCU
// writer.
func newFixedColorLightForMatter(t *testing.T, w Writer) *FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BSL0001"})
	ch := d.AddChannel("BSL0001:8", 8, "DIMMER_TRANSMITTER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "BSL0001:8", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "BSL0001:8", hmenum.ParameterColor, w, fixedColorValueList)
	return NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
}

// slotIndex is the position of a colour label in the BSL's VALUE_LIST —
// the raw index the CCU reports for that colour.
func slotIndex(t *testing.T, name string) int32 {
	t.Helper()
	for i, n := range fixedColorValueList {
		if n == name {
			return int32(i)
		}
	}
	t.Fatalf("%q is not in the value list", name)
	return 0
}

func TestFixedColorLightDeviceTypeIsExtendedColorLight(t *testing.T) {
	l := newFixedColorLightForMatter(t, &colorStubWriter{})
	if got := l.MatterDeviceType(); got != matterDeviceTypeExtendedColorLight {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x%04X", got, matterDeviceTypeExtendedColorLight)
	}
	servers := l.MatterClusterServers()
	if len(servers) != 3 {
		t.Fatalf("MatterClusterServers = %d servers, want OnOff + LevelControl + ColorControl", len(servers))
	}
	if cid := servers[2].MatterClusterID(); cid != 0x0300 {
		t.Fatalf("third server is cluster 0x%04X, want ColorControl 0x0300", cid)
	}
}

// TestFixedColorLightMoveToHueAndSaturationSnapsToTheSlot: a colour the
// controller writes becomes one COLOR write with the slot's label, and
// the server keeps the commanded colour until the CCU confirms the slot.
func TestFixedColorLightMoveToHueAndSaturationSnapsToTheSlot(t *testing.T) {
	w := &colorStubWriter{}
	l := newFixedColorLightForMatter(t, w)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)

	// 100° (Matter 71) is in the GREEN band.
	req := wire.MoveToHueAndSaturationRequest{Hue: hueToMatter(100), Saturation: 254}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("MoveToHueAndSaturation: %v", err)
	}
	if got := writtenValues(w)[hmenum.ParameterColor]; got != "GREEN" {
		t.Fatalf("COLOR written = %v, want GREEN", got)
	}
	// The write's optimistic local update already reports the slot, so the
	// command answers with the server settled on GREEN's reference colour,
	// 120°: the controller learns where its colour landed.
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(120) {
		t.Fatalf("CurrentHue right after the command = %d, want GREEN's %d", h, hueToMatter(120))
	}

	// The CCU's echo of the slot changes nothing and causes no second write.
	writes := len(w.calls)
	l.color.OnEvent(slotIndex(t, "GREEN"))
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(120) {
		t.Fatalf("CurrentHue after the confirmed GREEN = %d, want %d", h, hueToMatter(120))
	}
	if s := readU8(t, srv, wire.ColorCtrlAttrCurrentSaturation); s != saturationToMatter(100) {
		t.Fatalf("CurrentSaturation after the confirmed GREEN = %d, want %d", s, saturationToMatter(100))
	}
	if len(w.calls) != writes {
		t.Fatalf("the confirmed slot echoed into a write: %v", writtenValues(w))
	}
}

// TestFixedColorLightLowSaturationWritesWhite: a desaturated colour is
// the WHITE slot whatever its hue.
func TestFixedColorLightLowSaturationWritesWhite(t *testing.T) {
	w := &colorStubWriter{}
	l := newFixedColorLightForMatter(t, w)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)

	req := wire.MoveToHueAndSaturationRequest{Hue: hueToMatter(240), Saturation: 5}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("MoveToHueAndSaturation: %v", err)
	}
	if got := writtenValues(w)[hmenum.ParameterColor]; got != "WHITE" {
		t.Fatalf("COLOR written = %v, want WHITE", got)
	}
}

// TestFixedColorLightMoveToColorSnapsTheConvertedColour: an x/y target
// (the XY feature) is converted to hue/saturation and snapped like any
// other colour — CIE x/y of a saturated blue lands on BLUE.
func TestFixedColorLightMoveToColorSnapsTheConvertedColour(t *testing.T) {
	w := &colorStubWriter{}
	l := newFixedColorLightForMatter(t, w)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)

	x, y := hmToXY(240, 100)
	req := colorcontrolspec.MoveToColorRequest{ColorX: x, ColorY: y}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColor, req); err != nil {
		t.Fatalf("MoveToColor: %v", err)
	}
	if got := writtenValues(w)[hmenum.ParameterColor]; got != "BLUE" {
		t.Fatalf("COLOR written = %v, want BLUE", got)
	}
}

// TestFixedColorLightGatedWhileOff is the fixed-colour twin of the HS
// gate test: no COLOR write while off without ExecuteIfOff.
func TestFixedColorLightGatedWhileOff(t *testing.T) {
	w := &colorStubWriter{}
	l := newFixedColorLightForMatter(t, w)
	l.OnLevel(offLevel)
	srv := colorServer(t, l)

	req := wire.MoveToHueAndSaturationRequest{Hue: 0, Saturation: 254}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("gated MoveToHueAndSaturation returned error: %v", err)
	}
	if _, written := writtenValues(w)[hmenum.ParameterColor]; written {
		t.Fatal("gated MoveToHueAndSaturation wrote COLOR while off without ExecuteIfOff")
	}
}

// TestFixedColorLightServerStartsFromTheObservedSlot: a slot observed
// before the server is built is its initial colour.
func TestFixedColorLightServerStartsFromTheObservedSlot(t *testing.T) {
	w := &colorStubWriter{}
	l := newFixedColorLightForMatter(t, w)
	l.color.OnEvent(slotIndex(t, "PURPLE"))
	srv := colorServer(t, l)
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(300) {
		t.Fatalf("initial CurrentHue = %d, want PURPLE's %d", h, hueToMatter(300))
	}
}

// TestSoundPlayerLEDStaysADimmableLight pins ADR 0012's row for the
// HmIP-MP3P LED: the FixedColorLight it embeds does not pull the
// ColorControl in.
func TestSoundPlayerLEDStaysADimmableLight(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:6", 6, "DIMMER_TRANSMITTER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "MP3P0001:6", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "MP3P0001:6", hmenum.ParameterColor, w, fixedColorValueList)
	led := NewSoundPlayerLED(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true}})
	if got := led.MatterDeviceType(); got != matterDeviceTypeDimmableLight {
		t.Fatalf("MatterDeviceType = 0x%04X, want DimmableLight 0x%04X", got, matterDeviceTypeDimmableLight)
	}
	for _, s := range led.MatterClusterServers() {
		if s.MatterClusterID() == 0x0300 {
			t.Fatal("SoundPlayerLED serves ColorControl; ADR 0012 keeps it on OnOff + LevelControl")
		}
	}
}
