// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestGetEventDataFor_UsesSuppliedValue verifies that GetEventDataFor returns
// the explicitly passed value without reading from the DP's internal state.
func TestGetEventDataFor_UsesSuppliedValue(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(false)

	ed := dp.GetEventDataFor(true) // override: pass true even though DP holds false
	if ed.Value != true {
		t.Fatalf("GetEventDataFor: Value = %v, want true", ed.Value)
	}
	if ed.Parameter != "STATE" {
		t.Fatalf("GetEventDataFor: Parameter = %q, want %q", ed.Parameter, "STATE")
	}
}

// TestGetEventData_StillReadsInternalValue verifies the no-arg variant reads
// from the DP's confirmed value.
func TestGetEventData_StillReadsInternalValue(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(true)

	ed := dp.GetEventData()
	if ed.Value != true {
		t.Fatalf("GetEventData: Value = %v, want true", ed.Value)
	}
}

// TestOnEventAt_UsesProvidedTimestamp verifies that OnEventAt stamps the
// refreshedAt and modifiedAt fields with the supplied time rather than
// wall-clock time.
func TestOnEventAt_UsesProvidedTimestamp(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))

	fixed := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	dp.OnEventAt(3.14, fixed)

	v, ok := dp.Value()
	if !ok || v != 3.14 {
		t.Fatalf("OnEventAt: Value = %v observed=%v, want 3.14 true", v, ok)
	}
	if got := dp.RefreshedAt(); !got.Equal(fixed) {
		t.Fatalf("OnEventAt: RefreshedAt = %v, want %v", got, fixed)
	}
	if got := dp.ModifiedAt(); !got.Equal(fixed) {
		t.Fatalf("OnEventAt: ModifiedAt = %v, want %v", got, fixed)
	}
}

// TestOnEventAt_NoModifiedAtBumpOnSameValue confirms that when the new value
// equals the current confirmed value, modifiedAt does not advance.
func TestOnEventAt_NoModifiedAtBumpOnSameValue(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))

	t1 := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Second)
	dp.OnEventAt(1.0, t1)
	dp.OnEventAt(1.0, t2) // same value, later time

	if got := dp.ModifiedAt(); !got.Equal(t1) {
		t.Fatalf("OnEventAt: ModifiedAt should stay at t1=%v, got %v", t1, got)
	}
	if got := dp.RefreshedAt(); !got.Equal(t2) {
		t.Fatalf("OnEventAt: RefreshedAt should advance to t2=%v, got %v", t2, got)
	}
}
