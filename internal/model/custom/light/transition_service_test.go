// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// transitionRig is a light on a channel that declares both timer pairs with
// 10MS in their unit lists, as the HmIP-BSL, HmIP-RGBW and HmIP-MP3P do.
func transitionRig(t *testing.T) (*Light, *putWriter) {
	t.Helper()
	const addr = "TRN0001:1"
	w := &putWriter{}
	l := New(Config{
		Channel: newTimerPairChannel(t, addr, 1, w, unitsWithTenMilliseconds), Writer: w,
		Capabilities: custom.LightCapabilities{Dimmable: true, Transition: true},
	})
	return l, w
}

// singlePut returns the single put_paramset a command sent: a ramp that
// travels in a second write runs without the LEVEL it is meant to ramp.
func singlePut(t *testing.T, w *putWriter) map[string]any {
	t.Helper()
	if len(w.puts) != 1 {
		t.Fatalf("put_paramset calls = %d (%v), want exactly one", len(w.puts), w.puts)
	}
	return w.puts[0]
}

// assertRampBundle checks one paramset for LEVEL, the ramp pair and an
// on-time pair. The CCU refuses a ramp pair without an on-time pair with
// MISSING_NON_OPTIONAL_PARAMETER (measured on an HmIP-BSL, see
// notes/parity/by_design.md BD-Timer-RoundsToNearestStep).
func assertRampBundle(t *testing.T, put map[string]any, wantLevel float64, wantRamp int32) {
	t.Helper()
	tenMS := unitOrdinal(t, unitsWithTenMilliseconds, "10MS")
	if got, ok := put[string(hmenum.ParameterLevel)].(float64); !ok || got != wantLevel {
		t.Errorf("LEVEL = %v, want %v (put %v)", put[string(hmenum.ParameterLevel)], wantLevel, put)
	}
	if v, _ := put[string(hmenum.ParameterRampTimeValue)].(int32); v != wantRamp {
		t.Errorf("RAMP_TIME_VALUE = %v, want %d (put %v)", put[string(hmenum.ParameterRampTimeValue)], wantRamp, put)
	}
	if u, _ := put[string(hmenum.ParameterRampTimeUnit)].(int32); u != tenMS {
		t.Errorf("RAMP_TIME_UNIT = %v, want %d (10MS) (put %v)", put[string(hmenum.ParameterRampTimeUnit)], tenMS, put)
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit} {
		if _, ok := put[string(p)]; !ok {
			t.Errorf("%s missing beside the ramp (put %v)", p, put)
		}
	}
}

// TestLightServicesAcceptTransition drives the light's service methods with
// Home Assistant's JSON-schema `transition` key, in seconds, through the
// registry every plane invokes.
func TestLightServicesAcceptTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("set_level ON with brightness", func(t *testing.T) {
		t.Parallel()
		l, w := transitionRig(t)
		params := map[string]any{"state": "ON", "brightness": 255.0, "transition": 0.5}
		if err := l.Invoke(ctx, "set_level", params, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("set_level: %v", err)
		}
		assertRampBundle(t, singlePut(t, w), 1.0, 50)
	})

	t.Run("set_level ON with brightness and seconds", func(t *testing.T) {
		t.Parallel()
		// 2.5 s is not a whole second, so the on-time pair takes 10MS too
		// (custom.DurationUnits.Encode).
		l, w := transitionRig(t)
		params := map[string]any{"state": "ON", "brightness": 255.0, "seconds": 2.5, "transition": 0.5}
		if err := l.Invoke(ctx, "set_level", params, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("set_level: %v", err)
		}
		put := singlePut(t, w)
		assertRampBundle(t, put, 1.0, 50)
		tenMS := unitOrdinal(t, unitsWithTenMilliseconds, "10MS")
		if v, _ := put[string(hmenum.ParameterDurationValue)].(int32); v != 250 {
			t.Errorf("DURATION_VALUE = %v, want 250 (put %v)", put[string(hmenum.ParameterDurationValue)], put)
		}
		if u, _ := put[string(hmenum.ParameterDurationUnit)].(int32); u != tenMS {
			t.Errorf("DURATION_UNIT = %v, want %d (put %v)", put[string(hmenum.ParameterDurationUnit)], tenMS, put)
		}
	})

	t.Run("turn_on", func(t *testing.T) {
		t.Parallel()
		l, w := transitionRig(t)
		if err := l.Invoke(ctx, "turn_on", map[string]any{"transition": 0.5}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("turn_on: %v", err)
		}
		assertRampBundle(t, singlePut(t, w), l.turnOnLevel(), 50)
	})

	t.Run("set_level OFF", func(t *testing.T) {
		t.Parallel()
		l, w := transitionRig(t)
		params := map[string]any{"state": "OFF", "transition": 0.5}
		if err := l.Invoke(ctx, "set_level", params, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("set_level OFF: %v", err)
		}
		assertRampBundle(t, singlePut(t, w), 0.0, 50)
	})
}

// TestLightDeclaresTheTransitionItsChannelDescribes pins the capability to
// the channel, not to the profile: a plain dimmer's bare RAMP_TIME and the
// HmIP value/unit pair each declare a transition, whatever the profile said.
func TestLightDeclaresTheTransitionItsChannelDescribes(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	bare, _ := newLightRigPut(t, "BRP0001:1", w, custom.LightCapabilities{Dimmable: true}, hmenum.ParameterRampTime)
	if !bare.Capabilities.Transition {
		t.Error("a channel with a bare RAMP_TIME declares no transition")
	}
	pair := New(Config{
		Channel: newTimerPairChannel(t, "PRP0001:1", 1, w, unitsWithTenMilliseconds), Writer: w,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})
	if !pair.Capabilities.Transition {
		t.Error("a channel with RAMP_TIME_VALUE/RAMP_TIME_UNIT declares no transition")
	}
}

// TestLightServicesRejectInvalidTransition pins the boundary: a transition
// is a number of seconds and not negative.
func TestLightServicesRejectInvalidTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, method string
		params       map[string]any
	}{
		{"turn_on negative", "turn_on", map[string]any{"transition": -1.0}},
		{"set_level not a number", "set_level", map[string]any{"state": "ON", "transition": "slow"}},
		{"set_level OFF negative", "set_level", map[string]any{"state": "OFF", "transition": -0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, w := transitionRig(t)
			err := l.Invoke(context.Background(), tc.method, tc.params, hmenum.CommandPriorityHigh)
			if !errors.Is(err, payload.ErrServiceInvalidParam) {
				t.Fatalf("err = %v, want ErrServiceInvalidParam", err)
			}
			if len(w.puts) != 0 {
				t.Fatalf("a refused command reached the wire: %v", w.puts)
			}
		})
	}
}

// TestLightZeroTransitionIsAnInstantSwitch pins Home Assistant's meaning of
// 0: an instant change, which it sends for `transition: 0`
// (homeassistant/components/light/const.py, VALID_TRANSITION). Refusing it
// would refuse the whole command; it switches without a ramp instead, on a
// light that cannot fade as well.
func TestLightZeroTransitionIsAnInstantSwitch(t *testing.T) {
	t.Parallel()
	for _, canFade := range []bool{true, false} {
		w := &putWriter{}
		var extra []hmenum.Parameter
		if canFade {
			extra = append(extra, hmenum.ParameterRampTime)
		}
		l, _ := newLightRigPut(t, "ZTR0001:1", w, custom.LightCapabilities{Dimmable: true}, extra...)
		if err := l.Invoke(context.Background(), "turn_on", map[string]any{"transition": 0.0}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("canFade=%v: turn_on transition 0: %v", canFade, err)
		}
		if len(w.puts) != 0 || w.last != 1 {
			t.Fatalf("canFade=%v: want a plain LEVEL write of 1, got puts %v, last LEVEL %v", canFade, w.puts, w.last)
		}
	}
}

// TestLightWithoutRampRefusesTransition pins the other half of the
// capability: a channel that describes no ramp parameter neither declares a
// transition nor accepts one, so a command never puts a RAMP_TIME the device
// does not know on the wire.
func TestLightWithoutRampRefusesTransition(t *testing.T) {
	t.Parallel()
	const addr = "NRP0001:1"
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "NRP0001"})
	ch := d.AddChannel(addr, 1, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, addr, hmenum.ParameterLevel, w)
	// The profile asks for a transition; the channel has no ramp to run one.
	l := New(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, Transition: true}})
	if l.Capabilities.Transition {
		t.Fatal("a channel without RAMP_TIME declares a transition")
	}
	err := l.Invoke(context.Background(), "set_level", map[string]any{"state": "ON", "transition": 0.5}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, payload.ErrServiceInvalidParam) {
		t.Fatalf("err = %v, want ErrServiceInvalidParam", err)
	}
	if len(w.puts) != 0 {
		t.Fatalf("a refused command reached the wire: %v", w.puts)
	}
}

// soundLEDTransitionRig is the HmIP-MP3P LED channel: both timer pairs with
// 10MS, plus the COLOR its switch-on writes.
func soundLEDTransitionRig(t *testing.T) (*SoundPlayerLED, *putWriter) {
	t.Helper()
	const addr = "MP3P0002:6"
	w := &putWriter{}
	ch := newTimerPairChannel(t, addr, 6, w, unitsWithTenMilliseconds)
	putWritableSelect(ch, addr, hmenum.ParameterColor, w, fixedColorValueList)
	return NewSoundPlayerLED(Config{Channel: ch, Writer: w}), w
}

// TestSoundPlayerLEDServicesAcceptSecondsAndTransition holds the HmIP-MP3P
// LED, which overrides the light services with its own atomic writes, to the
// keys every other light takes.
func TestSoundPlayerLEDServicesAcceptSecondsAndTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tenMS := unitOrdinal(t, unitsWithTenMilliseconds, "10MS")

	t.Run("set_level ON", func(t *testing.T) {
		t.Parallel()
		led, w := soundLEDTransitionRig(t)
		params := map[string]any{"state": "ON", "brightness": 255.0, "seconds": 2.5, "transition": 0.5}
		if err := led.Invoke(ctx, "set_level", params, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("set_level: %v", err)
		}
		put := singlePut(t, w)
		assertRampBundle(t, put, 1.0, 50)
		if v, _ := put[string(hmenum.ParameterDurationValue)].(int32); v != 250 {
			t.Errorf("DURATION_VALUE = %v, want 250 (put %v)", put[string(hmenum.ParameterDurationValue)], put)
		}
		if u, _ := put[string(hmenum.ParameterDurationUnit)].(int32); u != tenMS {
			t.Errorf("DURATION_UNIT = %v, want %d (put %v)", put[string(hmenum.ParameterDurationUnit)], tenMS, put)
		}
	})

	t.Run("turn_on", func(t *testing.T) {
		t.Parallel()
		led, w := soundLEDTransitionRig(t)
		if err := led.Invoke(ctx, "turn_on", map[string]any{"transition": 0.5}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("turn_on: %v", err)
		}
		assertRampBundle(t, singlePut(t, w), 1.0, 50)
	})

	t.Run("set_level OFF", func(t *testing.T) {
		t.Parallel()
		led, w := soundLEDTransitionRig(t)
		if err := led.Invoke(ctx, "set_level", map[string]any{"state": "OFF", "transition": 0.5}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("set_level OFF: %v", err)
		}
		assertRampBundle(t, singlePut(t, w), 0.0, 50)
	})

	for _, tc := range []struct {
		name, method string
		params       map[string]any
	}{
		{"turn_on negative transition", "turn_on", map[string]any{"transition": -1.0}},
		{"set_level seconds not a number", "set_level", map[string]any{"state": "ON", "seconds": "soon"}},
		{"set_level OFF negative transition", "set_level", map[string]any{"state": "OFF", "transition": -0.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			led, w := soundLEDTransitionRig(t)
			err := led.Invoke(ctx, tc.method, tc.params, hmenum.CommandPriorityHigh)
			if !errors.Is(err, payload.ErrServiceInvalidParam) {
				t.Fatalf("err = %v, want ErrServiceInvalidParam", err)
			}
			if len(w.puts) != 0 {
				t.Fatalf("a refused command reached the wire: %v", w.puts)
			}
		})
	}
}
