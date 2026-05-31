// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// writableBoolDesc returns a minimal bool descriptor with WRITE permission.
func writableBoolDesc() hmproto.ParameterData {
	return hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
}

// writableIntDesc returns a minimal int descriptor with WRITE permission.
func writableIntDesc() hmproto.ParameterData {
	return hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
}

func TestValidateCrossParametersEmptyIsOK(t *testing.T) {
	if err := ValidateCrossParameters(nil); err != nil {
		t.Fatalf("empty input: unexpected error: %v", err)
	}
	if err := ValidateCrossParameters([]CrossEntry{}); err != nil {
		t.Fatalf("empty slice: unexpected error: %v", err)
	}
}

func TestValidateCrossParametersAllPass(t *testing.T) {
	entries := []CrossEntry{
		{Name: "ACTIVE", Desc: writableBoolDesc(), Value: hmtypes.BoolValue(true)},
		{Name: "COUNT", Desc: writableIntDesc(), Value: hmtypes.IntValue(5)},
	}
	if err := ValidateCrossParameters(entries); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestValidateCrossParametersCollectsAllViolations(t *testing.T) {
	// Two entries that will fail — wrong kinds.
	entries := []CrossEntry{
		{Name: "P1", Desc: writableBoolDesc(), Value: hmtypes.IntValue(1)},
		{Name: "P2", Desc: writableIntDesc(), Value: hmtypes.BoolValue(false)},
	}
	err := ValidateCrossParameters(entries)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var cve *CrossValidationError
	if !errors.As(err, &cve) {
		t.Fatalf("expected *CrossValidationError, got %T", err)
	}
	if len(cve.Violations) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(cve.Violations))
	}
	names := map[string]bool{}
	for _, v := range cve.Violations {
		names[v.Param] = true
	}
	for _, want := range []string{"P1", "P2"} {
		if !names[want] {
			t.Errorf("expected violation for %q", want)
		}
	}
}

func TestValidateCrossParametersReadOnlyRejected(t *testing.T) {
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead, // no WRITE
	}
	entries := []CrossEntry{
		{Name: "RO", Desc: desc, Value: hmtypes.BoolValue(true)},
	}
	err := ValidateCrossParameters(entries)
	if err == nil {
		t.Fatal("expected error for read-only parameter")
	}
	var cve *CrossValidationError
	if !errors.As(err, &cve) {
		t.Fatalf("expected *CrossValidationError, got %T", err)
	}
	if !errors.Is(cve.Violations[0].Err, ErrNotWritable) {
		t.Errorf("expected ErrNotWritable, got %v", cve.Violations[0].Err)
	}
}

func TestCrossViolationError(t *testing.T) {
	v := CrossViolation{Param: "X", Err: ErrNotWritable}
	s := v.Error()
	if s == "" {
		t.Fatal("CrossViolation.Error() returned empty string")
	}
}

func TestCrossValidationErrorMultiple(t *testing.T) {
	cve := &CrossValidationError{Violations: []CrossViolation{
		{Param: "A", Err: ErrNotWritable},
		{Param: "B", Err: ErrNaNOrInf},
	}}
	s := cve.Error()
	if s == "" {
		t.Fatal("CrossValidationError.Error() returned empty string")
	}
}
