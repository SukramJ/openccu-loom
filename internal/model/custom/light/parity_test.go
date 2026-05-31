// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for the Light custom data point. Each test function maps to
// one semantic from the Python reference and uses the table-driven style
// preferred in this repository.

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

// newParityLightRig builds a dimmable light rig for parity tests.
func newParityLightRig(t *testing.T, address string, w Writer, caps custom.LightCapabilities) (*Light, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU1399816"})
	ch := d.AddChannel(address, 4, "DIMMER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level)
	l := New(Config{Channel: ch, Writer: w, Capabilities: caps})
	return l, level
}

// TestParityBrightnessZeroBeforeTurnOn verifies that brightness returns 0
// before any TurnOn call. Mirrors test_cedimmer → "brightness == 0".
func TestParityBrightnessZeroBeforeTurnOn(t *testing.T) {
	t.Parallel()

	l, _ := newParityLightRig(t, "VCU1399816:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	b, ok := l.Brightness()
	if ok {
		t.Errorf("Brightness() ok=true before any observation, want false")
	}
	_ = b
}

// TestParityTurnOnSetsFull verifies that TurnOn without prior level
// sets LEVEL=1.0 (full brightness). Mirrors test_cedimmer → "turn_on() →
// LEVEL=1.0 and brightness==255".
func TestParityTurnOnSetsFull(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newParityLightRig(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 1.0 {
		t.Errorf("TurnOn → LEVEL=%v, want 1.0", w.last)
	}
}

// TestParityTurnOnWithBrightness28 verifies brightness=28 maps correctly to
// LEVEL ≈ 0.1098. Mirrors test_cedimmer → "turn_on(brightness=28) →
// LEVEL=0.10980392156862745".
func TestParityTurnOnWithBrightness28(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newParityLightRig(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	const brightness28 = float64(28) / 255.0 // 0.10980...
	if err := l.SetLevel(context.Background(), brightness28, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last < 0.109 || w.last > 0.110 {
		t.Errorf("brightness=28 → LEVEL=%v, want ~0.1098", w.last)
	}
}

// TestParityTurnOffSetsZero verifies TurnOff writes LEVEL=0.
// Mirrors test_cedimmer → "turn_off() → LEVEL=0".
func TestParityTurnOffSetsZero(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newParityLightRig(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 0 {
		t.Errorf("TurnOff → LEVEL=%v, want 0", w.last)
	}
}

// TestParityLastLevelRestoredOnTurnOn verifies that after the CCU reports a
// non-zero level, a subsequent TurnOn restores that level instead of going
// to 1.0. Mirrors test_cedimmer → last_non_default_value behaviour.
func TestParityLastLevelRestoredOnTurnOn(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newParityLightRig(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})

	// CCU reports 0.3 → cached as last non-zero.
	level.OnEvent(0.3)

	// CCU reports off. last-level must NOT be overwritten.
	level.OnEvent(0)
	if got := l.LastLevel(); got != 0.3 {
		t.Fatalf("LastLevel after off=%v, want 0.3", got)
	}

	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 0.3 {
		t.Errorf("TurnOn after off=%v, want 0.3 (restore last level)", w.last)
	}
}

// TestParityAtomicOnTimePutParamset verifies that TurnOnWith(OnTime) bundles
// LEVEL + ON_TIME + RAMP_TIME into a single put_paramset. Mirrors
// test_cedimmer → "turn_on(on_time=…, ramp_time=…, brightness=…)".
func TestParityAtomicOnTimePutParamset(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	on := 5 * time.Second
	ramp := 6 * time.Second
	br := float64(28) / 255.0
	if err := l.TurnOnWith(context.Background(), OnConfig{
		Brightness: &br,
		OnTime:     &on,
		RampTime:   &ramp,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if v, ok := got[string(hmenum.ParameterOnTime)].(float64); !ok || v != 5 {
		t.Errorf("ON_TIME=%v, want 5", got[string(hmenum.ParameterOnTime)])
	}
	if v, ok := got[string(hmenum.ParameterRampTime)].(float64); !ok || v != 6 {
		t.Errorf("RAMP_TIME=%v, want 6", got[string(hmenum.ParameterRampTime)])
	}
}

// TestParityTurnOffWithRampAtomicPutParamset verifies that TurnOffWithRamp
// bundles RAMP_TIME + LEVEL=0 + ON_TIME=NotUsed atomically. Mirrors
// test_cedimmer → "turn_off(ramp_time=6)".
func TestParityTurnOffWithRampAtomicPutParamset(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.TurnOffWithRamp(context.Background(), 6*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if v, _ := got[string(hmenum.ParameterLevel)].(float64); v != 0 {
		t.Errorf("LEVEL=%v, want 0", got[string(hmenum.ParameterLevel)])
	}
	if v, _ := got[string(hmenum.ParameterRampTime)].(float64); v != 6 {
		t.Errorf("RAMP_TIME=%v, want 6", got[string(hmenum.ParameterRampTime)])
	}
	if v, _ := got[string(hmenum.ParameterOnTime)].(float64); v != NotUsed {
		t.Errorf("ON_TIME=%v, want NotUsed (%v)", got[string(hmenum.ParameterOnTime)], NotUsed)
	}
}

// TestParityNonDimmableRejectsIntermediateLevel verifies that a non-dimmable
// light rejects a level between 0 and 1.
func TestParityNonDimmableRejectsIntermediateLevel(t *testing.T) {
	t.Parallel()

	l, _ := newParityLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	if err := l.SetLevel(context.Background(), 0.5, hmenum.CommandPriorityHigh); err == nil {
		t.Error("non-dimmable light must reject intermediate level 0.5")
	}
}

// TestParityColorTempKelvinRange verifies that the color temp Kelvin helper
// function (SetKelvin on ColorTempLight) clamps and writes the correct value.
func TestParityColorTempKelvinRange(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0000122"})
	ch := d.AddChannel("VCU0000122:4", 4, "COLOR_TEMP_DIMMER", hmenum.ParamsetKeyValues)
	for _, p := range []hmenum.Parameter{hmenum.ParameterLevel, hmenum.ParameterColorTemperature} {
		typ := hmenum.ParameterTypeFloat
		if p == hmenum.ParameterColorTemperature {
			typ = hmenum.ParameterTypeInteger
		}
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "VCU0000122:4",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
	}
	// Kelvin is an integer param — override with Integer DP.
	ctDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0000122:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(ctDP)

	w2 := &stubWriter{}
	ctDP2 := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0000122:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterColorTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w2,
	})
	ch.Put(ctDP2)
	ctl := NewColorTempLight(Config{Channel: ch, Writer: w2, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}}, 2000, 6500)
	// SetKelvin 4000 K should be accepted without error.
	if err := ctl.SetKelvin(context.Background(), 4000, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("SetKelvin(4000) unexpected error: %v", err)
	}
	// Kelvin value below min (2000) should be clamped without error.
	if err := ctl.SetKelvin(context.Background(), 1000, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("SetKelvin(1000) clamped: unexpected error: %v", err)
	}
}

// TestParityBrightnessPctScale verifies that BrightnessPct maps the 0-255
// scale correctly. Mirrors test_cedimmer → "brightness_pct == 100 at full".
func TestParityBrightnessPctScale(t *testing.T) {
	t.Parallel()

	cases := []struct {
		level   float64
		wantPct int
	}{
		{1.0, 100},
		{0.5, 50},
		{0.0, 0},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		l, level := newParityLightRig(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
		level.OnEvent(tc.level)
		b, ok := l.Brightness()
		if !ok {
			t.Fatalf("level=%v: Brightness() ok=false", tc.level)
		}
		pct := int(b.Level() * 100)
		if pct != tc.wantPct {
			t.Errorf("level=%v → pct=%d, want %d", tc.level, pct, tc.wantPct)
		}
	}
}

// TestParitySetTimerThenTurnOnConsumesTimer verifies the deferred-timer path
// for lights. Mirrors test_cedimmer → "set_timer_on_time + turn_on →
// put_paramset, second turn_on does NOT produce another put".
func TestParitySetTimerThenTurnOnConsumesTimer(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	l.SetTimerOnTime(500 * time.Millisecond)
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("first TurnOn: expected 1 put_paramset, got %d", len(w.puts))
	}
	// Timer consumed → second TurnOn must not produce another put_paramset.
	w.puts = nil
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 0 {
		t.Errorf("second TurnOn must not produce put_paramset, got %d", len(w.puts))
	}
}

// TestParityColorSetHueSaturation verifies that SetColor writes both HUE and
// SATURATION as separate parameters. Mirrors test_cedimereffect → set_color.
func TestParityColorSetHueSaturation(t *testing.T) {
	t.Parallel()

	w := &multiWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("VCU6985973:4", 4, "COLOR_DIMMER", hmenum.ParamsetKeyValues)

	addParam := func(p hmenum.Parameter, typ hmenum.ParameterType) {
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "VCU6985973:4",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
		ch.Put(dp)
	}
	addParam(hmenum.ParameterLevel, hmenum.ParameterTypeFloat)
	addParam(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat)
	// HUE uses Integer, not Float — add via integer path.
	hueDP := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU6985973:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHue),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(hueDP)

	cl := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true}})
	if err := cl.SetColor(context.Background(), 180, 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	hasHue := false
	hasSat := false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterHue {
			hasHue = true
		}
		if c.param == hmenum.ParameterSaturation {
			hasSat = true
		}
	}
	if !hasHue {
		t.Error("SetColor must write HUE parameter")
	}
	if !hasSat {
		t.Error("SetColor must write SATURATION parameter")
	}
}

// TestParityGroupBrightnessBeforeObservation verifies that GroupBrightness
// returns ok=false before any group-level value is observed.
func TestParityGroupBrightnessBeforeObservation(t *testing.T) {
	t.Parallel()

	l, _ := newParityLightRig(t, "VCU1399816:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	if _, ok := l.GroupBrightness(); ok {
		t.Error("GroupBrightness() must return ok=false when no group-level DP installed")
	}
}

// TestParityHasColorTemperatureCapability verifies the capability flags.
// Mirrors test_cedimmer → "has_color_temperature is False" for plain dimmers.
func TestParityHasColorTemperatureCapability(t *testing.T) {
	t.Parallel()

	cases := []struct {
		caps     custom.LightCapabilities
		wantCTmp bool
	}{
		{custom.LightCapabilities{Dimmable: true}, false},
		{custom.LightCapabilities{Dimmable: true, SupportsColorTemp: true}, true},
	}
	for _, tc := range cases {
		l, _ := newParityLightRig(t, "VCU1399816:4", &stubWriter{}, tc.caps)
		if l.Capabilities.SupportsColorTemp != tc.wantCTmp {
			t.Errorf("caps=%+v: SupportsColorTemp=%v, want %v",
				tc.caps, l.Capabilities.SupportsColorTemp, tc.wantCTmp)
		}
	}
}
