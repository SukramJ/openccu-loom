// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"fmt"
	"time"
)

// Typed extractors. Each returns a zero value plus error when the kind
// does not match. Callers that know the shape up front use these; the
// compiler enforces the return type.

// AsInt unwraps an [IntValue] into an int.
func AsInt(v Value) (int, error) {
	i, ok := v.(IntValue)
	if !ok {
		return 0, typeMismatch(v, "int")
	}
	return int(i), nil
}

// AsInt32 unwraps an [IntValue] into an int32.
func AsInt32(v Value) (int32, error) {
	i, ok := v.(IntValue)
	if !ok {
		return 0, typeMismatch(v, "int")
	}
	return int32(i), nil
}

// AsBool unwraps a [BoolValue].
func AsBool(v Value) (bool, error) {
	b, ok := v.(BoolValue)
	if !ok {
		return false, typeMismatch(v, "boolean")
	}
	return bool(b), nil
}

// AsString unwraps a [StringValue].
func AsString(v Value) (string, error) {
	s, ok := v.(StringValue)
	if !ok {
		return "", typeMismatch(v, "string")
	}
	return string(s), nil
}

// AsDouble unwraps a [DoubleValue].
func AsDouble(v Value) (float64, error) {
	d, ok := v.(DoubleValue)
	if !ok {
		return 0, typeMismatch(v, "double")
	}
	return float64(d), nil
}

// AsTime unwraps a [DateTimeValue].
func AsTime(v Value) (time.Time, error) {
	t, ok := v.(DateTimeValue)
	if !ok {
		return time.Time{}, typeMismatch(v, "dateTime.iso8601")
	}
	return time.Time(t), nil
}

// AsBytes unwraps a [Base64Value].
func AsBytes(v Value) ([]byte, error) {
	b, ok := v.(Base64Value)
	if !ok {
		return nil, typeMismatch(v, "base64")
	}
	return []byte(b), nil
}

// AsStruct unwraps a [StructValue].
func AsStruct(v Value) (StructValue, error) {
	s, ok := v.(StructValue)
	if !ok {
		return StructValue{}, typeMismatch(v, "struct")
	}
	return s, nil
}

// AsArray unwraps an [ArrayValue].
func AsArray(v Value) (ArrayValue, error) {
	a, ok := v.(ArrayValue)
	if !ok {
		return nil, typeMismatch(v, "array")
	}
	return a, nil
}

// AsStrings unwraps an [ArrayValue] whose elements are all [StringValue].
// Convenient because it's the most common shape on the wire (listDevices,
// listMethods, paramset names, …).
func AsStrings(v Value) ([]string, error) {
	arr, err := AsArray(v)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, err := AsString(e)
		if err != nil {
			return nil, fmt.Errorf("array element %d: %w", i, err)
		}
		out[i] = s
	}
	return out, nil
}

// StructField extracts a named member from a struct value, typed via the
// generic parameter T. The value must be a [StructValue] that contains
// the named field with the expected concrete type.
func StructField[T Value](v Value, name string) (T, error) {
	var zero T
	s, err := AsStruct(v)
	if err != nil {
		return zero, err
	}
	raw, ok := s.Get(name)
	if !ok {
		return zero, fmt.Errorf("xmlrpc: struct member %q not present", name)
	}
	typed, ok := raw.(T)
	if !ok {
		return zero, fmt.Errorf("xmlrpc: struct member %q: want %T, got %s", name, zero, raw.Kind())
	}
	return typed, nil
}

func typeMismatch(v Value, want string) error {
	if v == nil {
		return fmt.Errorf("xmlrpc: want %s, got nil value", want)
	}
	return fmt.Errorf("xmlrpc: want %s, got %s", want, v.Kind())
}
