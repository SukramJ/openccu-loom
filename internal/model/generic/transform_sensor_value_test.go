// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func descWithValueList(list ...string) hmproto.ParameterData {
	return hmproto.ParameterData{ValueList: list}
}

func descEmpty() hmproto.ParameterData { return hmproto.ParameterData{} }

// TestTransformSensorValueWithEnum verifies that integer indices are
// resolved to their label strings when a VALUE_LIST is present.
func TestTransformSensorValueWithEnum(t *testing.T) {
	t.Parallel()
	desc := descWithValueList("OPEN", "TILTED", "CLOSED")
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"int32(0)", int32(0), "OPEN"},
		{"int32(1)", int32(1), "TILTED"},
		{"int32(2)", int32(2), "CLOSED"},
		{"int(0)", int(0), "OPEN"},
		{"int64(2)", int64(2), "CLOSED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := generic.TransformSensorValue(desc, tc.raw)
			if got != tc.want {
				t.Fatalf("TransformSensorValue(%v) = %v; want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestTransformSensorValueOutOfRange verifies that an out-of-bounds
// index is passed through unchanged (no panic, no truncation).
func TestTransformSensorValueOutOfRange(t *testing.T) {
	t.Parallel()
	desc := descWithValueList("OPEN", "TILTED", "CLOSED")
	raw := int32(99)
	got := generic.TransformSensorValue(desc, raw)
	if got != raw {
		t.Fatalf("out-of-range: got %v; want original %v", got, raw)
	}
}

// TestTransformSensorValueNegativeIndex verifies that a negative
// index is passed through unchanged.
func TestTransformSensorValueNegativeIndex(t *testing.T) {
	t.Parallel()
	desc := descWithValueList("OPEN", "TILTED", "CLOSED")
	raw := int32(-1)
	got := generic.TransformSensorValue(desc, raw)
	if got != raw {
		t.Fatalf("negative index: got %v; want original %v", got, raw)
	}
}

// TestTransformSensorValueNoValueList verifies pass-through when no
// VALUE_LIST is present on the descriptor.
func TestTransformSensorValueNoValueList(t *testing.T) {
	t.Parallel()
	desc := descEmpty()
	raw := int32(1)
	got := generic.TransformSensorValue(desc, raw)
	if got != raw {
		t.Fatalf("no value_list: got %v; want original %v", got, raw)
	}
}

// TestTransformSensorValueNonInt verifies that a non-integer raw value
// is passed through when a VALUE_LIST is present (e.g. already a
// string label, or a float from a different sensor type).
func TestTransformSensorValueNonInt(t *testing.T) {
	t.Parallel()
	desc := descWithValueList("OPEN", "TILTED", "CLOSED")
	raw := "already_a_string"
	got := generic.TransformSensorValue(desc, raw)
	if got != raw {
		t.Fatalf("non-int: got %v; want original %v", got, raw)
	}
}

// TestTransformSensorValueFloatNonInt verifies that float64 is not
// treated as an integer index (no accidental conversion).
func TestTransformSensorValueFloatNonInt(t *testing.T) {
	t.Parallel()
	desc := descWithValueList("OPEN", "TILTED", "CLOSED")
	raw := float64(1.0)
	got := generic.TransformSensorValue(desc, raw)
	if got != raw {
		t.Fatalf("float64: got %v; want original %v", got, raw)
	}
}
