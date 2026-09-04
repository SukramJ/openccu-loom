// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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
// value. The conversion rules mirror the Python reference
// implementation's `to_homematic` singledispatch (converter.py):
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
//
// The six-decimal round is not a rule of the transports this daemon writes
// through — those serialise the float64 they are handed — but of the CCU's
// own encoders, and it is exact for two of the three: rfd's XML-RPC and
// ReGa/tclrpc both render a double with "%f", i.e. six decimals
// (../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:65 with :591-594 and
// :659-664, and ../OpenCCU-Base/src/tclrpc/tclrpc.cpp:222). Rounding here
// pre-empts a difference the CCU would introduce anyway on those two
// surfaces. It is NOT exact for HmIP legacy XML-RPC, whose serialiser
// preserves the full binary64 value (see the transport survey on
// [FloatTolerance]), so this is a lossy step for an HmIP float that declares
// more than six decimals — the boundary runs at the interface, not at the
// parameter.
//
// Measured against the tree, no non-test .go file calls this function: the
// outgoing value of a write is serialised by the transport, not here.
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
// canonical Go type for the given parameter type. It mirrors the reference
// wire→typed conversion — model/support.py convert_value (support.py:565-581)
// wrapped by the empty-string guard in model/data_point.py _convert_value
// (data_point.py:1449):
//
//   - FLOAT → float64; a numeric string is parsed (Python `float(value)`);
//     an empty string yields nil (LEVEL_2 with no slats → None)
//   - INTEGER → int; a numeric string is parsed (Python `int(float(value))`);
//     an empty string yields nil
//   - ENUM → value unchanged (convert_value leaves ENUM untouched: an int
//     index stays int, a label string stays string)
//   - BOOL → bool; a string is coerced via the to_bool truth set
//     (support/__init__.py:129)
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
		case string:
			return parseReadFloat(v)
		}
	case hmenum.ParameterTypeInteger:
		switch v := raw.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case float32:
			return int(v)
		case string:
			return parseReadInt(v)
		}
	case hmenum.ParameterTypeEnum:
		// convert_value leaves ENUM values unchanged: an integer index stays
		// int, a label string stays string. Narrow only the numeric forms.
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
		case string:
			return isBoolTrueString(v)
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

// parseReadFloat mirrors the FLOAT branch of the reference _convert_value:
// an empty string maps to None (model/data_point.py:1449), any other string
// is cast via `float(value)` (model/support.py:575). An unparseable non-empty
// string maps to nil — the reference catches the ValueError and returns None.
func parseReadFloat(s string) any {
	if s == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f
	}
	return nil
}

// parseReadInt mirrors the INTEGER branch of the reference _convert_value:
// an empty string maps to None (model/data_point.py:1449), any other string
// is cast via `int(float(value))` (model/support.py:577) so "255" and "12.0"
// both yield an int. An unparseable non-empty string maps to nil.
func parseReadInt(s string) any {
	if s == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return int(f)
	}
	return nil
}

// isBoolTrueString is the READ-path cast for a CCU-reported boolean carried
// as a string: the trimmed, lower-cased value is true only when it is one of
// y, yes, t, true, on or 1; every other string (including the empty string)
// is false, without an error. It is intentionally more permissive than the
// strict write-coerce asBool in coerce.go, which rejects an unrecognised
// token rather than silently treating it as false.
//
// The six-token set is wider than any CCU decoder and rests on the Python
// port (to_bool, support/__init__.py:129); it is unverified against the
// firmware. What the firmware does carry is the two textual boolean readers,
// and both are narrower: `<boolean>` accepts only the decimal 0 or 1 and
// rejects everything else (../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:425-437,
// `strtol` followed by `ivalue != 0 && ivalue != 1`), and the text form
// accepts exactly `true` / `false`, length-checked and case-sensitive
// (:470-488). Neither ever emits `y`, `t` or `on`, so the extra tokens cost
// nothing and are kept for the JSON-RPC / ReGa string path.
//
// The TrimSpace is not decoration: the firmware's own `<boolean>` reader
// parses the digit with `strtol` (:429), which skips leading whitespace, so
// trimming before the token match is at most as strict as the CCU.
//
// pkg/hmtypes.ToBool carries the same six tokens on the published pkg/
// surface for external consumers; it has no caller inside the daemon and
// does NOT trim, so ` true ` is true here and false there. The two token
// sets are pinned against each other by TestW2ParBoolTruthSetsAgree in
// tests/contract/; widening one alone fails that guard.
func isBoolTrueString(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "t", "true", "on", "1":
		return true
	}
	return false
}

// ConvertHMLevelToCPV converts a blind-axis level (0.0–1.0) to the
// hexadecimal combined-parameter-value byte the CCU expects inside a
// LEVEL_COMBINED write: `position * 200` in lowercase two-digit hex,
// i.e. one byte per axis over a 0.5 %-per-step grid.
//
//	0.0 → "0x00"    0.5 → "0x64"    1.0 → "0xc8"
//
// The product is rounded, not truncated. Exact half-percent positions
// are not representable in binary64 and several of them land just below
// their integer product — 0.29*200 is 57.99999999999999, 0.57 and 0.58
// behave the same way — so truncation moves the blind one 0.5 % step
// below the commanded position. Rounding is a deliberate divergence
// from the reference expression `int(value * 100 * 2)`, which carries
// that artefact.
//
// The input is clamped to [0,1] because the wire grammar has no room
// for anything else: an out-of-range level would render as "0x12c" or
// "0x-14" and break the two-byte LEVEL_COMBINED string for the axis
// that was in range as well. Out-of-range input is corrected here, not
// rejected, because the encoder has no channel to report on.
func ConvertHMLevelToCPV(value float64) string {
	switch {
	case value < 0:
		value = 0
	case value > 1:
		value = 1
	}
	return fmt.Sprintf("0x%02x", int(math.Round(value*100*2)))
}
