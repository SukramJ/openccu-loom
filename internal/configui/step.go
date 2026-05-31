// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ParameterStep returns the recommended UI step size for numeric
// parameters. The rules mirror parameter_tools.py:get_parameter_step:
//
//   - INTEGER → 1 (always)
//   - FLOAT, range ≤ 7     → 0.5
//   - FLOAT, range ≤ 100   → 1.0
//   - FLOAT, range > 100   → range/100 rounded to one decimal
//   - all other types      → nil (no step hint)
//
// Returns nil when the type is non-numeric or the bounds are missing.
func ParameterStep(desc hmproto.ParameterData) any {
	switch desc.Type {
	case hmenum.ParameterTypeInteger:
		return 1

	case hmenum.ParameterTypeAction,
		hmenum.ParameterTypeBool,
		hmenum.ParameterTypeDummy,
		hmenum.ParameterTypeEnum,
		hmenum.ParameterTypeString,
		hmenum.ParameterTypeEmpty:
		// Non-numeric types have no step hint.
		return nil

	case hmenum.ParameterTypeFloat:
		minVal := decodeRaw(desc.Min)
		maxVal := decodeRaw(desc.Max)
		minF, minOK := toFloat64(minVal)
		maxF, maxOK := toFloat64(maxVal)
		if !minOK || !maxOK {
			return 0.5
		}
		r := maxF - minF
		switch {
		case r <= 7:
			return 0.5
		case r <= 100:
			return 1.0
		default:
			// round to one decimal
			return roundOneDecimal(r / 100)
		}
	}
	return nil
}

// roundOneDecimal rounds v to one decimal place.
func roundOneDecimal(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
