// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

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
// decodes an integer to int32, JSON-decoded payloads to float64, and the
// unsigned and 32-bit float forms reach it from transports that decode a
// CCU integer without widening it first.
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
	case uint:
		return int(v), true //nolint:gosec // bounded against ValueList by every caller
	case uint32:
		return int(v), true
	case uint64:
		return int(v), true //nolint:gosec // bounded against ValueList by every caller
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// EnumValueIsIndex reports whether an ENUM parameter is written by its
// 0-based VALUE_LIST index rather than by its label, decided from the
// descriptor's MIN.
//
// rfd describes every option type with integer bounds
// (../OpenCCU-Base/src/libhsscomm/HSSLogicalTypeOption.cpp:96-112, MIN 0 and
// MAX len-1), so an integer MIN marks an index-based ENUM. The HM-CC-VG-1
// heating group carries the same bounds as numeric strings (MIN "0", MAX "2"
// on WEEK_PROGRAM_POINTER); those are indices too, as long as the string is
// not itself a VALUE_LIST entry. A MIN that is a label marks the label-based
// ENUMs of the HmIP family. Writing a label to the heating group breaks its
// settings page in the CCU WebUI until a reboot.
func EnumValueIsIndex(desc hmproto.ParameterData) bool {
	if desc.Type != hmenum.ParameterTypeEnum || len(desc.ValueList) == 0 {
		return false
	}
	raw := bytes.TrimSpace(desc.Min)
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || !isDigits(s) {
			return false
		}
		return !slices.Contains(desc.ValueList, s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return false
	}
	_, err := n.Int64()
	return err == nil
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// EnumLabelFromWire resolves an ENUM value as a transport delivers it,
// where a numeric string is the index rather than a label.
//
// This is deliberately a second, named semantics beside [EnumLabel] and not
// a widening of it. [EnumLabel] answers "what does this value mean" for a
// data point whose descriptor may spell an ENUM out as its label, so there a
// string is the answer. A transport frame is the other case: JSON-RPC
// renders the same integer index that XML-RPC sends as an int into "3", and
// reading that as a label would publish "3" where every other plane
// publishes "OPEN". Both readings are right for their own caller, which is
// why each gets a name here instead of one of them living in an adapter.
//
// A non-numeric string is still a label, and bools are still refused — an
// ENUM read must not turn `true` into VALUE_LIST[1].
func EnumLabelFromWire(desc hmproto.ParameterData, raw any) (label string, ok bool) {
	if s, isString := raw.(string); isString {
		idx, err := strconv.Atoi(s)
		if err != nil {
			return s, s != ""
		}
		if idx < 0 || idx >= len(desc.ValueList) {
			return "", false
		}
		return desc.ValueList[idx], true
	}
	return EnumLabel(desc, raw)
}
