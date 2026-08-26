// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestValueBehaviorChainParamOnly verifies stage 2 (param-only map) when
// no device model is set. ENERGY_COUNTER is MONOTONIC in the param map.
func TestValueBehaviorChainParamOnly(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "ENERGY_COUNTER", "")
	if got := dp.ValueBehavior(); got != hmenum.ValueBehaviorMonotonic {
		t.Fatalf("ValueBehavior()=%v, want MONOTONIC for ENERGY_COUNTER", got)
	}
}

// TestValueBehaviorChainDeviceOverride verifies stage 1 (device+param override).
// HM-CC-RT-DN.VALVE_STATE has an entry in sensorMetadataByDeviceAndParam.
func TestValueBehaviorChainDeviceOverride(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "VALVE_STATE", "HM-CC-RT-DN")
	if got := dp.ValueBehavior(); got != hmenum.ValueBehaviorInstantaneous {
		t.Fatalf("ValueBehavior()=%v, want INSTANTANEOUS for HM-CC-RT-DN.VALVE_STATE", got)
	}
}

// TestValueBehaviorChainUnitFallback verifies stage 3 (unit fallback).
// An unknown parameter with unit "°C" should resolve to INSTANTANEOUS via the
// unit map.
func TestValueBehaviorChainUnitFallback(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "SOME_UNKNOWN_TEMP", "")
	dp.Descriptor.Unit = "°C"
	if got := dp.ValueBehavior(); got != hmenum.ValueBehaviorInstantaneous {
		t.Fatalf("ValueBehavior()=%v, want INSTANTANEOUS for unit °C fallback", got)
	}
}

// TestValueBehaviorChainNone verifies that a completely unknown
// parameter + empty unit returns ValueBehaviorNone.
func TestValueBehaviorChainNone(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "UNKNOWN_PARAMETER_XYZ", "")
	if got := dp.ValueBehavior(); got != hmenum.ValueBehaviorNone {
		t.Fatalf("ValueBehavior()=%v, want None for unknown parameter", got)
	}
}

// buildTestDataPoint is a minimal helper that constructs a DataPoint[T]
// with the given parameter name and device model for test use.
func buildTestDataPoint[T comparable](t *testing.T, param, deviceModel string) *DataPoint[T] {
	t.Helper()
	key, err := hmtypes.NewDataPointKey("HmIP-RF", "TEST001:1", hmenum.ParamsetKeyValues, param)
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}
	return NewDataPoint[T](Spec{
		Key:         key,
		Descriptor:  hmproto.ParameterData{},
		DeviceModel: deviceModel,
		CentralName: "c1",
	})
}
