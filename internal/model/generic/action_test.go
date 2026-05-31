// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// action.go — Trigger
// ---------------------------------------------------------------------------

func TestActionTrigger(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterResetMotion, hmenum.ParameterTypeAction, hmenum.OperationsWrite)
	cfg.Writer = w
	a := NewAction(cfg)
	if err := a.Trigger(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	call, _ := w.last()
	if call.value != true {
		t.Fatalf("value=%v, want true", call.value)
	}
}

// ---------------------------------------------------------------------------
// action_number.go — ActionInteger Trigger, range check
// ---------------------------------------------------------------------------

func TestActionIntegerTrigger(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsWrite)
	cfg.Descriptor.Min = json.RawMessage("0")
	cfg.Descriptor.Max = json.RawMessage("10")
	cfg.Writer = w
	a := NewActionInteger(cfg)

	if err := a.Trigger(context.Background(), 5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Trigger(5): %v", err)
	}
	call, _ := w.last()
	if call.value != int32(5) {
		t.Fatalf("value=%v, want 5", call.value)
	}

	// Out-of-range.
	if err := a.Trigger(context.Background(), 99, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("out-of-range must error")
	}
}

// ---------------------------------------------------------------------------
// action_boolean.go — ActionBoolean Trigger no-writer path
// ---------------------------------------------------------------------------

func TestActionBooleanTriggerNoWriter(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterResetMotion, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	// No Writer.
	a := NewActionBoolean(cfg)
	if err := a.Trigger(context.Background(), true, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("no writer must return error")
	}
}

// ---------------------------------------------------------------------------
// action_string.go — ActionString Trigger no-writer / not-writable paths
// ---------------------------------------------------------------------------

func TestActionStringTriggerErrors(t *testing.T) {
	t.Parallel()
	// Not writable.
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsRead)
	cfg.Writer = &stubWriter{}
	a := NewActionString(cfg)
	if err := a.Trigger(context.Background(), "hi", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("read-only string action must error")
	}

	// No writer.
	cfg2 := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsWrite)
	a2 := NewActionString(cfg2)
	if err := a2.Trigger(context.Background(), "hi", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("no writer must error")
	}
}

// ---------------------------------------------------------------------------
// number.go — Float DescriptorRange / DescriptorMin / DescriptorMax
// ---------------------------------------------------------------------------

func TestFloatDescriptorHelpers(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterSetTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.Min = json.RawMessage("4.5")
	cfg.Descriptor.Max = json.RawMessage("30.5")
	f := NewFloat(cfg)

	lo, hi, ok := f.DescriptorRange()
	if !ok || lo != 4.5 || hi != 30.5 {
		t.Fatalf("DescriptorRange = %v, %v, %v", lo, hi, ok)
	}

	minVal, ok2 := f.DescriptorMin()
	if !ok2 || minVal != 4.5 {
		t.Fatalf("DescriptorMin = %v, %v", minVal, ok2)
	}

	maxVal, ok3 := f.DescriptorMax()
	if !ok3 || maxVal != 30.5 {
		t.Fatalf("DescriptorMax = %v, %v", maxVal, ok3)
	}

	// No descriptor → ok=false.
	f2 := NewFloat(baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	_, _, ok4 := f2.DescriptorRange()
	if ok4 {
		t.Fatal("empty descriptor must return ok=false")
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — AdditionalInformation, UpdateStatusFromWire, OnStatusChange
// ---------------------------------------------------------------------------

func TestDataPointAdditionalInformation(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	dp := NewDataPoint[float64](cfg)
	m := dp.AdditionalInformation()
	// May be nil or empty — just ensure it doesn't panic.
	_ = m
}

func TestDataPointUpdateStatusFromWire(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatusFromWire(hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead,
	})
	// Should not panic; coverage exercised by call.
}

func TestDataPointOnStatusChangeUnsubscribe(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	var n int
	unsub := dp.OnStatusChange(func(_, _ hmenum.ParameterStatus) { n++ })
	dp.UpdateStatus(hmenum.ParameterStatusNormal)
	unsub()
	unsub() // idempotent
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)
	if n != 1 {
		t.Fatalf("after unsub fired %d times, want 1", n)
	}
}
