// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import "github.com/SukramJ/openccu-loom/pkg/hmproto"

// EnumLabel resolves a value observed on an ENUM parameter to its
// VALUE_LIST label.
//
// The same ENUM reaches the daemon in two shapes: the wire value of an
// ENUM is its 0-based index, and that is what a data point carrying the
// descriptor's own type pushes, while the parameters some firmwares
// spell out arrive as the label itself. A consumer that type-asserts to
// one shape drops the other silently — the value never lands and nothing
// logs — so the interpretation lives here, next to the descriptor rules
// [Coerce] applies in the write direction.
//
// ok is false for an empty label, a value that is not an enum index, or
// an index outside the descriptor's VALUE_LIST.
func EnumLabel(desc hmproto.ParameterData, raw any) (label string, ok bool) {
	if s, isLabel := raw.(string); isLabel {
		return s, s != ""
	}
	idx, isIndex := enumIndex(raw)
	if !isIndex || idx < 0 || idx >= len(desc.ValueList) {
		return "", false
	}
	return desc.ValueList[idx], true
}

// enumIndex narrows the integer forms an ENUM index arrives in: XML-RPC
// decodes an integer to int32, JSON-decoded payloads to float64.
//
// Deliberately narrower than [asInt], which also coerces bools and
// numeric strings: an ENUM read must not turn `true` into VALUE_LIST[1].
func enumIndex(raw any) (int, bool) {
	switch v := raw.(type) {
	case int32:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
