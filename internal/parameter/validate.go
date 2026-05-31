// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ErrNotWritable is returned when a write is attempted on a parameter
// whose OPERATIONS mask lacks WRITE.
var ErrNotWritable = errors.New("parameter: not writable")

// WritabilityReporter is satisfied by any data-point type that can
// determine its own effective writability — taking into account both the
// wire descriptor's OPERATIONS mask and any overlay such as
// MarkForcedSensor. Every *generic.DataPoint[T] satisfies this interface.
// Callers that have a DP reference should prefer [ValidateWithDP] over
// [Validate] so the IsForcedSensor override is respected.
type WritabilityReporter interface {
	IsWritable() bool
}

// ErrStringTooLong is returned when a string value exceeds the
// descriptor's MAX length constraint.
var ErrStringTooLong = errors.New("parameter: string exceeds MAX length")

// ErrNaNOrInf is returned when a float value is NaN or infinite, which
// the CCU wire protocol cannot represent.
var ErrNaNOrInf = errors.New("parameter: float value is NaN or infinite")

// ValidateOptions controls optional hardening inside [ValidateWithOptions].
// The zero value applies the same semantics as the legacy [Validate] call:
// special values are accepted (AllowSpecialValues = true by default inside
// Validate), and precision is permissive (StrictPrecision = false).
type ValidateOptions struct {
	// AllowSpecialValues, when true (the default via [Validate]),
	// permits float/int values that match a SPECIAL sentinel entry even
	// if they fall outside the declared MIN/MAX range.
	AllowSpecialValues bool

	// StrictPrecision, when true, rejects float values that have a
	// fractional part when the descriptor's Unit implies integer steps
	// (i.e. Unit == "%" or Unit == "s" and Min/Max are integer-valued).
	// When false, fractional floats are accepted silently.
	StrictPrecision bool
}

// Validate checks whether v is acceptable for a parameter described by
// desc. It enforces kind agreement, numeric range, enum membership,
// string length, NaN/Inf rejection, and writability.
//
// Validate calls [ValidateWithOptions] with AllowSpecialValues=true and
// StrictPrecision=false, preserving backward-compatible semantics for all
// existing callers.
//
// Note: writability is checked against the raw descriptor's OPERATIONS
// mask. Callers that have a live data-point reference (and want the
// IsForcedSensor overlay to be respected) should use [ValidateWithDP]
// instead.
func Validate(desc hmproto.ParameterData, v hmtypes.ParamValue) error {
	return ValidateWithOptions(desc, v, ValidateOptions{
		AllowSpecialValues: true,
		StrictPrecision:    false,
	})
}

// ValidateWithDP is like [Validate] but consults dp's own [WritabilityReporter]
// interface for the writable check instead of reading it from the descriptor
// directly. This ensures the IsForcedSensor overlay — which demotes
// parameters like HmIP-eTRV.LEVEL to read-only sensors regardless of the
// wire descriptor — is respected.
//
// dp must not be nil. If dp does not implement [WritabilityReporter] the
// function falls back to the descriptor-only path (same as [Validate]).
//
// Callers: REST PUT /value handlers, MQTT command subscriber, WS dp-write
// handlers — any path that has a live data-point reference.
func ValidateWithDP(dp WritabilityReporter, desc hmproto.ParameterData, v hmtypes.ParamValue, opts ValidateOptions) error {
	// Override the descriptor-level writability check with the DP's own
	// IsWritable() which incorporates the IsForcedSensor overlay.
	if !dp.IsWritable() {
		return ErrNotWritable
	}
	// Delegate to ValidateWithOptions but skip its redundant writable check
	// by temporarily masking it: we pass a desc clone that has WRITE set so
	// the gate inside ValidateWithOptions becomes a no-op. We achieve this
	// more cleanly by extracting the type/range/enum checks into a separate
	// helper and calling it directly.
	return validateValue(desc, v, opts)
}

// ValidateWithOptions is like [Validate] but lets the caller control
// optional hardening via [ValidateOptions].
func ValidateWithOptions(desc hmproto.ParameterData, v hmtypes.ParamValue, opts ValidateOptions) error {
	// Writability is the first check so callers get an unambiguous error
	// when they hit a read-only parameter, regardless of value shape.
	// Note: this only checks the descriptor's OPERATIONS mask; it does NOT
	// honour the IsForcedSensor overlay. Use [ValidateWithDP] when a live
	// data-point reference is available.
	if !desc.IsWritable() {
		return ErrNotWritable
	}
	return validateValue(desc, v, opts)
}

// validateValue performs all type / range / enum checks without the
// writability gate. It is the shared inner implementation used by
// [ValidateWithOptions] (after its own writable check) and
// [ValidateWithDP] (which delegates writability to dp.IsWritable()).
func validateValue(desc hmproto.ParameterData, v hmtypes.ParamValue, opts ValidateOptions) error {
	switch desc.Type {
	case hmenum.ParameterTypeBool:
		if v.Kind != hmtypes.ValueKindBool {
			return fmt.Errorf("parameter: want bool, got %s", v.Kind)
		}

	case hmenum.ParameterTypeAction:
		// ACTION is a write-only trigger. The CCU accepts any bool-shaped
		// value; the semantic meaning is "fire". No further constraint.
		if v.Kind != hmtypes.ValueKindBool {
			return fmt.Errorf("parameter: want bool, got %s", v.Kind)
		}

	case hmenum.ParameterTypeInteger:
		if v.Kind != hmtypes.ValueKindInt {
			return fmt.Errorf("parameter: want int, got %s", v.Kind)
		}
		fv := float64(v.Int)
		if opts.AllowSpecialValues && isSpecialValue(desc, fv) {
			return nil
		}
		return checkNumericRange(desc, fv)

	case hmenum.ParameterTypeFloat:
		if v.Kind != hmtypes.ValueKindFloat {
			return fmt.Errorf("parameter: want float, got %s", v.Kind)
		}
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return ErrNaNOrInf
		}
		if opts.AllowSpecialValues && isSpecialValue(desc, v.Float) {
			return nil
		}
		if err := checkNumericRange(desc, v.Float); err != nil {
			return err
		}
		if opts.StrictPrecision {
			if err := checkFloatPrecision(desc, v.Float); err != nil {
				return err
			}
		}

	case hmenum.ParameterTypeEnum:
		if v.Kind != hmtypes.ValueKindInt {
			// Caller passed a label string instead of an index. The CCU
			// wire protocol expects the integer index. Reject clearly so
			// the caller can use Coerce to resolve the label first.
			if v.Kind == hmtypes.ValueKindString {
				return fmt.Errorf(
					"parameter: ENUM wants integer index, got string %q; use Coerce to resolve the label to an index",
					v.String,
				)
			}
			return fmt.Errorf("parameter: want enum index, got %s", v.Kind)
		}
		if len(desc.ValueList) > 0 {
			if v.Int < 0 || v.Int >= len(desc.ValueList) {
				return fmt.Errorf("parameter: ENUM index %d out of bounds (len=%d)", v.Int, len(desc.ValueList))
			}
		} else if v.Int < 0 {
			// No VALUE_LIST declared: the only invariant we can enforce is
			// non-negativity (enum indices are unsigned on the wire).
			return fmt.Errorf("parameter: ENUM index %d is negative", v.Int)
		}

	case hmenum.ParameterTypeString:
		if v.Kind != hmtypes.ValueKindString {
			return fmt.Errorf("parameter: want string, got %s", v.Kind)
		}
		// For STRING parameters the CCU encodes the maximum permitted
		// byte-length in the Max field as an integer. Zero / absent means
		// "unlimited".
		if maxLen := parseIntRaw(desc.Max); maxLen > 0 && len(v.String) > maxLen {
			return fmt.Errorf("%w: len=%d, max=%d", ErrStringTooLong, len(v.String), maxLen)
		}

	case hmenum.ParameterTypeEmpty, hmenum.ParameterTypeDummy:
		// No constraint.

	default:
		return fmt.Errorf("parameter: unknown TYPE %q", desc.Type)
	}
	return nil
}

// ---------- helpers ----------

// isSpecialValue reports whether fv matches any entry in the descriptor's
// SPECIAL list. The CCU encodes SPECIAL as a JSON array of
// {"ID": "<label>", "VALUE": <number>} objects. A match allows the value
// to bypass the normal MIN/MAX range check.
//
// This mirrors.py and the
// matchesSpecial function in internal/model/generic/bounds.go.
func isSpecialValue(desc hmproto.ParameterData, fv float64) bool {
	if len(desc.Special) == 0 {
		return false
	}
	type entry struct {
		ID    string      `json:"ID"`
		Value json.Number `json:"VALUE"`
	}
	var list []entry
	if err := json.Unmarshal(desc.Special, &list); err != nil {
		return false
	}
	for _, e := range list {
		f, err := e.Value.Float64()
		if err != nil {
			continue
		}
		if f == fv {
			return true
		}
	}
	return false
}

// checkFloatPrecision rejects a float value that has a fractional part when
// both the descriptor's Min and Max are integer-valued. This heuristic
// catches unit-step parameters (e.g. "%" or time in seconds) where the
// hardware only accepts whole numbers even though the parameter type is FLOAT.
//
// The check is skipped when either bound is absent or non-integer.
func checkFloatPrecision(desc hmproto.ParameterData, v float64) error {
	if v == math.Trunc(v) {
		return nil // no fractional part — always fine
	}
	lo, loOK := parseNumeric(desc.Min)
	hi, hiOK := parseNumeric(desc.Max)
	if !loOK || !hiOK {
		return nil // insufficient metadata — permissive
	}
	if lo != math.Trunc(lo) || hi != math.Trunc(hi) {
		return nil // bounds themselves are fractional — real float param
	}
	return fmt.Errorf("parameter: float value %g has fractional part but descriptor bounds [%g, %g] are integers", v, lo, hi)
}

// parseIntRaw extracts an integer from a json.RawMessage. Used for
// STRING parameter MAX (which encodes a byte-length). Returns 0 when
// the field is absent, empty, or not parseable as an integer.
func parseIntRaw(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}
