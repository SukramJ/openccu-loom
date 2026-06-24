// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// ============================================================
// parseCreateResult
// ============================================================

func TestParseCreateResult_PositiveID_ReturnsID(t *testing.T) {
	t.Parallel()
	id, err := parseCreateResult("5", hub.ErrRoomExists)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 5 {
		t.Errorf("id = %d, want 5", id)
	}
}

func TestParseCreateResult_NegativeTwo_ReturnsExistsErr(t *testing.T) {
	t.Parallel()
	_, err := parseCreateResult("-2", hub.ErrRoomExists)
	if !errors.Is(err, hub.ErrRoomExists) {
		t.Errorf("err = %v, want hub.ErrRoomExists", err)
	}
}

func TestParseCreateResult_NegativeOne_ReturnsGenericError(t *testing.T) {
	t.Parallel()
	_, err := parseCreateResult("-1", hub.ErrRoomExists)
	if err == nil {
		t.Fatal("expected non-nil error for -1 output")
	}
	if errors.Is(err, hub.ErrRoomExists) {
		t.Errorf("expected a generic error, not ErrRoomExists")
	}
}

func TestParseCreateResult_Zero_ReturnsGenericError(t *testing.T) {
	t.Parallel()
	_, err := parseCreateResult("0", hub.ErrFunctionExists)
	if err == nil {
		t.Fatal("expected non-nil error for 0 output")
	}
	if errors.Is(err, hub.ErrFunctionExists) {
		t.Errorf("expected a generic error, not ErrFunctionExists")
	}
}

func TestParseCreateResult_NonNumeric_ReturnsParseError(t *testing.T) {
	t.Parallel()
	_, err := parseCreateResult("abc", hub.ErrRoomExists)
	if err == nil {
		t.Fatal("expected parse error for non-numeric output")
	}
}

func TestParseCreateResult_WhitespacePadded_ReturnsID(t *testing.T) {
	t.Parallel()
	id, err := parseCreateResult("  7\n", hub.ErrRoomExists)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
}

func TestParseCreateResult_FunctionExists_Sentinel(t *testing.T) {
	t.Parallel()
	_, err := parseCreateResult("-2", hub.ErrFunctionExists)
	if !errors.Is(err, hub.ErrFunctionExists) {
		t.Errorf("err = %v, want hub.ErrFunctionExists", err)
	}
}

// ============================================================
// parseMutateResult
// ============================================================

func TestParseMutateResult_One_ReturnsNil(t *testing.T) {
	t.Parallel()
	if err := parseMutateResult("1", hub.ErrRoomNotFound); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestParseMutateResult_Zero_ReturnsNotFoundErr(t *testing.T) {
	t.Parallel()
	err := parseMutateResult("0", hub.ErrRoomNotFound)
	if !errors.Is(err, hub.ErrRoomNotFound) {
		t.Errorf("err = %v, want hub.ErrRoomNotFound", err)
	}
}

func TestParseMutateResult_NonNumeric_ReturnsParseError(t *testing.T) {
	t.Parallel()
	err := parseMutateResult("xyz", hub.ErrRoomNotFound)
	if err == nil {
		t.Fatal("expected parse error for non-numeric output")
	}
	if errors.Is(err, hub.ErrRoomNotFound) {
		t.Errorf("expected a parse error, not ErrRoomNotFound")
	}
}

func TestParseMutateResult_FunctionNotFound_Sentinel(t *testing.T) {
	t.Parallel()
	err := parseMutateResult("0", hub.ErrFunctionNotFound)
	if !errors.Is(err, hub.ErrFunctionNotFound) {
		t.Errorf("err = %v, want hub.ErrFunctionNotFound", err)
	}
}

func TestParseMutateResult_WhitespacePadded_Success(t *testing.T) {
	t.Parallel()
	if err := parseMutateResult(" 1\n", hub.ErrRoomNotFound); err != nil {
		t.Errorf("expected nil for whitespace-padded '1', got %v", err)
	}
}

func TestParseMutateResult_NegativeValue_ReturnsNotFoundErr(t *testing.T) {
	t.Parallel()
	// Any value other than 1 maps to notFoundErr.
	err := parseMutateResult("-1", hub.ErrRoomNotFound)
	if !errors.Is(err, hub.ErrRoomNotFound) {
		t.Errorf("err = %v, want hub.ErrRoomNotFound for value -1", err)
	}
}
