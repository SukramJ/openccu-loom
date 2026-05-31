// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestDecodeDoorLockRequestWithPin verifies that a 4-byte PIN encoded at
// context tag 0 is decoded correctly.
func TestDecodeDoorLockRequestWithPin(t *testing.T) {
	t.Parallel()
	want := []byte{0x01, 0x02, 0x03, 0x04}
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutOctets(tlv.ContextTag(0), want)
	})
	req, err := wire.DecodeDoorLockRequest(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(req.PinCode, want) {
		t.Errorf("PinCode = %v, want %v", req.PinCode, want)
	}
}

// TestDecodeDoorLockRequestWithoutPin verifies that an empty struct
// (no fields) decodes without error and leaves PinCode nil.
func TestDecodeDoorLockRequestWithoutPin(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(_ *tlv.Encoder) {})
	req, err := wire.DecodeDoorLockRequest(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.PinCode != nil {
		t.Errorf("PinCode = %v, want nil", req.PinCode)
	}
}

// TestDecodeDoorLockRequestEmptyPin verifies that an explicit empty
// octet-string at context tag 0 yields a non-nil PinCode of length 0,
// preserving the empty-vs-absent distinction that TLV expresses.
func TestDecodeDoorLockRequestEmptyPin(t *testing.T) {
	t.Parallel()
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutOctets(tlv.ContextTag(0), []byte{})
	})
	req, err := wire.DecodeDoorLockRequest(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.PinCode == nil {
		t.Fatal("PinCode is nil, want non-nil empty slice")
	}
	if len(req.PinCode) != 0 {
		t.Errorf("len(PinCode) = %d, want 0", len(req.PinCode))
	}
}

// TestDecodeDoorLockRequestPinDefensiveCopy is a regression for the
// alias-safety guard in the production code: mutating the source buffer
// after decoding must not affect the decoded PinCode.
func TestDecodeDoorLockRequestPinDefensiveCopy(t *testing.T) {
	t.Parallel()
	pin := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	payload := buildPayload(t, func(e *tlv.Encoder) {
		e.PutOctets(tlv.ContextTag(0), pin)
	})
	req, err := wire.DecodeDoorLockRequest(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Capture decoded value before mutation.
	want := bytes.Clone(req.PinCode)
	// Mutate the source payload (the octet-string value sits near the end).
	payload[len(payload)-2] ^= 0xFF
	if !bytes.Equal(req.PinCode, want) {
		t.Errorf("PinCode changed after payload mutation: got %v, want %v", req.PinCode, want)
	}
}

// TestDecodeDoorLockRequestTruncatedPayload verifies that a truncated
// wire payload (struct-open with no body) wraps ErrDoorLockMalformed.
func TestDecodeDoorLockRequestTruncatedPayload(t *testing.T) {
	t.Parallel()
	_, err := wire.DecodeDoorLockRequest([]byte{0x15}) // structure open only
	if !errors.Is(err, wire.ErrDoorLockMalformed) {
		t.Fatalf("err = %v, want ErrDoorLockMalformed", err)
	}
}
