// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct{ last float64 }

func (w *stubWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	if f, ok := value.(float64); ok {
		w.last = f
	}
	return nil
}

// putWriter exercises the atomic put_paramset path. Each PutParamset
// call is recorded as a copy of the values map.
type putWriter struct {
	stubWriter
	puts []map[string]any
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	p.puts = append(p.puts, cp)
	return nil
}

func newLightRig(t *testing.T, address string, w Writer, caps custom.LightCapabilities) (*Light, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "DIMMER", hmenum.ParamsetKeyValues)
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

func TestLightDimmable(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.SetLevel(context.Background(), 0.3, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 0.3 {
		t.Fatalf("writer saw %v", w.last)
	}
	br, ok := l.Brightness()
	if !ok || br.Level() != 0.3 {
		t.Fatalf("brightness=%v ok=%v", br.Level(), ok)
	}
}

func TestLightNonDimmableRejectsIntermediateLevel(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	if err := l.SetLevel(context.Background(), 0.5, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("non-dimmable should reject 0.5")
	}
}

func TestLightTurnOnRestoresLastLevel(t *testing.T) {
	w := &stubWriter{}
	l, level := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// Without prior observation, TurnOn should fall back to full power.
	if got := l.LastLevel(); got != 1.0 {
		t.Fatalf("fresh LastLevel=%v want 1.0", got)
	}
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 1.0 {
		t.Fatalf("fresh TurnOn writer saw %v want 1.0", w.last)
	}

	// CCU reports a 30 % level → cached as last non-zero.
	level.OnEvent(0.3)
	if got := l.LastLevel(); got != 0.3 {
		t.Fatalf("LastLevel after OnLevel(0.3)=%v want 0.3", got)
	}

	// CCU reports off (0). Last-level must NOT update — that is the
	// whole point of the tracker (otherwise toggle behaviour breaks).
	level.OnEvent(0)
	if got := l.LastLevel(); got != 0.3 {
		t.Fatalf("LastLevel after OnLevel(0)=%v want 0.3 (off must not overwrite)", got)
	}

	// TurnOn now restores 0.3.
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != 0.3 {
		t.Fatalf("TurnOn after off saw %v want 0.3 (restore last level)", w.last)
	}
}

func TestLightTurnOnWithAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	on := 5 * time.Second
	ramp := 6 * time.Second
	br := 0.10980392156862745
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
	if got[string(hmenum.ParameterLevel)].(float64) != br {
		t.Errorf("LEVEL=%v", got[string(hmenum.ParameterLevel)])
	}
	if got[string(hmenum.ParameterOnTime)].(float64) != 5 {
		t.Errorf("ON_TIME=%v", got[string(hmenum.ParameterOnTime)])
	}
	if got[string(hmenum.ParameterRampTime)].(float64) != 6 {
		t.Errorf("RAMP_TIME=%v", got[string(hmenum.ParameterRampTime)])
	}
}

func TestLightSetTimerThenTurnOnAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	l.SetTimerOnTime(500 * time.Millisecond) // 0.5s
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if v := got[string(hmenum.ParameterOnTime)].(float64); v < 0.49 || v > 0.51 {
		t.Errorf("ON_TIME=%v want ~0.5", v)
	}
	if got[string(hmenum.ParameterLevel)].(float64) != 1.0 {
		t.Errorf("LEVEL=%v want 1.0", got[string(hmenum.ParameterLevel)])
	}
	// Deferred timer must be consumed: a second TurnOn writes only LEVEL.
	w.puts = nil
	if err := l.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 0 {
		t.Errorf("second TurnOn must not produce another put_paramset (got %d)", len(w.puts))
	}
}

func TestLightTurnOffWithRampAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "VCU1399816:4", w, custom.LightCapabilities{Dimmable: true})
	if err := l.TurnOffWithRamp(context.Background(), 6*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterRampTime)].(float64) != 6 {
		t.Errorf("RAMP_TIME=%v", got[string(hmenum.ParameterRampTime)])
	}
	if got[string(hmenum.ParameterLevel)].(float64) != 0 {
		t.Errorf("LEVEL=%v want 0", got[string(hmenum.ParameterLevel)])
	}
	// ON_TIME=TimerNotUsed must accompany the ramp so the CCU does not silently
	// overlay an implicit off-timer.
	if got[string(hmenum.ParameterOnTime)].(float64) != custom.TimerNotUsed {
		t.Errorf("ON_TIME=%v want TimerNotUsed (%v)", got[string(hmenum.ParameterOnTime)], custom.TimerNotUsed)
	}
}

func newLightRigPut(t *testing.T, address string, w *putWriter, caps custom.LightCapabilities) (*Light, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "DIMMER", hmenum.ParamsetKeyValues)
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

func TestLightSharesLevelInstanceWithChannel(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-BDT:4", 1, "DIMMER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-BDT:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(level)
	l := New(Config{Channel: ch, Capabilities: custom.LightCapabilities{Dimmable: true}})
	if any(l.Float) != any(level) {
		t.Fatal("Light.Float must be the same instance as channel parameter")
	}
}

// TestLightGroupBrightness verifies that GroupBrightness and
// GroupBrightnessPct return the correct values from the group-level DP.
func TestLightGroupBrightness(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})

	// No group-level DP installed → (0, false).
	if _, ok := l.GroupBrightness(); ok {
		t.Error("GroupBrightness() without groupLevel should return ok=false")
	}
	if _, ok := l.GroupBrightnessPct(); ok {
		t.Error("GroupBrightnessPct() without groupLevel should return ok=false")
	}

	// Install a group-level DP.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GRP0001"})
	ch := d.AddChannel("GRP0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	grpLevel := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "GRP0001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Writer: nil,
	})
	ch.Put(grpLevel)

	l.SetGroupLevel(grpLevel)

	// Group-level DP present but no value observed yet → ok=false.
	if _, ok := l.GroupBrightness(); ok {
		t.Error("GroupBrightness() with unobserved groupLevel should return ok=false")
	}

	// Feed 0.5 → GroupBrightness = 127 or 128, GroupBrightnessPct = 50.
	grpLevel.OnEvent(0.5)

	b, ok := l.GroupBrightness()
	if !ok {
		t.Error("GroupBrightness() should return ok=true after observation")
	}
	if b != 127 && b != 128 {
		t.Errorf("GroupBrightness(0.5)=%d, want 127 or 128", b)
	}

	pct, ok := l.GroupBrightnessPct()
	if !ok {
		t.Error("GroupBrightnessPct() should return ok=true after observation")
	}
	if pct != 50 {
		t.Errorf("GroupBrightnessPct(0.5)=%d, want 50", pct)
	}

	// Clear the group-level DP.
	l.SetGroupLevel(nil)
	if _, ok := l.GroupBrightness(); ok {
		t.Error("GroupBrightness() after SetGroupLevel(nil) should return ok=false")
	}
}

// TestConvertFlashTimeToOnTimeList verifies that flash-time mapping.
func TestConvertFlashTimeToOnTimeList(t *testing.T) {
	cases := []struct {
		input int
		want  string
	}{
		{0, "PERMANENTLY_ON"},
		{-1, "PERMANENTLY_ON"},
		{6000, "PERMANENTLY_ON"},
		{100, "100MS"},
		{150, "100MS"}, // closer to 100MS than 200MS
		{175, "200MS"}, // equidistant: math.Abs(175-100)=75 > math.Abs(175-200)=25 → 200MS
		{500, "500MS"},
		{1000, "1S"},
		{2500, "2S"}, // closer to 2S(2000) than 3S(3000)
		{5000, "5S"},
	}
	for _, tc := range cases {
		got := ConvertFlashTimeToOnTimeList(tc.input)
		if got != tc.want {
			t.Errorf("ConvertFlashTimeToOnTimeList(%d)=%q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestSoundPlayerLEDTurnOn verifies that TurnOn bundles parameters
// into a single put_paramset with correct values.
func TestSoundPlayerLEDTurnOn(t *testing.T) {
	w := &putWriter{}

	// Build a minimal FixedColorLight via newLightRig (white light).
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "LED0001"})
	ch := d.AddChannel("LED0001:6", 6, "DIMMER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "LED0001:6",
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
	fc := NewFixedColorLight(Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true}})
	led := &SoundPlayerLED{FixedColorLight: fc}

	err := led.TurnOn(context.Background(), LedOnConfig{
		Brightness:  128,
		Repetitions: 3,
		FlashTimeMS: 500,
	}, w, "LED0001:6", hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("TurnOn error: %v", err)
	}
	if len(w.puts) == 0 {
		t.Fatal("expected at least one put_paramset call")
	}
	params := w.puts[len(w.puts)-1]
	// Level: 128/255 ≈ 0.502.
	if lvl, ok := params[string(hmenum.ParameterLevel)]; !ok {
		t.Error("missing LEVEL in put_paramset")
	} else if f, ok := lvl.(float64); !ok || f < 0.49 || f > 0.51 {
		t.Errorf("LEVEL=%v, want ~0.5", lvl)
	}
	// ON_TIME_LIST_1 should be "500MS".
	if v, ok := params[string(hmenum.ParameterOnTimeList1)]; !ok || v != "500MS" {
		t.Errorf("ON_TIME_LIST_1=%v, want 500MS", v)
	}
	// REPETITIONS should be "REPETITIONS_003".
	if v, ok := params[string(hmenum.ParameterRepetitions)]; !ok || v != "REPETITIONS_003" {
		t.Errorf("REPETITIONS=%v, want REPETITIONS_003", v)
	}
}
