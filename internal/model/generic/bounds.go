// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"encoding/json"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// checkFloatBounds returns [ErrOutOfRange] when v violates the
// descriptor's MIN / MAX. Missing bounds are treated as "no constraint".
// Values matching one of the descriptor's SPECIAL entries bypass the
// MIN/MAX check.
func checkFloatBounds(desc hmproto.ParameterData, v float64) error {
	if matchesSpecial(desc.Special, v) {
		return nil
	}
	if lo, ok := parseFloat(desc.Min); ok && v < lo {
		return wrapRangeError("float", v, lo, parseFloatOr(desc.Max, "∞"))
	}
	if hi, ok := parseFloat(desc.Max); ok && v > hi {
		return wrapRangeError("float", v, parseFloatOr(desc.Min, "-∞"), hi)
	}
	return nil
}

// checkIntBounds validates an integer value against the descriptor.
// Values matching one of the descriptor's SPECIAL entries bypass the
// MIN/MAX check.
func checkIntBounds(desc hmproto.ParameterData, v int64) error {
	if matchesSpecial(desc.Special, float64(v)) {
		return nil
	}
	if lo, ok := parseFloat(desc.Min); ok && float64(v) < lo {
		return wrapRangeError("int", v, lo, parseFloatOr(desc.Max, "∞"))
	}
	if hi, ok := parseFloat(desc.Max); ok && float64(v) > hi {
		return wrapRangeError("int", v, parseFloatOr(desc.Min, "-∞"), hi)
	}
	return nil
}

// matchesSpecial reports whether v matches one of the descriptor's
// SPECIAL sentinel values (e.g. NaN-replacement marker, "manu" sentinel
// for set-temperature). The CCU encodes SPECIAL as a list of
// {"ID": "X", "VALUE": <number>} objects.
func matchesSpecial(raw []byte, v float64) bool {
	if len(raw) == 0 {
		return false
	}
	type entry struct {
		ID    string      `json:"ID"`
		Value json.Number `json:"VALUE"`
	}
	var list []entry
	if err := json.Unmarshal(raw, &list); err != nil {
		return false
	}
	for _, e := range list {
		f, err := e.Value.Float64()
		if err != nil {
			continue
		}
		if f == v {
			return true
		}
	}
	return false
}

func parseFloat(raw json.RawMessage) (float64, bool) {
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

// parseFloatOr returns the parsed float or fallback when unparsable.
// Used only to build error messages; fallback is typed as any so we
// can pass "∞" / "-∞" sentinels.
func parseFloatOr(raw json.RawMessage, fallback any) any {
	if v, ok := parseFloat(raw); ok {
		return v
	}
	return fallback
}

// validateRange is the type-erased entry point used by
// [DataPoint.IsValueInRange]. It dispatches v on its dynamic type and applies
// the matching bounds check.
//
//   - bool: no range constraint → always nil
//   - string: when the descriptor declares a ValueList (ENUM type), the
//     string must appear in ValueList; otherwise unconstrained.
//   - integer types: numeric MIN / MAX, or enum index range when ValueList is
//     declared
//   - float types: numeric MIN / MAX
//
// nil values are accepted (vacuously valid) — matches the Python behaviour
// where `_value is None` short-circuits the check.
func validateRange(desc hmproto.ParameterData, v any) error {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case bool:
		return nil
	case string:
		return checkEnumString(desc, x)
	case float32:
		return checkFloatBounds(desc, float64(x))
	case float64:
		return checkFloatBounds(desc, x)
	case int:
		return checkIntOrEnum(desc, int64(x))
	case int8:
		return checkIntOrEnum(desc, int64(x))
	case int16:
		return checkIntOrEnum(desc, int64(x))
	case int32:
		return checkIntOrEnum(desc, int64(x))
	case int64:
		return checkIntOrEnum(desc, x)
	case uint8:
		return checkIntOrEnum(desc, int64(x))
	case uint16:
		return checkIntOrEnum(desc, int64(x))
	case uint32:
		return checkIntOrEnum(desc, int64(x))
	default:
		// Unknown dynamic type — caller is the source of T (which is
		// constrained to comparable). Treat as "no constraint" so we
		// never falsely reject a value the type system already
		// agreed on.
		return nil
	}
}

// checkIntOrEnum picks the right bounds check for an integer value:
// when the descriptor declares an ENUM ValueList, the value is treated
// as an index and bounds-checked against ValueList; otherwise the
// regular MIN/MAX check applies.
func checkIntOrEnum(desc hmproto.ParameterData, v int64) error {
	if len(desc.ValueList) > 0 {
		if v < 0 || v >= int64(len(desc.ValueList)) {
			return wrapRangeError("enum-index", v, 0, len(desc.ValueList)-1)
		}
		return nil
	}
	return checkIntBounds(desc, v)
}

// checkEnumString validates a string value against the descriptor's ValueList.
// When the descriptor carries a non-empty ValueList (ENUM parameter), the
// string must be one of the listed labels; an unlisted string returns
// [ErrUnknownLabel]. Parameters without a ValueList (plain STRING type) have
// no enumeration constraint and are accepted as-is.
func checkEnumString(desc hmproto.ParameterData, v string) error {
	if len(desc.ValueList) == 0 {
		return nil
	}
	for _, label := range desc.ValueList {
		if label == v {
			return nil
		}
	}
	return fmt.Errorf("%w: %q not in VALUE_LIST", ErrUnknownLabel, v)
}
