// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type stubWriter struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (w *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, call{p, v})
	return nil
}

func (w *stubWriter) find(p hmenum.Parameter) (any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.calls {
		if c.param == p {
			return c.value, true
		}
	}
	return nil, false
}

// --- IsCombined marker ---

func TestLevelCombinedIsCombined(t *testing.T) {
	t.Parallel()
	lc := NewLevelCombined("x:1", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	if !lc.IsCombined() {
		t.Fatal("LevelCombined.IsCombined() must return true")
	}
}

func TestHSColorIsCombined(t *testing.T) {
	t.Parallel()
	hs := NewHSColor("x:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if !hs.IsCombined() {
		t.Fatal("HSColor.IsCombined() must return true")
	}
}

func TestTimerIsCombined(t *testing.T) {
	t.Parallel()
	tm := NewTimer("x:1", &stubWriter{}, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	if !tm.IsCombined() {
		t.Fatal("Timer.IsCombined() must return true")
	}
}

// --- DataPointKey ---

func TestLevelCombinedDataPointKey(t *testing.T) {
	t.Parallel()
	lc := NewLevelCombinedWithCentral("ccu1", "DEV:1", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	key := lc.DataPointKey()
	if key.ChannelAddress != "DEV:1" {
		t.Errorf("ChannelAddress = %q, want DEV:1", key.ChannelAddress)
	}
	if key.Parameter != levelCombinedKeyName {
		t.Errorf("Parameter = %q, want %q", key.Parameter, levelCombinedKeyName)
	}
}

func TestHSColorDataPointKey(t *testing.T) {
	t.Parallel()
	hs := NewHSColorWithCentral("ccu1", "DEV:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	key := hs.DataPointKey()
	if key.ChannelAddress != "DEV:1" {
		t.Errorf("ChannelAddress = %q, want DEV:1", key.ChannelAddress)
	}
	if key.Parameter != hsColorKeyName {
		t.Errorf("Parameter = %q, want %q", key.Parameter, hsColorKeyName)
	}
}

// --- OnAnyUpdate (JSON encoding) ---

func TestLevelCombinedOnAnyUpdateJSON(t *testing.T) {
	t.Parallel()
	lc := NewLevelCombined("x:1", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	var got string
	unsub := lc.OnAnyUpdate(func(_, next any) {
		got, _ = next.(string)
	})
	defer unsub()
	lc.OnLevel(0.5)
	lc.OnSlatsLevel(0.25)
	if got == "" {
		t.Fatal("OnAnyUpdate did not fire")
	}
	if !strings.Contains(got, `"level"`) || !strings.Contains(got, `"slats"`) {
		t.Errorf("unexpected JSON payload: %s", got)
	}
}

func TestHSColorOnAnyUpdateJSON(t *testing.T) {
	t.Parallel()
	hs := NewHSColor("x:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	var got string
	unsub := hs.OnAnyUpdate(func(_, next any) {
		got, _ = next.(string)
	})
	defer unsub()
	hs.OnHue(120)
	hs.OnSaturation(0.5)
	if got == "" {
		t.Fatal("OnAnyUpdate did not fire")
	}
	if !strings.Contains(got, `"hue"`) || !strings.Contains(got, `"saturation"`) {
		t.Errorf("unexpected JSON payload: %s", got)
	}
}

// --- HSColor ---

func TestHSColorValueRequiresBothInputs(t *testing.T) {
	c := NewHSColor("HmIP-RGBW:3", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(120)
	if _, ok := c.Value(); ok {
		t.Fatal("value should not be observed with only hue")
	}
	c.OnSaturation(0.5)
	hs, ok := c.Value()
	if !ok || hs.Hue != 120 || hs.Saturation != 50 {
		t.Fatalf("value=%+v ok=%v", hs, ok)
	}
}

func TestHSColorSetColorSendsBoth(t *testing.T) {
	w := &stubWriter{}
	c := NewHSColor("x", w, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if err := c.SetColor(context.Background(), HS{Hue: 400, Saturation: 150}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, _ := w.find(hmenum.ParameterHue); v.(int32) != 40 { // 400 mod 360
		t.Errorf("hue=%v", v)
	}
	if v, _ := w.find(hmenum.ParameterSaturation); v.(float64) != 1.0 { // clamped
		t.Errorf("sat=%v", v)
	}
}

func TestHSColorOnUpdateFiresOnChange(t *testing.T) {
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	var count int
	c.OnUpdate(func(_, _ HS) { count++ })
	c.OnHue(100)
	c.OnSaturation(0.5) // first observed → fires
	c.OnSaturation(0.5) // no change → no fire
	c.OnSaturation(0.7) // change → fires
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
}

// --- Timer ---

func TestTimerRecalcUnitThresholds(t *testing.T) {
	cases := []struct {
		seconds  float64
		wantVal  float64
		wantUnit hmenum.TimerUnit
	}{
		{30, 30, hmenum.TimerUnitSeconds},
		{16343, 16343, hmenum.TimerUnitSeconds},
		{16344, 16344.0 / 60, hmenum.TimerUnitMinutes},
		{3600 * 1000, 1000, hmenum.TimerUnitHours}, // huge → hours
	}
	for _, c := range cases {
		v, u := RecalcUnit(c.seconds)
		if v != c.wantVal || u != c.wantUnit {
			t.Errorf("%vs → (%v, %v), want (%v, %v)", c.seconds, v, u, c.wantVal, c.wantUnit)
		}
	}
}

func TestTimerSetDurationSendsUnitAndValue(t *testing.T) {
	w := &stubWriter{}
	tm := NewTimer("HmIP:3", w, hmenum.ParameterOnTimeValue, hmenum.ParameterOnTimeUnit)
	if err := tm.SetDuration(context.Background(), 30*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, _ := w.find(hmenum.ParameterOnTimeUnit); v.(int32) != int32(hmenum.TimerUnitSeconds) {
		t.Errorf("unit=%v", v)
	}
	if v, _ := w.find(hmenum.ParameterOnTimeValue); v.(float64) != 30 {
		t.Errorf("value=%v", v)
	}
	// Round-trip via ingestion.
	d, ok := tm.Value()
	if !ok || d != 30*time.Second {
		t.Errorf("value=%v ok=%v", d, ok)
	}
}

func TestTimerOnComponentsAggregates(t *testing.T) {
	tm := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTimeValue, hmenum.ParameterOnTimeUnit)
	tm.OnComponents(2, hmenum.TimerUnitHours)
	d, ok := tm.Value()
	if !ok || d != 2*time.Hour {
		t.Fatalf("d=%v ok=%v", d, ok)
	}
}

// --- LevelCombined ---

func TestLevelCombinedValueRequiresBothInputs(t *testing.T) {
	l := NewLevelCombined("x", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	l.OnLevel(0.5)
	if _, ok := l.Value(); ok {
		t.Fatal("should not be observed yet")
	}
	l.OnSlatsLevel(0.25)
	v, ok := l.Value()
	if !ok {
		t.Fatal("not observed")
	}
	if v.Level.Level() != 0.5 || v.SlatsLevel.Level() != 0.25 {
		t.Fatalf("v=%+v", v)
	}
}

// IsRefreshed / StateUncertain on combined DPs

func TestLevelCombinedIsRefreshedRequiresBothInputs(t *testing.T) {
	l := NewLevelCombined("x", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	if l.IsRefreshed() {
		t.Fatal("IsRefreshed must be false before any input")
	}
	l.OnLevel(0.5)
	if l.IsRefreshed() {
		t.Fatal("IsRefreshed must be false after only LEVEL")
	}
	l.OnSlatsLevel(0.25)
	if !l.IsRefreshed() {
		t.Fatal("IsRefreshed must be true after both inputs")
	}
}

func TestLevelCombinedStateUncertainAlwaysFalse(t *testing.T) {
	l := NewLevelCombined("x", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	if l.StateUncertain() {
		t.Fatal("StateUncertain must always be false for LevelCombined")
	}
}

// --- Translation() accessors (data_point.go) ---

// TestHSColorTranslation verifies Translation() returns "".
func TestHSColorTranslation(t *testing.T) {
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if got := c.Translation(); got != "" {
		t.Errorf("HSColor.Translation() = %q, want empty", got)
	}
}

// TestTimerTranslation verifies Translation() returns "".
func TestTimerTranslation(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if got := tmr.Translation(); got != "" {
		t.Errorf("Timer.Translation() = %q, want empty", got)
	}
}

// --- Timer: ModifiedAt / RefreshedAt / ValueSeconds / OnUpdate ---

// TestTimerModifiedAtRefreshedAt verifies both return zero time.
func TestTimerModifiedAtRefreshedAt(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if !tmr.ModifiedAt().IsZero() {
		t.Error("ModifiedAt must be zero")
	}
	if !tmr.RefreshedAt().IsZero() {
		t.Error("RefreshedAt must be zero")
	}
}

// TestTimerValueSeconds verifies ValueSeconds follows observed state.
func TestTimerValueSeconds(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if _, ok := tmr.ValueSeconds(); ok {
		t.Error("ValueSeconds should return false before first observation")
	}
	tmr.OnComponents(60, hmenum.TimerUnitSeconds)
	v, ok := tmr.ValueSeconds()
	if !ok {
		t.Error("ValueSeconds should return true after observation")
	}
	if v != 60 {
		t.Errorf("ValueSeconds() = %v, want 60", v)
	}
}

// TestTimerOnUpdate verifies OnUpdate fires and unsubscribes correctly.
func TestTimerOnUpdate(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	var count int
	unsub := tmr.OnUpdate(func(_, _ float64) { count++ })

	tmr.OnComponents(10, hmenum.TimerUnitSeconds) // first observation → fire
	tmr.OnComponents(10, hmenum.TimerUnitSeconds) // no change → no fire
	tmr.OnComponents(20, hmenum.TimerUnitSeconds) // change → fire

	if count != 2 {
		t.Errorf("OnUpdate fired %d times, want 2", count)
	}
	unsub()
	tmr.OnComponents(30, hmenum.TimerUnitSeconds) // unsubscribed → no fire
	if count != 2 {
		t.Errorf("after Unsub OnUpdate fired %d times, want still 2", count)
	}
}

// TestTimerLoadDataPointValue verifies LoadDataPointValue calls loader for both params.
func TestTimerLoadDataPointValue(t *testing.T) {
	tmr := NewTimer("addr:1", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	var calls []string
	tmr.LoadDataPointValue(func(ch, p string) { calls = append(calls, p) })
	if len(calls) != 2 {
		t.Errorf("LoadDataPointValue: got %d calls, want 2", len(calls))
	}
	// nil loader is a no-op
	tmr.LoadDataPointValue(nil)
}

// TestTimerLoadDataPointValueNoUnit verifies LoadDataPointValue skips empty unit.
func TestTimerLoadDataPointValueNoUnit(t *testing.T) {
	tmr := NewTimer("addr:1", &stubWriter{}, hmenum.ParameterOnTime, "")
	var calls []string
	tmr.LoadDataPointValue(func(_, p string) { calls = append(calls, p) })
	if len(calls) != 1 {
		t.Errorf("LoadDataPointValue with empty unit: got %d calls, want 1", len(calls))
	}
}

// TestTimerSendDefault verifies SendDefault calls writer for both params.
func TestTimerSendDefault(t *testing.T) {
	w := &stubWriter{}
	tmr := NewTimer("addr:1", w, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	defaults := map[string]float64{
		string(hmenum.ParameterOnTime):     30,
		string(hmenum.ParameterOnTimeUnit): 0,
	}
	err := tmr.SendDefault(context.Background(), func(p string) (float64, bool) {
		v, ok := defaults[p]
		return v, ok
	}, hmenum.CommandPriorityLow)
	if err != nil {
		t.Fatalf("SendDefault: %v", err)
	}
	if len(w.calls) != 2 {
		t.Errorf("SendDefault: got %d calls, want 2", len(w.calls))
	}
}

// TestTimerSendDefaultNilFn verifies SendDefault is a no-op with nil fn.
func TestTimerSendDefaultNilFn(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if err := tmr.SendDefault(context.Background(), nil, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SendDefault(nil): %v", err)
	}
}

// TestTimerHasDataPoints verifies HasDataPoints reflects observation.
func TestTimerHasDataPoints(t *testing.T) {
	tmr := NewTimer("x", &stubWriter{}, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if tmr.HasDataPoints() {
		t.Error("HasDataPoints must be false before observation")
	}
	tmr.OnComponents(10, hmenum.TimerUnitSeconds)
	if !tmr.HasDataPoints() {
		t.Error("HasDataPoints must be true after observation")
	}
}

// TestTimerSetDuration verifies SetDuration writes and fires callbacks.
func TestTimerSetDuration(t *testing.T) {
	w := &stubWriter{}
	tmr := NewTimer("addr:1", w, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	var fired bool
	tmr.OnUpdate(func(_, _ float64) { fired = true })

	if err := tmr.SetDuration(context.Background(), 90*time.Second, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SetDuration: %v", err)
	}
	if !fired {
		t.Error("OnUpdate not fired after SetDuration")
	}
	// Second call with same value should still write but may not re-fire.
	w.mu.Lock()
	w.calls = nil
	w.mu.Unlock()
	if err := tmr.SetDuration(context.Background(), 90*time.Second, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SetDuration second: %v", err)
	}
}

// TestToSeconds covers all timer unit branches.
func TestToSeconds(t *testing.T) {
	tests := []struct {
		unit    hmenum.TimerUnit
		value   float64
		seconds float64
	}{
		{hmenum.TimerUnitSeconds, 10, 10},
		{hmenum.TimerUnitMinutes, 2, 120},
		{hmenum.TimerUnitHours, 1, 3600},
		{hmenum.TimerUnit(99), 5, 5}, // unknown unit → passthrough
	}
	for _, tt := range tests {
		got := toSeconds(tt.value, tt.unit)
		if got != tt.seconds {
			t.Errorf("toSeconds(%v, %v) = %v, want %v", tt.value, tt.unit, got, tt.seconds)
		}
	}
}

// TestRecalcUnitNegative verifies negative input is clamped to 0.
func TestRecalcUnitNegative(t *testing.T) {
	v, u := RecalcUnit(-1)
	if v != 0 || u != hmenum.TimerUnitSeconds {
		t.Errorf("RecalcUnit(-1) = (%v, %v), want (0, Seconds)", v, u)
	}
}

// TestRecalcUnitHours verifies very large values are expressed as hours.
func TestRecalcUnitHours(t *testing.T) {
	// 16344 * 60 = 980640 seconds → exceeds minute threshold → hours.
	input := float64(16344*60 + 1)
	_, u := RecalcUnit(input)
	if u != hmenum.TimerUnitHours {
		t.Errorf("RecalcUnit(%v): expected hmenum.TimerUnitHours, got %v", input, u)
	}
}

// --- LevelCombined: OnUpdate / clamp01 ---

// TestLevelCombinedOnUpdate verifies OnUpdate fires and unsubscribes.
func TestLevelCombinedOnUpdate(t *testing.T) {
	lc := NewLevelCombined("addr:1", &stubWriter{}, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	var count int
	unsub := lc.OnUpdate(func(_, _ LevelComposite) { count++ })

	lc.OnLevel(0.5)    // only level observed → no fire
	lc.OnSlatsLevel(0) // both observed → fire
	if count != 1 {
		t.Errorf("OnUpdate fired %d times after first composite, want 1", count)
	}
	lc.OnSlatsLevel(0) // no change → no fire
	if count != 1 {
		t.Errorf("OnUpdate fired %d times on no-change, want still 1", count)
	}
	lc.OnLevel(0.8) // change → fire
	if count != 2 {
		t.Errorf("OnUpdate fired %d times on change, want 2", count)
	}
	unsub()
	lc.OnLevel(0.1) // unsubscribed → no fire
	if count != 2 {
		t.Errorf("after Unsub OnUpdate fired %d, want still 2", count)
	}
}

// --- HSColor: LoadDataPointValue / SendDefault ---

// TestHSColorLoadDataPointValue verifies LoadDataPointValue calls for both params.
func TestHSColorLoadDataPointValue(t *testing.T) {
	c := NewHSColor("addr:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	var calls []string
	c.LoadDataPointValue(func(_, p string) { calls = append(calls, p) })
	if len(calls) != 2 {
		t.Errorf("LoadDataPointValue: got %d calls, want 2", len(calls))
	}
	c.LoadDataPointValue(nil) // nil is no-op
}

// TestHSColorSendDefault verifies SendDefault calls writer for both params.
func TestHSColorSendDefault(t *testing.T) {
	w := &stubWriter{}
	c := NewHSColor("addr:1", w, hmenum.ParameterHue, hmenum.ParameterSaturation)
	defaults := map[string]any{
		string(hmenum.ParameterHue):        int32(180),
		string(hmenum.ParameterSaturation): float64(0.5),
	}
	err := c.SendDefault(context.Background(), func(p string) (any, bool) {
		v, ok := defaults[p]
		return v, ok
	}, hmenum.CommandPriorityLow)
	if err != nil {
		t.Fatalf("SendDefault: %v", err)
	}
	if len(w.calls) != 2 {
		t.Errorf("SendDefault: got %d calls, want 2", len(w.calls))
	}
}

// TestHSColorSendDefaultNilFn verifies nil fn is a no-op.
func TestHSColorSendDefaultNilFn(t *testing.T) {
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if err := c.SendDefault(context.Background(), nil, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("SendDefault(nil): %v", err)
	}
}

// --- Signature ---

func TestHSColorSignature(t *testing.T) {
	t.Parallel()
	h := NewHSColor("VCU:1", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	got := h.Signature()
	if !strings.HasPrefix(got, "light/") {
		t.Fatalf("HSColor.Signature() = %q, want light/ prefix", got)
	}
	if !strings.HasSuffix(got, "/HSCOLOR") {
		t.Fatalf("HSColor.Signature() = %q, want /HSCOLOR suffix", got)
	}
}

func TestTimerSignature(t *testing.T) {
	t.Parallel()
	timer := NewTimer("VCU:1", nil, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	got := timer.Signature()
	if !strings.HasPrefix(got, "switch/") {
		t.Fatalf("Timer.Signature() = %q, want switch/ prefix", got)
	}
	if !strings.Contains(got, string(ParameterDuration)) {
		t.Fatalf("Timer.Signature() = %q, want DURATION in it", got)
	}
}

func TestLevelCombinedSignature(t *testing.T) {
	t.Parallel()
	lc := NewLevelCombined("VCU:1", nil, hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	got := lc.Signature()
	const want = "cover//LEVEL_COMBINED"
	if got != want {
		t.Fatalf("LevelCombined.Signature() = %q, want %q", got, want)
	}
}
