// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Coerce converts raw into a typed [hmtypes.ParamValue] consistent with
// desc.Type. Returns an error on shape mismatch or value outside the
// declared range.
//
// Supported inputs:
//   - nil → NoneValue
//   - bool
//   - int, int8..int64, uint..uint64, float32, float64
//   - string (parsed according to desc.Type)
//   - []string (only for ENUM with VALUE_LIST set)
//   - json.Number (parsed into the numeric target type)
func Coerce(desc hmproto.ParameterData, raw any) (hmtypes.ParamValue, error) {
	if raw == nil {
		return hmtypes.NoneValue(), nil
	}

	switch desc.Type {
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		b, err := asBool(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		return hmtypes.BoolValue(b), nil

	case hmenum.ParameterTypeInteger:
		i, err := asInt(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		// A declared SPECIAL sentinel bypasses MIN/MAX, keeping this
		// write-coerce path in lockstep with [Validate] and the runtime
		// read path (internal/model/generic bounds). See [MatchesSpecialValue].
		if !MatchesSpecialValue(desc, float64(i)) {
			if err := checkNumericRange(desc, float64(i)); err != nil {
				return hmtypes.ParamValue{}, err
			}
		}
		return hmtypes.IntValue(i), nil

	case hmenum.ParameterTypeFloat:
		f, err := asFloat(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		if !MatchesSpecialValue(desc, f) {
			if err := checkNumericRange(desc, f); err != nil {
				return hmtypes.ParamValue{}, err
			}
		}
		return hmtypes.FloatValue(f), nil

	case hmenum.ParameterTypeEnum:
		return coerceEnum(desc, raw)

	case hmenum.ParameterTypeString:
		s, err := asString(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		return hmtypes.StringValue(s), nil

	case hmenum.ParameterTypeEmpty, hmenum.ParameterTypeDummy:
		// No semantic constraints — pass the value through as string.
		s, err := asString(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		return hmtypes.StringValue(s), nil

	default:
		return hmtypes.ParamValue{}, fmt.Errorf("parameter: unknown TYPE %q", desc.Type)
	}
}

func coerceEnum(desc hmproto.ParameterData, raw any) (hmtypes.ParamValue, error) {
	if len(desc.ValueList) == 0 {
		// Some CCU paramsets expose ENUM without VALUE_LIST; fall back
		// to a bare integer coercion (the enum index).
		i, err := asInt(raw)
		if err != nil {
			return hmtypes.ParamValue{}, err
		}
		return hmtypes.IntValue(i), nil
	}

	// The wire value may arrive as the label string or as its index.
	if s, ok := raw.(string); ok {
		if idx := indexOf(desc.ValueList, s); idx >= 0 {
			return hmtypes.IntValue(idx), nil
		}
		return hmtypes.ParamValue{}, fmt.Errorf("parameter: ENUM value %q not in VALUE_LIST", s)
	}
	i, err := asInt(raw)
	if err != nil {
		return hmtypes.ParamValue{}, err
	}
	if i < 0 || i >= len(desc.ValueList) {
		return hmtypes.ParamValue{}, fmt.Errorf("parameter: ENUM index %d out of bounds (len=%d)", i, len(desc.ValueList))
	}
	return hmtypes.IntValue(i), nil
}

// ---------- type-narrowing helpers ----------

// asBool's truth table differs on purpose from
// [github.com/SukramJ/openccu-loom/internal/payload.ParamBool]: this one
// coerces values against a [hmproto.ParameterData] descriptor
// (case-insensitive, accepts "yes"/"no"); ParamBool coerces JSON-decoded
// north-bound service-call bodies (REST/MQTT) and matches what Home
// Assistant's `payload_on` / `payload_off` MQTT templates commonly send.
// Different boundaries, different tolerances — keep both, do not unify.
//
// The float branches are load-bearing rather than defensive: [Coerce] is
// also the REST write boundary, and the request body is decoded by
// encoding/json without UseNumber, so every JSON number in
// `{"value": 1}` arrives as a float64 — the CCU's own 1/0 spelling of a
// BOOL, which would otherwise be rejected with a 400.
func asBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case int:
		return v != 0, nil
	case int32:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case float32:
		return v != 0, nil
	case float64:
		return v != 0, nil
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		switch s {
		case "1", "true", "on", "yes":
			return true, nil
		case "0", "false", "off", "no", "":
			return false, nil
		}
		return false, fmt.Errorf("parameter: cannot coerce %q to bool", v)
	case json.Number:
		if v == "0" {
			return false, nil
		}
		return true, nil
	default:
		return false, fmt.Errorf("parameter: cannot coerce %T to bool", raw)
	}
}

// intFromFloat narrows a float to int with the guards Go's own conversion
// does not perform.
//
// A conversion whose value does not fit the destination — or is NaN or an
// infinity — is undefined in Go: the result is whatever the platform
// produces, and on the 32-bit shipped target that starts just past two
// billion rather than at nine quintillion. A CCU that reports a runaway
// counter, or a NaN the wire decoder let through, would otherwise land in
// the parameter store as a plausible-looking small number instead of a
// rejected write.
//
// Truncation toward zero is preserved: only the guard is new.
func intFromFloat(v float64) (int, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("parameter: %v is not a finite number", v)
	}
	if v > float64(math.MaxInt) || v < float64(math.MinInt) {
		return 0, fmt.Errorf("parameter: float value %v overflows int", v)
	}
	return int(v), nil
}

func asInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		// On a 32-bit build (armv7, the shipped add-on target) int is
		// 32 bits, so an unchecked narrowing here can silently wrap an
		// out-of-range value into a different in-range one before
		// checkNumericRange ever sees it — the same class of bug the
		// uint/uint64 branches below already guard against, and that
		// [hmtypes.NewParamValue] documents for the read path.
		if v > int64(math.MaxInt) || v < int64(math.MinInt) {
			return 0, fmt.Errorf("parameter: int64 value %d overflows int", v)
		}
		return int(v), nil //nolint:gosec // bounds-checked above; see #20
	case uint:
		if v > uint(math.MaxInt) {
			return 0, fmt.Errorf("parameter: uint value %d overflows int", v)
		}
		return int(v), nil //nolint:gosec // bounds-checked above; see #20
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		if v > uint64(math.MaxInt) {
			return 0, fmt.Errorf("parameter: uint64 value %d overflows int", v)
		}
		return int(v), nil //nolint:gosec // bounds-checked above; see #20
	case float32:
		return intFromFloat(float64(v))
	case float64:
		return intFromFloat(v)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("parameter: cannot coerce %q to int: %w", v, err)
		}
		return n, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("parameter: cannot coerce %v to int: %w", v, err)
		}
		if n > int64(math.MaxInt) || n < int64(math.MinInt) {
			return 0, fmt.Errorf("parameter: json.Number value %d overflows int", n)
		}
		return int(n), nil //nolint:gosec // bounds-checked above; see #20
	default:
		return 0, fmt.Errorf("parameter: cannot coerce %T to int", raw)
	}
}

func asFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("parameter: cannot coerce %q to float: %w", v, err)
		}
		return f, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, fmt.Errorf("parameter: cannot coerce %v to float: %w", v, err)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("parameter: cannot coerce %T to float", raw)
	}
}

func asString(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case int, int32, int64, float32, float64, bool, json.Number:
		return fmt.Sprint(v), nil
	default:
		return "", fmt.Errorf("parameter: cannot coerce %T to string", raw)
	}
}

func checkNumericRange(desc hmproto.ParameterData, v float64) error {
	if lo, ok := parseNumeric(desc.Min); ok && v < lo {
		return fmt.Errorf("parameter: value %g below MIN %g", v, lo)
	}
	if hi, ok := parseNumeric(desc.Max); ok && v > hi {
		return fmt.Errorf("parameter: value %g above MAX %g", v, hi)
	}
	return nil
}

func parseNumeric(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return f, true
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
