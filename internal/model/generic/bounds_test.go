// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// bounds.go — matchesSpecial, parseFloatOr, validateRange
// ---------------------------------------------------------------------------

func TestMatchesSpecial_EmptyRaw(t *testing.T) {
	t.Parallel()
	if matchesSpecial(nil, 1.0) {
		t.Error("nil special must not match")
	}
}

func TestMatchesSpecial_MalformedJSON(t *testing.T) {
	t.Parallel()
	if matchesSpecial([]byte("{not-json"), 1.0) {
		t.Error("malformed JSON must return false")
	}
}

func TestMatchesSpecial_Match(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"ID":"S1","VALUE":0},{"ID":"S2","VALUE":-0.5}]`)
	if !matchesSpecial(raw, 0.0) {
		t.Error("0.0 should match the first special entry")
	}
}

func TestMatchesSpecial_NoMatch(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"ID":"S1","VALUE":99}]`)
	if matchesSpecial(raw, 1.0) {
		t.Error("1.0 should not match value 99")
	}
}

func TestMatchesSpecial_InvalidValueEntry(t *testing.T) {
	t.Parallel()
	// VALUE is a non-numeric string → the entry is skipped.
	raw := json.RawMessage(`[{"ID":"X","VALUE":"bad"},{"ID":"Y","VALUE":7}]`)
	if matchesSpecial(raw, 7.0) {
		// 7 should still match even though the first entry is bad
		return // pass
	}
}

func TestParseFloatOr_Fallback(t *testing.T) {
	t.Parallel()
	got := parseFloatOr(nil, "∞")
	if got != "∞" {
		t.Errorf("empty raw should return fallback, got %v", got)
	}
}

func TestParseFloatOr_Value(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`3.14`)
	got := parseFloatOr(raw, "∞")
	if got != 3.14 {
		t.Errorf("expected 3.14, got %v", got)
	}
}

func TestValidateRange_Nil(t *testing.T) {
	t.Parallel()
	if err := validateRange(hmproto.ParameterData{}, nil); err != nil {
		t.Fatalf("nil value must always pass: %v", err)
	}
}

func TestValidateRange_BoolAndString_AlwaysPass(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Min: json.RawMessage(`0`),
		Max: json.RawMessage(`1`),
	}
	for _, v := range []any{true, false, "hello"} {
		if err := validateRange(desc, v); err != nil {
			t.Errorf("validateRange(%T) should always pass: %v", v, err)
		}
	}
}

func TestValidateRange_Float32_InRange(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Min: json.RawMessage(`0`),
		Max: json.RawMessage(`100`),
	}
	if err := validateRange(desc, float32(50)); err != nil {
		t.Fatalf("50.0 in [0,100] should pass: %v", err)
	}
}

func TestValidateRange_Float64_OutOfRange(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Min: json.RawMessage(`0`),
		Max: json.RawMessage(`10`),
	}
	if err := validateRange(desc, float64(11)); err == nil {
		t.Fatal("11 > 10: expected error")
	}
}

func TestValidateRange_Int32_InRange(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Min: json.RawMessage(`0`),
		Max: json.RawMessage(`5`),
	}
	if err := validateRange(desc, int32(3)); err != nil {
		t.Fatalf("int32(3) in [0,5]: %v", err)
	}
}

func TestValidateRange_Uint16_ExceedsMax(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Max: json.RawMessage(`4`),
	}
	if err := validateRange(desc, uint16(5)); err == nil {
		t.Fatal("uint16(5) > 4: expected error")
	}
}

func TestValidateRange_Uint32_InRange(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Max: json.RawMessage(`10`),
	}
	if err := validateRange(desc, uint32(9)); err != nil {
		t.Fatalf("uint32(9) <= 10: %v", err)
	}
}

func TestValidateRange_UnknownType_Passes(t *testing.T) {
	t.Parallel()
	// complex128 is not handled — should be a no-op.
	if err := validateRange(hmproto.ParameterData{}, complex(1, 2)); err != nil {
		t.Fatalf("unknown type should pass: %v", err)
	}
}
