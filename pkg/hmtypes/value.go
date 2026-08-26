// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParamValue represents a primitive CCU parameter value in its native
// Go type. The zero value (nil interior, KindNone) means "absent".
//
// Only the six types the CCU can carry over a paramset are supported:
// bool, int, float64, string, []string (enum), and nil. Higher-level
// types (structs, arrays) belong on a different abstraction.
type ParamValue struct {
	Kind   ValueKind
	Bool   bool
	Int    int
	Float  float64
	String string
	List   []string
}

// ValueKind tags the active member of a [ParamValue].
type ValueKind int

// ValueKind values. The string form is stable (used in logs and tests).
const (
	ValueKindNone ValueKind = iota
	ValueKindBool
	ValueKindInt
	ValueKindFloat
	ValueKindString
	ValueKindList
)

// String returns a compact kind label.
func (k ValueKind) String() string {
	switch k {
	case ValueKindNone:
		return "none"
	case ValueKindBool:
		return "bool"
	case ValueKindInt:
		return "int"
	case ValueKindFloat:
		return "float"
	case ValueKindString:
		return "string"
	case ValueKindList:
		return "list"
	default:
		return "unknown"
	}
}

// BoolValue returns a ParamValue holding b.
func BoolValue(b bool) ParamValue { return ParamValue{Kind: ValueKindBool, Bool: b} }

// IntValue returns a ParamValue holding i.
func IntValue(i int) ParamValue { return ParamValue{Kind: ValueKindInt, Int: i} }

// FloatValue returns a ParamValue holding f.
func FloatValue(f float64) ParamValue { return ParamValue{Kind: ValueKindFloat, Float: f} }

// StringValue returns a ParamValue holding s.
func StringValue(s string) ParamValue { return ParamValue{Kind: ValueKindString, String: s} }

// ListValue returns a ParamValue holding a copy of l.
func ListValue(l []string) ParamValue {
	out := make([]string, len(l))
	copy(out, l)
	return ParamValue{Kind: ValueKindList, List: out}
}

// NoneValue returns the absent-value sentinel.
func NoneValue() ParamValue { return ParamValue{Kind: ValueKindNone} }

// NewParamValue converts an arbitrary Go value into a [ParamValue].
// Supported input types are the ones a JSON decoder produces plus the
// primitive Go counterparts: bool, int/int32/int64, float64, string,
// []string, []any (of strings), and nil.
func NewParamValue(v any) (ParamValue, error) {
	switch x := v.(type) {
	case nil:
		return NoneValue(), nil
	case bool:
		return BoolValue(x), nil
	case int:
		return IntValue(x), nil
	case int32:
		return IntValue(int(x)), nil
	case int64:
		// Preserve values that exceed the platform int range (relevant on
		// 32-bit builds such as armv7) as a float rather than wrapping.
		if x > math.MaxInt || x < math.MinInt {
			return FloatValue(float64(x)), nil
		}
		return IntValue(int(x)), nil
	case float32:
		return FloatValue(float64(x)), nil
	case float64:
		// Collapse integer-valued floats ("5" in JSON) back to int so
		// sysvars keyed as INTEGER don't round-trip as a float. Bound to
		// the int32 range before narrowing: int32's limits are exactly
		// representable as float64 and lie safely inside the platform int
		// range, so int(x) cannot overflow (unlike float64(math.MaxInt),
		// which rounds up to 2^63 and leaves the conversion unsound). CCU
		// integer values are well within int32; larger integer-valued
		// floats keep their float representation.
		if x == math.Trunc(x) && x >= math.MinInt32 && x <= math.MaxInt32 {
			return IntValue(int(x)), nil
		}
		return FloatValue(x), nil
	case string:
		return StringValue(x), nil
	case []string:
		return ListValue(x), nil
	case []any:
		out := make([]string, 0, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return ParamValue{}, fmt.Errorf("NewParamValue: list element %d is %T, want string", i, e)
			}
			out = append(out, s)
		}
		return ListValue(out), nil
	}
	return ParamValue{}, fmt.Errorf("NewParamValue: unsupported type %T", v)
}

// Unwrap returns the active field as an untyped any. Returns nil for
// [ValueKindNone].
func (v ParamValue) Unwrap() any {
	switch v.Kind {
	case ValueKindNone:
		return nil
	case ValueKindBool:
		return v.Bool
	case ValueKindInt:
		return v.Int
	case ValueKindFloat:
		return v.Float
	case ValueKindString:
		return v.String
	case ValueKindList:
		out := make([]string, len(v.List))
		copy(out, v.List)
		return out
	}
	return nil
}

// IsNone reports whether v is the absent-value sentinel.
func (v ParamValue) IsNone() bool { return v.Kind == ValueKindNone }

// AsString returns a canonical string rendering of the value.
func (v ParamValue) AsString() string {
	switch v.Kind {
	case ValueKindNone:
		return ""
	case ValueKindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case ValueKindInt:
		return strconv.Itoa(v.Int)
	case ValueKindFloat:
		return strconv.FormatFloat(v.Float, 'f', -1, 64)
	case ValueKindString:
		return v.String
	case ValueKindList:
		return "[" + strings.Join(v.List, ",") + "]"
	default:
		return fmt.Sprintf("<unknown:%d>", v.Kind)
	}
}

// Equal reports structural equality.
func (v ParamValue) Equal(other ParamValue) bool {
	if v.Kind != other.Kind {
		return false
	}
	switch v.Kind {
	case ValueKindNone:
		return true
	case ValueKindBool:
		return v.Bool == other.Bool
	case ValueKindInt:
		return v.Int == other.Int
	case ValueKindFloat:
		return v.Float == other.Float
	case ValueKindString:
		return v.String == other.String
	case ValueKindList:
		if len(v.List) != len(other.List) {
			return false
		}
		for i := range v.List {
			if v.List[i] != other.List[i] {
				return false
			}
		}
		return true
	}
	return false
}
