// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// matter.go — MatterFloatValue
// ---------------------------------------------------------------------------

func TestMatterFloatValue_NilSensor_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var s *Sensor[float64]
	v, ok := s.MatterFloatValue()
	if ok || v != 0 {
		t.Errorf("nil sensor: expected (0, false), got (%v, %v)", v, ok)
	}
}

func TestMatterFloatValue_NoValue_ReturnsFalse(t *testing.T) {
	t.Parallel()
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature,
		hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	v, ok := s.MatterFloatValue()
	if ok || v != 0 {
		t.Errorf("unobserved: expected (0, false), got (%v, %v)", v, ok)
	}
}

func TestMatterFloatValue_WithValue_ReturnsFloat(t *testing.T) {
	t.Parallel()
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature,
		hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	s.OnEvent(22.5)
	v, ok := s.MatterFloatValue()
	if !ok || v != 22.5 {
		t.Errorf("expected (22.5, true), got (%v, %v)", v, ok)
	}
}

func TestMatterFloatValue_IntegerSensor_ReturnsFloat(t *testing.T) {
	t.Parallel()
	s := NewIntegerSensor(baseCfg(hmenum.ParameterHumidity,
		hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent))
	s.OnEvent(int32(55))
	v, ok := s.MatterFloatValue()
	if !ok || v != 55.0 {
		t.Errorf("expected (55.0, true), got (%v, %v)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// matter.go — MatterBoolValue
// ---------------------------------------------------------------------------

func TestMatterBoolValue_NilBinarySensor_ReturnsFalse(t *testing.T) {
	t.Parallel()
	var b *BinarySensor
	v, ok := b.MatterBoolValue()
	if ok || v {
		t.Errorf("nil binary sensor: expected (false, false), got (%v, %v)", v, ok)
	}
}

func TestMatterBoolValue_NoValue_ReturnsFalse(t *testing.T) {
	t.Parallel()
	bs := NewBinarySensor(baseCfg(hmenum.ParameterMotion,
		hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	v, ok := bs.MatterBoolValue()
	if ok || v {
		t.Errorf("unobserved: expected (false, false), got (%v, %v)", v, ok)
	}
}

func TestMatterBoolValue_True_ReturnsTrue(t *testing.T) {
	t.Parallel()
	bs := NewBinarySensor(baseCfg(hmenum.ParameterMotion,
		hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	bs.OnEvent(true)
	v, ok := bs.MatterBoolValue()
	if !ok || !v {
		t.Errorf("expected (true, true), got (%v, %v)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// matter.go — Switch.OnMatterValueChanged
// ---------------------------------------------------------------------------

func TestSwitch_OnMatterValueChanged_NilSwitch_Safe(t *testing.T) {
	t.Parallel()
	var s *Switch
	unsub := s.OnMatterValueChanged(func() {})
	unsub() // must not panic
}

func TestSwitch_OnMatterValueChanged_NilCallback_Safe(t *testing.T) {
	t.Parallel()
	sw := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	unsub := sw.OnMatterValueChanged(nil)
	unsub() // must not panic
}

func TestSwitch_OnMatterValueChanged_FiresOnValueChange(t *testing.T) {
	t.Parallel()
	sw := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	count := 0
	_ = sw.OnMatterValueChanged(func() { count++ })
	sw.OnEvent(true)
	sw.OnEvent(false)
	if count < 1 {
		t.Errorf("expected at least 1 callback, got %d", count)
	}
}
