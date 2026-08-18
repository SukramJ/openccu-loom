// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"math"
	"testing"
)

// TestAsIntRejectsFloatsItCannotRepresent pins the guard on the float
// branches of asInt.
//
// Go leaves a float-to-int conversion undefined when the value does not
// fit, or is NaN or an infinity — the result is whatever the platform
// produces. On the 32-bit shipped target the limit is just past two
// billion, which a runaway CCU counter reaches, so without the guard such
// a value is stored as a plausible small number instead of being refused.
func TestAsIntRejectsFloatsItCannotRepresent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   any
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"beyond int", math.MaxFloat64},
		{"below int", -math.MaxFloat64},
		{"NaN as float32", float32(math.NaN())},
	} {
		if _, err := asInt(tc.in); err == nil {
			t.Errorf("asInt(%s): want an error, got none", tc.name)
		}
	}

	// The ordinary path keeps truncating toward zero, as Go does.
	for _, tc := range []struct {
		in   any
		want int
	}{
		{3.7, 3},
		{-3.7, -3},
		{float32(2.5), 2},
	} {
		got, err := asInt(tc.in)
		if err != nil {
			t.Errorf("asInt(%v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("asInt(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
