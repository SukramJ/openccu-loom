// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// datapoint.go — containsOptionalKey / specialContains
// ---------------------------------------------------------------------------

func TestContainsOptionalKey_Present(t *testing.T) {
	t.Parallel()
	// special string that contains "OPTIONAL"
	if !containsOptionalKey(`["OPTIONAL","FOO"]`) {
		t.Error("expected true when OPTIONAL is present")
	}
}

func TestContainsOptionalKey_Absent(t *testing.T) {
	t.Parallel()
	if containsOptionalKey(`["FOO","BAR"]`) {
		t.Error("expected false when OPTIONAL is absent")
	}
}

func TestContainsOptionalKey_TooShort(t *testing.T) {
	t.Parallel()
	if containsOptionalKey("OPT") {
		t.Error("expected false for too-short string")
	}
}

func TestSpecialContains_Found(t *testing.T) {
	t.Parallel()
	if !specialContains("hello world", "world") {
		t.Error("expected true")
	}
}

func TestSpecialContains_NotFound(t *testing.T) {
	t.Parallel()
	if specialContains("hello", "xyz") {
		t.Error("expected false")
	}
}

func TestSpecialContains_Empty(t *testing.T) {
	t.Parallel()
	if specialContains("", "x") {
		t.Error("expected false for empty haystack")
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — lastColon
// ---------------------------------------------------------------------------

func TestLastColon_Present(t *testing.T) {
	t.Parallel()
	if idx := lastColon("A:1"); idx != 1 {
		t.Errorf("expected 1, got %d", idx)
	}
}

func TestLastColon_Absent(t *testing.T) {
	t.Parallel()
	if idx := lastColon("NoColon"); idx != -1 {
		t.Errorf("expected -1, got %d", idx)
	}
}

func TestLastColon_MultipleColons(t *testing.T) {
	t.Parallel()
	if idx := lastColon("a:b:c"); idx != 3 {
		t.Errorf("expected 3, got %d", idx)
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — DataPointKey nil receiver
// ---------------------------------------------------------------------------

func TestDataPointKey_NilReceiver(t *testing.T) {
	t.Parallel()
	var dp *DataPoint[bool]
	key := dp.DataPointKey()
	if key.Parameter != "" || key.ChannelAddress != "" {
		t.Errorf("expected zero DataPointKey for nil dp, got %+v", key)
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — IsWritable when forced sensor
// ---------------------------------------------------------------------------

func TestIsWritable_ForcedSensor_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent)
	dp := NewDataPoint[bool](cfg)
	dp.MarkForcedSensor()
	if dp.IsWritable() {
		t.Error("expected IsWritable=false when forced sensor")
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — Category when forced sensor
// ---------------------------------------------------------------------------

func TestCategory_ForcedSensor_ReturnsSensor(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := NewDataPoint[bool](cfg)
	dp.MarkForcedSensor()
	if got := dp.Category(); got != hmenum.DataPointCategorySensor {
		t.Errorf("expected Sensor, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — OnRemoved unsubscribe path (covers idx < len guard)
// ---------------------------------------------------------------------------

func TestOnRemoved_UnsubscribeBeforeNotify(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead))
	called := false
	unsub := dp.OnRemoved(func() { called = true })
	unsub() // unsubscribe before NotifyRemoved
	dp.NotifyRemoved()
	if called {
		t.Error("callback should not fire after unsubscribe")
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — UpdateStatusFromWire various paths
// ---------------------------------------------------------------------------

func TestUpdateStatusFromWire_Int32InValueList(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	dp := NewDataPoint[bool](cfg)
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL", "UNKNOWN", "ERROR"})
	// int32 path that maps to a valid ParameterStatus.
	dp.UpdateStatusFromWire(int32(0))
	got, _ := dp.Status()
	if got != hmenum.ParameterStatusNormal {
		t.Errorf("expected NORMAL, got %v", got)
	}
}

func TestUpdateStatusFromWire_Int64InValueList(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	dp := NewDataPoint[bool](cfg)
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL"})
	dp.UpdateStatusFromWire(int64(0))
	got, _ := dp.Status()
	if got != hmenum.ParameterStatusNormal {
		t.Errorf("expected NORMAL, got %v", got)
	}
}

func TestUpdateStatusFromWire_IntInValueList(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	dp := NewDataPoint[bool](cfg)
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL"})
	dp.UpdateStatusFromWire(int(0))
	got, _ := dp.Status()
	if got != hmenum.ParameterStatusNormal {
		t.Errorf("expected NORMAL, got %v", got)
	}
}

func TestUpdateStatusFromWire_StringPath(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	dp := NewDataPoint[bool](cfg)
	dp.UpdateStatusFromWire("OVERFLOW")
	got, _ := dp.Status()
	if got != hmenum.ParameterStatusOverflow {
		t.Errorf("expected OVERFLOW, got %v", got)
	}
}

func TestUpdateStatusFromWire_UnknownStringIsNoop(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	dp := NewDataPoint[bool](cfg)
	dp.UpdateStatusFromWire("NOT_A_STATUS")
	// Status stays at the unset default ("").
	got, set := dp.Status()
	if set && got != "" {
		t.Errorf("expected unset status, got %v (set=%v)", got, set)
	}
}

// ---------------------------------------------------------------------------
// datapoint.go — RawValue unconfirmed path
// ---------------------------------------------------------------------------

func TestRawValue_WithUnconfirmedValue(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := NewDataPoint[float64](cfg)
	dp.WriteUnconfirmedValue(42.0, time.Time{})
	raw, ok := dp.RawValue()
	if !ok {
		t.Fatal("expected ok=true for unconfirmed value")
	}
	if v, _ := raw.(float64); v != 42.0 {
		t.Errorf("expected 42.0, got %v", raw)
	}
}
