// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ConvertReadValue mirrors the reference wire→typed conversion. These cases
// pin the two behaviours called out for parity: the empty-string→None guard
// for FLOAT/INTEGER (model/data_point.py:1449 _convert_value) and the to_bool
// truth set for BOOL (support/__init__.py:129).

func TestConvertReadValueEmptyStringYieldsNil(t *testing.T) {
	t.Parallel()
	for _, pt := range []hmenum.ParameterType{hmenum.ParameterTypeFloat, hmenum.ParameterTypeInteger} {
		if got := parameter.ConvertReadValue(pt, ""); got != nil {
			t.Errorf("ConvertReadValue(%s, \"\") = %#v, want nil (mirrors _convert_value empty-string guard)", pt, got)
		}
	}
}

func TestConvertReadValueNumericStringCast(t *testing.T) {
	t.Parallel()
	// FLOAT: Python float(value); INTEGER: Python int(float(value)).
	if got := parameter.ConvertReadValue(hmenum.ParameterTypeFloat, "1.5"); got != 1.5 {
		t.Errorf("ConvertReadValue(FLOAT, \"1.5\") = %#v, want 1.5", got)
	}
	if got := parameter.ConvertReadValue(hmenum.ParameterTypeInteger, "255"); got != 255 {
		t.Errorf("ConvertReadValue(INTEGER, \"255\") = %#v, want 255", got)
	}
	if got := parameter.ConvertReadValue(hmenum.ParameterTypeInteger, "12.0"); got != 12 {
		t.Errorf("ConvertReadValue(INTEGER, \"12.0\") = %#v, want 12 (int(float(value)))", got)
	}
}

func TestConvertReadValueBoolTruthSet(t *testing.T) {
	t.Parallel()
	// to_bool: lower-cased string is true only for y, yes, t, true, on, 1;
	// every other string (including "") is false, without an error.
	cases := map[string]bool{
		"y": true, "Y": true, "yes": true, "YES": true, "t": true, "true": true,
		"on": true, "On": true, "1": true,
		"n": false, "no": false, "off": false, "false": false, "0": false,
		"": false, "banana": false,
	}
	for in, want := range cases {
		if got := parameter.ConvertReadValue(hmenum.ParameterTypeBool, in); got != want {
			t.Errorf("ConvertReadValue(BOOL, %q) = %v, want %v", in, got, want)
		}
	}
}

// The write-coerce path (Coerce → asBool) is intentionally stricter than the
// read-path to_bool cast: it rejects an unrecognised token with an error
// rather than silently coercing it to false, so a bad REST/MQTT write body is
// caught at the boundary. This pins that deliberate divergence.
func TestCoerceBoolRejectsUnknownToken(t *testing.T) {
	t.Parallel()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	if _, err := parameter.Coerce(desc, "yes"); err != nil {
		t.Errorf("Coerce(BOOL, \"yes\") should succeed: %v", err)
	}
	if _, err := parameter.Coerce(desc, "banana"); err == nil {
		t.Error("Coerce(BOOL, \"banana\") should reject an unrecognised token on the write path")
	}
}
