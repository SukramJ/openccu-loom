// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"math"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DiffResult summarises the comparison of an optimistic-sent value
// against what the CCU actually reports back.
type DiffResult struct {
	// Match is true when the values agree (typed, modulo float tolerance).
	Match bool
	// Kind is the kind of the compared values when both sides agree on
	// kind; ValueKindNone when Mismatch is caused by kind divergence.
	Kind hmtypes.ValueKind
	// RelDiff is the relative numeric difference for float kinds.
	// Zero for non-numeric kinds or when values match exactly.
	RelDiff float64
}

// FloatTolerance is the relative epsilon used by [Diff] when comparing
// float values. CCU firmware rounds floats in transit, so bit-exact
// comparison produces false mismatches.
const FloatTolerance = 1e-6

// Diff compares sent against got and reports whether they effectively
// agree. For floats it uses [FloatTolerance]; for lists it does an
// element-wise comparison.
func Diff(sent, got hmtypes.ParamValue) DiffResult {
	if sent.Kind != got.Kind {
		return DiffResult{Match: false, Kind: hmtypes.ValueKindNone}
	}
	switch sent.Kind {
	case hmtypes.ValueKindNone:
		return DiffResult{Match: true, Kind: hmtypes.ValueKindNone}
	case hmtypes.ValueKindBool:
		return DiffResult{Match: sent.Bool == got.Bool, Kind: hmtypes.ValueKindBool}
	case hmtypes.ValueKindInt:
		return DiffResult{Match: sent.Int == got.Int, Kind: hmtypes.ValueKindInt}
	case hmtypes.ValueKindFloat:
		return diffFloat(sent.Float, got.Float)
	case hmtypes.ValueKindString:
		return DiffResult{Match: sent.String == got.String, Kind: hmtypes.ValueKindString}
	case hmtypes.ValueKindList:
		if len(sent.List) != len(got.List) {
			return DiffResult{Match: false, Kind: hmtypes.ValueKindList}
		}
		for i := range sent.List {
			if sent.List[i] != got.List[i] {
				return DiffResult{Match: false, Kind: hmtypes.ValueKindList}
			}
		}
		return DiffResult{Match: true, Kind: hmtypes.ValueKindList}
	}
	return DiffResult{Match: false}
}

func diffFloat(sent, got float64) DiffResult {
	if sent == got {
		return DiffResult{Match: true, Kind: hmtypes.ValueKindFloat}
	}
	denom := math.Max(1, math.Max(math.Abs(sent), math.Abs(got)))
	rel := math.Abs(sent-got) / denom
	return DiffResult{
		Match:   rel <= FloatTolerance,
		Kind:    hmtypes.ValueKindFloat,
		RelDiff: rel,
	}
}
