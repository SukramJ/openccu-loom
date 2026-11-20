// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newRGBWLightRigWithOnTimeUnit builds a RGBWLight on a channel that carries
// DURATION_VALUE/DURATION_UNIT — the real HmIP-BSL wire shape for a timed
// on-time (godevccu paramset_descriptions/HmIP-BSL.json, channel :8: LEVEL,
// COLOR, DURATION_VALUE, DURATION_UNIT, RAMP_TIME_VALUE, RAMP_TIME_UNIT; no
// device in the fleet carries ON_TIME_UNIT). This is the same setup that
// previously triggered the regression where plain TurnOn would emit a
// put_paramset with DURATION_VALUE/DURATION_UNIT instead of a bare LEVEL
// SetValue.
func newRGBWLightRigWithOnTimeUnit(t *testing.T, address string, w *putWriter) *RGBWLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGBW0002"})
	ch := d.AddChannel(address, 1, "RGBW", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableInteger(ch, address, hmenum.ParameterHue, w)
	putWritableFloat(ch, address, hmenum.ParameterSaturation, w)
	putWritableInteger(ch, address, hmenum.ParameterColorTemperature, w)
	// DURATION_VALUE/DURATION_UNIT are present on the channel so
	// resolveOnTimeParams resolves the value/unit timer pair inside New().
	putWritableFloat(ch, address, hmenum.ParameterDurationValue, w)
	putWritableInteger(ch, address, hmenum.ParameterDurationUnit, w)
	r := NewRGBWLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})
	return r
}

// newFixedColorLightRigWithOnTimeUnit builds a FixedColorLight on a channel
// that carries DURATION_VALUE/DURATION_UNIT, matching the real HmIP-BSL
// signal-light channel (godevccu paramset_descriptions/HmIP-BSL.json,
// channel :8).
func newFixedColorLightRigWithOnTimeUnit(t *testing.T, address string, w *putWriter) *FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SIG0001"})
	ch := d.AddChannel(address, 1, "SIGNAL_CHIME", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableSelect(ch, address, hmenum.ParameterColor, w, []string{
		"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE",
	})
	// DURATION_VALUE/DURATION_UNIT presence makes resolveOnTimeParams resolve
	// the value/unit timer pair, exactly as it does on a real HmIP-BSL channel.
	putWritableFloat(ch, address, hmenum.ParameterDurationValue, w)
	putWritableInteger(ch, address, hmenum.ParameterDurationUnit, w)
	fc := NewFixedColorLight(Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})
	return fc
}

// TestRGBWLightPlainTurnOnDoesNotSendOnTime verifies that a plain TurnOn (no
// explicit on-time / timer) on a channel that carries ON_TIME_UNIT emits only
// a LEVEL write and does not send any put_paramset containing ON_TIME or
// DURATION parameters. This is the regression guard for bug #3210: before the
// fix, RGBW lights included the TimerNotUsed sentinel in every plain TurnOn,
// which made the device switch off again immediately.
func TestRGBWLightPlainTurnOnDoesNotSendOnTime(t *testing.T) {
	w := &putWriter{}
	r := newRGBWLightRigWithOnTimeUnit(t, "RGBW0002:1", w)

	if err := r.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned unexpected error: %v", err)
	}

	// A plain TurnOn on a non-signal light must NOT produce a put_paramset.
	if len(w.puts) != 0 {
		t.Errorf("plain TurnOn on RGBW with ON_TIME_UNIT produced %d put_paramset call(s), want 0; payload: %v",
			len(w.puts), w.puts)
	}

	// The SetValue path (via LEVEL's own writer) must have been used instead.
	if w.last == 0 {
		t.Error("plain TurnOn on RGBW must write LEVEL via SetValue, but stubWriter.last is 0")
	}

	// Confirm that ON_TIME is absent from every recorded call.
	for _, put := range w.puts {
		if _, hasOnTime := put[string(hmenum.ParameterOnTime)]; hasOnTime {
			t.Errorf("ON_TIME must not appear in plain TurnOn for RGBW, got: %v", put)
		}
	}
}

// TestFixedColorLightPlainTurnOnSendsNoTimer pins a plain switch-on of a
// signal light to LEVEL without a timer. The timer-not-used sentinel on the
// DURATION pair (111600, H) kept an HmIP-WRC6-230 signal channel off, while a
// LEVEL write alone switched it on and also ended a timer still running
// (notes/parity/by_design.md, A2-BD10).
func TestFixedColorLightPlainTurnOnSendsNoTimer(t *testing.T) {
	w := &putWriter{}
	fc := newFixedColorLightRigWithOnTimeUnit(t, "SIG0001:1", w)

	if err := fc.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned unexpected error: %v", err)
	}
	for _, put := range w.puts {
		for _, p := range []hmenum.Parameter{hmenum.ParameterOnTime, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit} {
			if v, has := put[string(p)]; has {
				t.Errorf("plain switch-on sends %s=%v; payload: %v", p, v, put)
			}
		}
	}
	levelInPut := len(w.puts) > 0 && w.puts[len(w.puts)-1][string(hmenum.ParameterLevel)] != nil
	if !levelInPut && w.last != 1.0 {
		t.Errorf("plain switch-on wrote no LEVEL (puts %v, last SetValue %v)", w.puts, w.last)
	}
}
