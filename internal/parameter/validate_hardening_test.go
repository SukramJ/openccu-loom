// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// specialJSON builds a JSON-encoded SPECIAL list with a single entry whose
// VALUE is v. The ID is arbitrary (callers that care only about VALUE can
// reuse this helper).
func specialJSON(id string, v float64) json.RawMessage {
	raw, _ := json.Marshal([]map[string]any{{"ID": id, "VALUE": v}})
	return raw
}

// writableFloat returns a writable FLOAT descriptor with the given lo/hi bounds.
func writableFloat(lo, hi float64) hmproto.ParameterData {
	minRaw, _ := json.Marshal(lo)
	maxRaw, _ := json.Marshal(hi)
	return hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsWrite,
		Min:        json.RawMessage(minRaw),
		Max:        json.RawMessage(maxRaw),
	}
}

// TestValidateSpecialValueAcceptedOutsideRange verifies that a value matching
// a SPECIAL entry is accepted even when it falls outside the declared MIN/MAX.
// Concrete case: thermostat TEMPERATURE_SETPOINT — "FROST" sentinel 4.5 °C
// lives below the heating-range floor of 10 °C.
func TestValidateSpecialValueAcceptedOutsideRange(t *testing.T) {
	t.Parallel()
	desc := writableFloat(10, 30)
	desc.Special = specialJSON("FROST", 4.5)

	// 4.5 is below MIN=10 but is a SPECIAL value — must be accepted.
	if err := Validate(desc, hmtypes.FloatValue(4.5)); err != nil {
		t.Errorf("special value 4.5 should be accepted, got: %v", err)
	}
}

// TestValidateNearSpecialValueRejected verifies that a value close to but
// not exactly matching a SPECIAL entry is still rejected as out-of-range.
func TestValidateNearSpecialValueRejected(t *testing.T) {
	t.Parallel()
	desc := writableFloat(10, 30)
	desc.Special = specialJSON("FROST", 4.5)

	// 4.6 is close to the special value 4.5 but not equal — must be rejected.
	if err := Validate(desc, hmtypes.FloatValue(4.6)); err == nil {
		t.Error("4.6 should be rejected (not a special value, below MIN=10)")
	}
}

// TestValidateEnumByLabelStringRejected verifies that passing a string label
// for an ENUM parameter is rejected with a clear message directing the caller
// to use Coerce. The CCU wire protocol expects an integer index.
func TestValidateEnumByLabelStringRejected(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsWrite,
		ValueList:  []string{"AUTO", "MANUAL"},
	}
	err := Validate(desc, hmtypes.StringValue("AUTO"))
	if err == nil {
		t.Fatal("string label for ENUM should be rejected")
	}
	// Error message must be actionable.
	if !contains(err.Error(), "Coerce") {
		t.Errorf("error should mention Coerce, got: %q", err.Error())
	}
}

// TestValidateEnumIndexInBoundsAccepted verifies that index 0 and the last
// valid index are both accepted for an ENUM parameter.
func TestValidateEnumIndexInBoundsAccepted(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsWrite,
		ValueList:  []string{"AUTO", "MANUAL", "OFF"},
	}
	for _, idx := range []int{0, 2} {
		if err := Validate(desc, hmtypes.IntValue(idx)); err != nil {
			t.Errorf("index %d should be accepted: %v", idx, err)
		}
	}
}

// TestValidateEnumIndexNegativeRejected verifies that a negative enum index
// is always rejected, even when there is no ValueList.
func TestValidateEnumIndexNegativeRejected(t *testing.T) {
	t.Parallel()

	// With a ValueList.
	descWithList := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsWrite,
		ValueList:  []string{"AUTO", "MANUAL"},
	}
	if err := Validate(descWithList, hmtypes.IntValue(-1)); err == nil {
		t.Error("index -1 should be rejected (with ValueList)")
	}

	// Without a ValueList (bare ENUM).
	descBare := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeEnum,
		Operations: hmenum.OperationsWrite,
	}
	if err := Validate(descBare, hmtypes.IntValue(-1)); err == nil {
		t.Error("index -1 should be rejected (no ValueList)")
	}
}

// TestValidateStringMaxLenEnforced verifies that a string exceeding the
// descriptor's MAX byte-length is rejected with ErrStringTooLong.
func TestValidateStringMaxLenEnforced(t *testing.T) {
	t.Parallel()
	const maxLen = 10
	maxRaw, _ := json.Marshal(maxLen)
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeString,
		Operations: hmenum.OperationsWrite,
		Max:        json.RawMessage(maxRaw),
	}

	// Exactly at limit — accepted.
	if err := Validate(desc, hmtypes.StringValue("0123456789")); err != nil {
		t.Errorf("10-char string should be accepted (max=10): %v", err)
	}

	// One character over the limit — rejected.
	err := Validate(desc, hmtypes.StringValue("01234567890"))
	if err == nil {
		t.Fatal("11-char string should be rejected (max=10)")
	}
	if !errors.Is(err, ErrStringTooLong) {
		t.Errorf("want ErrStringTooLong, got: %v", err)
	}
}

// TestValidateStringMaxLenZeroMeansUnlimited verifies that a descriptor
// with Max=0 (or absent Max) imposes no length constraint on strings.
func TestValidateStringMaxLenZeroMeansUnlimited(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeString,
		Operations: hmenum.OperationsWrite,
		// Max intentionally absent (zero value == json.RawMessage(nil)).
	}
	longString := make([]byte, 1000)
	for i := range longString {
		longString[i] = 'x'
	}
	if err := Validate(desc, hmtypes.StringValue(string(longString))); err != nil {
		t.Errorf("unlimited string should be accepted: %v", err)
	}
}

// TestValidateWriteOnReadOnlyParam verifies that writing a read-only
// parameter returns ErrNotWritable as the very first error, before any
// range or type checking.
func TestValidateWriteOnReadOnlyParam(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead, // no WRITE bit
		Min:        json.RawMessage("0"),
		Max:        json.RawMessage("100"),
	}
	err := Validate(desc, hmtypes.FloatValue(50))
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("want ErrNotWritable, got %v", err)
	}
}

// TestValidateZeroDescGracefulError verifies that a zero-value ParameterData
// (Operations=0 → not writable) returns a clear, non-panic error.
func TestValidateZeroDescGracefulError(t *testing.T) {
	t.Parallel()
	err := Validate(hmproto.ParameterData{}, hmtypes.ParamValue{})
	if err == nil {
		t.Fatal("zero desc should return an error")
	}
	// Must be the not-writable error (Operations=0 has no WRITE bit).
	if !errors.Is(err, ErrNotWritable) {
		t.Errorf("want ErrNotWritable for zero desc, got %v", err)
	}
}

// TestValidateFloatPrecisionWithIntegerBounds verifies that
// ValidateWithOptions with StrictPrecision=true rejects fractional float
// values when the descriptor bounds are integer-valued.
func TestValidateFloatPrecisionWithIntegerBounds(t *testing.T) {
	t.Parallel()
	desc := writableFloat(0, 100)
	desc.Unit = "%"

	opts := ValidateOptions{AllowSpecialValues: true, StrictPrecision: true}

	// Whole number — accepted.
	if err := ValidateWithOptions(desc, hmtypes.FloatValue(50.0), opts); err != nil {
		t.Errorf("50.0 should be accepted with StrictPrecision: %v", err)
	}

	// Fractional — rejected.
	if err := ValidateWithOptions(desc, hmtypes.FloatValue(50.5), opts); err == nil {
		t.Error("50.5 should be rejected when bounds are integers and StrictPrecision=true")
	}

	// Permissive default (StrictPrecision=false) — fractional accepted.
	if err := Validate(desc, hmtypes.FloatValue(50.5)); err != nil {
		t.Errorf("50.5 should be accepted in permissive mode: %v", err)
	}
}

// TestValidateFloatNaNAndInfRejected verifies that NaN, +Inf, and -Inf are
// always rejected for FLOAT parameters regardless of MIN/MAX constraints.
func TestValidateFloatNaNAndInfRejected(t *testing.T) {
	t.Parallel()
	desc := writableFloat(-1e9, 1e9)

	badValues := []float64{
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
	}
	for _, fv := range badValues {
		err := Validate(desc, hmtypes.FloatValue(fv))
		if err == nil {
			t.Errorf("float %v should be rejected", fv)
			continue
		}
		if !errors.Is(err, ErrNaNOrInf) {
			t.Errorf("float %v: want ErrNaNOrInf, got %v", fv, err)
		}
	}
}

// TestValidateBoolKindMismatch verifies that sending an int to a BOOL
// parameter returns a clear "want bool, got int" error.
func TestValidateBoolKindMismatch(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsWrite,
	}
	err := Validate(desc, hmtypes.IntValue(1))
	if err == nil {
		t.Fatal("int sent to bool param should fail")
	}
	if !contains(err.Error(), "want bool") {
		t.Errorf("error should mention 'want bool', got: %q", err.Error())
	}
}

// TestValidateSpecialValueIntParamAccepted verifies that the special-value
// bypass also works for INTEGER parameters.
func TestValidateSpecialValueIntParamAccepted(t *testing.T) {
	t.Parallel()
	minRaw, _ := json.Marshal(0)
	maxRaw, _ := json.Marshal(100)
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsWrite,
		Min:        json.RawMessage(minRaw),
		Max:        json.RawMessage(maxRaw),
		// -1 is the "off" sentinel for some thermostat parameters.
		Special: specialJSON("OFF", -1),
	}

	// -1 is below MIN=0 but is a SPECIAL value — must be accepted.
	if err := Validate(desc, hmtypes.IntValue(-1)); err != nil {
		t.Errorf("special int value -1 should be accepted, got: %v", err)
	}

	// -2 is not a special value and is below MIN — must be rejected.
	if err := Validate(desc, hmtypes.IntValue(-2)); err == nil {
		t.Error("-2 should be rejected (not special, below MIN=0)")
	}
}

// TestValidateWithOptionsAllowSpecialValuesFalse verifies that callers can
// opt out of special-value leniency via ValidateWithOptions.
func TestValidateWithOptionsAllowSpecialValuesFalse(t *testing.T) {
	t.Parallel()
	desc := writableFloat(10, 30)
	desc.Special = specialJSON("FROST", 4.5)

	opts := ValidateOptions{AllowSpecialValues: false, StrictPrecision: false}
	// 4.5 is a SPECIAL value, but special values are not allowed — rejected.
	if err := ValidateWithOptions(desc, hmtypes.FloatValue(4.5), opts); err == nil {
		t.Error("4.5 should be rejected when AllowSpecialValues=false")
	}
}

// ---------- internal helpers for test assertions ----------

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	if s == sub {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
