// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"fmt"
	"math"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ConvertableParameters is the set of parameter names whose values
// may carry datetime / duration encodings (combined-parameter strings)
// that require conversion before they are safe to pass to the CCU.
var ConvertableParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterCombinedParameter: {},
	hmenum.ParameterLevelCombined:     {},
}

// IsConvertable reports whether p is a parameter whose values may
// require special conversion via [ConvertHMLevelToCPV].
func IsConvertable(p hmenum.Parameter) bool {
	_, ok := ConvertableParameters[p]
	return ok
}

// ToHomematicValue converts a Go value to a Homematic-compatible wire
// Value. The conversion rules mirror
// singledispatch (converter.py):
//
// - bool → int (1 / 0)
// - float64 → float64 rounded to 6 decimal places
// - time.Time → ISO-8601 string
// - time.Duration → total seconds as float64
// - fmt.Stringer → its String() return value
// - nil → nil
// - anything else → value unchanged
//
// Slices and maps are handled recursively so that paramset payloads
// ([]any, map[string]any) round-trip correctly.
func ToHomematicValue(value any) any {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return math.Round(v*1_000_000) / 1_000_000
	case float32:
		return math.Round(float64(v)*1_000_000) / 1_000_000
	case time.Time:
		return v.Format(time.RFC3339)
	case time.Duration:
		return v.Seconds()
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = ToHomematicValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = ToHomematicValue(item)
		}
		return out
	case fmt.Stringer:
		return v.String()
	default:
		return value
	}
}

// FromHomematicValue converts a Homematic wire value to the idiomatic Go type
// indicated by targetType. When targetType is nil the value is returned
// unchanged.
//
// - int with targetType "bool" → bool - string with targetType "time.Time" →
// time.Time (RFC 3339 / ISO) - anything else → value unchanged
func FromHomematicValue(value any, targetType string) (any, error) {
	if targetType == "" {
		return value, nil
	}
	switch targetType {
	case "bool":
		switch v := value.(type) {
		case int:
			return v != 0, nil
		case float64:
			return v != 0, nil
		case bool:
			return v, nil
		default:
			return value, nil
		}
	case "time.Time":
		s, ok := value.(string)
		if !ok {
			return value, nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			// Try ISO 8601 without timezone
			t, err = time.Parse("2006-01-02T15:04:05", s)
			if err != nil {
				return nil, fmt.Errorf("converter: cannot parse %q as time: %w", s, err)
			}
		}
		return t, nil
	default:
		return value, nil
	}
}

// ConvertReadValue casts a raw wire value returned by GetValue to the
// canonical Go type for the given parameter type. This mirrors the
// convert_value / convert_from_pd type-casting logic:
//
//   - FLOAT → float64
//   - INTEGER, ENUM → int (via float64 intermediate for JSON-decoded numbers)
//   - BOOL → bool
//   - STRING → string
//
// Values that are already the correct Go type pass through unchanged.
// Unknown types and nil are returned as-is.
func ConvertReadValue(paramType hmenum.ParameterType, raw any) any {
	if raw == nil {
		return nil
	}
	switch paramType {
	case hmenum.ParameterTypeFloat:
		switch v := raw.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum:
		switch v := raw.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case float32:
			return int(v)
		}
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		switch v := raw.(type) {
		case bool:
			return v
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	case hmenum.ParameterTypeString:
		if s, ok := raw.(string); ok {
			return s
		}
	case hmenum.ParameterTypeDummy, hmenum.ParameterTypeEmpty:
		// No type-specific conversion for dummy/empty parameters.
	}
	return raw
}

// ConvertHMLevelToCPV converts a float-level value (0.0–1.0) to the
// hexadecimal combined-parameter-value string used by the CCU for
// LEVEL_COMBINED writes.
//
// Python format('#04x') produces a minimum total width of 4 including
// the "0x" prefix, so values 0–255 are always at least 4 chars:
//
//	0.0 → "0x00" (int(0*200) = 0)
//	0.5 → "0x64" (int(0.5*200) = 100)
//	1.0 → "0xc8" (int(1.0*200) = 200)
func ConvertHMLevelToCPV(value float64) string {
	n := int(value * 100 * 2)
	// Produce the same output as Python's format(n, '#04x'):
	// minimum total width 4 including "0x" prefix → minimum 2 hex digits.
	return fmt.Sprintf("0x%02x", n)
}
