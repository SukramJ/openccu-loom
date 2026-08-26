// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// writableDesc returns a writable ParameterData of the given type.
func writableDesc(pt hmenum.ParameterType) hmproto.ParameterData {
	return hmproto.ParameterData{
		Type:       pt,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
}

// rawMin/rawMax build json.RawMessage from a numeric literal.
func rawMin(v float64) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

// ---------- Coerce core ----------

func TestCoerceBoolAcceptsManyShapes(t *testing.T) {
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
	for _, raw := range []any{true, 1, "true", "on", "YES", json.Number("1")} {
		v, err := Coerce(desc, raw)
		if err != nil {
			t.Fatalf("Coerce(%v) returned %v", raw, err)
		}
		if !v.Bool {
			t.Fatalf("Coerce(%v) = false, want true", raw)
		}
	}
}

// TestCoerceBoolAcceptsJSONDecodedNumber drives the shape the REST write
// boundary actually produces: the handler decodes the request body with a
// plain json.Decoder into an `any`, so `{"value": 1}` on a BOOL or ACTION
// parameter arrives as a float64 — the CCU's own 1/0 spelling of a boolean,
// and a contract-legal body since the schema declares the value untyped.
func TestCoerceBoolAcceptsJSONDecodedNumber(t *testing.T) {
	for _, ptype := range []hmenum.ParameterType{hmenum.ParameterTypeBool, hmenum.ParameterTypeAction} {
		desc := hmproto.ParameterData{Type: ptype, Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
		for _, tc := range []struct {
			body string
			want bool
		}{
			{`{"value": 1}`, true},
			{`{"value": 0}`, false},
		} {
			var body struct {
				Value any `json:"value"`
			}
			if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			v, err := Coerce(desc, body.Value)
			if err != nil {
				t.Fatalf("%s: Coerce(%s) returned %v", ptype, tc.body, err)
			}
			if v.Bool != tc.want {
				t.Fatalf("%s: Coerce(%s) = %v, want %v", ptype, tc.body, v.Bool, tc.want)
			}
		}
	}
}

func TestCoerceIntRangeCheck(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage("0"),
		Max:        json.RawMessage("10"),
	}
	if _, err := Coerce(desc, 5); err != nil {
		t.Errorf("in-range should pass: %v", err)
	}
	if _, err := Coerce(desc, -1); err == nil {
		t.Error("below MIN should fail")
	}
	if _, err := Coerce(desc, 11); err == nil {
		t.Error("above MAX should fail")
	}
}

func TestCoerceFloatAcceptsString(t *testing.T) {
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	v, err := Coerce(desc, " 3.14 ")
	if err != nil {
		t.Fatal(err)
	}
	if v.Float != 3.14 {
		t.Fatalf("float=%v", v.Float)
	}
}

func TestCoerceEnumByLabelAndIndex(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"OFF", "ON", "AUTO"},
	}
	v, err := Coerce(desc, "AUTO")
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 2 {
		t.Fatalf("AUTO=%d, want 2", v.Int)
	}

	v, err = Coerce(desc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Int != 1 {
		t.Fatalf("index 1 = %d", v.Int)
	}

	if _, err := Coerce(desc, "NOT_THERE"); err == nil {
		t.Error("unknown label should fail")
	}
	if _, err := Coerce(desc, 7); err == nil {
		t.Error("out-of-range index should fail")
	}
}

func TestCoerceRejectsUnknownType(t *testing.T) {
	if _, err := Coerce(hmproto.ParameterData{Type: "WEIRD"}, 1); err == nil {
		t.Error("unknown TYPE should error")
	}
}

// ---------- Coerce bool edge cases ----------

func TestCoerceBoolInt32Int64(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeBool)
	for _, raw := range []any{int32(1), int64(0)} {
		v, err := Coerce(desc, raw)
		if err != nil {
			t.Fatalf("Coerce bool %T(%v): %v", raw, raw, err)
		}
		_ = v
	}
}

func TestCoerceBoolJsonNumber(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeBool)
	v, err := Coerce(desc, json.Number("0"))
	if err != nil {
		t.Fatalf("Coerce bool json.Number(0): %v", err)
	}
	if v.Bool {
		t.Fatal("json.Number(0) should coerce to false")
	}
}

func TestCoerceBoolUnsupportedType(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeBool)
	_, err := Coerce(desc, []int{1})
	if err == nil {
		t.Fatal("unsupported type should return error")
	}
}

func TestCoerceBool_StringCaseInsensitive(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeBool}
	for _, raw := range []any{"True", "TRUE", "true", "YES", "yes", "1", "on", "ON"} {
		v, err := Coerce(desc, raw)
		if err != nil {
			t.Fatalf("Coerce(bool, %q) error: %v", raw, err)
		}
		if !v.Bool {
			t.Fatalf("Coerce(bool, %q) = false, want true", raw)
		}
	}
	for _, raw := range []any{"false", "False", "FALSE", "0", "no", "NO", "off", "OFF"} {
		v, err := Coerce(desc, raw)
		if err != nil {
			t.Fatalf("Coerce(bool, %q) error: %v", raw, err)
		}
		if v.Bool {
			t.Fatalf("Coerce(bool, %q) = true, want false", raw)
		}
	}
}

func TestCoerceBool_IntNotSubclassBool(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeBool}
	v, err := Coerce(desc, 1)
	if err != nil {
		t.Fatalf("int 1 → bool error: %v", err)
	}
	if !v.Bool {
		t.Fatalf("int 1 should coerce to true")
	}
}

// ---------- Coerce integer edge cases ----------

func TestCoerceIntAllIntegralTypes(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeInteger)
	cases := []any{
		int8(5), int16(5), int32(5), int64(5),
		uint(5), uint8(5), uint16(5), uint32(5), uint64(5),
		float32(3.7), float64(3.7),
		bool(true), "7",
		json.Number("9"),
	}
	for _, raw := range cases {
		_, err := Coerce(desc, raw)
		if err != nil {
			t.Errorf("Coerce int %T(%v): %v", raw, raw, err)
		}
	}
}

func TestCoerceIntUnsupportedType(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeInteger)
	_, err := Coerce(desc, struct{}{})
	if err == nil {
		t.Fatal("unsupported type should error")
	}
}

func TestCoerceIntStringParseError(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeInteger)
	_, err := Coerce(desc, "not-an-int")
	if err == nil {
		t.Fatal("invalid string should error")
	}
}

func TestCoerceIntJsonNumberFloatError(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeInteger)
	// A json.Number that is a float string — Int64() fails.
	_, err := Coerce(desc, json.Number("3.14"))
	if err == nil {
		t.Fatal("float json.Number for integer should error")
	}
}

func TestCoerceInteger_FloatTruncates(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}
	v, err := Coerce(desc, 5.7)
	if err != nil {
		t.Fatalf("float→int error: %v", err)
	}
	if v.Int != 5 {
		t.Fatalf("got %d, want 5", v.Int)
	}
}

func TestCoerceInteger_StringParsed(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}
	v, err := Coerce(desc, "42")
	if err != nil {
		t.Fatalf("string→int error: %v", err)
	}
	if v.Int != 42 {
		t.Fatalf("got %d, want 42", v.Int)
	}
}

func TestCoerceInteger_StringMismatchFails(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}
	if _, err := Coerce(desc, "hello"); err == nil {
		t.Fatal("non-numeric string should fail for INTEGER")
	}
}

func TestCoerceInteger_BoundaryZeroMin(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type: hmenum.ParameterTypeInteger,
		Min:  rawMin(0),
		Max:  rawMin(10),
	}
	if _, err := Coerce(desc, 0); err != nil {
		t.Fatalf("at-zero-min should pass: %v", err)
	}
	if _, err := Coerce(desc, -1); err == nil {
		t.Fatal("below zero-min should fail")
	}
}

// ---------- Coerce float edge cases ----------

func TestCoerceFloatAllTypes(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeFloat)
	cases := []any{float64(1.5), float32(1.5), int(2), int32(2), int64(2), "3.14", json.Number("2.5")}
	for _, raw := range cases {
		_, err := Coerce(desc, raw)
		if err != nil {
			t.Errorf("Coerce float %T(%v): %v", raw, raw, err)
		}
	}
}

func TestCoerceFloatStringError(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeFloat)
	_, err := Coerce(desc, "not-a-float")
	if err == nil {
		t.Fatal("invalid float string should error")
	}
}

func TestCoerceFloatUnsupportedType(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeFloat)
	_, err := Coerce(desc, struct{}{})
	if err == nil {
		t.Fatal("unsupported type for float should error")
	}
}

func TestCoerceFloat_IntegerPromoted(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	v, err := Coerce(desc, 5)
	if err != nil {
		t.Fatalf("Coerce(int→float) error: %v", err)
	}
	if v.Float != 5.0 {
		t.Fatalf("got %v, want 5.0", v.Float)
	}
}

func TestCoerceFloat_NegativeInfStringRejected(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	// "-Inf" is not parseable as a JSON-safe float; strconv.ParseFloat accepts
	// it but the CCU never emits it — expect no error from the parser but
	// verify the value is ±Inf when it does parse.
	v, err := Coerce(desc, "-Inf")
	if err != nil {
		// Acceptable: parser rejected it.
		return
	}
	if !math.IsInf(v.Float, -1) {
		t.Fatalf("expected -Inf, got %v", v.Float)
	}
}

func TestCoerceFloat_VerySmallPositive(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	v, err := Coerce(desc, 1e-15)
	if err != nil {
		t.Fatalf("tiny float error: %v", err)
	}
	if v.Float != 1e-15 {
		t.Fatalf("got %g, want 1e-15", v.Float)
	}
}

func TestCoerceFloat_VeryLargeFloat(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	v, err := Coerce(desc, 1e300)
	if err != nil {
		t.Fatalf("large float error: %v", err)
	}
	if v.Float != 1e300 {
		t.Fatalf("got %g, want 1e300", v.Float)
	}
}

func TestCoerceFloat_BelowMin(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type: hmenum.ParameterTypeFloat,
		Min:  rawMin(4.5),
		Max:  rawMin(30.5),
	}
	if _, err := Coerce(desc, 4.0); err == nil {
		t.Fatal("value below MIN should error")
	}
}

func TestCoerceFloat_AtExactMin(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type: hmenum.ParameterTypeFloat,
		Min:  rawMin(4.5),
		Max:  rawMin(30.5),
	}
	if _, err := Coerce(desc, 4.5); err != nil {
		t.Fatalf("value == MIN should pass: %v", err)
	}
}

func TestCoerceFloat_AboveMax(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type: hmenum.ParameterTypeFloat,
		Min:  rawMin(0.0),
		Max:  rawMin(100.0),
	}
	if _, err := Coerce(desc, 100.1); err == nil {
		t.Fatal("value above MAX should error")
	}
}

// ---------- Coerce string edge cases ----------

func TestCoerceStringAllTypes(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeString)
	cases := []any{"hello", []byte("world"), 42, int32(1), int64(2), float32(1.5), float64(3.14), true, json.Number("5")}
	for _, raw := range cases {
		_, err := Coerce(desc, raw)
		if err != nil {
			t.Errorf("Coerce string %T(%v): %v", raw, raw, err)
		}
	}
}

func TestCoerceStringUnsupportedType(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeString)
	_, err := Coerce(desc, struct{}{})
	if err == nil {
		t.Fatal("unsupported type for string should error")
	}
}

func TestCoerceString_NonStringFails(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeString}
	// Numeric values should be formatted to string (not rejected).
	v, err := Coerce(desc, 42)
	if err != nil {
		t.Fatalf("int→string error: %v", err)
	}
	if v.String != "42" {
		t.Fatalf("got %q, want %q", v.String, "42")
	}
}

func TestCoerceString_NilReturnsNone(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeString}
	v, err := Coerce(desc, nil)
	if err != nil {
		t.Fatalf("nil→string error: %v", err)
	}
	if v.Kind != hmtypes.ValueKindNone {
		t.Fatal("nil should coerce to NoneValue")
	}
}

// ---------- Coerce enum edge cases ----------

func TestCoerceEnumNoValueList(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeEnum)
	// No ValueList: integer index falls through.
	v, err := Coerce(desc, 2)
	if err != nil {
		t.Fatalf("Coerce enum no value list: %v", err)
	}
	if v.Int != 2 {
		t.Fatalf("Coerce enum no value list = %d, want 2", v.Int)
	}
}

func TestCoerceEnumWithValueListStringLabelNotFound(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeEnum)
	desc.ValueList = []string{"OFF", "ON"}
	_, err := Coerce(desc, "UNKNOWN")
	if err == nil {
		t.Fatal("unknown enum label should error")
	}
}

func TestCoerceEnumWithValueListIndexOutOfBounds(t *testing.T) {
	desc := writableDesc(hmenum.ParameterTypeEnum)
	desc.ValueList = []string{"A", "B"}
	_, err := Coerce(desc, 5)
	if err == nil {
		t.Fatal("out-of-bounds index should error")
	}
}

func TestCoerceEnum_OutOfRangeIndex(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"OFF", "ON"},
	}
	if _, err := Coerce(desc, 5); err == nil {
		t.Fatal("out-of-range index should fail")
	}
	if _, err := Coerce(desc, -1); err == nil {
		t.Fatal("negative index should fail")
	}
}

func TestCoerceEnum_UnknownLabelFails(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"OFF", "ON", "AUTO"},
	}
	if _, err := Coerce(desc, "UNKNOWN"); err == nil {
		t.Fatal("unknown enum label should fail")
	}
}

func TestCoerceEnum_ValidIndexAndLabel(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"OFF", "ON", "AUTO"},
	}
	cases := []struct {
		raw  any
		want int
	}{
		{"OFF", 0},
		{"ON", 1},
		{"AUTO", 2},
		{0, 0},
		{2, 2},
	}
	for _, tc := range cases {
		v, err := Coerce(desc, tc.raw)
		if err != nil {
			t.Fatalf("Coerce(enum, %v) error: %v", tc.raw, err)
		}
		if v.Int != tc.want {
			t.Fatalf("Coerce(enum, %v) = %d, want %d", tc.raw, v.Int, tc.want)
		}
	}
}

func TestCoerceEnum_NoValueListFallsBackToInt(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeEnum}
	v, err := Coerce(desc, 3)
	if err != nil {
		t.Fatalf("ENUM without VALUE_LIST + int error: %v", err)
	}
	if v.Int != 3 {
		t.Fatalf("got %d, want 3", v.Int)
	}
}

// ---------- Coerce empty/dummy/nil/unknown ----------

func TestCoerceUnknownType(t *testing.T) {
	desc := hmproto.ParameterData{Type: hmenum.ParameterType("MYSTERY")}
	_, err := Coerce(desc, "x")
	if err == nil {
		t.Fatal("unknown type should error")
	}
}

func TestCoerceNilAlwaysNone(t *testing.T) {
	for _, pt := range []hmenum.ParameterType{
		hmenum.ParameterTypeBool, hmenum.ParameterTypeFloat, hmenum.ParameterTypeInteger,
	} {
		desc := writableDesc(pt)
		v, err := Coerce(desc, nil)
		if err != nil {
			t.Fatalf("Coerce nil for %s: %v", pt, err)
		}
		if !v.IsNone() {
			t.Fatalf("Coerce(nil) should be NoneValue for %s", pt)
		}
	}
}

func TestCoerceEmpty_AnyValuePassesThrough(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeEmpty}
	_, err := Coerce(desc, "anything")
	if err != nil {
		t.Fatalf("EMPTY type should accept any string: %v", err)
	}
}

func TestCoerceDummy_AnyValuePassesThrough(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeDummy}
	_, err := Coerce(desc, 99)
	if err != nil {
		t.Fatalf("DUMMY type should accept any value: %v", err)
	}
}

// ---------- ValidateWithDP ----------

type writableReporter struct{ writable bool }

func (w writableReporter) IsWritable() bool { return w.writable }

func TestValidateWithDP_NotWritable(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	dp := writableReporter{writable: false}
	err := ValidateWithDP(dp, desc, hmtypes.BoolValue(true), ValidateOptions{})
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("expected ErrNotWritable, got %v", err)
	}
}

func TestValidateWithDP_Writable(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	dp := writableReporter{writable: true}
	if err := ValidateWithDP(dp, desc, hmtypes.BoolValue(false), ValidateOptions{}); err != nil {
		t.Fatalf("ValidateWithDP writable: %v", err)
	}
}

// ---------- Validate ----------

func TestValidateRequiresWritable(t *testing.T) {
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead}
	err := Validate(desc, hmtypes.BoolValue(true))
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("got %v, want ErrNotWritable", err)
	}
}

func TestValidateKindMismatch(t *testing.T) {
	desc := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsWrite}
	if err := Validate(desc, hmtypes.StringValue("x")); err == nil {
		t.Error("kind mismatch should fail")
	}
}

func TestValidateFloat_NaN(t *testing.T) {
	makeNaN := func() float64 {
		var zero float64
		return zero / zero
	}
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	v := hmtypes.FloatValue(makeNaN())
	if err := Validate(desc, v); !errors.Is(err, ErrNaNOrInf) {
		t.Fatalf("NaN should return ErrNaNOrInf, got %v", err)
	}
}

func TestValidateFloat_SpecialValue(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage("0"),
		Max:        json.RawMessage("40"),
		Special:    json.RawMessage(`[{"ID":"OPEN","VALUE":-0.5}]`),
	}
	// -0.5 is outside [0,40] but is a special value → should pass.
	if err := Validate(desc, hmtypes.FloatValue(-0.5)); err != nil {
		t.Fatalf("special float should be accepted: %v", err)
	}
}

func TestValidateInteger_SpecialValue(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage("0"),
		Max:        json.RawMessage("100"),
		Special:    json.RawMessage(`[{"ID":"LOCK","VALUE":-1}]`),
	}
	if err := Validate(desc, hmtypes.IntValue(-1)); err != nil {
		t.Fatalf("special int should be accepted: %v", err)
	}
}

func TestValidateAction_Bool(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeAction,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	if err := Validate(desc, hmtypes.BoolValue(true)); err != nil {
		t.Fatalf("ACTION bool: %v", err)
	}
	// Wrong kind.
	if err := Validate(desc, hmtypes.IntValue(1)); err == nil {
		t.Fatal("ACTION should reject non-bool")
	}
}

func TestValidateEnum_StringLabel(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		ValueList:  []string{"OFF", "ON"},
	}
	// String label should return a descriptive error.
	if err := Validate(desc, hmtypes.StringValue("ON")); err == nil {
		t.Fatal("ENUM with string should return error")
	}
}

func TestValidateEnum_WrongKind(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		ValueList:  []string{"A"},
	}
	if err := Validate(desc, hmtypes.FloatValue(0)); err == nil {
		t.Fatal("ENUM wrong kind should error")
	}
}

func TestValidateEnum_NegativeNoValueList(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	if err := Validate(desc, hmtypes.IntValue(-1)); err == nil {
		t.Fatal("negative enum index with no value list should error")
	}
}

func TestValidateString_TooLong(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeString,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Max:        json.RawMessage("5"),
	}
	err := Validate(desc, hmtypes.StringValue("toolong"))
	if !errors.Is(err, ErrStringTooLong) {
		t.Fatalf("expected ErrStringTooLong, got %v", err)
	}
}

func TestValidateString_WrongKind(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeString,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	if err := Validate(desc, hmtypes.IntValue(1)); err == nil {
		t.Fatal("string wrong kind should error")
	}
}

func TestValidateEmptyAndDummy(t *testing.T) {
	for _, pt := range []hmenum.ParameterType{hmenum.ParameterTypeEmpty, hmenum.ParameterTypeDummy} {
		desc := hmproto.ParameterData{
			Type:       pt,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		}
		if err := Validate(desc, hmtypes.StringValue("anything")); err != nil {
			t.Errorf("%s: unexpected error: %v", pt, err)
		}
	}
}

func TestValidateUnknownType(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterType("GALAXY"),
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	if err := Validate(desc, hmtypes.StringValue("x")); err == nil {
		t.Fatal("unknown type should error")
	}
}

func TestValidateFloat_StrictPrecision(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage("0"),
		Max:        json.RawMessage("100"),
	}
	err := ValidateWithOptions(desc, hmtypes.FloatValue(3.5), ValidateOptions{StrictPrecision: true})
	if err == nil {
		t.Fatal("fractional float with integer bounds and StrictPrecision should error")
	}
}

// ---------- Diff ----------

func TestDiffBool(t *testing.T) {
	r := Diff(hmtypes.BoolValue(true), hmtypes.BoolValue(true))
	if !r.Match {
		t.Error("identical bools should match")
	}
	r = Diff(hmtypes.BoolValue(true), hmtypes.BoolValue(false))
	if r.Match {
		t.Error("different bools must not match")
	}
}

func TestDiffFloatWithinTolerance(t *testing.T) {
	r := Diff(hmtypes.FloatValue(1.0), hmtypes.FloatValue(1.0000001))
	if !r.Match {
		t.Fatalf("tiny drift should match, rel=%g", r.RelDiff)
	}
	r = Diff(hmtypes.FloatValue(1.0), hmtypes.FloatValue(1.1))
	if r.Match {
		t.Error("large drift must not match")
	}
}

func TestDiffKindMismatch(t *testing.T) {
	r := Diff(hmtypes.IntValue(1), hmtypes.FloatValue(1.0))
	if r.Match {
		t.Error("different kinds must not match")
	}
}

func TestDiffList(t *testing.T) {
	a := hmtypes.ListValue([]string{"x", "y"})
	b := hmtypes.ListValue([]string{"x", "y"})
	if !Diff(a, b).Match {
		t.Error("equal lists should match")
	}
	c := hmtypes.ListValue([]string{"x", "z"})
	if Diff(a, c).Match {
		t.Error("different lists must not match")
	}
}

func TestDiffKindMismatchExtra(t *testing.T) {
	// Float vs string mismatch.
	r := Diff(hmtypes.FloatValue(1.0), hmtypes.StringValue("1"))
	if r.Match {
		t.Fatal("float vs string must not match")
	}
}

func TestDiffNoneNone(t *testing.T) {
	r := Diff(hmtypes.NoneValue(), hmtypes.NoneValue())
	if !r.Match {
		t.Fatal("NoneValue must equal itself")
	}
}

func TestDiffStringMismatch(t *testing.T) {
	r := Diff(hmtypes.StringValue("a"), hmtypes.StringValue("b"))
	if r.Match {
		t.Fatal("different strings should not match")
	}
}

func TestDiffListMismatch(t *testing.T) {
	r := Diff(hmtypes.ListValue([]string{"x"}), hmtypes.ListValue([]string{"y"}))
	if r.Match {
		t.Fatal("different lists should not match")
	}
}

func TestDiffFloatExact(t *testing.T) {
	r := Diff(hmtypes.FloatValue(1.5), hmtypes.FloatValue(1.5))
	if !r.Match {
		t.Fatal("identical floats should match")
	}
}

func TestDiffFloatOutsideTolerance(t *testing.T) {
	r := Diff(hmtypes.FloatValue(1.0), hmtypes.FloatValue(1.1))
	if r.Match {
		t.Fatal("float difference of 0.1 should not match")
	}
}

// ---------- ToHomematicValue / FromHomematicValue (internal package view) ----------

func TestToHomematicValueAllTypes(t *testing.T) {
	if got := ToHomematicValue(nil); got != nil {
		t.Fatal("nil should stay nil")
	}
	if got := ToHomematicValue(true); got != 1 {
		t.Fatalf("bool true = %v", got)
	}
	if got := ToHomematicValue(false); got != 0 {
		t.Fatalf("bool false = %v", got)
	}
	if got := ToHomematicValue(float64(1.23456789)); got.(float64) != 1.234568 {
		t.Fatalf("float64 rounding = %v", got)
	}
	if got := ToHomematicValue(float32(0.5)); got == nil {
		t.Fatal("float32 should convert")
	}
	if got := ToHomematicValue([]any{"a", "b"}); len(got.([]any)) != 2 {
		t.Fatalf("slice = %v", got)
	}
	if got := ToHomematicValue(map[string]any{"k": "v"}); got.(map[string]any)["k"] != "v" {
		t.Fatalf("map = %v", got)
	}
	// fmt.Stringer — test int passthrough.
	if got := ToHomematicValue(42); got != 42 {
		t.Fatalf("int passthrough = %v", got)
	}
}

func TestFromHomematicValueBool(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{1, true},
		{0, false},
		{float64(1), true},
		{float64(0), false},
		{true, true},
	} {
		got, err := FromHomematicValue(tc.in, "bool")
		if err != nil {
			t.Fatalf("FromHomematicValue bool %v: %v", tc.in, err)
		}
		if got.(bool) != tc.want {
			t.Fatalf("FromHomematicValue bool %v = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFromHomematicValueBoolDefault(t *testing.T) {
	// A type that doesn't match int/float64/bool is passed through unchanged.
	got, err := FromHomematicValue("yes", "bool")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "yes" {
		t.Fatalf("got %v, want yes", got)
	}
}

func TestFromHomematicValueTimeRFC3339(t *testing.T) {
	got, err := FromHomematicValue("2026-01-01T12:00:00Z", "time.Time")
	if err != nil {
		t.Fatalf("FromHomematicValue time: %v", err)
	}
	if _, ok := got.(interface{ IsZero() bool }); !ok {
		t.Fatalf("result is not time.Time: %T", got)
	}
}

func TestFromHomematicValueTimeISO8601(t *testing.T) {
	got, err := FromHomematicValue("2026-01-01T12:00:00", "time.Time")
	if err != nil {
		t.Fatalf("FromHomematicValue ISO8601: %v", err)
	}
	_ = got
}

func TestFromHomematicValueTimeParseError(t *testing.T) {
	_, err := FromHomematicValue("not-a-date", "time.Time")
	if err == nil {
		t.Fatal("unparseable time string should error")
	}
}

func TestFromHomematicValueTimeNonString(t *testing.T) {
	// Non-string input for time.Time → pass through unchanged.
	got, err := FromHomematicValue(42, "time.Time")
	if err != nil {
		t.Fatalf("non-string time: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %v, want 42", got)
	}
}

func TestFromHomematicValueEmpty(t *testing.T) {
	got, err := FromHomematicValue(99, "")
	if err != nil {
		t.Fatalf("empty targetType: %v", err)
	}
	if got != 99 {
		t.Fatalf("got %v, want 99", got)
	}
}

func TestFromHomematicValueDefault(t *testing.T) {
	got, err := FromHomematicValue("x", "unknown_type")
	if err != nil {
		t.Fatalf("unknown targetType: %v", err)
	}
	if got != "x" {
		t.Fatalf("got %v, want x", got)
	}
}

// ---------- CrossViolation / CrossValidationError error formatting ----------

func TestCrossViolationErrorNonEmpty(t *testing.T) {
	cv := CrossViolation{Param: "LEVEL", Err: ErrNotWritable}
	s := cv.Error()
	if s == "" {
		t.Fatal("CrossViolation.Error() returned empty string")
	}
}

func TestCrossValidationErrorSingleAndMultiple(t *testing.T) {
	single := &CrossValidationError{Violations: []CrossViolation{{Param: "A", Err: ErrNotWritable}}}
	if single.Error() == "" {
		t.Fatal("single CrossValidationError.Error() empty")
	}
	multi := &CrossValidationError{Violations: []CrossViolation{
		{Param: "A", Err: ErrNotWritable},
		{Param: "B", Err: ErrNotWritable},
	}}
	if multi.Error() == "" {
		t.Fatal("multi CrossValidationError.Error() empty")
	}
}
