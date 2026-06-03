// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cluster

// The bridge's attribute decoder surfaces a TLV-decoded write value as a
// width-independent Go numeric — unsigned integers as uint64, signed as
// int64, floats as float64 (see bridge/attribute_value_reader.go). A
// cluster server that stores a narrow native type (enum8, int16, …) must
// therefore accept any numeric and narrow it, rather than asserting one
// exact Go type: a strict `value.(uint8)` rejects the uint64 the decoder
// produces and the whole Write fails with IM status Failure.

// writeInt funnels any supported numeric write value to int64. Returns
// false for non-numeric values (string, []byte, struct, nil).
func writeInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true //nolint:gosec // write values are small native-width fields
	case float32:
		return int64(x), true
	case float64:
		return int64(x), true
	}
	return 0, false
}

// AsUint8 coerces a decoded write value (uint64/int64/native ints/float)
// to uint8 — the Go type cluster servers store for enum8 / uint8
// attributes such as Thermostat.SystemMode or WindowCovering modes.
func AsUint8(v any) (uint8, bool) {
	n, ok := writeInt(v)
	if !ok {
		return 0, false
	}
	return uint8(n), true //nolint:gosec // intentional narrowing of a native-width write value
}

// AsInt16 coerces a decoded write value to int16 — the Go type cluster
// servers store for signed 16-bit attributes such as Thermostat
// setpoints (centi-degrees Celsius).
func AsInt16(v any) (int16, bool) {
	n, ok := writeInt(v)
	if !ok {
		return 0, false
	}
	return int16(n), true //nolint:gosec // intentional narrowing of a native-width write value
}
