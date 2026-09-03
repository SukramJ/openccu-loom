// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// These tests guard the dispatch cases for the two registered light profiles
// that used to fall through the type-switch to "unsupported data point type":
// DRGDaliLight (HmIP-DRG-DALI) and SoundPlayerLED (HmIP-MP3P status LED). Both
// are advertised as controllable, so every turn_on/off/brightness/color(_temp)
// command over REST/WS/MQTT errored until they were routed like the other
// lights. Each assertion below fails with an "unsupported data point type" error
// if its case is removed.

// actionSelectDP builds a write-only enum action DP (the shape EFFECT takes on
// the DALI channel) so NewDRGDaliLight resolves an ActionSelect for it.
func actionSelectDP(address string, param hmenum.Parameter, w generic.Writer, valueList []string) *generic.ActionSelect {
	return generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  valueList,
		},
		Writer: w,
	})
}

func buildDRGDaliLightDP(t *testing.T, addr string, w *dispatchWriter) *light.DRGDaliLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "DALI", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(intDP(addr+":1", hmenum.ParameterColorTemperature, w))
	ch.Put(actionSelectDP(addr+":1", hmenum.ParameterEffect, w, []string{"OFF", "FLASH"}))
	return light.NewDRGDaliLight(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}}, 2000, 6500)
}

func buildSoundPlayerLEDDP(t *testing.T, addr string, w *dispatchWriter) *light.SoundPlayerLED {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "MP3P_LED", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(selectDP(addr+":1", hmenum.ParameterColor, w,
		[]string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"}))
	// The HmIP-MP3P LED profile is dimmable / colour-capable (see
	// newSoundPlayerLEDConstructor); mirror it so set_brightness routes.
	return light.NewSoundPlayerLED(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true}})
}

// ============================================================
// DRGDaliLight (HmIP-DRG-DALI)
// ============================================================

func TestDispatchDRGDaliLight_TurnOnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildDRGDaliLightDP(t, "DALI001", w)
	// Seed LEVEL=0 (off) so turn_on crosses an on/off boundary and actually
	// writes, mirroring TestDispatchLight_TurnOn.
	if l.Float != nil {
		l.OnEvent(0)
	}
	disp, _ := buildDispatcher(t, "DALI001", "LEVEL", l)

	if err := disp.InvokeCustomDP(context.Background(), "DALI001", "LEVEL", "turn_on", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("turn_on wrote nothing")
	}

	if err := disp.InvokeCustomDP(context.Background(), "DALI001", "LEVEL", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off: %v", err)
	}
	s, ok := w.lastSet()
	if !ok || s.value != 0.0 {
		t.Fatalf("turn_off did not write LEVEL=0 (ok=%v, set=%+v)", ok, s)
	}
}

func TestDispatchDRGDaliLight_SetBrightness(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildDRGDaliLightDP(t, "DALI002", w)
	disp, _ := buildDispatcher(t, "DALI002", "LEVEL", l)

	params := map[string]any{"brightness": 0.5}
	if err := disp.InvokeCustomDP(context.Background(), "DALI002", "LEVEL", "set_brightness", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_brightness: %v", err)
	}
	s, ok := w.lastSet()
	if !ok || s.value != 0.5 {
		t.Fatalf("set_brightness did not write LEVEL=0.5 (ok=%v, set=%+v)", ok, s)
	}
}

func TestDispatchDRGDaliLight_SetColorTemperature(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildDRGDaliLightDP(t, "DALI003", w)
	disp, _ := buildDispatcher(t, "DALI003", "LEVEL", l)

	params := map[string]any{"kelvin": float64(4000)}
	if err := disp.InvokeCustomDP(context.Background(), "DALI003", "LEVEL", "set_color_temperature", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color_temperature: %v", err)
	}
	if len(w.setsFor(hmenum.ParameterColorTemperature)) == 0 {
		t.Fatal("set_color_temperature never wrote COLOR_TEMPERATURE")
	}
}

// DALI is ColorTempLight plus an optional EFFECT surface; set_effect must reach
// the EFFECT parameter rather than error as an unknown operation.
func TestDispatchDRGDaliLight_SetEffect(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildDRGDaliLightDP(t, "DALI004", w)
	disp, _ := buildDispatcher(t, "DALI004", "LEVEL", l)

	params := map[string]any{"label": "FLASH"}
	if err := disp.InvokeCustomDP(context.Background(), "DALI004", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_effect: %v", err)
	}
	sets := w.setsFor(hmenum.ParameterEffect)
	if len(sets) != 1 {
		t.Fatalf("EFFECT writes = %d, want 1", len(sets))
	}
	if sets[0].value != "FLASH" {
		t.Fatalf("EFFECT written = %v, want %q", sets[0].value, "FLASH")
	}
}

// ============================================================
// SoundPlayerLED (HmIP-MP3P status LED)
// ============================================================

// TestDispatchSoundPlayerLED_TurnOnOff is the regression guard for the
// dispatcher routing bug: turn_on/turn_off used to go through the embedded
// FixedColorLight's plain Light.TurnOn/TurnOff, which write LEVEL only. The
// LED stays dark at its power-on COLOR=BLACK default even though LEVEL
// reports "on" — a plain turn_on must write COLOR (not just LEVEL), and
// turn_off must write COLOR=BLACK atomically with ON_TIME=0 to also clear
// any running flash timer.
func TestDispatchSoundPlayerLED_TurnOnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildSoundPlayerLEDDP(t, "MP3P001", w)
	if l.Float != nil {
		l.OnEvent(0)
	}
	disp, _ := buildDispatcher(t, "MP3P001", "LEVEL", l)

	if err := disp.InvokeCustomDP(context.Background(), "MP3P001", "LEVEL", "turn_on", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	onPut, ok := w.lastPut()
	if !ok {
		t.Fatal("turn_on did not reach an atomic put_paramset")
	}
	if _, ok := onPut.values["COLOR"]; !ok {
		t.Fatalf("turn_on did not write COLOR, only %v — the LED stays dark at COLOR=BLACK", onPut.values)
	}
	if v, ok := onPut.values["LEVEL"]; !ok || v == 0.0 {
		t.Fatalf("turn_on did not turn LEVEL on, got %v", onPut.values)
	}

	if err := disp.InvokeCustomDP(context.Background(), "MP3P001", "LEVEL", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off: %v", err)
	}
	offPut, ok := w.lastPut()
	if !ok {
		t.Fatal("turn_off did not reach an atomic put_paramset")
	}
	if got := offPut.values["COLOR"]; got != "BLACK" {
		t.Fatalf("turn_off COLOR = %v, want BLACK", got)
	}
	if got := offPut.values["ON_TIME"]; got != 0.0 {
		t.Fatalf("turn_off ON_TIME = %v, want 0.0 (must clear a running flash timer)", got)
	}
}

// TestDispatchSoundPlayerLED_TurnOnWithHSColorAndFlash verifies the LED's
// own optional turn_on fields (hue/saturation, flash timing, repetitions)
// reach the wire, so a caller is not limited to the fixed-colour path's
// "label"/"slot" shape for a one-shot flashing turn_on.
func TestDispatchSoundPlayerLED_TurnOnWithHSColorAndFlash(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildSoundPlayerLEDDP(t, "MP3P004", w)
	disp, _ := buildDispatcher(t, "MP3P004", "LEVEL", l)

	params := map[string]any{
		"hue":           float64(0),
		"saturation":    float64(100),
		"flash_time_ms": float64(500),
		"repetitions":   float64(3),
	}
	if err := disp.InvokeCustomDP(context.Background(), "MP3P004", "LEVEL", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	put, ok := w.lastPut()
	if !ok {
		t.Fatal("turn_on did not reach an atomic put_paramset")
	}
	if got := put.values["COLOR"]; got != "RED" {
		t.Fatalf("COLOR = %v, want RED (hue=0/saturation=100)", got)
	}
	if got := put.values["ON_TIME_LIST_1"]; got != "500MS" {
		t.Fatalf("ON_TIME_LIST_1 = %v, want 500MS", got)
	}
	if got := put.values["REPETITIONS"]; got != "REPETITIONS_003" {
		t.Fatalf("REPETITIONS = %v, want REPETITIONS_003", got)
	}
}

// TestDispatchSoundPlayerLED_SetLevelStateShapeRoutesToLEDMethods verifies
// the HA mqtt-light `set_level{"state":"ON"/"OFF"}` JSON shape also reaches
// the LED's own atomic turn_on/turn_off rather than the LEVEL-only path.
func TestDispatchSoundPlayerLED_SetLevelStateShapeRoutesToLEDMethods(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildSoundPlayerLEDDP(t, "MP3P005", w)
	disp, _ := buildDispatcher(t, "MP3P005", "LEVEL", l)

	params := map[string]any{"state": "ON"}
	if err := disp.InvokeCustomDP(context.Background(), "MP3P005", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level state=ON: %v", err)
	}
	put, ok := w.lastPut()
	if !ok {
		t.Fatal("set_level state=ON did not reach an atomic put_paramset")
	}
	if _, ok := put.values["COLOR"]; !ok {
		t.Fatalf("set_level state=ON did not write COLOR, got %v", put.values)
	}

	params = map[string]any{"state": "OFF"}
	if err := disp.InvokeCustomDP(context.Background(), "MP3P005", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level state=OFF: %v", err)
	}
	put, ok = w.lastPut()
	if !ok || put.values["COLOR"] != "BLACK" {
		t.Fatalf("set_level state=OFF did not write COLOR=BLACK, got %+v (ok=%v)", put.values, ok)
	}
}

func TestDispatchSoundPlayerLED_SetBrightness(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildSoundPlayerLEDDP(t, "MP3P002", w)
	disp, _ := buildDispatcher(t, "MP3P002", "LEVEL", l)

	params := map[string]any{"brightness": 0.5}
	if err := disp.InvokeCustomDP(context.Background(), "MP3P002", "LEVEL", "set_brightness", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_brightness: %v", err)
	}
	s, ok := w.lastSet()
	if !ok || s.value != 0.5 {
		t.Fatalf("set_brightness did not write LEVEL=0.5 (ok=%v, set=%+v)", ok, s)
	}
}

func TestDispatchSoundPlayerLED_SetColorByLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildSoundPlayerLEDDP(t, "MP3P003", w)
	disp, _ := buildDispatcher(t, "MP3P003", "LEVEL", l)

	params := map[string]any{"label": "GREEN"}
	if err := disp.InvokeCustomDP(context.Background(), "MP3P003", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color: %v", err)
	}
	sets := w.setsFor(hmenum.ParameterColor)
	if len(sets) != 1 {
		t.Fatalf("COLOR writes = %d, want 1", len(sets))
	}
	if sets[0].value != "GREEN" {
		t.Fatalf("COLOR written = %v, want %q", sets[0].value, "GREEN")
	}
}
