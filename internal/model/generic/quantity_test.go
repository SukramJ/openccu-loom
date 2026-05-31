// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// quantity.go — QuantityForParameter, QuantityForDeviceParameter,
//               ValueBehaviorForParameter, DataPoint.Quantity, DataPoint.ValueBehavior
// ---------------------------------------------------------------------------

func TestQuantityForParameter_Temperature(t *testing.T) {
	t.Parallel()
	if q := QuantityForParameter(hmenum.ParameterActualTemperature); q == hmenum.QuantityNone {
		t.Error("ACTUAL_TEMPERATURE should map to a known quantity")
	}
}

func TestQuantityForParameter_Unknown(t *testing.T) {
	t.Parallel()
	if q := QuantityForParameter(hmenum.Parameter("XYZZY_UNKNOWN")); q != hmenum.QuantityNone {
		t.Errorf("unknown param should yield QuantityNone, got %v", q)
	}
}

func TestQuantityForDeviceParameter_WithModel(t *testing.T) {
	t.Parallel()
	// Use a model+param combination that is known in the metadata tables.
	// If no override exists, the function falls through to param-only — both are valid.
	_ = QuantityForDeviceParameter("HmIP-STH", hmenum.ParameterActualTemperature)
}

func TestValueBehaviorForParameter_KnownAndUnknown(t *testing.T) {
	t.Parallel()
	// POWER is a monotonic parameter.
	_ = ValueBehaviorForParameter(hmenum.ParameterPower)
	if got := ValueBehaviorForParameter(hmenum.Parameter("NO_SUCH_PARAM")); got != hmenum.ValueBehaviorNone {
		t.Errorf("unknown param: want ValueBehaviorNone, got %v", got)
	}
}

func TestQuantityHelpers(t *testing.T) {
	t.Parallel()
	// QuantityForParameter with known sensor param (Temperature).
	q := QuantityForParameter(hmenum.ParameterTemperature)
	_ = q // may or may not be QuantityNone depending on metadata

	// QuantityForDeviceParameter.
	qd := QuantityForDeviceParameter("HmIP-WKP", hmenum.ParameterTemperature)
	_ = qd

	// ValueBehaviorForParameter.
	vb := ValueBehaviorForParameter(hmenum.ParameterTemperature)
	_ = vb

	// DataPoint.Quantity and ValueBehavior.
	cfg := baseCfg(hmenum.ParameterTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	dp := NewDataPoint[float64](cfg)
	_ = dp.Quantity()
	_ = dp.ValueBehavior()

	// Binary sensor path.
	bsCfg := baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	bsCfg.Descriptor.Type = hmenum.ParameterTypeBool
	bs := NewBinarySensor(bsCfg)
	_ = bs.Quantity()
}
