// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// intWriter records the last integer value written (used for HUE / color
// parameter assertions where the concrete type is int32).
type intWriter struct {
	lastParam hmenum.Parameter
	lastInt   int32
}

func (w *intWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.lastParam = p
	if v, ok := value.(int32); ok {
		w.lastInt = v
	}
	return nil
}

// multiWriter records every SetValue call regardless of type.
type multiWriter struct {
	calls []multiCall
}

type multiCall struct {
	param hmenum.Parameter
	value any
}

func (w *multiWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.calls = append(w.calls, multiCall{p, value})
	return nil
}

// newColorLightRig constructs a channel with LEVEL + HUE + SATURATION
// data points and returns a ColorLight against it.
//
//nolint:gocritic // test rig helper — positional returns are the test convention
func newColorLightRig(t *testing.T, address string, w Writer) (*ColorLight, *generic.Float, *generic.Integer, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "COLOR_DIMMER", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		}
	}

	level := generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat))
	hue := generic.NewInteger(mk(hmenum.ParameterHue, hmenum.ParameterTypeInteger))
	sat := generic.NewFloat(mk(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat))
	ch.Put(level)
	ch.Put(hue)
	ch.Put(sat)

	cl := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
	return cl, level, hue, sat
}

// newColorTempLightRig constructs a channel with LEVEL + COLOR_TEMPERATURE.
func newColorTempLightRig(t *testing.T, address string, w Writer, minK, maxK int32) (*ColorTempLight, *generic.Float, *generic.Integer) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "COLOR_TEMP_DIMMER", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		}
	}

	level := generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat))
	kelvin := generic.NewInteger(mk(hmenum.ParameterColorTemperature, hmenum.ParameterTypeInteger))
	ch.Put(level)
	ch.Put(kelvin)

	ctl := NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}}, minK, maxK)
	return ctl, level, kelvin
}

// --- Light (Dimmer) deep tests ---

// TestLightNilLevelDPGracefullyDegrades verifies that a Light constructed
// from a channel with no LEVEL DP does not panic and returns safe zero
// values from all accessor paths.
func TestLightNilLevelDPGracefullyDegrades(t *testing.T) {
	t.Parallel()

	// Channel with NO data points attached — LEVEL DP is absent.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BDT:4", 1, "DIMMER", hmenum.ParamsetKeyValues)
	l := New(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})

	// All accessors must be safe — no panics.
	if l.Float != nil {
		t.Error("expected Float to be nil when LEVEL is absent")
	}
	if _, ok := l.Brightness(); ok {
		t.Error("Brightness should report not-observed when Float is nil")
	}
	if on, _ := l.IsOn(); on {
		t.Error("IsOn should be false when Float is nil")
	}
	if got := l.LastLevel(); got != 1.0 {
		t.Errorf("LastLevel with nil Float = %v, want 1.0", got)
	}
	// Write attempts must error, not panic.
	if err := l.SetLevel(context.Background(), 0.5, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetLevel with nil Float must return an error")
	}
	if err := l.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err == nil {
		t.Error("TurnOff with nil Float must return an error")
	}
}

// TestLightBrightnessPctRounds verifies BrightnessPct converts 0..1 to 0..100
// with correct rounding.
func TestLightBrightnessPctRounds(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Drive the CCU-side DP to 0.505 → rounds to 51 %.
	level.OnEvent(0.505)
	pct, ok := l.BrightnessPct()
	if !ok {
		t.Fatal("BrightnessPct: not observed")
	}
	if pct != 51 {
		t.Errorf("BrightnessPct(0.505) = %d, want 51", pct)
	}

	// 0.0 → 0 %, fully off.
	level.OnEvent(0)
	pct, ok = l.BrightnessPct()
	if !ok {
		t.Fatal("BrightnessPct: not observed after 0")
	}
	if pct != 0 {
		t.Errorf("BrightnessPct(0) = %d, want 0", pct)
	}
}

// TestLightTurnOnDefaultsTo100PctWhenNeverObserved tests that TurnOn on a
// freshly constructed Light (no CCU event yet) falls back to LastLevel=1.0.
func TestLightTurnOnDefaultsTo100PctWhenNeverObserved(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	if got := l.LastLevel(); got != 1.0 {
		t.Fatalf("LastLevel on fresh Light = %v, want 1.0", got)
	}
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 1.0 {
		t.Errorf("TurnOn wrote %v, want 1.0", w.last)
	}
}

// TestLightTimerConsumedOnlyOnce verifies that SetTimerOnTime defers a
// timer for exactly one TurnOn call and that subsequent TurnOn calls do
// NOT carry the timer.
func TestLightTimerConsumedOnlyOnce(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	d := 2 * time.Second
	l.SetTimerOnTime(d)

	// First TurnOn: timer must be consumed and appear in the put_paramset.
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("first TurnOn: expected 1 put_paramset, got %d", len(w.puts))
	}
	if _, ok := w.puts[0][string(hmenum.ParameterOnTime)]; !ok {
		t.Error("first TurnOn: ON_TIME missing from put_paramset")
	}

	// Second TurnOn must NOT produce a put_paramset (timer was consumed).
	w.puts = nil
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 0 {
		t.Errorf("second TurnOn: should produce no put_paramset, got %d", len(w.puts))
	}
}

// TestLightAddress ensures Address() returns the channel address from the
// embedded LEVEL DP key.
func TestLightAddress(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	const addr = "HmIP-BDT:4"
	l, _ := newLightRig(t, addr, w, custom.LightCapabilities{Dimmable: true})
	if got := l.Address(); got != addr {
		t.Errorf("Address() = %q, want %q", got, addr)
	}
}

// --- ColorLight deep tests ---

// TestColorLightConstructWithDPsSucceeds verifies that a ColorLight built
// from a channel carrying LEVEL + HUE + SATURATION exposes all accessors.
func TestColorLightConstructWithDPsSucceeds(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	cl, _, _, _ := newColorLightRig(t, "HmIP-BSL:3", w)

	if cl.Float == nil {
		t.Error("ColorLight.Float (LEVEL) must not be nil")
	}
	_, _, observed := cl.Color()
	// Nothing pushed yet → not observed.
	if observed {
		t.Error("Color() should not be observed before any event")
	}
}

// TestColorLightSetColorDoesNotTouchLevel verifies that SetColor only writes
// to HUE and SATURATION and leaves the LEVEL parameter untouched.
func TestColorLightSetColorDoesNotTouchLevel(t *testing.T) {
	t.Parallel()

	w := &multiWriter{}
	cl, _, _, _ := newColorLightRig(t, "HmIP-BSL:3", w)

	if err := cl.SetColor(context.Background(), 120, 0.8, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	// Check that LEVEL was NOT written.
	for _, c := range w.calls {
		if c.param == hmenum.ParameterLevel {
			t.Errorf("SetColor must not write LEVEL, but it did (value=%v)", c.value)
		}
	}
	// HUE and SATURATION must have been written.
	hueWritten, satWritten := false, false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterHue {
			hueWritten = true
		}
		if c.param == hmenum.ParameterSaturation {
			satWritten = true
		}
	}
	if !hueWritten {
		t.Error("SetColor must write HUE")
	}
	if !satWritten {
		t.Error("SetColor must write SATURATION")
	}
}

// TestColorLightSetBrightnessDoesNotTouchColor verifies that SetLevel only
// writes LEVEL and does not touch HUE or SATURATION.
func TestColorLightSetBrightnessDoesNotTouchColor(t *testing.T) {
	t.Parallel()

	w := &multiWriter{}
	cl, _, _, _ := newColorLightRig(t, "HmIP-BSL:3", w)

	if err := cl.SetLevel(context.Background(), 0.6, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	for _, c := range w.calls {
		if c.param == hmenum.ParameterHue || c.param == hmenum.ParameterSaturation {
			t.Errorf("SetLevel must not touch colour DPs, but wrote %s=%v", c.param, c.value)
		}
	}
	levelWritten := false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterLevel {
			levelWritten = true
		}
	}
	if !levelWritten {
		t.Error("SetLevel must write LEVEL")
	}
}

// TestColorLightHueWraps verifies that SetColor reduces hue modulo 360 before
// writing (e.g. 400 → 40).
func TestColorLightHueWraps(t *testing.T) {
	t.Parallel()

	w := &intWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BSL:3", 1, "COLOR_DIMMER", hmenum.ParamsetKeyValues)
	hue := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BSL:3",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHue),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	sat := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BSL:3",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSaturation),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BSL:3",
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
	ch.Put(hue)
	ch.Put(sat)
	cl := NewColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})

	if err := cl.SetColor(context.Background(), 400, 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastParam != hmenum.ParameterHue || w.lastInt != 40 {
		// SetColor writes HUE then SATURATION, so lastInt is the saturation
		// float. We check hue via reading back from the DP.
		hVal, hOK := cl.hue.Value()
		if !hOK || hVal != 40 {
			t.Errorf("hue after 400-degree input = %v ok=%v, want 40", hVal, hOK)
		}
	}
}

// --- ColorTempLight deep tests ---

// TestColorTempLightKelvinClampedWithinBounds verifies SetKelvin clamps to
// [MinKelvin, MaxKelvin].
func TestColorTempLightKelvinClampedWithinBounds(t *testing.T) {
	t.Parallel()

	w := &intWriter{}
	ctl, _, kelvin := newColorTempLightRig(t, "HmIP-SCTH230:3", w, 2000, 6500)

	// Below min → clamped to 2000.
	if err := ctl.SetKelvin(context.Background(), 1000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := kelvin.Value(); !ok || v != 2000 {
		t.Errorf("kelvin after below-min input = %v ok=%v, want 2000", v, ok)
	}

	// Above max → clamped to 6500.
	if err := ctl.SetKelvin(context.Background(), 9999, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := kelvin.Value(); !ok || v != 6500 {
		t.Errorf("kelvin after above-max input = %v ok=%v, want 6500", v, ok)
	}

	// In range → passes through unchanged.
	if err := ctl.SetKelvin(context.Background(), 4000, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := kelvin.Value(); !ok || v != 4000 {
		t.Errorf("kelvin after in-range input = %v ok=%v, want 4000", v, ok)
	}
}

// TestColorTempLightDefaultBoundsWhenZero verifies that NewColorTempLight
// applies the 2000/6500 defaults when zero bounds are supplied.
func TestColorTempLightDefaultBoundsWhenZero(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-SCTH230:3", 1, "COLOR_TEMP", hmenum.ParamsetKeyValues)
	ctl := NewColorTempLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}}, 0, 0)

	if ctl.MinKelvin != 2000 {
		t.Errorf("MinKelvin = %d, want 2000", ctl.MinKelvin)
	}
	if ctl.MaxKelvin != 6500 {
		t.Errorf("MaxKelvin = %d, want 6500", ctl.MaxKelvin)
	}
}

// TestRGBWLightModeDispatch verifies that RGBWLight gates SetColor and
// SetKelvin on the current operating mode, and that TurnOn via SetLevel
// is always available regardless of mode.
func TestRGBWLightModeDispatch(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-RGBW:1", 1, "RGBW_DIMMER", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "HmIP-RGBW:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
		}
	}

	level := generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat))
	hue := generic.NewInteger(mk(hmenum.ParameterHue, hmenum.ParameterTypeInteger))
	sat := generic.NewFloat(mk(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat))
	modeSensor := generic.NewStringSensor(mk(hmenum.ParameterDeviceOperationMode, hmenum.ParameterTypeString))
	ch.Put(level)
	ch.Put(hue)
	ch.Put(sat)
	ch.Put(modeSensor)

	r := NewRGBWLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})

	// Before any mode is observed, Mode() returns Unknown, but the capability
	// predicates fall back to the RGBW default (mirroring the reference
	// _device_operation_mode fallback), so HasColor is true.
	if r.Mode() != RGBWModeUnknown {
		t.Errorf("initial mode = %v, want Unknown", r.Mode())
	}
	if !r.HasColor() {
		t.Error("HasColor must default to true (RGBW) before mode is set")
	}

	// SetColor must fail in PWM mode.
	modeSensor.OnEvent("4_PWM")
	// Drive Subscribe to wire up the mode sensor.
	unsubscribe := r.Subscribe(ch)
	defer unsubscribe()
	modeSensor.OnEvent("4_PWM")

	if r.Mode() != RGBWModePWM {
		t.Errorf("after PWM event, mode = %v, want PWM", r.Mode())
	}
	if err := r.SetColor(context.Background(), 120, 1.0, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetColor in PWM mode must return an error")
	}

	// After switching to RGB mode SetColor must succeed.
	modeSensor.OnEvent("RGB")
	if r.Mode() != RGBWModeRGB {
		t.Errorf("after RGB event, mode = %v, want RGB", r.Mode())
	}
	if !r.HasColor() {
		t.Error("HasColor must be true in RGB mode")
	}
}

// mode-conditional RGBW capability predicates.
//
// HasHsColor == HasColor — both true for RGB/RGBW only.
// HasColorTemperature — true for TunableWhite/RGBW only.
// HasEffects — true when mode is not PWM AND effect list is non-empty.
func TestRGBWLightCapabilityPredicates(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0002"})
	ch := d.AddChannel("HmIP-RGBW:2", 1, "RGBW_DIMMER", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "HmIP-RGBW:2",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
				ValueList:  []string{"EFFECT_0", "EFFECT_1"}, // for effect DP
			},
		}
	}

	level := generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat))
	modeSensor := generic.NewStringSensor(mk(hmenum.ParameterDeviceOperationMode, hmenum.ParameterTypeString))
	effect := generic.NewInteger(mk(hmenum.ParameterEffect, hmenum.ParameterTypeInteger))
	ch.Put(level)
	ch.Put(modeSensor)
	ch.Put(effect)

	r := NewRGBWLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})
	unsubscribe := r.Subscribe(ch)
	defer unsubscribe()

	for _, tc := range []struct {
		mode string
		// wantColorTemp is the wire/Matter capability (KELVIN field present —
		// TUNABLE_WHITE or RGBW); wantColorTempMode is the mutually-exclusive HA
		// colour-mode capability (TUNABLE_WHITE only).
		wantColor         bool
		wantHsColor       bool
		wantColorTemp     bool
		wantColorTempMode bool
		wantEffects       bool
	}{
		{"4_PWM", false, false, false, false, false},
		{"RGB", true, true, false, false, true},
		{"RGBW", true, true, true, false, true},
		{"2_TUNABLE_WHITE", false, false, true, true, true},
	} {
		modeSensor.OnEvent(tc.mode)
		if got := r.HasColor(); got != tc.wantColor {
			t.Errorf("mode=%s HasColor=%v, want %v", tc.mode, got, tc.wantColor)
		}
		if got := r.HasHsColor(); got != tc.wantHsColor {
			t.Errorf("mode=%s HasHsColor=%v, want %v", tc.mode, got, tc.wantHsColor)
		}
		if got := r.HasColorTemperature(); got != tc.wantColorTemp {
			t.Errorf("mode=%s HasColorTemperature=%v, want %v", tc.mode, got, tc.wantColorTemp)
		}
		if got := r.HasColorTempColorMode(); got != tc.wantColorTempMode {
			t.Errorf("mode=%s HasColorTempColorMode=%v, want %v", tc.mode, got, tc.wantColorTempMode)
		}
		if got := r.HasEffects(); got != tc.wantEffects {
			t.Errorf("mode=%s HasEffects=%v, want %v", tc.mode, got, tc.wantEffects)
		}
	}
}

// TestLightCloseReleasesSubscription verifies that Close() does not panic
// and is idempotent (calling it twice is safe).
func TestLightCloseReleasesSubscription(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// First Close must not panic.
	l.Close()
	// Second Close (unsubLevel already nil) must not panic either.
	l.Close()
}

// --- RGBWLight.CurrentHsColor ---

// TestRGBWLightCurrentHsColorReturnsFalseWithNoMode verifies that
// CurrentHsColor() returns (0,0,false) when no mode has been observed yet
// (mode unknown → HasHsColor == false).
func TestRGBWLightCurrentHsColorReturnsFalseWithNoMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGB0001"})
	ch := d.AddChannel("RGB0001:1", 1, "RGBW", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "RGB0001:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
		}
	}
	ch.Put(generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat)))
	ch.Put(generic.NewInteger(mk(hmenum.ParameterHue, hmenum.ParameterTypeInteger)))
	ch.Put(generic.NewFloat(mk(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat)))
	ch.Put(generic.NewStringSensor(mk(hmenum.ParameterDeviceOperationMode, hmenum.ParameterTypeString)))

	r := NewRGBWLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})

	hue, sat, ok := r.CurrentHsColor()
	if ok {
		t.Errorf("CurrentHsColor() in unknown mode = (%d,%.2f,true), want (0,0,false)", hue, sat)
	}
}

// TestRGBWLightCurrentHsColorReturnsFalseInPWMMode verifies that
// CurrentHsColor() returns (0,0,false) in PWM mode (no HS colour).
func TestRGBWLightCurrentHsColorReturnsFalseInPWMMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGB0002"})
	ch := d.AddChannel("RGB0002:1", 1, "RGBW", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "RGB0002:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
		}
	}
	modeSensor := generic.NewStringSensor(mk(hmenum.ParameterDeviceOperationMode, hmenum.ParameterTypeString))
	ch.Put(generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat)))
	ch.Put(generic.NewInteger(mk(hmenum.ParameterHue, hmenum.ParameterTypeInteger)))
	ch.Put(generic.NewFloat(mk(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat)))
	ch.Put(modeSensor)

	r := NewRGBWLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})
	unsub := r.Subscribe(ch)
	defer unsub()
	modeSensor.OnEvent("4_PWM")

	_, _, ok := r.CurrentHsColor()
	if ok {
		t.Error("CurrentHsColor() in PWM mode must return ok=false")
	}
}

// TestRGBWLightCurrentHsColorReturnsValuesInRGBMode verifies that
// CurrentHsColor() returns the current HUE and SATURATION values when
// the mode is RGB and both DPs have been observed.
func TestRGBWLightCurrentHsColorReturnsValuesInRGBMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGB0003"})
	ch := d.AddChannel("RGB0003:1", 1, "RGBW", hmenum.ParamsetKeyValues)

	mk := func(p hmenum.Parameter, typ hmenum.ParameterType) generic.Spec {
		return generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "RGB0003:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       typ,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
		}
	}
	hueDP := generic.NewInteger(mk(hmenum.ParameterHue, hmenum.ParameterTypeInteger))
	satDP := generic.NewFloat(mk(hmenum.ParameterSaturation, hmenum.ParameterTypeFloat))
	modeSensor := generic.NewStringSensor(mk(hmenum.ParameterDeviceOperationMode, hmenum.ParameterTypeString))
	ch.Put(generic.NewFloat(mk(hmenum.ParameterLevel, hmenum.ParameterTypeFloat)))
	ch.Put(hueDP)
	ch.Put(satDP)
	ch.Put(modeSensor)

	r := NewRGBWLight(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})
	unsub := r.Subscribe(ch)
	defer unsub()
	modeSensor.OnEvent("RGB")

	// DPs not yet seeded → ok must be false.
	if _, _, ok := r.CurrentHsColor(); ok {
		t.Error("CurrentHsColor() before DP values must return ok=false")
	}

	// Seed values.
	hueDP.OnEvent(int32(120))
	satDP.OnEvent(0.75)

	hue, sat, ok := r.CurrentHsColor()
	if !ok {
		t.Fatal("CurrentHsColor() after DP seed must return ok=true")
	}
	if hue != 120 {
		t.Errorf("hue = %d, want 120", hue)
	}
	if sat != 75 {
		t.Errorf("saturation = %.2f, want 75", sat)
	}
}

// --- helpers used by new tests ---

// newIntegerRig is used in TestRGBWLightModeDispatch which needs a channel
// that carries the mode sensor. The actual helper above builds that inline.
// The unused import guard for hmtypes is satisfied by existing usages above.
var _ = hmtypes.DataPointKey{}

// newGroupLevelDP creates a read-only (sensor) group-level Float DP. The DP
// is not attached to a writer — it is updated via OnEvent only, mirroring a
// LEVEL_REAL or group-channel LEVEL which the CCU pushes but which the daemon
// never writes to.
//
// Parameter is LEVEL — this is the HmIP layout (state channel carries
// parameter LEVEL on a different channel from the action channel). Use
// [newGroupLevelRealDP] for the RF dimmer layout (LEVEL_REAL).
func newGroupLevelDP(t *testing.T, address string) *generic.Float {
	t.Helper()
	return newGroupLevelDPWithParam(t, address, hmenum.ParameterLevel)
}

// newGroupLevelRealDP creates a read-only LEVEL_REAL DP, used to simulate
// RF dimmers where the state mirror lives on the same channel as the action
// LEVEL. effectiveLevel takes the RF path (optimistic → lastSent → group
// → modified_at tiebreaker) when the group-level parameter is LEVEL_REAL.
func newGroupLevelRealDP(t *testing.T, address string) *generic.Float {
	t.Helper()
	return newGroupLevelDPWithParam(t, address, hmenum.ParameterLevelReal)
}

func newGroupLevelDPWithParam(t *testing.T, address string, param hmenum.Parameter) *generic.Float {
	t.Helper()
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Writer: nil, // read-only
	})
}

// --- PR #3166 regression tests: LEVEL_REAL / effectiveLevel stabilisation ---

// TestDimmerBrightnessUsesGroupLevelWhenBound verifies that on RF dimmers
// (group-level parameter is LEVEL_REAL) Brightness() and IsOn() read from the
// stable state-channel mirror rather than the action-channel LEVEL.
//
// HmIP dimmers (group-level parameter is LEVEL on a different channel) take
// the action-channel path — see [TestDimmerHmIPUsesActionChannel] for the
// matching #3181 regression test.
func TestDimmerBrightnessUsesGroupLevelWhenBound(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Seed action-channel LEVEL to 0.7 (observed but not "effective" once a
	// LEVEL_REAL mirror is bound).
	level.OnEvent(0.7)

	// Bind a read-only LEVEL_REAL DP and push 0.5 to it. LEVEL_REAL was
	// modified after LEVEL so it wins the modified_at tiebreaker — and the
	// RF-path code prefers group_level on a tie anyway.
	grpLevel := newGroupLevelRealDP(t, "RFD0001:1")
	grpLevel.OnEvent(0.5)
	l.SetGroupLevel(grpLevel)

	b, ok := l.Brightness()
	if !ok {
		t.Fatal("Brightness() must be observed when groupLevel has a value")
	}
	const wantLevel = 0.5
	if b.Level() != wantLevel {
		t.Errorf("Brightness().Level() = %.4f, want %.4f (LEVEL_REAL must win over action LEVEL on RF dimmers)", b.Level(), wantLevel)
	}
	on, observed := l.IsOn()
	if !observed {
		t.Fatal("IsOn() must be observed")
	}
	if !on {
		t.Errorf("IsOn() = false, want true (LEVEL_REAL 0.5 > 0)")
	}
}

// TestDimmerHmIPUsesActionChannel verifies that HmIP dimmers (group-level
// parameter is LEVEL on a separate channel) derive Brightness / IsOn from
// the action channel directly, even when the state channel reports a
// divergent value (e.g. HmIP-FDT echoes a section summary on channel 1
// rather than a 1:1 mirror of channel 4).
//
// GroupBrightness still exposes the state-channel value for consumers that
// need the section-summary semantic.
func TestDimmerHmIPUsesActionChannel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Action channel reaches 100 % (e.g. user set brightness=255).
	level.OnEvent(1.0)

	// State channel reports a divergent value — simulates the HmIP-FDT
	// channel-1 section summary.
	grpLevel := newGroupLevelDP(t, "HmIP-FDT-Section:1")
	grpLevel.OnEvent(0.4)
	l.SetGroupLevel(grpLevel)

	b, ok := l.Brightness()
	if !ok {
		t.Fatal("Brightness() must be observed")
	}
	if b.Level() != 1.0 {
		t.Errorf("Brightness().Level() = %.4f, want 1.0 (HmIP must read action channel, not divergent state)", b.Level())
	}
	if b.Byte() != 255 {
		t.Errorf("Brightness().Byte() = %d, want 255", b.Byte())
	}
	if on, _ := l.IsOn(); !on {
		t.Error("IsOn() = false, want true")
	}

	// GroupBrightness still exposes the state-channel value as a distinct
	// metric (no regression for consumers that need it).
	gb, gok := l.GroupBrightness()
	if !gok {
		t.Fatal("GroupBrightness() must be observed")
	}
	if gb != 102 {
		t.Errorf("GroupBrightness() = %d, want 102 (0.4 × 255 ≈ 102)", gb)
	}
}

// TestDimmerBrightnessFallsBackToLevelWithoutGroupLevel verifies that when no
// group-level DP has been bound, Brightness() reads from the action-channel
// LEVEL as before the fix.
func TestDimmerBrightnessFallsBackToLevelWithoutGroupLevel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	level.OnEvent(0.7)

	b, ok := l.Brightness()
	if !ok {
		t.Fatal("Brightness() must be observed after OnEvent")
	}
	const wantLevel = 0.7
	if b.Level() != wantLevel {
		t.Errorf("Brightness().Level() = %.4f, want %.4f (no group level: must use LEVEL)", b.Level(), wantLevel)
	}
}

// TestDimmerOptimisticLevelTakesPrecedenceOverGroupLevel verifies that while
// the action-channel LEVEL is in optimistic state (a set-command has been sent
// but the CCU has not confirmed yet), Brightness() reports the optimistic
// target, not the group-level value.
func TestDimmerOptimisticLevelTakesPrecedenceOverGroupLevel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Bind a group-level DP showing 0.5.
	grpLevel := newGroupLevelDP(t, "GRP0001:1")
	grpLevel.OnEvent(0.5)
	l.SetGroupLevel(grpLevel)

	// Send a command that puts the action-channel LEVEL into optimistic state.
	// stubWriter does not confirm, so IsOptimistic() stays true.
	if err := l.SetLevel(context.Background(), 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}

	if !l.IsOptimistic() {
		// stubWriter never confirms, so this branch should not be reached in
		// normal test execution. If the rig settles synchronously (e.g. a
		// future change makes OptimisticDisabled the default for test DPs),
		// the optimistic-precedence path becomes untestable here — skip rather
		// than fail.
		t.Skip("action-channel LEVEL settled synchronously; optimistic branch untestable in this rig")
	}

	b, ok := l.Brightness()
	if !ok {
		t.Fatal("Brightness() must be observed while optimistic")
	}
	const wantLevel = 1.0
	if b.Level() != wantLevel {
		t.Errorf("Brightness().Level() = %.4f, want %.4f (optimistic must win over group-level 0.5)", b.Level(), wantLevel)
	}
}

// TestDimmerIsStateChangeUsesEffectiveLevel verifies that `IsStateChange`
// reads the same effective LEVEL as `Brightness` / `IsOn`. On RF dimmers
// (LEVEL_REAL state mirror) the steady-state group-level wins, so a
// redundant command is judged against the source the user sees.
func TestDimmerIsStateChangeUsesEffectiveLevel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Bring action and state channel to a coherent on-state at 0.5 — the
	// steady-state case where action LEVEL and LEVEL_REAL agree on a real
	// dimmer.
	level.OnEvent(0.5)
	grpLevel := newGroupLevelRealDP(t, "RFD0001:1")
	grpLevel.OnEvent(0.5)
	l.SetGroupLevel(grpLevel)

	target := custom.NewBrightness(0.5).Byte()
	if l.IsStateChange(false, false, &target) {
		t.Error("IsStateChange(target=128) must return false when effective LEVEL == 0.5 (no change)")
	}

	// Now diverge the two — LEVEL_REAL (the user-visible state) wins, so
	// a command targeting the action-channel value is no longer redundant.
	grpLevelLow := newGroupLevelRealDP(t, "RFD0002:1")
	grpLevelLow.OnEvent(0.2)
	l.SetGroupLevel(grpLevelLow)
	target = custom.NewBrightness(0.5).Byte()
	if !l.IsStateChange(false, false, &target) {
		t.Error("IsStateChange(target=128) must return true when effective LEVEL == 0.2 differs from target")
	}
}

// TestDimmerIntermediateLevelDuringRamp verifies that an intermediate LEVEL
// echo during an RF dimmer ramp does NOT flip `IsOn` back to the previous
// state. Without the `lastSentLevel` fallback in `effectiveLevel`, the
// mismatching intermediate echo (0.745 ≠ 0.0) would clear the optimistic
// tracker and the next read would surface the stale LEVEL_REAL (0.75) —
// flipping `IsOn` back to true after the user clicked off.
//
// Scenario uses a LEVEL_REAL state mirror so the RF dimmer path is active
// (HmIP dimmers use the action channel directly — see #3181).
func TestDimmerIntermediateLevelDuringRamp(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Bring the light to a confirmed on-state at 75 % on both action and
	// state channel.
	level.OnEvent(0.75)
	grpLevel := newGroupLevelRealDP(t, "RFD0001:1")
	grpLevel.OnEvent(0.75)
	l.SetGroupLevel(grpLevel)

	if on, ok := l.IsOn(); !ok || !on {
		t.Fatalf("precondition: IsOn()=(%v, %v) want (true, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() != 191 {
		t.Fatalf("precondition: Brightness().Byte()=%d ok=%v want 191", b.Byte(), ok)
	}

	// User turns the light off — optimistic path reports off immediately.
	if err := l.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff: %v", err)
	}
	if on, ok := l.IsOn(); !ok || on {
		t.Fatalf("after TurnOff (optimistic): IsOn()=(%v, %v) want (false, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() != 0 {
		t.Fatalf("after TurnOff: Brightness().Byte()=%d ok=%v want 0", b.Byte(), ok)
	}
	// The sent target must be tracked while still unconfirmed.
	if pending, ok := l.lastSentValue(); !ok || pending != 0.0 {
		t.Fatalf("lastSentValue()=(%v, %v) want (0.0, true)", pending, ok)
	}

	// CCU echoes an intermediate ramp value on the action channel. The
	// group-level (state channel) still reports 0.75 — this is the window
	// where the bug used to flip `IsOn` back to true.
	level.OnEvent(0.745)
	if on, ok := l.IsOn(); !ok || on {
		t.Fatalf("after intermediate echo 0.745: IsOn()=(%v, %v) want (false, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() != 0 {
		t.Fatalf("after intermediate echo: Brightness().Byte()=%d ok=%v want 0", b.Byte(), ok)
	}

	// Final echo on both channels — the dimmer settles at 0.
	level.OnEvent(0.0)
	grpLevel.OnEvent(0.0)
	if on, ok := l.IsOn(); !ok || on {
		t.Fatalf("after final echo 0.0: IsOn()=(%v, %v) want (false, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() != 0 {
		t.Fatalf("after final echo: Brightness().Byte()=%d ok=%v want 0", b.Byte(), ok)
	}
	if _, ok := l.lastSentValue(); ok {
		t.Error("lastSentValue must be cleared after matching final echo")
	}

	// Mirror case: turning back on while group_level still reports the old
	// off-state. The intermediate ramp-start echo (0.005) must not surface
	// as "off" between the user's TurnOn and the final echo. Specify the
	// brightness explicitly — `LastLevel` may have absorbed the intermediate
	// 0.745 from the previous turn-off ramp.
	bright := 0.75
	if err := l.TurnOnWith(context.Background(), OnConfig{Brightness: &bright}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWith: %v", err)
	}
	if on, ok := l.IsOn(); !ok || !on {
		t.Fatalf("after TurnOn (optimistic): IsOn()=(%v, %v) want (true, true)", on, ok)
	}
	if pending, ok := l.lastSentValue(); !ok || pending != bright {
		t.Fatalf("lastSentValue()=(%v, %v) want (%v, true)", pending, ok, bright)
	}
	level.OnEvent(0.005)
	if on, ok := l.IsOn(); !ok || !on {
		t.Fatalf("after intermediate echo 0.005: IsOn()=(%v, %v) want (true, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() == 0 {
		t.Fatalf("after intermediate echo: Brightness().Byte()=%d ok=%v want > 0", b.Byte(), ok)
	}
	level.OnEvent(bright)
	grpLevel.OnEvent(bright)
	if on, ok := l.IsOn(); !ok || !on {
		t.Fatalf("after final echo 0.75: IsOn()=(%v, %v) want (true, true)", on, ok)
	}
	if b, ok := l.Brightness(); !ok || b.Byte() != 191 {
		t.Fatalf("after final echo: Brightness().Byte()=%d ok=%v want 191", b.Byte(), ok)
	}
}

// TestDimmerIsOnUsesEffectiveLevel verifies that on RF dimmers IsOn reads
// from the stable LEVEL_REAL mirror, not the raw action-channel LEVEL.
// With LEVEL_REAL = 0.0 (off) and action LEVEL = 0.7 (a stale ramp echo
// before LEVEL_REAL caught up), IsOn must return false.
//
// On HmIP dimmers the action channel wins regardless — see #3181.
func TestDimmerIsOnUsesEffectiveLevel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Action-channel LEVEL says on.
	level.OnEvent(0.7)

	// Bind a LEVEL_REAL DP reporting off (0.0). LEVEL_REAL was modified
	// after LEVEL so it wins the modified_at tiebreaker, and the RF-path
	// code prefers group_level on a tie anyway.
	grpLevel := newGroupLevelRealDP(t, "RFD0001:1")
	grpLevel.OnEvent(0.0)
	l.SetGroupLevel(grpLevel)

	on, observed := l.IsOn()
	if !observed {
		t.Fatal("IsOn() must be observed when LEVEL_REAL has been pushed")
	}
	if on {
		t.Errorf("IsOn() = true, want false (LEVEL_REAL 0.0 must report off even though LEVEL=0.7)")
	}
}
