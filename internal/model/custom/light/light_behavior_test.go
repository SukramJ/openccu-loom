// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for behavior branches across all light types: Light, ColorLight,
// ColorTempLight, FixedColorLight, EffectLight, DRGDaliLight, RGBWLight,
// SoundPlayerLED, topology, and matter server helpers.

package light

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func putStringSensor(ch *device.Channel, address string, p hmenum.Parameter, w Writer) {
	ch.Put(generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
}

func putWritableIntegerWithValueList(ch *device.Channel, address string, p hmenum.Parameter, w Writer, vl []string) {
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  vl,
		},
		Writer: w,
	}))
}

func newEffectLightRigWithEffects(t *testing.T, address string, w Writer, effects []string) *EffectLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "EFF0001"})
	ch := d.AddChannel(address, 1, "COLOR_DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableIntegerWithValueList(ch, address, hmenum.ParameterProgram, w, effects)
	return NewEffectLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true}})
}

func newRGBWLightRigCh(t *testing.T, address string, w Writer, channelNo int) *RGBWLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGBW0001"})
	ch := d.AddChannel(address, channelNo, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterColorTemperature, w)
	putWritableIntegerWithValueList(ch, address, hmenum.ParameterEffect, w, []string{"OFF", "EFFECT1", "EFFECT2"})
	putStringSensor(ch, address, hmenum.ParameterDeviceOperationMode, w)
	r := NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsColorTemp: true}})
	unsub := r.Subscribe(ch)
	t.Cleanup(unsub)
	return r
}

// ─── Light: uncovered branches ──────────────────────────────────────────────

// TestLightNamePostfix verifies the base Light.NamePostfix returns "".
func TestLightNamePostfix(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if got := l.NamePostfix(); got != "" {
		t.Errorf("Light.NamePostfix() = %q, want %q", got, "")
	}
}

// TestLightIsRefreshed verifies IsRefreshed returns false before any event and
// true after one.
func TestLightIsRefreshed(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	if l.IsRefreshed() {
		t.Error("IsRefreshed() should be false before any event")
	}
	level.OnEvent(0.5)
	if !l.IsRefreshed() {
		t.Error("IsRefreshed() should be true after OnEvent")
	}
}

// TestLightSubDataPointKeys verifies SubDataPointKeys returns the LEVEL DP key.
func TestLightSubDataPointKeys(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	keys := l.SubDataPointKeys()
	if len(keys) != 1 {
		t.Fatalf("SubDataPointKeys() len=%d, want 1", len(keys))
	}
	if keys[0].Parameter != string(hmenum.ParameterLevel) {
		t.Errorf("SubDataPointKeys()[0].Parameter = %q, want LEVEL", keys[0].Parameter)
	}
}

// TestLightSubDataPointKeysNilFloat verifies SubDataPointKeys returns nil when Float is nil.
func TestLightSubDataPointKeysNilFloat(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BDT:4", 1, "DIMMER", hmenum.ParamsetKeyValues)
	l := New(Config{Channel: ch, Capabilities: custom.LightCapabilities{}})
	if keys := l.SubDataPointKeys(); keys != nil {
		t.Errorf("SubDataPointKeys() on nil Float = %v, want nil", keys)
	}
}

// TestLightSetTimerRampTime verifies SetTimerRampTime stores the duration and
// that TurnOn includes it in the put_paramset.
func TestLightSetTimerRampTime(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	l.SetTimerRampTime(3 * time.Second)
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if v, ok := got[string(hmenum.ParameterRampTime)]; !ok {
		t.Error("RAMP_TIME missing from put_paramset")
	} else if v.(float64) != 3 {
		t.Errorf("RAMP_TIME=%v, want 3", v)
	}
}

// TestLightSetTimerZeroClearsTimer verifies that SetTimerOnTime(0) clears a
// previously stored timer.
func TestLightSetTimerZeroClearsTimer(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	l.SetTimerOnTime(5 * time.Second)
	// Clear it.
	l.SetTimerOnTime(0)
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// With no pending timer, TurnOn uses plain SetLevel — no put_paramset.
	if len(w.puts) != 0 {
		t.Errorf("expected 0 put_paramset after clearing timer, got %d", len(w.puts))
	}
}

// TestLightSetOnTime verifies SetOnTime dispatches two SetValue calls
// (ON_TIME_VALUE + ON_TIME_UNIT) without error.
func TestLightSetOnTime(t *testing.T) {
	w := &multiWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.SetOnTime(context.Background(), w, "HmIP-BDT:4", 30*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetOnTime error: %v", err)
	}
	found := map[hmenum.Parameter]bool{}
	for _, c := range w.calls {
		found[c.param] = true
	}
	if !found[hmenum.ParameterOnTimeValue] {
		t.Error("SetOnTime must write ON_TIME_VALUE")
	}
	if !found[hmenum.ParameterOnTimeUnit] {
		t.Error("SetOnTime must write ON_TIME_UNIT")
	}
}

// TestLightSetRampTime verifies SetRampTime dispatches two SetValue calls
// (RAMP_TIME_VALUE + RAMP_TIME_UNIT) without error.
func TestLightSetRampTime(t *testing.T) {
	w := &multiWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.SetRampTime(context.Background(), w, "HmIP-BDT:4", 10*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetRampTime error: %v", err)
	}
	found := map[hmenum.Parameter]bool{}
	for _, c := range w.calls {
		found[c.param] = true
	}
	if !found[hmenum.ParameterRampTimeValue] {
		t.Error("SetRampTime must write RAMP_TIME_VALUE")
	}
	if !found[hmenum.ParameterRampTimeUnit] {
		t.Error("SetRampTime must write RAMP_TIME_UNIT")
	}
}

// TestLightClearLastSent verifies the internal clearLastSent helper resets the
// tracking slot so lastSentValue returns (0, false).
func TestLightClearLastSent(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.recordLastSent(0.5)
	if _, ok := l.lastSentValue(); !ok {
		t.Fatal("expected lastSentValue ok=true after recordLastSent")
	}
	l.clearLastSent()
	if _, ok := l.lastSentValue(); ok {
		t.Error("expected lastSentValue ok=false after clearLastSent")
	}
}

// TestLightTurnOffWithRampZeroDurationFallsThrough verifies that a zero or
// negative ramp duration delegates to plain TurnOff.
func TestLightTurnOffWithRampZeroDurationFallsThrough(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.TurnOffWithRamp(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOffWithRamp(0): %v", err)
	}
	// stubWriter.last records the value sent via SetValue.
	if w.last != 0 {
		t.Errorf("TurnOffWithRamp(0) wrote %v, want 0", w.last)
	}
}

// TestLightAddressNilFloat verifies Address() returns "" when Float is nil.
func TestLightAddressNilFloat(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("X:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	l := New(Config{Channel: ch})
	if got := l.Address(); got != "" {
		t.Errorf("Address() on nil Float = %q, want %q", got, "")
	}
}

// TestLightIsStateChangeBranches exercises all IsStateChange branches:
// unobserved, turnOn, turnOff, and brightness mismatch.
func TestLightIsStateChangeBranches(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Not observed yet → always a state change.
	if !l.IsStateChange(false, false, nil) {
		t.Error("IsStateChange on unobserved must return true")
	}

	// Observe off.
	level.OnEvent(0)
	// turnOn while off → change.
	if !l.IsStateChange(true, false, nil) {
		t.Error("IsStateChange(turnOn) while off must return true")
	}
	// turnOff while off → no change.
	if l.IsStateChange(false, true, nil) {
		t.Error("IsStateChange(turnOff) while off must return false")
	}

	// Observe on.
	level.OnEvent(0.5)
	// turnOff while on → change.
	if !l.IsStateChange(false, true, nil) {
		t.Error("IsStateChange(turnOff) while on must return true")
	}
	// Same brightness → no change.
	b := custom.NewBrightness(0.5).Byte()
	if l.IsStateChange(false, false, &b) {
		t.Error("IsStateChange with matching brightness must return false")
	}
	// Different brightness → change.
	diff := b + 10
	if !l.IsStateChange(false, false, &diff) {
		t.Error("IsStateChange with different brightness must return true")
	}
}

// TestLightHAComponentAndTopicSlot exercises the topology methods.
func TestLightHAComponentAndTopicSlot(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if got := l.HAComponent(); got != "light" {
		t.Errorf("HAComponent() = %q, want %q", got, "light")
	}
	slot := l.TopicSlot()
	if slot.Parameter != "light" {
		t.Errorf("TopicSlot().Parameter = %q, want %q", slot.Parameter, "light")
	}
}

// ─── ColorLight: uncovered branches ─────────────────────────────────────────

// TestColorLightNamePostfix verifies ColorLight.NamePostfix returns "color".
func TestColorLightNamePostfix(t *testing.T) {
	w := &colorStubWriter{}
	ch := newColorRig(t, "X:1", w, custom.LightCapabilities{})
	l := NewColorLight(Config{Channel: ch, Writer: w})
	if got := l.NamePostfix(); got != "color" {
		t.Errorf("ColorLight.NamePostfix() = %q, want %q", got, "color")
	}
}

// TestColorLightColorObserved verifies Color() reports observed=true after DP
// events.
func TestColorLightColorObserved(t *testing.T) {
	w := &colorStubWriter{}
	cl, _, hue, sat := newColorLightRig(t, "X:1", w)
	_, _, obs := cl.Color()
	if obs {
		t.Error("Color() before events must be unobserved")
	}
	hue.OnEvent(int32(120))
	sat.OnEvent(0.8)
	h, s, obs := cl.Color()
	if !obs {
		t.Error("Color() after events must be observed")
	}
	if h != 120 || s != 0.8 {
		t.Errorf("Color() = (%d, %.2f), want (120, 0.80)", h, s)
	}
}

// TestColorLightSetColorMissingDP verifies SetColor returns an error when
// HUE or SATURATION DP is absent.
func TestColorLightSetColorMissingDP(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	// No HUE/SAT DPs.
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, &colorStubWriter{})
	l := NewColorLight(Config{Channel: ch})
	if err := l.SetColor(context.Background(), 120, 0.5, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetColor with missing HUE/SAT must return an error")
	}
}

// ─── ColorTempLight: uncovered branches ─────────────────────────────────────

// TestColorTempLightNamePostfix verifies the "color_temp" postfix.
func TestColorTempLightNamePostfix(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, _ := newColorTempLightRig(t, "X:1", w, 2000, 6500)
	if got := ctl.NamePostfix(); got != "color_temp" {
		t.Errorf("ColorTempLight.NamePostfix() = %q, want %q", got, "color_temp")
	}
}

// TestColorTempLightKelvinObserved verifies Kelvin returns observed=false until
// an event arrives.
func TestColorTempLightKelvinObserved(t *testing.T) {
	w := &colorStubWriter{}
	ctl, _, kelvin := newColorTempLightRig(t, "X:1", w, 2000, 6500)
	if _, ok := ctl.Kelvin(); ok {
		t.Error("Kelvin() must be unobserved before any event")
	}
	kelvin.OnEvent(int32(4000))
	k, ok := ctl.Kelvin()
	if !ok || k != 4000 {
		t.Errorf("Kelvin() = (%d, %v), want (4000, true)", k, ok)
	}
}

// TestColorTempLightSetKelvinMissingDP verifies SetKelvin returns an error when
// the COLOR_TEMPERATURE DP is absent.
func TestColorTempLightSetKelvinMissingDP(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, &colorStubWriter{})
	l := NewColorTempLight(Config{Channel: ch}, 2000, 6500)
	if err := l.SetKelvin(context.Background(), 3000, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetKelvin with missing COLOR_TEMPERATURE must return an error")
	}
}

// ─── FixedColorLight: uncovered branches ────────────────────────────────────

// TestFixedColorLightNamePostfix verifies the "color" postfix.
func TestFixedColorLightNamePostfix(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w})
	if got := l.NamePostfix(); got != "color" {
		t.Errorf("FixedColorLight.NamePostfix() = %q, want %q", got, "color")
	}
}

// TestFixedColorLightColorName verifies ColorName returns the correct string.
func TestFixedColorLightColorName(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "X:1", hmenum.ParameterColor, w, fixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w})

	// Not observed yet.
	if _, ok := l.ColorName(); ok {
		t.Error("ColorName() before event must be unobserved")
	}

	// Set to FixedColorBlue (4 → BLUE).
	if err := l.SetColor(context.Background(), FixedColorBlue, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	name, ok := l.ColorName()
	if !ok {
		t.Error("ColorName() after SetColor must be observed")
	}
	if name != "BLUE" {
		t.Errorf("ColorName() = %q, want %q", name, "BLUE")
	}
}

// TestFixedColorLightColorNameUnknownSlot verifies ColorName returns ("", false)
// for an unknown slot value.
func TestFixedColorLightColorNameUnknownSlot(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "X:1", hmenum.ParameterColor, w, fixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w})
	// Inject an unknown slot index (99) directly onto the Select DP.
	colorDP := ch.Parameter(hmenum.ParameterColor)
	if colorDP != nil {
		if sel, ok := colorDP.(*generic.Select); ok {
			sel.OnEvent(int32(99))
		}
	}
	_, okName := l.ColorName()
	// Unknown slot → ColorName returns ("", false).
	if okName {
		t.Error("ColorName() for unknown slot must return ok=false")
	}
}

// TestFixedColorLightChannelHsColor verifies ChannelHsColor maps the CHANNEL_COLOR
// string sensor to (hue, sat, ok).
func TestFixedColorLightChannelHsColor(t *testing.T) {
	w := &colorStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "X:1", hmenum.ParameterColor, w, fixedColorValueList)
	// Add CHANNEL_COLOR sensor.
	channelColorDP := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "X:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterChannelColor),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(channelColorDP)

	l := NewFixedColorLight(Config{Channel: ch, Writer: w})

	// Not observed.
	_, _, ok := l.ChannelHsColor()
	if ok {
		t.Error("ChannelHsColor() before any event must return ok=false")
	}

	// Known colour: RED maps to hue=0, sat=1.
	channelColorDP.OnEvent("RED")
	h, s, ok := l.ChannelHsColor()
	if !ok {
		t.Error("ChannelHsColor() after RED event must return ok=true")
	}
	if h != 0 || s != 1 {
		t.Errorf("ChannelHsColor(RED) = (%d, %.2f), want (0, 1.00)", h, s)
	}

	// Unknown name: returns (0, 0, true) — unknown fallback per code.
	channelColorDP.OnEvent("UNKNOWN_COLOR_XYZ")
	h, s, ok = l.ChannelHsColor()
	if !ok {
		t.Error("ChannelHsColor() with unknown name must still return ok=true (fallback)")
	}
	if h != 0 || s != 0 {
		t.Errorf("ChannelHsColor(unknown) = (%d, %.2f), want (0, 0.00)", h, s)
	}
}

// TestHSToFixedColorMapping verifies the hue banding + saturation threshold.
func TestHSToFixedColorMapping(t *testing.T) {
	cases := []struct {
		hue  int32
		sat  float64
		want FixedColor
	}{
		{0, 0.01, FixedColorWhite},    // sat < 0.05 → white
		{0, 1.0, FixedColorRed},       // 0° → red
		{359, 1.0, FixedColorRed},     // 359° wraps to red band
		{60, 1.0, FixedColorYellow},   // 60° → yellow
		{120, 1.0, FixedColorGreen},   // 120° → green
		{180, 1.0, FixedColorCyan},    // 180° → cyan
		{240, 1.0, FixedColorBlue},    // 240° → blue
		{300, 1.0, FixedColorMagenta}, // 300° → magenta
	}
	for _, tc := range cases {
		got := HSToFixedColor(tc.hue, tc.sat)
		if got != tc.want {
			t.Errorf("HSToFixedColor(%d, %.2f) = %d, want %d", tc.hue, tc.sat, got, tc.want)
		}
	}
}

// TestFixedColorToHSRoundTrip verifies FixedColorToHS for all named colours.
func TestFixedColorToHSRoundTrip(t *testing.T) {
	cases := []struct {
		c       FixedColor
		wantHue int32
		wantSat float64
	}{
		{FixedColorWhite, 0, 0},
		{FixedColorRed, 0, 1},
		{FixedColorYellow, 60, 1},
		{FixedColorGreen, 120, 1},
		{FixedColorCyan, 180, 1},
		{FixedColorBlue, 240, 1},
		{FixedColorMagenta, 300, 1},
		{FixedColorBlack, 0, 0}, // default case
	}
	for _, tc := range cases {
		h, s := FixedColorToHS(tc.c)
		if h != tc.wantHue || s != tc.wantSat {
			t.Errorf("FixedColorToHS(%d) = (%d, %.2f), want (%d, %.2f)", tc.c, h, s, tc.wantHue, tc.wantSat)
		}
	}
}

// TestFixedColorLightSetColorMissingDP verifies SetColor errors when COLOR DP absent.
func TestFixedColorLightSetColorMissingDP(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, &colorStubWriter{})
	// No COLOR DP.
	l := NewFixedColorLight(Config{Channel: ch})
	if err := l.SetColor(context.Background(), FixedColorRed, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetColor with missing COLOR DP must return an error")
	}
}

// TestTurnOnFixedColorFullBundle verifies TurnOnFixedColor sends a full bundle.
func TestTurnOnFixedColorFullBundle(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "X:1", hmenum.ParameterColor, w, fixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})

	dur := 10 * time.Second
	br := 0.5
	if err := l.TurnOnFixedColor(context.Background(), FixedColorOnConfig{
		Color:          FixedColorGreen,
		HasColor:       true,
		ColorBehaviour: "ON",
		Duration:       &dur,
		Brightness:     &br,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnFixedColor error: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected at least one put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	// TurnOnFixedColor sends the string label "GREEN" for FixedColorGreen.
	if params[string(hmenum.ParameterColor)] != "GREEN" {
		t.Errorf("COLOR=%v, want GREEN", params[string(hmenum.ParameterColor)])
	}
	if params[string(hmenum.ParameterColorBehaviour)] != "ON" {
		t.Errorf("COLOR_BEHAVIOUR=%v, want ON", params[string(hmenum.ParameterColorBehaviour)])
	}
	if params[string(hmenum.ParameterLevel)].(float64) != 0.5 {
		t.Errorf("LEVEL=%v, want 0.5", params[string(hmenum.ParameterLevel)])
	}
}

// TestTurnOnFixedColorNoBehaviourDefault verifies that HasColor=true with
// empty ColorBehaviour defaults to "ON".
func TestTurnOnFixedColorNoBehaviourDefault(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "FCL", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "X:1", hmenum.ParameterColor, w, fixedColorValueList)
	l := NewFixedColorLight(Config{Channel: ch, Writer: w})
	if err := l.TurnOnFixedColor(context.Background(), FixedColorOnConfig{
		Color:    FixedColorRed,
		HasColor: true,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnFixedColor: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	if got := w.puts[0][string(hmenum.ParameterColorBehaviour)]; got != "ON" {
		t.Errorf("COLOR_BEHAVIOUR=%v, want ON (default)", got)
	}
}

// ─── EffectLight: uncovered branches ────────────────────────────────────────

// TestEffectLightNamePostfix verifies the "effect" postfix.
func TestEffectLightNamePostfix(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRigWithEffects(t, "X:1", w, []string{"Off", "Slow", "Fast"})
	if got := el.NamePostfix(); got != "effect" {
		t.Errorf("EffectLight.NamePostfix() = %q, want %q", got, "effect")
	}
}

// TestEffectLightEffectsAndEffect verifies Effects() and Effect().
func TestEffectLightEffectsAndEffect(t *testing.T) {
	w := &colorStubWriter{}
	effects := []string{"Off", "Slow", "Fast"}
	el := newEffectLightRigWithEffects(t, "X:1", w, effects)

	got := el.Effects()
	if len(got) != len(effects) {
		t.Fatalf("Effects() len=%d, want %d", len(got), len(effects))
	}
	for i, e := range effects {
		if got[i] != e {
			t.Errorf("Effects()[%d]=%q, want %q", i, got[i], e)
		}
	}

	// No program event yet.
	if _, _, obs := el.Effect(); obs {
		t.Error("Effect() before event must be unobserved")
	}

	// Inject an event via the underlying program DP.
	progDP := el.program
	if progDP != nil {
		progDP.OnEvent(int32(1))
	}
	idx, label, obs := el.Effect()
	if !obs {
		t.Fatal("Effect() after event must be observed")
	}
	if idx != 1 || label != "Slow" {
		t.Errorf("Effect() = (%d, %q), want (1, Slow)", idx, label)
	}
}

// TestEffectLightEffectOutOfRange tests Effect() when index is out of effects list.
func TestEffectLightEffectOutOfRange(t *testing.T) {
	w := &colorStubWriter{}
	effects := []string{"Off", "Slow"}
	el := newEffectLightRigWithEffects(t, "X:1", w, effects)
	// Inject an out-of-range index.
	if el.program != nil {
		el.program.OnEvent(int32(99))
	}
	idx, label, obs := el.Effect()
	if !obs {
		t.Fatal("Effect() with out-of-range index must still be observed")
	}
	if idx != 99 || label != "" {
		t.Errorf("Effect() = (%d, %q), want (99, \"\")", idx, label)
	}
}

// TestEffectLightSetEffect verifies SetEffect dispatches to the program DP.
func TestEffectLightSetEffect(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRigWithEffects(t, "X:1", w, []string{"Off", "Slow", "Fast"})
	if err := el.SetEffect(context.Background(), 2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetEffect(2): %v", err)
	}
	// Last call must be to PROGRAM with value 2.
	found := false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterProgram {
			found = true
			if c.value.(int32) != 2 {
				t.Errorf("PROGRAM=%v, want 2", c.value)
			}
		}
	}
	if !found {
		t.Error("SetEffect must write PROGRAM parameter")
	}
}

// TestEffectLightSetEffectOutOfRange verifies SetEffect rejects out-of-range indices.
func TestEffectLightSetEffectOutOfRange(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRigWithEffects(t, "X:1", w, []string{"Off", "Slow"})
	if err := el.SetEffect(context.Background(), 5, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetEffect(out-of-range) must return an error")
	}
	if err := el.SetEffect(context.Background(), -1, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetEffect(-1) must return an error")
	}
}

// TestEffectLightSetEffectByLabel verifies SetEffectByLabel dispatches correctly.
func TestEffectLightSetEffectByLabel(t *testing.T) {
	w := &colorStubWriter{}
	el := newEffectLightRigWithEffects(t, "X:1", w, []string{"Off", "Slow", "Fast"})
	if err := el.SetEffectByLabel(context.Background(), "Fast", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetEffectByLabel(Fast): %v", err)
	}
	found := false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterProgram && c.value.(int32) == 2 {
			found = true
		}
	}
	if !found {
		t.Error("SetEffectByLabel(Fast) must write PROGRAM=2")
	}
	if err := el.SetEffectByLabel(context.Background(), "UNKNOWN", hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetEffectByLabel(unknown) must return an error")
	}
}

// TestEffectLightEffectsNil verifies Effects() returns nil when effects list is nil.
func TestEffectLightEffectsNil(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "X0001"})
	ch := d.AddChannel("X:1", 1, "EFFECT", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "X:1", hmenum.ParameterLevel, &colorStubWriter{})
	el := NewEffectLight(Config{Channel: ch})
	if el.Effects() != nil {
		t.Error("Effects() with nil list must return nil")
	}
}

// ─── DRGDaliLight: uncovered branches ───────────────────────────────────────

// TestDRGDaliLightNamePostfix verifies the "color_temp" postfix.
func TestDRGDaliLightNamePostfix(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "D0001"})
	ch := d.AddChannel("D:1", 1, "DALI", hmenum.ParamsetKeyValues)
	l := NewDRGDaliLight(Config{Channel: ch}, 2700, 6500)
	if got := l.NamePostfix(); got != "color_temp" {
		t.Errorf("DRGDaliLight.NamePostfix() = %q, want %q", got, "color_temp")
	}
}

// TestDRGDaliLightSetEffectNoOp verifies SetEffect on a DALI light without an
// EFFECT parameter is a no-op (nil effect field → early return nil).
func TestDRGDaliLightSetEffectNoOp(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "D0001"})
	ch := d.AddChannel("D:1", 1, "DALI", hmenum.ParamsetKeyValues)
	l := NewDRGDaliLight(Config{Channel: ch}, 2700, 6500)
	if err := l.SetEffect(context.Background(), "Off", hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("DRGDaliLight.SetEffect must be a no-op when no EFFECT DP present, got: %v", err)
	}
}

// ─── RGBWLight: uncovered branches ──────────────────────────────────────────

// TestRGBWLightNamePostfixAllModes verifies NamePostfix for each mode.
func TestRGBWLightNamePostfixAllModes(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"RGB", "hs"},
		{"RGBW", "hs"},
		{"TUNABLE_WHITE", "color_temp"},
		{"PWM", ""},
		{"", ""},
	}
	for _, tc := range cases {
		r := &RGBWLight{}
		if tc.mode != "" {
			r.recordMode(tc.mode)
		}
		if got := r.NamePostfix(); got != tc.want {
			t.Errorf("NamePostfix (mode=%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// TestRGBWLightHasMode verifies HasMode returns false before any event and
// true after one.
func TestRGBWLightHasMode(t *testing.T) {
	r := newRGBWLightRigCh(t, "RGBW:1", &colorStubWriter{}, 1)
	if r.HasMode() {
		t.Error("HasMode() before any event must return false")
	}
	r.recordMode("PWM")
	if !r.HasMode() {
		t.Error("HasMode() after recordMode must return true")
	}
}

// TestRGBWLightUsageAllModes verifies Usage() returns the correct value for all
// channel/mode combinations.
func TestRGBWLightUsageAllModes(t *testing.T) {
	// Unknown mode → RGBWUsageUnknown.
	r := &RGBWLight{channelNo: 1}
	if got := r.Usage(); got != RGBWUsageUnknown {
		t.Errorf("Usage() unknown mode = %v, want RGBWUsageUnknown", got)
	}

	// RGB mode: channels 2,3,4 → Secondary; channel 1 → Primary.
	for _, no := range []int{1, 2, 3, 4} {
		r2 := &RGBWLight{channelNo: no}
		r2.recordMode("RGB")
		got := r2.Usage()
		want := RGBWUsagePrimary
		if no == 2 || no == 3 || no == 4 {
			want = RGBWUsageSecondary
		}
		if got != want {
			t.Errorf("Usage() RGB ch%d = %v, want %v", no, got, want)
		}
	}

	// TunableWhite: channels 3,4 → Secondary; others → Primary.
	for _, no := range []int{1, 2, 3, 4} {
		r3 := &RGBWLight{channelNo: no}
		r3.recordMode("TUNABLE_WHITE")
		got := r3.Usage()
		want := RGBWUsagePrimary
		if no == 3 || no == 4 {
			want = RGBWUsageSecondary
		}
		if got != want {
			t.Errorf("Usage() TunableWhite ch%d = %v, want %v", no, got, want)
		}
	}

	// RGBW: same as RGB for secondary channels.
	r4 := &RGBWLight{channelNo: 3}
	r4.recordMode("RGBW")
	if got := r4.Usage(); got != RGBWUsageSecondary {
		t.Errorf("Usage() RGBW ch3 = %v, want RGBWUsageSecondary", got)
	}
}

// TestRGBWLightKelvinBranches verifies Kelvin returns false when DP absent and
// true after an event.
func TestRGBWLightKelvinBranches(t *testing.T) {
	// No kelvin DP.
	r := &RGBWLight{}
	if _, ok := r.Kelvin(); ok {
		t.Error("Kelvin() with nil kelvin DP must return ok=false")
	}

	// With DP.
	w := &colorStubWriter{}
	r2 := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	if _, ok := r2.Kelvin(); ok {
		t.Error("Kelvin() before event must return ok=false")
	}
	// Feed an event.
	r2.recordMode("TUNABLE_WHITE")
	kelvinDP := r2.kelvin
	if kelvinDP != nil {
		kelvinDP.OnEvent(int32(4000))
	}
	k, ok := r2.Kelvin()
	if !ok {
		t.Fatal("Kelvin() after event must return ok=true")
	}
	if k != 4000 {
		t.Errorf("Kelvin() = %d, want 4000", k)
	}
}

// TestRGBWLightSetKelvinModeCheck verifies SetKelvin rejects non-temp modes.
func TestRGBWLightSetKelvinModeCheck(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	r.recordMode("RGB")
	if err := r.SetKelvin(context.Background(), 4000, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetKelvin in RGB mode must return an error")
	}
}

// TestRGBWLightSetKelvinClampsAndSucceeds verifies SetKelvin clamps and writes.
func TestRGBWLightSetKelvinClampsAndSucceeds(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	r.recordMode("TUNABLE_WHITE")
	// Below min.
	if err := r.SetKelvin(context.Background(), 100, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetKelvin(100): %v", err)
	}
	// Above max.
	if err := r.SetKelvin(context.Background(), 99999, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetKelvin(99999): %v", err)
	}
}

// TestRGBWLightEffectsBranches exercises Effects() branching.
func TestRGBWLightEffectsBranches(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)

	// PWM mode → no effects.
	r.recordMode("PWM")
	if r.Effects() != nil {
		t.Error("Effects() in PWM mode must return nil")
	}
	// RGB mode → has effects.
	r.recordMode("RGB")
	effs := r.Effects()
	if len(effs) == 0 {
		t.Error("Effects() in RGB mode must return non-nil slice")
	}
}

// TestRGBWLightSetEffectBranches exercises SetEffect error paths.
func TestRGBWLightSetEffectBranches(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)

	// Mode without effects → error.
	r.recordMode("PWM")
	if err := r.SetEffect(context.Background(), "BLINKING_SLOW", hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetEffect in PWM mode must return an error")
	}

	// RGB mode with unknown label → error.
	r.recordMode("RGB")
	if err := r.SetEffect(context.Background(), "UNKNOWN_EFFECT_LABEL_XYZ", hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetEffect(unknown label) must return an error")
	}
}

// TestRGBWLightSetColorModeCheck verifies SetColor rejects non-color modes.
func TestRGBWLightSetColorModeCheck(t *testing.T) {
	w := &colorStubWriter{}
	r := newRGBWLightRigCh(t, "RGBW:1", w, 1)
	r.recordMode("TUNABLE_WHITE")
	if err := r.SetColor(context.Background(), 120, 1.0, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetColor in TUNABLE_WHITE mode must return an error")
	}
}

// ─── SoundPlayerLED: uncovered branches ─────────────────────────────────────

// TestSoundPlayerLEDAvailableOnTimesAndRepetitions verifies the accessor methods.
func TestSoundPlayerLEDAvailableOnTimesAndRepetitions(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	// Add ON_TIME_LIST_1 and REPETITIONS sensors.
	ch.Put(generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "LED0001:6",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterOnTimeList1),
		},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeString,
			ValueList: []string{"100MS", "500MS", "PERMANENTLY_ON"},
		},
	}))
	ch.Put(generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "LED0001:6",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterRepetitions),
		},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeString,
			ValueList: []string{"NO_REPETITION", "REPETITIONS_001", "INFINITE_REPETITIONS"},
		},
	}))
	led := NewSoundPlayerLED(Config{Channel: ch, Writer: w})

	times := led.AvailableOnTimes()
	if len(times) != 3 {
		t.Errorf("AvailableOnTimes() len=%d, want 3", len(times))
	}
	reps := led.AvailableRepetitions()
	if len(reps) != 3 {
		t.Errorf("AvailableRepetitions() len=%d, want 3", len(reps))
	}
}

// TestSoundPlayerLEDTurnOffDispatchesPutParamset verifies TurnOff bundles
// COLOR=BLACK + ON_TIME=0 into one atomic call.
func TestSoundPlayerLEDTurnOffDispatchesPutParamset(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	fc := NewFixedColorLight(Config{Channel: ch, Writer: w})
	led := &SoundPlayerLED{FixedColorLight: fc}

	if err := led.TurnOff(context.Background(), w, "LED0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff error: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected at least one put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	if params[string(hmenum.ParameterColor)] != "BLACK" {
		t.Errorf("COLOR=%v, want BLACK", params[string(hmenum.ParameterColor)])
	}
	if params[string(hmenum.ParameterOnTime)] != 0.0 {
		t.Errorf("ON_TIME=%v, want 0", params[string(hmenum.ParameterOnTime)])
	}
}

// TestSoundPlayerLEDTurnOffNilWriter verifies TurnOff errors on nil writer.
func TestSoundPlayerLEDTurnOffNilWriter(t *testing.T) {
	led := &SoundPlayerLED{}
	if err := led.TurnOff(context.Background(), nil, "X:1", hmenum.CommandPriorityHigh); err == nil {
		t.Error("TurnOff(nil writer) must return an error")
	}
}

// TestSoundPlayerLEDCurrentColorOrWhiteFallbacks verifies currentColorOrWhite.
func TestSoundPlayerLEDCurrentColorOrWhiteFallbacks(t *testing.T) {
	// nil FixedColorLight → FixedColorWhite.
	led := &SoundPlayerLED{}
	if got := led.currentColorOrWhite(); got != FixedColorWhite {
		t.Errorf("currentColorOrWhite(nil) = %d, want %d (WHITE)", got, FixedColorWhite)
	}

	// FixedColorLight with unobserved COLOR → FixedColorWhite.
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "LED0001:6", hmenum.ParameterColor, w, fixedColorValueList)
	fc := NewFixedColorLight(Config{Channel: ch, Writer: w})
	led2 := &SoundPlayerLED{FixedColorLight: fc}
	if got := led2.currentColorOrWhite(); got != FixedColorWhite {
		t.Errorf("currentColorOrWhite(unobserved) = %d, want %d (WHITE)", got, FixedColorWhite)
	}
}

// TestSoundPlayerLEDFlashBranches verifies Flash dispatches and validates indices.
func TestSoundPlayerLEDFlashBranches(t *testing.T) {
	w := &multiWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	putWritableSelect(ch, "LED0001:6", hmenum.ParameterColor, w, fixedColorValueList)

	led := &SoundPlayerLED{
		FixedColorLight:      NewFixedColorLight(Config{Channel: ch, Writer: w}),
		availableOnTimes:     []string{"100MS", "500MS"},
		availableRepetitions: []string{"NO_REPETITION", "REPETITIONS_001"},
	}

	// Out-of-range on-time index.
	if err := led.Flash(context.Background(), FixedColorRed, 5, 0, w, "LED0001:6", hmenum.CommandPriorityHigh); err == nil {
		t.Error("Flash with out-of-range onTimeIdx must return an error")
	}
	// Negative on-time index.
	if err := led.Flash(context.Background(), FixedColorRed, -1, 0, w, "LED0001:6", hmenum.CommandPriorityHigh); err == nil {
		t.Error("Flash with negative onTimeIdx must return an error")
	}
	// Out-of-range rep index.
	if err := led.Flash(context.Background(), FixedColorRed, 0, 5, w, "LED0001:6", hmenum.CommandPriorityHigh); err == nil {
		t.Error("Flash with out-of-range repIdx must return an error")
	}
	// Nil writer.
	if err := led.Flash(context.Background(), FixedColorRed, 0, 0, nil, "LED0001:6", hmenum.CommandPriorityHigh); err == nil {
		t.Error("Flash with nil writer must return an error")
	}
	// Valid call.
	if err := led.Flash(context.Background(), FixedColorRed, 0, 0, w, "LED0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Flash valid: %v", err)
	}
}

// TestSoundPlayerLEDTurnOnWithHSColor verifies TurnOn resolves hs_color.
func TestSoundPlayerLEDTurnOnWithHSColor(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	fc := NewFixedColorLight(Config{Channel: ch, Writer: w})
	led := &SoundPlayerLED{FixedColorLight: fc}

	hs := [2]float64{0, 1.0} // RED
	if err := led.TurnOn(context.Background(), LedOnConfig{
		Brightness:  200,
		HSColor:     &hs,
		FlashTimeMS: 200,
		Repetitions: 2,
	}, w, "LED0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn with HSColor: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	if params[string(hmenum.ParameterColor)] != "RED" {
		t.Errorf("COLOR=%v, want RED", params[string(hmenum.ParameterColor)])
	}
}

// TestSoundPlayerLEDTurnOnWithDeferredTimer verifies the deferred timer path.
func TestSoundPlayerLEDTurnOnWithDeferredTimer(t *testing.T) {
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "LED0001:6", hmenum.ParameterLevel, w)
	fc := NewFixedColorLight(Config{Channel: ch, Writer: w})
	led := &SoundPlayerLED{FixedColorLight: fc}
	// Set a deferred timer.
	led.SetTimerOnTime(3 * time.Second)

	if err := led.TurnOn(context.Background(), LedOnConfig{}, w, "LED0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn with deferred timer: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	if params[string(hmenum.ParameterOnTime)] == nil {
		t.Error("ON_TIME must be present when deferred timer was set")
	}
}

// TestSoundPlayerLEDTurnOnNilWriter verifies TurnOn errors on nil writer.
func TestSoundPlayerLEDTurnOnNilWriter(t *testing.T) {
	led := &SoundPlayerLED{}
	if err := led.TurnOn(context.Background(), LedOnConfig{}, nil, "X:1", hmenum.CommandPriorityHigh); err == nil {
		t.Error("TurnOn(nil writer) must return an error")
	}
}

// ─── topology.go: uncovered branches ────────────────────────────────────────

// TestLightTopicSlotNoSplit verifies TopicSlot handles an address without a
// colon (SplitChannelAddress returns ok=false).
func TestLightTopicSlotNoSplit(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "FLAT"})
	ch := d.AddChannel("FLAT", 1, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, "FLAT", hmenum.ParameterLevel, &stubWriter{})
	l := New(Config{Channel: ch, Writer: &stubWriter{}, Capabilities: custom.LightCapabilities{}})
	slot := l.TopicSlot()
	if slot.Parameter != "light" {
		t.Errorf("TopicSlot().Parameter = %q, want %q", slot.Parameter, "light")
	}
}

// ─── matter.go: uncovered branches ──────────────────────────────────────────

// TestLightMatterDeviceType verifies MatterDeviceType returns the correct ID.
func TestLightMatterDeviceType(t *testing.T) {
	w := &stubWriter{}
	lDim, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if got := lDim.MatterDeviceType(); got != matterDeviceTypeDimmableLight {
		t.Errorf("MatterDeviceType(dimmable) = 0x%04X, want 0x%04X", got, matterDeviceTypeDimmableLight)
	}

	lNonDim, _ := newLightRig(t, "HmIP-SW:1", w, custom.LightCapabilities{})
	if got := lNonDim.MatterDeviceType(); got != matterDeviceTypeOnOffLight {
		t.Errorf("MatterDeviceType(non-dimmable) = 0x%04X, want 0x%04X", got, matterDeviceTypeOnOffLight)
	}
}

// TestLightMatterClusterServers verifies MatterClusterServers length.
func TestLightMatterClusterServers(t *testing.T) {
	w := &stubWriter{}
	lDim, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	servers := lDim.MatterClusterServers()
	if len(servers) != 4 { // OnOff + LevelControl + Groups + ScenesManagement
		t.Errorf("MatterClusterServers(dimmable) len=%d, want 4", len(servers))
	}

	lNonDim, _ := newLightRig(t, "HmIP-SW:1", w, custom.LightCapabilities{})
	servers = lNonDim.MatterClusterServers()
	if len(servers) != 3 { // OnOff + Groups + ScenesManagement
		t.Errorf("MatterClusterServers(non-dimmable) len=%d, want 3", len(servers))
	}
}

// TestLightOnOffServerRead verifies lightOnOffServer.MatterRead for all attributes.
func TestLightOnOffServerRead(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	s := lightOnOffServer{l: l}

	// Unobserved → (false, true). Apple Home's HAP-mapper aborts on
	// nil OnOff during HAP-service rebuild, so the unobserved path
	// must surface the boolean default rather than a TLV null.
	v, ok := s.MatterRead(matterAttrOnOffOnOff)
	if !ok || v != false {
		t.Errorf("MatterRead(OnOff) unobserved = (%v, %v), want (false, true)", v, ok)
	}

	// Observed off.
	level.OnEvent(0)
	v, ok = s.MatterRead(matterAttrOnOffOnOff)
	if !ok || v.(bool) != false {
		t.Errorf("MatterRead(OnOff) off = (%v, %v), want (false, true)", v, ok)
	}

	// Observed on.
	level.OnEvent(0.5)
	v, ok = s.MatterRead(matterAttrOnOffOnOff)
	if !ok || v.(bool) != true {
		t.Errorf("MatterRead(OnOff) on = (%v, %v), want (true, true)", v, ok)
	}

	// OnOff cluster has no Options attribute per matter.js HEAD; reads
	// of 0x000F must surface ok=false (UnsupportedAttribute).
	if _, ok := s.MatterRead(0x000F); ok {
		t.Error("MatterRead(0x000F) on lightOnOffServer must return ok=false")
	}

	// FeatureMap.
	v, ok = s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Errorf("MatterRead(FeatureMap) = (%v, %v), want (0, true)", v, ok)
	}

	// ClusterRevision.
	v, ok = s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Errorf("MatterRead(ClusterRevision) = (%v, %v)", v, ok)
	}

	// Unknown attribute → (nil, false).
	v, ok = s.MatterRead(0xFFFF)
	if ok || v != nil {
		t.Errorf("MatterRead(unknown) = (%v, %v), want (nil, false)", v, ok)
	}
}

// TestLightOnOffServerWrite verifies lightOnOffServer.MatterWrite.
func TestLightOnOffServerWrite(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	s := lightOnOffServer{l: l}

	// Write true → TurnOn.
	if err := s.MatterWrite(context.Background(), matterAttrOnOffOnOff, true, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterWrite(true): %v", err)
	}
	// Write false → TurnOff.
	if err := s.MatterWrite(context.Background(), matterAttrOnOffOnOff, false, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterWrite(false): %v", err)
	}
	// Unknown attribute.
	if err := s.MatterWrite(context.Background(), 0xFFFF, true, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite(unknown attr) must return error")
	}
	// Wrong type.
	if err := s.MatterWrite(context.Background(), matterAttrOnOffOnOff, "bad", hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite(wrong type) must return error")
	}
}

// TestLightOnOffServerInvoke verifies lightOnOffServer.MatterInvoke.
func TestLightOnOffServerInvoke(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	s := lightOnOffServer{l: l}

	// Off command.
	if _, err := s.MatterInvoke(context.Background(), matterCmdOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(Off): %v", err)
	}
	// On command.
	if _, err := s.MatterInvoke(context.Background(), matterCmdOn, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(On): %v", err)
	}
	// Toggle when off → TurnOn.
	level.OnEvent(0)
	if _, err := s.MatterInvoke(context.Background(), matterCmdToggle, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(Toggle/off): %v", err)
	}
	// Toggle when on → TurnOff.
	level.OnEvent(0.5)
	if _, err := s.MatterInvoke(context.Background(), matterCmdToggle, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(Toggle/on): %v", err)
	}
	// Unknown command.
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterInvoke(unknown cmd) must return error")
	}
}

// TestLightOnOffServerMatterReportableAndAttributes verifies the list methods.
func TestLightOnOffServerMatterReportableAndAttributes(t *testing.T) {
	s := lightOnOffServer{l: &Light{}}
	if r := s.MatterReportable(); len(r) == 0 {
		t.Error("MatterReportable must return at least one attribute")
	}
	if a := s.MatterAttributes(); len(a) == 0 {
		t.Error("MatterAttributes must return at least one attribute")
	}
}

// TestLightLevelServerRead verifies lightLevelServer.MatterRead.
func TestLightLevelServerRead(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	s := lightLevelServer{l: l}

	// Unobserved.
	v, ok := s.MatterRead(matterAttrLevelCurrent)
	if !ok || v != nil {
		t.Errorf("MatterRead(CurrentLevel) unobserved = (%v, %v), want (nil, true)", v, ok)
	}

	// Observed.
	level.OnEvent(0.5)
	v, ok = s.MatterRead(matterAttrLevelCurrent)
	if !ok || v == nil {
		t.Errorf("MatterRead(CurrentLevel) observed = (%v, %v), want (uint8, true)", v, ok)
	}

	// Options.
	v, ok = s.MatterRead(matterAttrLevelOptions)
	if !ok || v.(uint8) != 0 {
		t.Errorf("MatterRead(Options) = (%v, %v), want (0, true)", v, ok)
	}

	// OnLevel → nil (nullable).
	v, ok = s.MatterRead(matterAttrLevelOnLevel)
	if !ok {
		t.Errorf("MatterRead(OnLevel): ok=false")
	}
	if v != nil {
		t.Errorf("MatterRead(OnLevel) = %v, want nil", v)
	}

	// FeatureMap.
	_, ok = s.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Errorf("MatterRead(FeatureMap): ok=false")
	}

	// ClusterRevision.
	_, ok = s.MatterRead(matterAttrClusterRevision)
	if !ok {
		t.Errorf("MatterRead(ClusterRevision): ok=false")
	}

	// Unknown.
	_, ok = s.MatterRead(0xFFFF)
	if ok {
		t.Error("MatterRead(unknown) must return ok=false")
	}
}

// TestLightLevelServerWriteAndInvoke verifies lightLevelServer write and invoke paths.
func TestLightLevelServerWriteAndInvoke(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	s := lightLevelServer{l: l}

	// Write uint8.
	if err := s.MatterWrite(context.Background(), matterAttrLevelCurrent, uint8(127), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterWrite(CurrentLevel): %v", err)
	}
	// Wrong attr.
	if err := s.MatterWrite(context.Background(), 0xFFFF, uint8(127), hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite(unknown attr) must return error")
	}
	// Wrong type.
	if err := s.MatterWrite(context.Background(), matterAttrLevelCurrent, "bad", hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite(wrong type) must return error")
	}

	// Invoke MoveToLevel bare uint8.
	if _, err := s.MatterInvoke(context.Background(), matterCmdMoveToLevel, uint8(100), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToLevel/uint8): %v", err)
	}
	// Invoke MoveToLevel map.
	if _, err := s.MatterInvoke(context.Background(), matterCmdMoveToLevelWithOnOff, map[string]any{"level": uint8(50)}, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("MatterInvoke(MoveToLevelWithOnOff/map): %v", err)
	}
	// Invoke unknown command.
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterInvoke(unknown cmd) must return error")
	}
}

// TestExtractMoveToLevelBranches verifies all extractMoveToLevel branches.
func TestExtractMoveToLevelBranches(t *testing.T) {
	// Bare uint8.
	v, err := extractMoveToLevel(uint8(42))
	if err != nil || v != 42 {
		t.Errorf("extractMoveToLevel(uint8) = (%d, %v), want (42, nil)", v, err)
	}

	// Map with level.
	v, err = extractMoveToLevel(map[string]any{"level": uint8(10)})
	if err != nil || v != 10 {
		t.Errorf("extractMoveToLevel(map{level:10}) = (%d, %v), want (10, nil)", v, err)
	}

	// Map missing level key.
	_, err = extractMoveToLevel(map[string]any{})
	if err == nil {
		t.Error("extractMoveToLevel(map{}) must return error")
	}

	// Map with wrong type for level.
	_, err = extractMoveToLevel(map[string]any{"level": "bad"})
	if err == nil {
		t.Error("extractMoveToLevel(map{level:string}) must return error")
	}

	// Unsupported type.
	_, err = extractMoveToLevel("not-a-valid-type")
	if err == nil {
		t.Error("extractMoveToLevel(string) must return error")
	}
}

// TestLightLevelServerMatterReportableAndAttributes verifies list methods.
func TestLightLevelServerMatterReportableAndAttributes(t *testing.T) {
	s := lightLevelServer{l: &Light{}}
	if r := s.MatterReportable(); len(r) == 0 {
		t.Error("MatterReportable must return at least one attribute")
	}
	if a := s.MatterAttributes(); len(a) == 0 {
		t.Error("MatterAttributes must return at least one attribute")
	}
}

// TestBrightnessToMatterBranches verifies the clamping in brightnessToMatter.
func TestBrightnessToMatterBranches(t *testing.T) {
	// Normal value.
	b := custom.NewBrightness(0.5)
	got := brightnessToMatter(b)
	if got == 0 || got > matterLevelMax {
		t.Errorf("brightnessToMatter(0.5) = %d, want in (0, %d]", got, matterLevelMax)
	}

	// Full brightness → matterLevelMax.
	bFull := custom.NewBrightness(1.0)
	if got := brightnessToMatter(bFull); got != matterLevelMax {
		t.Errorf("brightnessToMatter(1.0) = %d, want %d", got, matterLevelMax)
	}
}

// TestMatterLevelToHMBranches verifies matterLevelToHM saturation.
func TestMatterLevelToHMBranches(t *testing.T) {
	if got := matterLevelToHM(matterLevelMax); got != 1.0 {
		t.Errorf("matterLevelToHM(matterLevelMax) = %.4f, want 1.0", got)
	}
	if got := matterLevelToHM(0); got != 0.0 {
		t.Errorf("matterLevelToHM(0) = %.4f, want 0.0", got)
	}
	mid := matterLevelToHM(matterLevelMax / 2)
	if mid <= 0 || mid >= 1 {
		t.Errorf("matterLevelToHM(mid) = %.4f, expected in (0, 1)", mid)
	}
}
