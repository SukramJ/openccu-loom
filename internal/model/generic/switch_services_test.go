// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// switch.go — Invoke (registered services: turn_on, turn_off, set)
// ---------------------------------------------------------------------------

func TestSwitch_Invoke_TurnOn(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if err := s.Invoke(context.Background(), "turn_on", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
}

func TestSwitch_Invoke_TurnOff(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if err := s.Invoke(context.Background(), "turn_off", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_off: %v", err)
	}
}

func TestSwitch_Invoke_Set_MissingParam(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	// Passing empty params → paramBool should fail.
	if err := s.Invoke(context.Background(), "set", map[string]any{}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("set service with no 'value' param: expected error")
	}
}

// ---------------------------------------------------------------------------
// switch.go — SetTimerOnTime / GetAndStartTimer / SetOnTime / TurnOnWithTimer
// ---------------------------------------------------------------------------

func TestSwitch_SetTimerOnTime_NegativeClears(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	s.SetTimerOnTime(5 * time.Second)
	s.SetTimerOnTime(-1) // should clear
	_, ok := s.GetAndStartTimer()
	if ok {
		t.Error("negative timer should clear: expected ok=false")
	}
}

func TestSwitch_GetAndStartTimer_AlreadyFiredCase(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	// Manually set up the "already fired" condition:
	// timerOnTimeEnd is in the future, pending is ≤ 0.
	s.timerMu.Lock()
	d := time.Duration(-1)
	s.pending = &d
	s.timerOnTimeEnd = time.Now().Add(10 * time.Minute)
	s.timerMu.Unlock()

	secs, ok := s.GetAndStartTimer()
	if !ok || secs != -1 {
		t.Errorf("already-fired case: want (-1, true), got (%v, %v)", secs, ok)
	}
}

func TestSwitch_SetOnTime_NoWriter(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	// Writer is nil → ErrNoWriter
	if err := s.SetOnTime(context.Background(), 5*time.Second, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected ErrNoWriter")
	}
}

func TestSwitch_SetOnTime_NegativeClampsToZero(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	// Negative duration → should clamp to 0 and succeed (not panic).
	if err := s.SetOnTime(context.Background(), -5e9, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetOnTime negative: %v", err)
	}
	last, ok := w.last()
	if !ok {
		t.Fatal("expected a SetValue call")
	}
	if v, _ := last.value.(float64); v != 0 {
		t.Errorf("negative duration should clamp to 0 seconds, got %v", last.value)
	}
}

func TestSwitch_TurnOnWithTimer_UsesParamsetWriter(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	pw := &switchParamsetWriter{}
	s.Writer = pw
	if err := s.TurnOnWithTimer(context.Background(), 5*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWithTimer with ParamsetWriter: %v", err)
	}
	if pw.calls != 1 {
		t.Errorf("expected 1 PutParamset call, got %d", pw.calls)
	}
}

func TestSwitch_TurnOnWithTimer_FallbackNoParamsetWriter(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	// stubWriter does NOT implement ParamsetWriter → fallback path.
	if err := s.TurnOnWithTimer(context.Background(), 3e9, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWithTimer fallback: %v", err)
	}
}

// ---------------------------------------------------------------------------
// switch.go — Set not writable, WriterAsBackend, NewSwitch with ParamsetWriter
// ---------------------------------------------------------------------------

func TestSwitchSetNotWritable(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Writer = &stubWriter{}
	sw := NewSwitch(cfg)
	if err := sw.Set(context.Background(), true, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("read-only switch.Set must error")
	}
}

func TestWriterAsBackend(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	backend := WriterAsBackend(w)
	if backend == nil {
		t.Fatal("WriterAsBackend must not return nil")
	}
	// SetValue via the backend.
	err := backend.SetValue(context.Background(), "CH:1", hmenum.ParameterState, true, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	// PutParamset falls through to writer (no paramset support on stubWriter).
	_ = backend.PutParamset(context.Background(), "CH:1", hmenum.ParamsetKeyValues, map[string]any{"STATE": true}, hmenum.CommandPriorityHigh)
}

func TestNewSwitchWithParamsetWriter(t *testing.T) {
	t.Parallel()
	pw := &switchParamsetWriter{}
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsWrite)
	cfg.Writer = pw
	sw := NewSwitch(cfg)
	if sw == nil {
		t.Fatal("NewSwitch must not return nil with Writer=ParamsetWriter")
	}
}

// switchParamsetWriter satisfies both Writer and ParamsetWriter, used
// to verify the ParamsetWriter fast-path in TurnOnWithTimer.
type switchParamsetWriter struct {
	calls int
}

func (s *switchParamsetWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (s *switchParamsetWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	s.calls++
	return nil
}

// failingParamsetWriter satisfies Writer + ParamsetWriter and fails its
// PutParamset call — used to exercise the optimistic-rollback-on-send-error
// path of the atomic ON_TIME+STATE turn-on.
type failingParamsetWriter struct {
	putErr error
}

func (w *failingParamsetWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (w *failingParamsetWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	return w.putErr
}

// TestSwitchTurnOnWithTimerRollsBackOptimisticOnPutParamsetError pins the
// #3238 fix on the switch side: the atomic ON_TIME+STATE put_paramset stages
// STATE optimistically, so when the CCU rejects the write (e.g. RESPONSE_NAK
// after retries are exhausted) the optimistic value must roll back immediately
// — not linger until the 30s optimistic-update timeout.
func TestSwitchTurnOnWithTimerRollsBackOptimisticOnPutParamsetError(t *testing.T) {
	t.Parallel()
	// EVENT bit required so the optimistic tracker actually stages (a
	// parameter with no CCU echo skips optimistic by design).
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent)
	cfg.Writer = &failingParamsetWriter{putErr: errors.New("RESPONSE_NAK")}
	s := NewSwitch(cfg)
	s.OnEvent(false) // last CCU-confirmed value

	err := s.TurnOnWithTimer(context.Background(), 5*time.Second, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected wire error from PutParamset")
	}
	if s.IsOptimistic() {
		t.Fatal("optimistic STATE must roll back immediately on PutParamset error (#3238), not linger until timeout")
	}
	if v, _ := s.Value(); v != false {
		t.Fatalf("value must revert to last confirmed false, got %v", v)
	}
}
