// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ParamBool
// ---------------------------------------------------------------------------

func TestParamBoolMissingKey(t *testing.T) {
	_, err := ParamBool(map[string]any{}, "on")
	if !errors.Is(err, ErrServiceMissingParam) {
		t.Fatalf("want ErrServiceMissingParam, got %v", err)
	}
}

func TestParamBoolTypes(t *testing.T) {
	cases := []struct {
		raw  any
		want bool
	}{
		{true, true},
		{false, false},
		{float64(1), true},
		{float64(0), false},
		{int(1), true},
		{int(0), false},
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"on", true},
		{"ON", true},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"0", false},
		{"off", false},
		{"OFF", false},
	}
	for _, c := range cases {
		got, err := ParamBool(map[string]any{"k": c.raw}, "k")
		if err != nil {
			t.Errorf("raw=%v: unexpected error %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("raw=%v: got %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParamBoolInvalidType(t *testing.T) {
	_, err := ParamBool(map[string]any{"k": "maybe"}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParamFloat64
// ---------------------------------------------------------------------------

func TestParamFloat64MissingKey(t *testing.T) {
	_, err := ParamFloat64(map[string]any{}, "x")
	if !errors.Is(err, ErrServiceMissingParam) {
		t.Fatalf("want ErrServiceMissingParam, got %v", err)
	}
}

func TestParamFloat64Types(t *testing.T) {
	cases := []struct {
		raw  any
		want float64
	}{
		{float64(3.14), 3.14},
		{float32(2.5), float64(float32(2.5))},
		{int(7), 7.0},
		{int32(8), 8.0},
		{int64(9), 9.0},
		{"1.5", 1.5},
	}
	for _, c := range cases {
		got, err := ParamFloat64(map[string]any{"k": c.raw}, "k")
		if err != nil {
			t.Errorf("raw=%v: unexpected error %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("raw=%v: got %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParamFloat64InvalidType(t *testing.T) {
	_, err := ParamFloat64(map[string]any{"k": "notanumber"}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam, got %v", err)
	}
}

func TestParamFloat64InvalidKind(t *testing.T) {
	_, err := ParamFloat64(map[string]any{"k": true}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for bool input, got %v", err)
	}
}

// TestParamFloat64TrailingGarbage verifies that a numeric string followed by
// non-numeric trailing characters is rejected rather than silently truncated
// (fmt.Sscanf stops at the first unparsable rune and reports no error).
func TestParamFloat64TrailingGarbage(t *testing.T) {
	_, err := ParamFloat64(map[string]any{"k": "42xyz"}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for trailing garbage, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParamInt32
// ---------------------------------------------------------------------------

func TestParamInt32MissingKey(t *testing.T) {
	_, err := ParamInt32(map[string]any{}, "n")
	if !errors.Is(err, ErrServiceMissingParam) {
		t.Fatalf("want ErrServiceMissingParam, got %v", err)
	}
}

func TestParamInt32Types(t *testing.T) {
	cases := []struct {
		raw  any
		want int32
	}{
		{int32(42), 42},
		{int(100), 100},
		{int64(200), 200},
		{float64(99.0), 99},
		{"123", 123},
	}
	for _, c := range cases {
		got, err := ParamInt32(map[string]any{"k": c.raw}, "k")
		if err != nil {
			t.Errorf("raw=%v: unexpected error %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("raw=%v: got %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestParamInt32OverflowInt(t *testing.T) {
	// The value is built at run time rather than written as a constant. As
	// `int(1 << 32)` it does not compile where int is 32 bits wide — a test
	// asserting that an oversized int is rejected, which itself could not be
	// compiled for the platform whose int width is the whole point. That is
	// not hypothetical: this project ships an armv7 binary, and this line was
	// the only thing standing between the module and a 32-bit `go vet`.
	if strconv.IntSize == 32 {
		t.Skip("int is 32 bits here, so no int value exceeds int32 and the case under test cannot arise")
	}
	tooBig := int(math.MaxInt32)
	tooBig++

	_, err := ParamInt32(map[string]any{"k": tooBig}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for overflow int, got %v", err)
	}
}

func TestParamInt32OverflowInt64(t *testing.T) {
	_, err := ParamInt32(map[string]any{"k": int64(1 << 33)}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for overflow int64, got %v", err)
	}
}

func TestParamInt32OverflowFloat64(t *testing.T) {
	_, err := ParamInt32(map[string]any{"k": float64(1 << 32)}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for overflow float64, got %v", err)
	}
}

func TestParamInt32InvalidString(t *testing.T) {
	_, err := ParamInt32(map[string]any{"k": "abc"}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for bad string, got %v", err)
	}
}

// TestParamInt32TrailingGarbage verifies that a numeric string followed by
// non-numeric trailing characters is rejected rather than silently truncated
// (fmt.Sscanf stops at the first unparsable rune and reports no error).
func TestParamInt32TrailingGarbage(t *testing.T) {
	_, err := ParamInt32(map[string]any{"k": "42xyz"}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for trailing garbage, got %v", err)
	}
}

func TestParamInt32InvalidKind(t *testing.T) {
	_, err := ParamInt32(map[string]any{"k": true}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam for bool input, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParamString
// ---------------------------------------------------------------------------

func TestParamStringMissingKey(t *testing.T) {
	_, err := ParamString(map[string]any{}, "s")
	if !errors.Is(err, ErrServiceMissingParam) {
		t.Fatalf("want ErrServiceMissingParam, got %v", err)
	}
}

func TestParamStringTypes(t *testing.T) {
	cases := []struct {
		raw  any
		want string
	}{
		{"hello", "hello"},
		{true, "true"},
		{int(42), "42"},
		{int32(7), "7"},
		{int64(99), "99"},
		{float32(1.5), "1.5"},
		{float64(3.14), "3.14"},
	}
	for _, c := range cases {
		got, err := ParamString(map[string]any{"k": c.raw}, "k")
		if err != nil {
			t.Errorf("raw=%v: unexpected error %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("raw=%v: got %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestParamStringInvalidType(t *testing.T) {
	_, err := ParamString(map[string]any{"k": []int{1, 2}}, "k")
	if !errors.Is(err, ErrServiceInvalidParam) {
		t.Fatalf("want ErrServiceInvalidParam, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// EpochSeconds (wrapper.go)
// ---------------------------------------------------------------------------

func TestEpochSecondsZeroTime(t *testing.T) {
	if got := EpochSeconds(time.Time{}); got != 0 {
		t.Fatalf("zero time: want 0, got %v", got)
	}
}

func TestEpochSecondsNonZero(t *testing.T) {
	ts := time.Unix(1_700_000_000, 500_000_000)
	got := EpochSeconds(ts)
	want := float64(ts.UnixNano()) / 1e9
	if got != want {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// ---------------------------------------------------------------------------
// RegisterGlobalScalarArgKey (registry.go)
// ---------------------------------------------------------------------------

func TestRegisterGlobalScalarArgKeyEmptyInputsNoOp(t *testing.T) {
	// Empty method or key: should be a no-op (no panic, no entry created).
	before := GlobalScalarArgKey("__empty_test_method__")
	RegisterGlobalScalarArgKey("", "somekey")
	RegisterGlobalScalarArgKey("__empty_test_method__", "")
	after := GlobalScalarArgKey("__empty_test_method__")
	if before != after {
		t.Fatalf("empty inputs should not modify globalScalarArgs")
	}
}

func TestRegisterGlobalScalarArgKeyConflictPanics(t *testing.T) {
	const method = "__conflict_test_unique_99__"
	RegisterGlobalScalarArgKey(method, "keyA")
	defer func() {
		// Clean up so subsequent runs don't see the key.
		globalScalarArgsMu.Lock()
		delete(globalScalarArgs, method)
		globalScalarArgsMu.Unlock()
	}()

	v := recoverPanic(func() { RegisterGlobalScalarArgKey(method, "keyB") })
	if v == nil {
		t.Fatal("expected panic on conflicting key registration")
	}
}

func TestRegisterGlobalScalarArgKeySameKeyNoOp(t *testing.T) {
	const method = "__same_key_test_unique_88__"
	RegisterGlobalScalarArgKey(method, "keyA")
	defer func() {
		globalScalarArgsMu.Lock()
		delete(globalScalarArgs, method)
		globalScalarArgsMu.Unlock()
	}()
	// Re-registering the same key must not panic.
	RegisterGlobalScalarArgKey(method, "keyA")
	if GlobalScalarArgKey(method) != "keyA" {
		t.Fatal("same-key re-registration must keep the value")
	}
}

// ---------------------------------------------------------------------------
// isZero (payload.go) — exercised via ForWith / IncludeZero paths
// ---------------------------------------------------------------------------

type zeroTestStruct struct {
	PtrField   *string        `payload:"state"`
	SliceField []string       `payload:"state"`
	MapField   map[string]int `payload:"state"`
	BoolField  bool           `payload:"state"`
	IntField   int            `payload:"state"`
	FloatField float64        `payload:"state"`
}

func TestIsZeroViaForWith(t *testing.T) {
	s := zeroTestStruct{} // all fields zero
	withZero := ForWith(&s, KindState, Options{IncludeZero: true})
	withoutZero := ForWith(&s, KindState, Options{IncludeZero: false})

	if len(withZero) == 0 {
		t.Error("IncludeZero=true must not omit any tagged fields")
	}
	if len(withoutZero) != 0 {
		t.Errorf("IncludeZero=false must omit all zero fields, got %v", withoutZero)
	}
}
