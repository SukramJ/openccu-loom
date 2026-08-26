// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// intDescNoRange builds an INTEGER parameter description with no
// MIN/MAX bound, so a coerced value is judged purely on whether it fits
// in the platform int type, not on a declared range.
func intDescNoRange() hmproto.ParameterData {
	return hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsWrite,
	}
}

// TestCoerceIntBoundaryValuesRoundTrip pins that the platform int
// boundary values — not just values comfortably inside it — are
// accepted for both the int64 and json.Number input shapes. It is the
// regression guard for the overflow check added alongside the existing
// uint/uint64 guards in asInt: math.MaxInt/MinInt equal
// math.MaxInt64/MinInt64 on the 64-bit platform this suite runs on, so
// the guard can only be observed rejecting a value on a 32-bit build
// (armv7, the shipped add-on target) where int is 32 bits — this test
// instead locks that the guard's boundary is inclusive and does not
// reject a legitimate value at the edge.
func TestCoerceIntBoundaryValuesRoundTrip(t *testing.T) {
	desc := intDescNoRange()
	values := []int64{math.MinInt, math.MaxInt, 0, -1, 1}

	for _, v := range values {
		got, err := parameter.Coerce(desc, v)
		if err != nil {
			t.Errorf("Coerce(int64(%d)) unexpected error: %v", v, err)
			continue
		}
		if want := hmtypes.IntValue(int(v)); !got.Equal(want) {
			t.Errorf("Coerce(int64(%d)) = %v, want %v", v, got, want)
		}
	}

	for _, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%d): %v", v, err)
		}
		got, err := parameter.Coerce(desc, json.Number(raw))
		if err != nil {
			t.Errorf("Coerce(json.Number(%d)) unexpected error: %v", v, err)
			continue
		}
		if want := hmtypes.IntValue(int(v)); !got.Equal(want) {
			t.Errorf("Coerce(json.Number(%d)) = %v, want %v", v, got, want)
		}
	}
}
