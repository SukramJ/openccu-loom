// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeModulating builds a Modulating valve backed by a real *generic.Float.
func makeModulating(t *testing.T) *valve.Modulating {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0001"})
	ch := d.AddChannel("MOD0001:4", 4, "VALVE", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	v := custom.FloatField(ch, hmenum.ParameterLevel)
	if v == nil {
		t.Fatal("FloatField returned nil — channel missing LEVEL DP")
	}
	return valve.NewModulating(ch)
}

// TestModulatingIsStateChangeReturnsTrueWhenUnobserved verifies that
// IsStateChange always returns true before the first LEVEL observation.
func TestModulatingIsStateChangeReturnsTrueWhenUnobserved(t *testing.T) {
	t.Parallel()

	v := makeModulating(t)
	if v == nil {
		t.Skip("Modulating nil — LEVEL DP not attached")
	}
	if !v.IsStateChange(0.5) {
		t.Error("IsStateChange(0.5) before observation = false, want true")
	}
}

// TestModulatingIsStateChangeReturnsFalseWhenSameValue verifies that
// differences smaller than 0.005 are treated as equal and IsStateChange
// returns false.
func TestModulatingIsStateChangeReturnsFalseWhenSameValue(t *testing.T) {
	t.Parallel()

	v := makeModulating(t)
	if v == nil {
		t.Skip("Modulating nil — LEVEL DP not attached")
	}
	// Push current level = 0.5.
	v.OnLevel(0.5)

	// Delta = 0.002 < 0.005: should be treated as no change.
	if v.IsStateChange(0.502) {
		t.Error("IsStateChange(0.502) when current=0.5 (delta<0.005) = true, want false")
	}
}

// TestModulatingIsStateChangeReturnsTrueWhenDifferentValue verifies that
// differences >= 0.005 trigger a state change.
func TestModulatingIsStateChangeReturnsTrueWhenDifferentValue(t *testing.T) {
	t.Parallel()

	v := makeModulating(t)
	if v == nil {
		t.Skip("Modulating nil — LEVEL DP not attached")
	}
	v.OnLevel(0.5)

	// Delta = 0.01 >= 0.005: should report a change.
	if !v.IsStateChange(0.51) {
		t.Error("IsStateChange(0.51) when current=0.5 (delta>=0.005) = false, want true")
	}
}

// TestModulatingIsStateChangeExactBoundary verifies the 0.005 boundary
// precisely: 0.005 is a change, 0.0049 is not.
func TestModulatingIsStateChangeExactBoundary(t *testing.T) {
	t.Parallel()

	v := makeModulating(t)
	if v == nil {
		t.Skip("Modulating nil — LEVEL DP not attached")
	}
	v.OnLevel(0.0)

	if v.IsStateChange(0.0049) {
		t.Error("IsStateChange(0.0049) with current=0.0 = true, want false (delta<0.005)")
	}
	if !v.IsStateChange(0.005) {
		t.Error("IsStateChange(0.005) with current=0.0 = false, want true (delta>=0.005)")
	}
}
