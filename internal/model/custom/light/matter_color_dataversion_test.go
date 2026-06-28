// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityMatterJS_CTColorDataVersionBumpsOnInvoke verifies that a
// successful MoveToColorTemperature invoke via ctColorServer increments
// MatterDataVersion. The CT server shares the embedded Light's
// dataVersion field with OnOff and LevelControl.
func TestParityMatterJS_CTColorDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true},
	}, 2700, 6500)
	before := l.MatterDataVersion()

	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	// MoveToColorTemperature(370 mireds ≈ 2700 K).
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, uint16(370), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToColorTemperature: %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after CT invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_CTColorDataVersionStableOnRead verifies that
// MatterRead on the ctColorServer does not alter MatterDataVersion.
func TestParityMatterJS_CTColorDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true},
	}, 2700, 6500)
	before := l.MatterDataVersion()

	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	cc.MatterRead(matterAttrColorColorTemperatureMireds)
	cc.MatterRead(matterAttrColorColorMode)

	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_HSColorDataVersionBumpsOnInvoke verifies that a
// successful MoveToHueAndSaturation invoke via hsColorServer increments
// MatterDataVersion.
func TestParityMatterJS_HSColorDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true},
	})
	before := l.MatterDataVersion()

	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	if _, err := hs.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 200}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToHueAndSaturation: %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after HS invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_HSColorDataVersionStableOnRead verifies that
// MatterRead on the hsColorServer does not alter MatterDataVersion.
func TestParityMatterJS_HSColorDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newColorRig(t, "HmIP-RGB:4", w, custom.LightCapabilities{SupportsColor: true, Dimmable: true})
	l := NewColorLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, Dimmable: true},
	})
	before := l.MatterDataVersion()

	var hs hsColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(hsColorServer); ok {
			hs = v
		}
	}
	hs.MatterRead(matterAttrColorCurrentHue)
	hs.MatterRead(matterAttrColorCurrentSaturation)

	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_RGBWColorDataVersionBumpsOnHSInvoke verifies that
// a successful MoveToHueAndSaturation invoke via rgbwColorServer
// increments MatterDataVersion. The device mode must be set to RGB
// before the call to satisfy the SetColor capability gate.
func TestParityMatterJS_RGBWColorDataVersionBumpsOnHSInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})
	l.recordMode("RGB") // enable the HS colour path
	before := l.MatterDataVersion()

	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	if _, err := rgbw.MatterInvoke(context.Background(), matterCmdColorMoveToHueAndSaturation, wire.MoveToHueAndSaturationRequest{Hue: 64, Saturation: 128}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("RGBW MoveToHueAndSaturation: %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after RGBW HS invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_RGBWColorDataVersionBumpsOnCTInvoke verifies that
// a successful MoveToColorTemperature invoke via rgbwColorServer
// increments MatterDataVersion.
func TestParityMatterJS_RGBWColorDataVersionBumpsOnCTInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})
	l.recordMode("TUNABLE_WHITE")
	before := l.MatterDataVersion()

	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	if _, err := rgbw.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, uint16(370), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("RGBW MoveToColorTemperature: %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after RGBW CT invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_RGBWColorDataVersionStableOnRead verifies that
// MatterRead on the rgbwColorServer does not alter MatterDataVersion.
func TestParityMatterJS_RGBWColorDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true},
	})
	before := l.MatterDataVersion()

	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	rgbw.MatterRead(matterAttrColorCurrentHue)
	rgbw.MatterRead(matterAttrColorColorTemperatureMireds)

	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}
