// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// quantity.go — DataPoint.Quantity read chain. The classification tables it
// reads live in internal/parameter and are pinned there; these cases pin the
// chain's stage order and its field-wise fallthrough.
// ---------------------------------------------------------------------------

// TestDataPointQuantityParamOnly covers stage 3: no device model, a
// parameter the classification knows.
func TestDataPointQuantityParamOnly(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "ACTUAL_TEMPERATURE", "")
	if got := dp.Quantity(); got != hmenum.QuantityTemperature {
		t.Fatalf("Quantity()=%q, want temperature for ACTUAL_TEMPERATURE", got)
	}
}

// TestDataPointQuantityUnknown covers the miss: an unclassified parameter
// with no unit yields no quantity rather than a guess.
func TestDataPointQuantityUnknown(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "XYZZY_UNKNOWN", "")
	if got := dp.Quantity(); got != hmenum.QuantityNone {
		t.Fatalf("Quantity()=%q, want QuantityNone for an unclassified parameter", got)
	}
}

// TestDataPointQuantityDeviceOverride covers stage 2: the per-model overlay
// wins over the param-only answer.
func TestDataPointQuantityDeviceOverride(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "CODE_STATE", "HmIP-WKP")
	if got := dp.Quantity(); got != hmenum.QuantityEnum {
		t.Fatalf("Quantity()=%q, want enum for HmIP-WKP.CODE_STATE", got)
	}
}

// TestDataPointQuantityUnitFallback covers stage 4: an unclassified
// parameter still resolves through its unit.
func TestDataPointQuantityUnitFallback(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "SOME_UNKNOWN_TEMP", "")
	dp.Descriptor.Unit = "°C"
	if got := dp.Quantity(); got != hmenum.QuantityTemperature {
		t.Fatalf("Quantity()=%q, want temperature via the °C unit fallback", got)
	}
}

// TestDataPointQuantityFieldWiseFallthrough pins the field-wise chain: the
// HM-CC-RT-DN.VALVE_STATE override classifies only the value behavior, so
// the search for a quantity must continue past it instead of stopping on a
// record that has one field set.
func TestDataPointQuantityFieldWiseFallthrough(t *testing.T) {
	t.Parallel()
	dp := buildTestDataPoint[float64](t, "VALVE_STATE", "HM-CC-RT-DN")
	dp.Descriptor.Unit = "%"
	if got := dp.Quantity(); got != hmenum.QuantityNone {
		t.Fatalf("Quantity()=%q, want QuantityNone: neither the override nor %% carries a quantity", got)
	}
	if got := dp.ValueBehavior(); got != hmenum.ValueBehaviorInstantaneous {
		t.Fatalf("ValueBehavior()=%q, want INSTANTANEOUS from the device override", got)
	}
}

// TestBinarySensorQuantityUsesTheBinaryChain pins that a BinarySensor
// resolves through the binary-sensor tables, not the sensor ones.
func TestBinarySensorQuantityUsesTheBinaryChain(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Kind = KindBinarySensor
	if got := NewBinarySensor(cfg).Quantity(); got != hmenum.QuantityMotion {
		t.Fatalf("Quantity()=%q, want motion for a MOTION binary sensor", got)
	}
}
