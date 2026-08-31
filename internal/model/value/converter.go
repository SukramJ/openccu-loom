// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package value provides converters between Go-native types and
// Homematic wire values, and between Homematic level values and
// combined-parameter values (CPV) used by BidCos blinds / covers.
//
// The two converter families mirror
// `converter.py`
//
// 1. [ToHomematicValue] / [FromHomematicValue] — type-dispatch value
// conversion (bool→int, float rounding, time.Duration→seconds, …)
// 2. [ConvertCPVToHMLevel] / [ConvertCPVToHMIPLevel] — the BidCos
// "hex level" → float64 decode for COMBINED_PARAMETER channels. The
// encode direction lives in internal/parameter.ConvertHMLevelToCPV,
// the single home of the LEVEL_COMBINED byte encoding.
//
// [ToHomematicValue] (mirrors `to_homematic_value`)
// [FromHomematicValue] (mirrors `from_homematic_value`)
// [ConvertableParameters] (mirrors `CONVERTABLE_PARAMETERS`)
package value

import (
	"fmt"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ConvertableParameters lists the parameter names whose values go through the
// CPV ↔ level round-trip.
var ConvertableParameters = [...]hmenum.Parameter{
	hmenum.ParameterCombinedParameter,
	hmenum.ParameterLevelCombined,
}

// IsConvertableParameter reports whether p is in [ConvertableParameters].
func IsConvertableParameter(p hmenum.Parameter) bool {
	for _, cp := range ConvertableParameters {
		if cp == p {
			return true
		}
	}
	return false
}

// ToHomematicValue converts a Go value to its Homematic wire representation.
//
// - bool → int (true=1, false=0) - float64 → float64 (rounded to 6 decimal
// places) - float32 → float64 (rounded to 6 decimal places) - time.Duration →
// float64 (total seconds) - time.Time → string (RFC3339) - fmt.Stringer →
// string - all others → value unchanged
func ToHomematicValue(v any) any {
	switch val := v.(type) {
	case bool:
		if val {
			return 1
		}
		return 0
	case float64:
		return roundFloat(val, 6)
	case float32:
		return roundFloat(float64(val), 6)
	case time.Duration:
		return val.Seconds()
	case time.Time:
		return val.Format(time.RFC3339)
	}
	return v
}

// FromHomematicValue converts a Homematic wire value to a Go-native type,
// optionally coercing to targetType.
//
// - int/int64 with targetType=bool → bool - string with targetType=time.Time
// → parsed time.Time (RFC3339) - all others → value unchanged
//
// targetType nil means "no coercion". Pass
// reflect.TypeOf((*bool)(nil)).Elem() etc. when coercion is needed, or use
// the typed helpers [BoolFromHM] / [TimeFromHM].
func FromHomematicValue(v any, targetType string) any {
	switch targetType {
	case "bool":
		switch val := v.(type) {
		case int:
			return val != 0
		case int64:
			return val != 0
		case float64:
			return val != 0
		case bool:
			return val
		}
	case "time.Time":
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
			// Try ISO without timezone.
			if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
				return t
			}
		}
	}
	return v
}

// ConvertCPVToHMLevel converts a BidCos CPV hex string back to a
// floating-point level (0..1).
func ConvertCPVToHMLevel(cpv string) (float64, bool) {
	var raw int
	if _, err := fmt.Sscanf(cpv, "%v", &raw); err != nil {
		return 0, false
	}
	return float64(raw) / 100 / 2, true
}

// ConvertCPVToHMIPLevel converts a BidCos CPV integer string to a
// floating-point level for HmIP.
//
// int(value) / 100
func ConvertCPVToHMIPLevel(cpv string) (float64, bool) {
	var raw int
	if _, err := fmt.Sscanf(cpv, "%d", &raw); err != nil {
		return 0, false
	}
	return float64(raw) / 100, true
}

// roundFloat rounds f to decimals decimal places.
func roundFloat(f float64, decimals int) float64 {
	shift := math.Pow(10, float64(decimals))
	return math.Round(f*shift) / shift
}
