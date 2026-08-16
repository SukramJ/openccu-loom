// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeGoToLiftPercentageField0 verifies that field 0 carries the
// canonical 16-bit percent100ths value as-is (5000 → 5000), matching
// matter.js window-covering-cluster.element.ts:95 (LiftPercent100thsValue
// id 0x0).
func TestDecodeGoToLiftPercentageField0(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 5000)
	})
	got, err := wire.DecodeGoToLiftPercentage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LiftPercent100thsValue != 5000 {
		t.Errorf("LiftPercent100thsValue = %d, want 5000", got.LiftPercent100thsValue)
	}
}

// TestDecodeGoToLiftPercentageField1Ignored verifies that field 1 — the
// removed 8-bit percentage, conformance "X" in matter.js
// window-covering-cluster.element.ts:96 — is ignored rather than taken
// as the position.
func TestDecodeGoToLiftPercentageField1Ignored(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 5000)
		e.PutUint(tlv.ContextTag(1), 50)
	})
	got, err := wire.DecodeGoToLiftPercentage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LiftPercent100thsValue != 5000 {
		t.Errorf("LiftPercent100thsValue = %d, want 5000 (field 1 must be ignored)", got.LiftPercent100thsValue)
	}
}

// TestDecodeGoToLiftPercentageMalformed verifies that a truncated
// payload wraps ErrWindowCoveringMalformed.
func TestDecodeGoToLiftPercentageMalformed(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeGoToLiftPercentage([]byte{0x15})
	if !errors.Is(err, wire.ErrWindowCoveringMalformed) {
		t.Fatalf("err = %v, want ErrWindowCoveringMalformed", err)
	}
}

// TestDecodeGoToTiltPercentageField0 verifies that field 0 carries the
// canonical 16-bit percent100ths value as-is (3000 → 3000), matching
// matter.js window-covering-cluster.element.ts:104.
func TestDecodeGoToTiltPercentageField0(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 3000)
	})
	got, err := wire.DecodeGoToTiltPercentage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TiltPercent100thsValue != 3000 {
		t.Errorf("TiltPercent100thsValue = %d, want 3000", got.TiltPercent100thsValue)
	}
}

// TestDecodeGoToTiltPercentageField1Ignored verifies that field 1 — the
// removed 8-bit percentage, conformance "X" — is ignored.
func TestDecodeGoToTiltPercentageField1Ignored(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 3000)
		e.PutUint(tlv.ContextTag(1), 30)
	})
	got, err := wire.DecodeGoToTiltPercentage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TiltPercent100thsValue != 3000 {
		t.Errorf("TiltPercent100thsValue = %d, want 3000 (field 1 must be ignored)", got.TiltPercent100thsValue)
	}
}

// TestDecodeGoToTiltPercentageMalformed verifies that a truncated
// payload wraps ErrWindowCoveringMalformed.
func TestDecodeGoToTiltPercentageMalformed(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeGoToTiltPercentage([]byte{0x15})
	if !errors.Is(err, wire.ErrWindowCoveringMalformed) {
		t.Fatalf("err = %v, want ErrWindowCoveringMalformed", err)
	}
}

// TestDecodeGoToLiftValueTag0 verifies that context tag 0 carries the
// raw uint16 position (1234 → LiftValue=1234).
func TestDecodeGoToLiftValueTag0(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 1234)
	})
	got, err := wire.DecodeGoToLiftValue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LiftValue != 1234 {
		t.Errorf("LiftValue = %d, want 1234", got.LiftValue)
	}
}

// TestDecodeGoToLiftValueMalformed verifies that a truncated payload
// wraps ErrWindowCoveringMalformed.
func TestDecodeGoToLiftValueMalformed(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeGoToLiftValue([]byte{0x15})
	if !errors.Is(err, wire.ErrWindowCoveringMalformed) {
		t.Fatalf("err = %v, want ErrWindowCoveringMalformed", err)
	}
}

// TestDecodeGoToTiltValueTag0 verifies that context tag 0 carries the
// raw uint16 position (4321 → TiltValue=4321).
func TestDecodeGoToTiltValueTag0(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), 4321)
	})
	got, err := wire.DecodeGoToTiltValue(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TiltValue != 4321 {
		t.Errorf("TiltValue = %d, want 4321", got.TiltValue)
	}
}

// TestDecodeGoToTiltValueMalformed verifies that a truncated payload
// wraps ErrWindowCoveringMalformed.
func TestDecodeGoToTiltValueMalformed(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeGoToTiltValue([]byte{0x15})
	if !errors.Is(err, wire.ErrWindowCoveringMalformed) {
		t.Fatalf("err = %v, want ErrWindowCoveringMalformed", err)
	}
}
