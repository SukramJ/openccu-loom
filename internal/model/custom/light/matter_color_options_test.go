// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHSColorMoveToHueAndSaturationGatedWhileOff mirrors matter.js
// ColorControlServer.ts:721-736 moveToHueAndSaturation() +
// ColorControlServer.ts:1733 #optionsAllowExecution(): while the
// device is off, the command silently no-ops unless the effective
// ExecuteIfOff option is set.
func TestHSColorMoveToHueAndSaturationGatedWhileOff(t *testing.T) {
	w := &stubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, Dimmable: true}
	ch := newColorRig(t, "HmIP-RGB:4", w, caps)
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: caps})
	l.OnLevel(0.0) // off
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}

	// No ExecuteIfOff option: silent no-op, no error, no write.
	req := wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 254}
	if _, err := hs.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("gated MoveToHueAndSaturation returned error: %v", err)
	}
	if hue, _, ok := l.Color(); ok && hue != 0 {
		t.Fatalf("gated MoveToHueAndSaturation wrote hue=%d while off without ExecuteIfOff", hue)
	}

	// ExecuteIfOff effective (mask bit 0 set AND override bit 0 set): executes.
	req.OptionsMask, req.OptionsOverride = 0x01, 0x01
	if _, err := hs.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ExecuteIfOff MoveToHueAndSaturation returned error: %v", err)
	}
	hue, sat, ok := l.Color()
	if !ok || hue != matterHueToHM(127) || sat != matterSaturationToHM(254) {
		t.Fatalf("ExecuteIfOff MoveToHueAndSaturation did not apply: hue=%d sat=%v ok=%v", hue, sat, ok)
	}
}

// TestHSColorMoveToHueAndSaturationExecutesWhileOn confirms the gate
// does not interfere with the common case: the device is on, so the
// command applies regardless of the Options fields.
func TestHSColorMoveToHueAndSaturationExecutesWhileOn(t *testing.T) {
	w := &stubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, Dimmable: true}
	ch := newColorRig(t, "HmIP-RGB:4", w, caps)
	l := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: caps})
	l.OnLevel(0.5) // on
	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	req := wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 254}
	if _, err := hs.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToHueAndSaturation while on returned error: %v", err)
	}
	if hue, _, ok := l.Color(); !ok || hue != matterHueToHM(127) {
		t.Fatalf("MoveToHueAndSaturation while on did not apply: hue=%d ok=%v", hue, ok)
	}
}

// TestCTColorMoveToColorTemperatureGatedWhileOff covers the CT-only
// projection's equivalent gate (ColorControlServer.ts:950-961).
func TestCTColorMoveToColorTemperatureGatedWhileOff(t *testing.T) {
	w := &stubWriter{}
	caps := custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, caps, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: caps}, 2700, 6500)
	l.OnLevel(0.0) // off
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}

	req := wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 370}
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("gated MoveToColorTemperature returned error: %v", err)
	}
	if _, ok := l.Kelvin(); ok {
		t.Fatal("gated MoveToColorTemperature wrote KELVIN while off without ExecuteIfOff")
	}

	req.OptionsMask, req.OptionsOverride = 0x01, 0x01
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ExecuteIfOff MoveToColorTemperature returned error: %v", err)
	}
	if k, ok := l.Kelvin(); !ok || k != miredsToKelvin(370) {
		t.Fatalf("ExecuteIfOff MoveToColorTemperature did not apply: kelvin=%d ok=%v", k, ok)
	}
}

// TestRGBWColorMoveToHueGatedWhileOff covers the combined HS+CT
// projection's gate for the HS command family.
func TestRGBWColorMoveToHueGatedWhileOff(t *testing.T) {
	w := &stubWriter{}
	caps := custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, caps)
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: caps})
	l.OnLevel(0.0) // off
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}

	req := wire.MoveToHueRequest{Hue: 127}
	if _, err := rgbw.MatterInvoke(context.Background(), matterCmdColorMoveToHue, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("gated MoveToHue returned error: %v", err)
	}
	if hue, _, ok := l.CurrentHsColor(); ok && hue != 0 {
		t.Fatalf("gated MoveToHue wrote hue=%d while off without ExecuteIfOff", hue)
	}

	req.OptionsMask, req.OptionsOverride = 0x01, 0x01
	if _, err := rgbw.MatterInvoke(context.Background(), matterCmdColorMoveToHue, req, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("ExecuteIfOff MoveToHue returned error: %v", err)
	}
	if hue, _, ok := l.CurrentHsColor(); !ok || hue != matterHueToHM(127) {
		t.Fatalf("ExecuteIfOff MoveToHue did not apply: hue=%d ok=%v", hue, ok)
	}
}
