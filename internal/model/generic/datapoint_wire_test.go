// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// DataPoint — RawValue / OnWireValue / OnAnyUpdate
// ---------------------------------------------------------------------------

func TestDataPointRawValue(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	_, ok := dp.RawValue()
	if ok {
		t.Fatal("fresh DP: RawValue ok must be false")
	}
	dp.OnEvent(3.14)
	v, ok := dp.RawValue()
	if !ok || v.(float64) != 3.14 {
		t.Fatalf("after OnEvent: RawValue = %v, %v", v, ok)
	}
}

func TestDataPointOnWireValue(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))

	// Exact type.
	if !dp.OnWireValue(float64(2)) {
		t.Fatal("exact type must return true")
	}
	// Coercible.
	if !dp.OnWireValue(int32(3)) {
		t.Fatal("coercible int32 must return true")
	}
	// nil → false.
	if dp.OnWireValue(nil) {
		t.Fatal("nil must return false")
	}
	// Inconvertible string.
	if dp.OnWireValue("not-a-number") {
		t.Fatal("non-numeric string to float64 must return false")
	}
}

func TestDataPointOnAnyUpdate(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[int32](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead))
	var got []any
	unsub := dp.OnAnyUpdate(func(_, next any) { got = append(got, next) })
	dp.OnEvent(10)
	dp.OnEvent(20)
	unsub()
	dp.OnEvent(30)
	if len(got) != 2 || got[0].(int32) != 10 || got[1].(int32) != 20 {
		t.Fatalf("OnAnyUpdate got=%v", got)
	}

	// nil fn → no panic.
	dp.OnAnyUpdate(nil)
}

// ---------------------------------------------------------------------------
// DataPoint — ModifiedRecently / RefreshedRecently / IsRefreshed
// ---------------------------------------------------------------------------

func TestDataPointModifiedRefreshedRecently(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))

	if dp.ModifiedRecently() {
		t.Fatal("fresh DP: ModifiedRecently must be false")
	}
	if dp.RefreshedRecently() {
		t.Fatal("fresh DP: RefreshedRecently must be false")
	}
	if dp.IsRefreshed() {
		t.Fatal("fresh DP: IsRefreshed must be false")
	}

	dp.OnEvent(true)
	if !dp.ModifiedRecently() {
		t.Fatal("just-received value: ModifiedRecently must be true")
	}
	if !dp.RefreshedRecently() {
		t.Fatal("just-received value: RefreshedRecently must be true")
	}
	if !dp.IsRefreshed() {
		t.Fatal("after OnEvent: IsRefreshed must be true")
	}
}
