// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package value holds a second, older transcription of the Homematic value
// conversions. Nothing in a running daemon reaches it: measured against the
// tree, no non-test .go file imports this package. Read that as a warning
// label, not as an invitation — it is NOT the home of these rules, and a new
// caller belongs on the live sites named below, not here.
//
// 1. [ToHomematicValue] / [FromHomematicValue] — type-dispatch value
// conversion (bool→int, float rounding, time.Duration→seconds, …). The
// same rule set is written out a second time in internal/parameter, and
// that copy is the fuller one: it also converts the elements of a []any /
// map[string]any payload, resolves a fmt.Stringer, and reports a parse
// failure as an error instead of passing the value through. This copy in
// turn accepts an int64 for a bool target, which the other does not. Neither
// copy has a production caller — the outgoing value of a write is serialised
// by the transport.
// "hex level" → float64 decode for COMBINED_PARAMETER channels. The decode a
// running daemon performs is backends.ParseCombinedParameter, which does not
// agree with this pair on every input; the encode direction lives in
// internal/parameter.ConvertHMLevelToCPV, the single home of the
// LEVEL_COMBINED byte encoding.
//
// The type-dispatch pair mirrors the Python reference `converter.py`
// (`to_homematic_value` / `from_homematic_value`), which is where the rules
// came from — provenance, not authority. The firmware authority for the
// six-decimal float rounding is recorded on internal/parameter.ToHomematicValue.
package value

import (
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ConvertableParameters lists the parameter names whose values go through the
// CPV ↔ level round-trip.
//
// It is a mirror with no production caller. The two declarations a running
// daemon consults are internal/parameter.ConvertableParameters (write path)
// and internal/client/backends.CombinedParameters (callback path); all three
// are held to one membership by TestConvertableParameterSetsAgree in
// tests/contract/. Add a combined parameter to the two live sets — this one
// follows to keep the guard green, it does not gate anything.
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

// roundFloat rounds f to decimals decimal places.
func roundFloat(f float64, decimals int) float64 {
	shift := math.Pow(10, float64(decimals))
	return math.Round(f*shift) / shift
}
