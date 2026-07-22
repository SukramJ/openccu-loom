// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestParameterDataIsDeterminable(t *testing.T) {
	t.Parallel()

	// Happy path: the DETERMINE bit is set alongside READ/WRITE, the
	// common real-world shape for a MASTER field the CCU WebUI offers a
	// "Determine" link for.
	determinable := ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsDetermine,
	}
	if !determinable.IsDeterminable() {
		t.Error("expected IsDeterminable() to be true when OPERATIONS carries the DETERMINE bit")
	}

	// Failure path: READ+WRITE without DETERMINE must not be reported as
	// determinable — the overwhelming majority of parameters.
	plain := ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
	if plain.IsDeterminable() {
		t.Error("expected IsDeterminable() to be false without the DETERMINE bit")
	}

	// Edge case: the zero value (no OPERATIONS at all) must not panic
	// and must report false.
	var zero ParameterData
	if zero.IsDeterminable() {
		t.Error("zero-value ParameterData must not be determinable")
	}
}

// TestParameterDataOperationsAccessorsAgreeWithBitmask pins that every
// ParameterData accessor delegates to the underlying hmenum.Operations
// bitmask rather than re-implementing the bit test — so IsDeterminable
// (and its siblings) stay correct as the bitmask itself is extended.
func TestParameterDataOperationsAccessorsAgreeWithBitmask(t *testing.T) {
	t.Parallel()

	p := ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsDetermine,
	}
	if !p.IsReadable() {
		t.Error("IsReadable() should mirror Operations.IsReadable()")
	}
	if p.IsWritable() {
		t.Error("IsWritable() should be false: WRITE bit not set")
	}
	if !p.IsEvent() {
		t.Error("IsEvent() should mirror Operations.IsEvent()")
	}
	if !p.IsDeterminable() {
		t.Error("IsDeterminable() should mirror Operations.IsDeterminable()")
	}
}
