// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	// resolveOnTimeParams (and hasOnTimeUnit, derived from it) resolve
	// true inside New().
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
// channel :8). FixedColorLight sets resetsOnTimeOnTurnOn=true, so plain
// TurnOn on this rig must emit the NotUsed sentinel via put_paramset.
func newFixedColorLightRigWithOnTimeUnit(t *testing.T, address string, w *putWriter) *FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SIG0001"})
	ch := d.AddChannel(address, 1, "SIGNAL_CHIME", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	putWritableSelect(ch, address, hmenum.ParameterColor, w, []string{
		"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE",
	})
	// DURATION_VALUE/DURATION_UNIT presence makes hasOnTimeUnit=true via
	// resolveOnTimeParams, exactly as it does on a real HmIP-BSL channel.
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
// fix, resetsOnTimeOnTurnOn was unintentionally true for RGBW lights, causing
// the NotUsed sentinel to be included in every plain TurnOn and making the
// device switch off again immediately.
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

// TestFixedColorLightPlainTurnOnSendsNotUsedSentinel verifies that a plain
// TurnOn on a FixedColorLight (signal light) channel that carries ON_TIME_UNIT
// emits a put_paramset with ON_TIME=NotUsed. This preserves the #3111
// behaviour: without the sentinel the old on-time timer remains active and the
// light switches itself off unexpectedly after the previous timer expires.
func TestFixedColorLightPlainTurnOnSendsNotUsedSentinel(t *testing.T) {
	w := &putWriter{}
	fc := newFixedColorLightRigWithOnTimeUnit(t, "SIG0001:1", w)

	if err := fc.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn returned unexpected error: %v", err)
	}

	// Exactly one atomic put_paramset must have been dispatched.
	if len(w.puts) != 1 {
		t.Fatalf("plain TurnOn on FixedColorLight with ON_TIME_UNIT produced %d put_paramset call(s), want 1",
			len(w.puts))
	}

	got := w.puts[0]

	// ON_TIME must be present and equal to the NotUsed sentinel.
	rawOnTime, hasOnTime := got[string(hmenum.ParameterOnTime)]
	if !hasOnTime {
		t.Fatalf("put_paramset missing ON_TIME; payload: %v", got)
	}
	onTime, ok := rawOnTime.(float64)
	if !ok {
		t.Fatalf("ON_TIME is not float64: %T(%v)", rawOnTime, rawOnTime)
	}
	if onTime != NotUsed {
		t.Errorf("ON_TIME=%v, want NotUsed (%v)", onTime, NotUsed)
	}

	// LEVEL must also be present in the same bundle.
	if _, hasLevel := got[string(hmenum.ParameterLevel)]; !hasLevel {
		t.Errorf("put_paramset missing LEVEL; payload: %v", got)
	}
}
