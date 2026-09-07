// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newMP3PLEDChannel builds the HmIP-MP3P channel-6 wire shape
// (godevccu paramset_descriptions/HmIP-MP3P.json, VCU1543608:6): the timer
// parameters are DURATION_VALUE/DURATION_UNIT and
// RAMP_TIME_VALUE/RAMP_TIME_UNIT; the channel carries neither ON_TIME nor
// RAMP_TIME. HmIP-BSL:8, HmIP-RGBW:1 and HmIP-DRG-DALI:1 share the shape.
func newMP3PLEDChannel(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel(address, 6, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableSelect(ch, address, hmenum.ParameterColor, w, []string{
		"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE",
	})
	putWritableInteger(ch, address, hmenum.ParameterDurationValue, w)
	putWritableInteger(ch, address, hmenum.ParameterDurationUnit, w)
	putWritableInteger(ch, address, hmenum.ParameterRampTimeValue, w)
	putWritableInteger(ch, address, hmenum.ParameterRampTimeUnit, w)
	return ch
}

func assertNoBareTimerKeys(t *testing.T, params map[string]any) {
	t.Helper()
	for _, k := range []hmenum.Parameter{hmenum.ParameterOnTime, hmenum.ParameterRampTime} {
		if _, bare := params[string(k)]; bare {
			t.Errorf("put_paramset carries bare %s, which the channel does not declare: %v", k, params)
		}
	}
}

func assertTimerPair(t *testing.T, params map[string]any, valueParam, unitParam hmenum.Parameter, wantValue, wantUnit int32) {
	t.Helper()
	if v, ok := params[string(valueParam)].(int32); !ok || v != wantValue {
		t.Errorf("%s=%v (%T), want %d", valueParam, params[string(valueParam)], params[string(valueParam)], wantValue)
	}
	if u, ok := params[string(unitParam)].(int32); !ok || u != wantUnit {
		t.Errorf("%s=%v (%T), want %d", unitParam, params[string(unitParam)], params[string(unitParam)], wantUnit)
	}
}

// TestSoundPlayerLEDTurnOnUsesDurationAndRampTimePairs mirrors
// CustomDpSoundPlayerLed (light.py): on_time goes out as
// DURATION_VALUE/DURATION_UNIT and ramp_time as RAMP_TIME_VALUE/RAMP_TIME_UNIT.
func TestSoundPlayerLEDTurnOnUsesDurationAndRampTimePairs(t *testing.T) {
	w := &putWriter{}
	ch := newMP3PLEDChannel(t, "MP3P0001:6", w)
	led := NewSoundPlayerLED(Config{Channel: ch, Writer: w})

	if err := led.TurnOn(context.Background(), LedOnConfig{OnTime: 5, RampTime: 6}, w, "MP3P0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	assertNoBareTimerKeys(t, params)
	assertTimerPair(t, params, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 5, int32(hmenum.TimerUnitSeconds))
	assertTimerPair(t, params, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 6, int32(hmenum.TimerUnitSeconds))
}

// TestSoundPlayerLEDTurnOffUsesDurationPair mirrors turn_off in
// CustomDpSoundPlayerLed: the timer reset is DURATION_VALUE=0 with the
// seconds unit, never a bare ON_TIME.
func TestSoundPlayerLEDTurnOffUsesDurationPair(t *testing.T) {
	w := &putWriter{}
	ch := newMP3PLEDChannel(t, "MP3P0001:6", w)
	led := NewSoundPlayerLED(Config{Channel: ch, Writer: w})

	if err := led.TurnOff(context.Background(), w, "MP3P0001:6", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff: %v", err)
	}
	params := w.puts[len(w.puts)-1]
	assertNoBareTimerKeys(t, params)
	if params[string(hmenum.ParameterColor)] != "BLACK" {
		t.Errorf("COLOR=%v, want BLACK", params[string(hmenum.ParameterColor)])
	}
	assertTimerPair(t, params, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 0, int32(hmenum.TimerUnitSeconds))
}

// TestLightTurnOnWithRampUsesRampTimePairOnValueUnitChannel covers the
// Matter transition path (setLevelRamped → TurnOnWith) on a channel whose
// ramp is RAMP_TIME_VALUE/RAMP_TIME_UNIT: the ramp must take that shape and
// the on-time sentinel the DURATION pair.
func TestLightTurnOnWithRampUsesRampTimePairOnValueUnitChannel(t *testing.T) {
	w := &putWriter{}
	ch := newMP3PLEDChannel(t, "MP3P0001:6", w)
	l := New(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, Transition: true}})

	target, ramp := 0.5, 3*time.Second
	if err := l.TurnOnWith(context.Background(), OnConfig{Brightness: &target, RampTime: &ramp}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWith: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	assertNoBareTimerKeys(t, params)
	assertTimerPair(t, params, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 3, int32(hmenum.TimerUnitSeconds))
	sentinelValue, sentinelUnit := custom.EncodeTimerDuration(time.Duration(custom.TimerNotUsed * float64(time.Second)))
	assertTimerPair(t, params, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, sentinelValue, sentinelUnit)
}

// TestLightTurnOffWithRampUsesRampTimePairOnValueUnitChannel covers the off
// direction of the same Matter path.
func TestLightTurnOffWithRampUsesRampTimePairOnValueUnitChannel(t *testing.T) {
	w := &putWriter{}
	ch := newMP3PLEDChannel(t, "MP3P0001:6", w)
	l := New(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, Transition: true}})

	if err := l.TurnOffWithRamp(context.Background(), 3*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOffWithRamp: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected put_paramset")
	}
	params := w.puts[len(w.puts)-1]
	assertNoBareTimerKeys(t, params)
	if params[string(hmenum.ParameterLevel)] != 0.0 {
		t.Errorf("LEVEL=%v, want 0", params[string(hmenum.ParameterLevel)])
	}
	assertTimerPair(t, params, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 3, int32(hmenum.TimerUnitSeconds))
	sentinelValue, sentinelUnit := custom.EncodeTimerDuration(time.Duration(custom.TimerNotUsed * float64(time.Second)))
	assertTimerPair(t, params, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, sentinelValue, sentinelUnit)
}

// TestLightTurnOnWithRampKeepsBareRampTimeOnPlainDimmer is the negative
// control: a plain dimmer channel (bare RAMP_TIME, bare ON_TIME) keeps the
// bare keys.
func TestLightTurnOnWithRampKeepsBareRampTimeOnPlainDimmer(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRig(t, "DIM0001:1", w, custom.LightCapabilities{Dimmable: true, Transition: true})
	target, ramp := 0.5, 3*time.Second
	if err := l.TurnOnWith(context.Background(), OnConfig{Brightness: &target, RampTime: &ramp}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWith: %v", err)
	}
	params := w.puts[len(w.puts)-1]
	if v, _ := params[string(hmenum.ParameterRampTime)].(float64); v != 3 {
		t.Errorf("RAMP_TIME=%v, want 3", params[string(hmenum.ParameterRampTime)])
	}
	if _, pair := params[string(hmenum.ParameterRampTimeValue)]; pair {
		t.Errorf("plain dimmer must not receive RAMP_TIME_VALUE: %v", params)
	}
}
