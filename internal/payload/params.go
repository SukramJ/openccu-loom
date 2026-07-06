// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrServiceMissingParam is returned by the Param* decoders when a
// required key is absent from the service-method request body.
// Wrapped with the offending key name for diagnostics.
var ErrServiceMissingParam = errors.New("payload: service missing required param")

// ErrServiceInvalidParam is returned when a key is present but its
// value cannot be coerced to the expected Go type.
var ErrServiceInvalidParam = errors.New("payload: service param has invalid type")

// ParamBool decodes a required bool param. JSON numbers (1 / 0) are
// coerced to true / false to match what HA's MQTT layer typically
// sends through `payload_on` / `payload_off` templates; common string
// spellings ("true" / "false" / "on" / "off") are also accepted.
//
// Its truth table is deliberately narrower/wider in different ways than
// internal/parameter's CCU-side `asBool` (case-sensitive spelling list vs.
// case-insensitive + "yes"/"no"): the two coerce different boundaries
// (north-bound service-call JSON here vs. CCU wire values there against a
// parameter descriptor) and are not meant to converge — see the comment on
// `asBool` in internal/parameter/coerce.go for the other side.
func ParamBool(params map[string]any, key string) (bool, error) {
	raw, ok := params[key]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrServiceMissingParam, key)
	}
	switch v := raw.(type) {
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case int:
		return v != 0, nil
	case string:
		switch v {
		case "true", "True", "TRUE", "1", "on", "ON":
			return true, nil
		case "false", "False", "FALSE", "0", "off", "OFF":
			return false, nil
		}
	}
	return false, fmt.Errorf("%w: %q", ErrServiceInvalidParam, key)
}

// ParamFloat64 decodes a required float64 param. JSON-decoded numbers
// always arrive as float64; integer literals and numeric strings are
// also accepted (lets HA's `{{ value }}` template work without an
// explicit `| float` filter).
func ParamFloat64(params map[string]any, key string) (float64, error) {
	raw, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrServiceMissingParam, key)
	}
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
		// strconv.ParseFloat (unlike fmt.Sscanf) rejects trailing
		// garbage such as "42xyz" instead of silently truncating it.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrServiceInvalidParam, key)
}

// ParamInt32 decodes a required int32 param. Out-of-range integer
// inputs (|v| > MaxInt32) produce ErrServiceInvalidParam — silent
// truncation would surprise callers that supply 64-bit indices.
func ParamInt32(params map[string]any, key string) (int32, error) {
	raw, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrServiceMissingParam, key)
	}
	const maxI32, minI32 = 1<<31 - 1, -(1 << 31)
	switch v := raw.(type) {
	case int32:
		return v, nil
	case int:
		if v > maxI32 || v < minI32 {
			return 0, fmt.Errorf("%w: %q overflows int32", ErrServiceInvalidParam, key)
		}
		return int32(v), nil
	case int64:
		if v > maxI32 || v < minI32 {
			return 0, fmt.Errorf("%w: %q overflows int32", ErrServiceInvalidParam, key)
		}
		return int32(v), nil
	case float64:
		if v > float64(maxI32) || v < float64(minI32) {
			return 0, fmt.Errorf("%w: %q overflows int32", ErrServiceInvalidParam, key)
		}
		return int32(v), nil
	case string:
		// strconv.ParseInt (unlike fmt.Sscanf) rejects trailing
		// garbage such as "42xyz" instead of silently truncating it.
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			return int32(n), nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrServiceInvalidParam, key)
}

// ParamString decodes a required string param. Numeric / bool values
// are formatted to their canonical Go string form for caller
// convenience.
func ParamString(params map[string]any, key string) (string, error) {
	raw, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrServiceMissingParam, key)
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case bool, int, int32, int64, float32, float64:
		return fmt.Sprintf("%v", v), nil
	}
	return "", fmt.Errorf("%w: %q", ErrServiceInvalidParam, key)
}
