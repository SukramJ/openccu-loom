// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CrossEntry pairs one parameter descriptor with the value the caller
// intends to write. It is the unit of input for [ValidateCrossParameters].
type CrossEntry struct {
	// Name is the parameter name (e.g. "SET_POINT_TEMPERATURE"). Used
	// in error messages; does not affect validation logic.
	Name string
	// Desc is the wire descriptor for the parameter.
	Desc hmproto.ParameterData
	// Value is the value the caller wants to write.
	Value hmtypes.ParamValue
}

// CrossViolation describes one failed cross-parameter constraint.
type CrossViolation struct {
	// Param is the name of the parameter that triggered the violation.
	Param string
	// Err is the underlying validation error.
	Err error
}

// Error implements error.
func (v CrossViolation) Error() string {
	return fmt.Sprintf("parameter %q: %v", v.Param, v.Err)
}

// CrossValidationError is returned by [ValidateCrossParameters] when
// one or more entries fail. It implements [error] and exposes the full
// list of violations for structured handling.
type CrossValidationError struct {
	Violations []CrossViolation
}

// Error implements error.
func (e *CrossValidationError) Error() string {
	if len(e.Violations) == 1 {
		return fmt.Sprintf("cross-parameter validation failed: %v", e.Violations[0])
	}
	return fmt.Sprintf("cross-parameter validation failed (%d violations): first: %v", len(e.Violations), e.Violations[0])
}

// ValidateCrossParameters validates a batch of (descriptor, value) pairs
// as a single logical transaction. It calls [ValidateWithOptions] on each
// entry with [ValidateOptions]{AllowSpecialValues: true} and collects all
// violations before returning so the caller sees the full picture in one
// shot rather than discovering failures one at a time.
//
// This is the standalone form of the paramset write validation used by
// the session-based MASTER editor and by REST PUT /paramsets. It is
// factored out so callers outside a session (e.g. test helpers,
// direct coordinator calls) can re-use the same rule set.
//
// Returns nil when all entries pass. Returns [*CrossValidationError]
// when one or more entries fail; the caller can type-assert to get
// the full violation list.
func ValidateCrossParameters(entries []CrossEntry) error {
	if len(entries) == 0 {
		return nil
	}
	opts := ValidateOptions{AllowSpecialValues: true, StrictPrecision: false}
	var violations []CrossViolation
	for i := range entries {
		if err := ValidateWithOptions(entries[i].Desc, entries[i].Value, opts); err != nil {
			violations = append(violations, CrossViolation{Param: entries[i].Name, Err: err})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &CrossValidationError{Violations: violations}
}
