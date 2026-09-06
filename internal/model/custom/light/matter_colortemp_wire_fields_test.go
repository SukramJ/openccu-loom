// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestColorTempMoveToColorTemperatureTypedWireShape drives ctColorServer
// (a ColorTempLight's ColorControl server) with the real wire shape the
// bridge's fields reader produces for MoveToColorTemperature (0x0A): this
// command HAS a typed decoder (decodeMoveToColorTemperatureFields in
// go-fabric bridge/fields_reader.go), so the payload arrives
// as wire.MoveToColorTemperatureRequest, not a map. 370 mireds ≈ 2700 K.
// colorStubWriter (not the float64-only stubWriter) is required here
// because SetKelvin writes an int32, not a float64.
func TestColorTempMoveToColorTemperatureTypedWireShape(t *testing.T) {
	w := &colorStubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	l.OnLevel(1.0) // on — the ExecuteIfOff gate only matters while off (see matter_color_options_test.go).
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	req := wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 370}
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, req); err != nil {
		t.Fatalf("MoveToColorTemperature typed wire-shape err: %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterColorTemperature || got.value.(int32) != miredsToKelvin(370) {
		t.Fatalf("MoveToColorTemperature(370 mireds) wrote %+v, want {%v %v}", got, hmenum.ParameterColorTemperature, miredsToKelvin(370))
	}
}

// TestColorTempMoveToColorTemperatureGenericTagMapWireShape covers the
// generic-decode fallback: a command-tag-keyed map[uint8]any whose
// unsigned integer values land as uint64 (see decodeGenericTagMap in
// go-fabric bridge/fields_reader.go). Tag 0 is
// ColorTemperatureMireds. The prior extractor only accepted a typed
// request, bare uint16, or string-keyed map, so a generic-decode path
// (e.g. an unrecognised sub-shape) would have failed here.
func TestColorTempMoveToColorTemperatureGenericTagMapWireShape(t *testing.T) {
	w := &colorStubWriter{}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, custom.LightCapabilities{SupportsColorTemp: true}, 2700, 6500)
	l := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}}, 2700, 6500)
	l.OnLevel(1.0) // on — the ExecuteIfOff gate only matters while off (see matter_color_options_test.go).
	var cc ctColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(ctColorServer); ok {
			cc = v
		}
	}
	fields := map[uint8]any{0: uint64(370)}
	if _, err := cc.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, fields); err != nil {
		t.Fatalf("MoveToColorTemperature generic wire-shape err: %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterColorTemperature || got.value.(int32) != miredsToKelvin(370) {
		t.Fatalf("MoveToColorTemperature(map tag 0=370) wrote %+v, want {%v %v}", got, hmenum.ParameterColorTemperature, miredsToKelvin(370))
	}
}

// TestRGBWMoveToColorTemperatureGenericTagMapWireShape exercises the same
// generic-decode fallback on rgbwColorServer, the other MatterInvoke
// consumer of extractColorTempMireds. The RGBW light must be in
// TunableWhite mode for SetKelvin to be accepted.
func TestRGBWMoveToColorTemperatureGenericTagMapWireShape(t *testing.T) {
	w := &colorStubWriter{}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true})
	l := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}})
	l.recordMode("2_TUNABLE_WHITE")
	l.OnLevel(1.0) // on — the ExecuteIfOff gate only matters while off (see matter_color_options_test.go).
	var rgbw rgbwColorServer
	for _, s := range l.MatterClusterServers() {
		if v, ok := s.(rgbwColorServer); ok {
			rgbw = v
		}
	}
	fields := map[uint8]any{0: uint64(370)}
	if _, err := rgbw.MatterInvoke(context.Background(), matterCmdColorMoveToColorTemperature, fields); err != nil {
		t.Fatalf("MoveToColorTemperature generic wire-shape err: %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterColorTemperature || got.value.(int32) != miredsToKelvin(370) {
		t.Fatalf("MoveToColorTemperature(map tag 0=370) wrote %+v, want {%v %v}", got, hmenum.ParameterColorTemperature, miredsToKelvin(370))
	}
}
