// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeSetpointRaiseLowerModeBoth verifies a round-trip with
// Mode=Both (2) and a positive Amount=5.
func TestDecodeSetpointRaiseLowerModeBoth(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ThermostatSetpointModeBoth))
		e.PutInt(tlv.ContextTag(1), 5)
	})
	got, err := wire.DecodeSetpointRaiseLower(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != wire.ThermostatSetpointModeBoth {
		t.Errorf("Mode = %d, want %d (Both)", got.Mode, wire.ThermostatSetpointModeBoth)
	}
	if got.Amount != 5 {
		t.Errorf("Amount = %d, want 5", got.Amount)
	}
}

// TestDecodeSetpointRaiseLowerNegativeAmount verifies that a negative
// signed Amount is decoded correctly (int8 sign-extension).
func TestDecodeSetpointRaiseLowerNegativeAmount(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutUint(tlv.ContextTag(0), uint64(wire.ThermostatSetpointModeHeat))
		e.PutInt(tlv.ContextTag(1), -3)
	})
	got, err := wire.DecodeSetpointRaiseLower(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Amount != -3 {
		t.Errorf("Amount = %d, want -3", got.Amount)
	}
}

// TestDecodeSetpointRaiseLowerMalformed verifies that a truncated
// payload wraps ErrThermostatMalformed.
func TestDecodeSetpointRaiseLowerMalformed(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeSetpointRaiseLower([]byte{0x15})
	if !errors.Is(err, wire.ErrThermostatMalformed) {
		t.Fatalf("err = %v, want ErrThermostatMalformed", err)
	}
}
