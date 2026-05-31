// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// dispatcher_converters_test.go covers toFloat64, toInt32, paramTime,
// dispatchLight set_level branches, and dispatchSiren / dispatchTextDisplay.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ============================================================
// toFloat64 tests
// ============================================================

func TestToFloat64Float64(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(float64(3.14))
	if err != nil || f != 3.14 {
		t.Fatalf("toFloat64(float64) = %v, %v", f, err)
	}
}

func TestToFloat64Float32(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(float32(1.5))
	if err != nil || f != float64(float32(1.5)) {
		t.Fatalf("toFloat64(float32) = %v, %v", f, err)
	}
}

func TestToFloat64Int(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(int(7))
	if err != nil || f != 7.0 {
		t.Fatalf("toFloat64(int) = %v, %v", f, err)
	}
}

func TestToFloat64Int32(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(int32(42))
	if err != nil || f != 42.0 {
		t.Fatalf("toFloat64(int32) = %v, %v", f, err)
	}
}

func TestToFloat64Int64(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(int64(100))
	if err != nil || f != 100.0 {
		t.Fatalf("toFloat64(int64) = %v, %v", f, err)
	}
}

func TestToFloat64JSONNumber(t *testing.T) {
	t.Parallel()
	f, err := toFloat64(json.Number("2.718"))
	if err != nil || f != 2.718 {
		t.Fatalf("toFloat64(json.Number) = %v, %v", f, err)
	}
}

func TestToFloat64String(t *testing.T) {
	t.Parallel()
	f, err := toFloat64("9.99")
	if err != nil || f != 9.99 {
		t.Fatalf("toFloat64(string) = %v, %v", f, err)
	}
}

func TestToFloat64StringBad(t *testing.T) {
	t.Parallel()
	_, err := toFloat64("notanumber")
	if err == nil {
		t.Fatal("expected error for non-numeric string")
	}
}

func TestToFloat64Unsupported(t *testing.T) {
	t.Parallel()
	_, err := toFloat64(struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// ============================================================
// toInt32 tests
// ============================================================

func TestToInt32Float64(t *testing.T) {
	t.Parallel()
	n, err := toInt32(float64(5))
	if err != nil || n != 5 {
		t.Fatalf("toInt32(float64) = %v, %v", n, err)
	}
}

func TestToInt32Float32(t *testing.T) {
	t.Parallel()
	n, err := toInt32(float32(3))
	if err != nil || n != 3 {
		t.Fatalf("toInt32(float32) = %v, %v", n, err)
	}
}

func TestToInt32Int(t *testing.T) {
	t.Parallel()
	n, err := toInt32(int(12))
	if err != nil || n != 12 {
		t.Fatalf("toInt32(int) = %v, %v", n, err)
	}
}

func TestToInt32Int32(t *testing.T) {
	t.Parallel()
	n, err := toInt32(int32(99))
	if err != nil || n != 99 {
		t.Fatalf("toInt32(int32) = %v, %v", n, err)
	}
}

func TestToInt32Int64(t *testing.T) {
	t.Parallel()
	n, err := toInt32(int64(7))
	if err != nil || n != 7 {
		t.Fatalf("toInt32(int64) = %v, %v", n, err)
	}
}

func TestToInt32JSONNumber(t *testing.T) {
	t.Parallel()
	n, err := toInt32(json.Number("42"))
	if err != nil || n != 42 {
		t.Fatalf("toInt32(json.Number) = %v, %v", n, err)
	}
}

func TestToInt32JSONNumberBad(t *testing.T) {
	t.Parallel()
	_, err := toInt32(json.Number("notint"))
	if err == nil {
		t.Fatal("expected error for bad JSON number")
	}
}

func TestToInt32String(t *testing.T) {
	t.Parallel()
	n, err := toInt32("17")
	if err != nil || n != 17 {
		t.Fatalf("toInt32(string) = %v, %v", n, err)
	}
}

func TestToInt32StringBad(t *testing.T) {
	t.Parallel()
	_, err := toInt32("nope")
	if err == nil {
		t.Fatal("expected error for non-numeric string")
	}
}

func TestToInt32Unsupported(t *testing.T) {
	t.Parallel()
	_, err := toInt32(struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// ============================================================
// paramTime tests
// ============================================================

func TestParamTimeMissing(t *testing.T) {
	t.Parallel()
	_, err := paramTime(map[string]any{}, "when")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for missing param, got %v", err)
	}
}

func TestParamTimeNonString(t *testing.T) {
	t.Parallel()
	_, err := paramTime(map[string]any{"when": 42}, "when")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for non-string, got %v", err)
	}
}

func TestParamTimeRFC3339(t *testing.T) {
	t.Parallel()
	when := "2026-01-15T12:00:00Z"
	got, err := paramTime(map[string]any{"when": when}, "when")
	if err != nil {
		t.Fatalf("paramTime RFC3339: %v", err)
	}
	if got.Year() != 2026 {
		t.Errorf("year = %d, want 2026", got.Year())
	}
}

func TestParamTimeRelative(t *testing.T) {
	t.Parallel()
	before := time.Now()
	got, err := paramTime(map[string]any{"when": "+1h"}, "when")
	if err != nil {
		t.Fatalf("paramTime +1h: %v", err)
	}
	if got.Before(before) {
		t.Error("relative +1h must be in the future")
	}
}

func TestParamTimeInvalidFormat(t *testing.T) {
	t.Parallel()
	_, err := paramTime(map[string]any{"when": "not-a-time"}, "when")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for invalid format, got %v", err)
	}
}

// ============================================================
// dispatchLight set_level branches
// ============================================================

func TestDispatchLight_SetLevelStateOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET001", w)
	disp, _ := buildDispatcher(t, "LSET001", "LEVEL", l)

	params := map[string]any{"state": "OFF"}
	if err := disp.InvokeCustomDP(context.Background(), "LSET001", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level state=OFF: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected write call")
	}
	if s.value != 0.0 {
		t.Errorf("OFF should write 0.0, got %v", s.value)
	}
}

func TestDispatchLight_SetLevelStateOnNoBrightness(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET002", w)
	// buildLightDP seeded LEVEL=0.8 (on). Push LEVEL=0 so the
	// subsequent set_level{state:"ON"} actually crosses the on/off
	// boundary — IsStateChangeFull suppresses the wire write when
	// the light is already on at the lastLevel.
	if d := l.Float; d != nil {
		d.OnEvent(0)
	}
	disp, _ := buildDispatcher(t, "LSET002", "LEVEL", l)

	params := map[string]any{"state": "ON"}
	if err := disp.InvokeCustomDP(context.Background(), "LSET002", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level state=ON: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected write call for turn_on")
	}
}

func TestDispatchLight_SetLevelStateOnWithBrightness(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET003", w)
	disp, _ := buildDispatcher(t, "LSET003", "LEVEL", l)

	params := map[string]any{"state": "on", "brightness": float64(128)}
	if err := disp.InvokeCustomDP(context.Background(), "LSET003", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level state=on+brightness: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected write call")
	}
	// 128/255 ≈ 0.502
	if s.value.(float64) < 0.4 || s.value.(float64) > 0.6 {
		t.Errorf("brightness 128 → level should be ~0.5, got %v", s.value)
	}
}

func TestDispatchLight_SetLevelBrightnessOnly(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET004", w)
	disp, _ := buildDispatcher(t, "LSET004", "LEVEL", l)

	params := map[string]any{"brightness": float64(255)}
	if err := disp.InvokeCustomDP(context.Background(), "LSET004", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level brightness=255: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected write call")
	}
	if s.value != 1.0 {
		t.Errorf("brightness=255 → level=1.0, got %v", s.value)
	}
}

func TestDispatchLight_SetLevelLegacyLevel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET005", w)
	disp, _ := buildDispatcher(t, "LSET005", "LEVEL", l)

	params := map[string]any{"level": 0.7}
	if err := disp.InvokeCustomDP(context.Background(), "LSET005", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level level=0.7: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected write call")
	}
	if s.value != 0.7 {
		t.Errorf("level=0.7 → wrote %v", s.value)
	}
}

func TestDispatchLight_SetLevelNoPayload(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LSET006", w)
	disp, _ := buildDispatcher(t, "LSET006", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "LSET006", "LEVEL", "set_level", map[string]any{}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for empty set_level, got %v", err)
	}
}

// ============================================================
// dispatchSiren additional branches
// ============================================================

func TestDispatchSiren_TurnOnWithDurationExtra(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN001", w)
	disp, _ := buildDispatcher(t, "SRN001", "STATE", carrier)

	params := map[string]any{"duration": "5s"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN001", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("siren turn_on with duration: %v", err)
	}
}

func TestDispatchSiren_TurnOnWithAcoustic(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN002", w)
	disp, _ := buildDispatcher(t, "SRN002", "STATE", carrier)

	params := map[string]any{"acoustic": "FREQUENCY_RISING"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN002", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("siren turn_on with acoustic: %v", err)
	}
}

func TestDispatchSiren_TurnOnWithOptical(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN003", w)
	disp, _ := buildDispatcher(t, "SRN003", "STATE", carrier)

	params := map[string]any{"optical": "BLINKING_RED"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN003", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("siren turn_on with optical: %v", err)
	}
}

func TestDispatchSiren_TurnOnBadDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN004", w)
	disp, _ := buildDispatcher(t, "SRN004", "STATE", carrier)

	params := map[string]any{"duration": "not-a-duration"}
	err := disp.InvokeCustomDP(context.Background(), "SRN004", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad duration, got %v", err)
	}
}

func TestDispatchSiren_TurnOnBadAcoustic(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN005", w)
	disp, _ := buildDispatcher(t, "SRN005", "STATE", carrier)

	params := map[string]any{"acoustic": struct{}{}}
	err := disp.InvokeCustomDP(context.Background(), "SRN005", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad acoustic, got %v", err)
	}
}

func TestDispatchSiren_TurnOnBadOptical(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN006", w)
	disp, _ := buildDispatcher(t, "SRN006", "STATE", carrier)

	params := map[string]any{"optical": struct{}{}}
	err := disp.InvokeCustomDP(context.Background(), "SRN006", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad optical, got %v", err)
	}
}

func TestDispatchSiren_UnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN007", w)
	disp, _ := buildDispatcher(t, "SRN007", "STATE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "SRN007", "STATE", "invalid_op", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// dispatchTextDisplay additional branches
// ============================================================

func TestDispatchTextDisplay_WriteWithSound(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT001", w)
	disp, _ := buildDispatcher(t, "TXT001", "STATE", carrier)

	params := map[string]any{
		"id":    float64(1),
		"text":  "hello",
		"sound": "alarm",
	}
	if err := disp.InvokeCustomDP(context.Background(), "TXT001", "STATE", "write", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("textdisplay write with sound: %v", err)
	}
}

func TestDispatchTextDisplay_WriteNoSound(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT002", w)
	disp, _ := buildDispatcher(t, "TXT002", "STATE", carrier)

	params := map[string]any{
		"id":   float64(1),
		"text": "hello",
	}
	if err := disp.InvokeCustomDP(context.Background(), "TXT002", "STATE", "write", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("textdisplay write without sound: %v", err)
	}
}

func TestDispatchTextDisplay_WriteMissingID(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT003", w)
	disp, _ := buildDispatcher(t, "TXT003", "STATE", carrier)

	params := map[string]any{"text": "hello"}
	err := disp.InvokeCustomDP(context.Background(), "TXT003", "STATE", "write", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for missing id, got %v", err)
	}
}

func TestDispatchTextDisplay_ClearExtra(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT004", w)
	disp, _ := buildDispatcher(t, "TXT004", "STATE", carrier)

	params := map[string]any{"id": float64(2)}
	if err := disp.InvokeCustomDP(context.Background(), "TXT004", "STATE", "clear", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("textdisplay clear: %v", err)
	}
}

func TestDispatchTextDisplay_ClearMissingID(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT005", w)
	disp, _ := buildDispatcher(t, "TXT005", "STATE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "TXT005", "STATE", "clear", map[string]any{}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for missing id, got %v", err)
	}
}

func TestDispatchTextDisplay_UnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT006", w)
	disp, _ := buildDispatcher(t, "TXT006", "STATE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "TXT006", "STATE", "blink", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

func TestDispatchTextDisplay_WriteWithBadSoundOptions(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT007", w)
	disp, _ := buildDispatcher(t, "TXT007", "STATE", carrier)

	params := map[string]any{
		"id":    float64(1),
		"sound": 99, // not a string → ErrBadParam
	}
	err := disp.InvokeCustomDP(context.Background(), "TXT007", "STATE", "write", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad sound, got %v", err)
	}
}

func TestDispatchTextDisplay_WriteWithBadTextType(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT008", w)
	disp, _ := buildDispatcher(t, "TXT008", "STATE", carrier)

	params := map[string]any{
		"id":   float64(1),
		"text": 42, // not a string
	}
	err := disp.InvokeCustomDP(context.Background(), "TXT008", "STATE", "write", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, handlers.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad text type, got %v", err)
	}
}
