// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"math"
	"strconv"
	"strings"
)

// coerceWire converts v into T when a lossless (or culturally-lossless
// for strings) conversion exists. It powers [DataPoint.OnWireValue]
// and thereby the initial seeding flow: Rega/JSON-RPC deliver values
// as untyped JSON scalars, while each data point owns a specific T.
//
// Covered conversions:
//   - bool:   bool passthrough; string "true"/"false"/"1"/"0"; any numeric
//   - int32:  int, int32, int64, float64 (truncating); numeric string
//   - float64: every numeric type; numeric string
//   - string:  string passthrough; everything else stringified
//
// Unknown type parameters fall through to (zero, false).
func coerceWire[T any](v any) (T, bool) {
	var zero T
	switch any(zero).(type) {
	case bool:
		if b, ok := toBool(v); ok {
			return any(b).(T), true //nolint:errcheck,forcetypeassert // type switch guarantees
		}
	case int32:
		if n, ok := toInt64(v); ok && n >= math.MinInt32 && n <= math.MaxInt32 {
			return any(int32(n)).(T), true //nolint:errcheck,forcetypeassert // bounds-checked above
		}
	case int:
		if n, ok := toInt64(v); ok && n >= math.MinInt && n <= math.MaxInt {
			return any(int(n)).(T), true //nolint:errcheck,forcetypeassert // bounds-checked above
		}
	case int64:
		if n, ok := toInt64(v); ok {
			return any(n).(T), true //nolint:errcheck,forcetypeassert // type switch guarantees
		}
	case float64:
		if f, ok := toFloat64(v); ok {
			return any(f).(T), true //nolint:errcheck,forcetypeassert // type switch guarantees
		}
	case string:
		return any(toString(v)).(T), true //nolint:errcheck,forcetypeassert // type switch guarantees
	}
	return zero, false
}

func toBool(v any) (value, ok bool) { //nolint:nonamedreturns // documents return intent
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		// The empty string is deliberately absent from both lists: it is
		// "not a boolean", not a confirmed false. The CCU's own XML-RPC
		// value library — the one rfd and hs485d link — rejects it as a
		// parse failure in both textual boolean readers
		// (../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:425-437,
		// boolFromXml takes only the decimal tokens 0 and 1 and fails when
		// strtol consumed nothing; :470-488, boolFromText takes exactly
		// "true" and "false"), and its emitters never produce one (:439
		// writes "1"/"0", :490 writes "true"/"false").
		//
		// This reader sits on the ingest side: the REST and MQTT write paths
		// coerce through internal/parameter.Coerce, which hands a typed Go
		// bool down, so the empty string never reaches here from them. The
		// same empty-string branch was removed there, where it did reach a
		// device.
		//
		// The eight named literals are wider than that firmware set —
		// "on"/"yes"/"off"/"no" appear in no CCU decoder. They rest on the
		// Python port alone and are kept for client compatibility;
		// unverified against the CCU.
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "on", "yes":
			return true, true
		case "false", "0", "off", "no":
			return false, true
		}
	case int:
		return x != 0, true
	case int32:
		return x != 0, true
	case int64:
		return x != 0, true
	case float32:
		return x != 0, true
	case float64:
		return x != 0, true
	}
	return false, false
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case float32:
		return int64(x), true
	case float64:
		return int64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			// ParseFloat accepts "nan"/"inf" as valid float text; a
			// non-finite value has no JSON representation and would
			// corrupt every north-bound response encoding the resulting
			// data point alongside healthy ones — reject it here rather
			// than seed a float64 data point that can never round-trip.
			return f, true
		}
	}
	return 0, false
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
