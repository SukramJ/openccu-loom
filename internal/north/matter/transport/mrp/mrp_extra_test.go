// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestNewCounterRandomSeed verifies that NewCounter (crypto/rand seeded)
// returns a non-error result and the counter value is accessible.
func TestNewCounterRandomSeed(t *testing.T) {
	t.Parallel()

	c, err := mrp.NewCounter()
	if err != nil {
		t.Fatalf("NewCounter: %v", err)
	}
	// Peek should return the seed (random but non-zero with extremely high probability).
	peek := c.Peek()
	_ = peek // just verify no panic
}

// TestEncodeStatusReportMinimalPayload verifies the 8-byte header
// layout for a call without optional protocolData.
func TestEncodeStatusReportMinimalPayload(t *testing.T) {
	t.Parallel()

	out := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralSuccess,
		uint32(mrp.SecureChannelProtocolID),
		mrp.SCStatusProtocolSessionEstablishmentSuccess,
		nil,
	)
	if len(out) != 8 {
		t.Fatalf("len=%d, want 8", len(out))
	}
	if binary.LittleEndian.Uint16(out[0:2]) != mrp.SCStatusGeneralSuccess {
		t.Errorf("generalCode=%04X, want %04X", binary.LittleEndian.Uint16(out[0:2]), mrp.SCStatusGeneralSuccess)
	}
	if binary.LittleEndian.Uint32(out[2:6]) != uint32(mrp.SecureChannelProtocolID) {
		t.Errorf("protocolID=%08X, want %08X", binary.LittleEndian.Uint32(out[2:6]), uint32(mrp.SecureChannelProtocolID))
	}
	if binary.LittleEndian.Uint16(out[6:8]) != mrp.SCStatusProtocolSessionEstablishmentSuccess {
		t.Errorf("protocolCode=%04X", binary.LittleEndian.Uint16(out[6:8]))
	}
}

// TestEncodeStatusReportWithPayload verifies that protocolData is
// appended after the 8-byte header.
func TestEncodeStatusReportWithPayload(t *testing.T) {
	t.Parallel()

	payload := []byte{0xAA, 0xBB, 0xCC}
	out := mrp.EncodeStatusReport(
		mrp.SCStatusGeneralFailure,
		0x0001_0000,
		mrp.SCStatusProtocolNoSharedTrustRoots,
		payload,
	)
	if len(out) != 8+len(payload) {
		t.Fatalf("len=%d, want %d", len(out), 8+len(payload))
	}
	for i, b := range payload {
		if out[8+i] != b {
			t.Errorf("payload[%d]=%02X, want %02X", i, out[8+i], b)
		}
	}
}

// TestEncodeStatusReportGeneralCodes locks the two GeneralCode constants.
func TestEncodeStatusReportGeneralCodes(t *testing.T) {
	t.Parallel()

	if mrp.SCStatusGeneralSuccess != 0x0000 {
		t.Errorf("SCStatusGeneralSuccess = %04X, want 0x0000", mrp.SCStatusGeneralSuccess)
	}
	if mrp.SCStatusGeneralFailure != 0x0001 {
		t.Errorf("SCStatusGeneralFailure = %04X, want 0x0001", mrp.SCStatusGeneralFailure)
	}
}

// TestAckTrackerNewAckTrackerNegativeDelay verifies the guard inside
// NewAckTracker that clamps negative delays to 0.
func TestAckTrackerNewAckTrackerNegativeDelay(t *testing.T) {
	t.Parallel()

	// Negative delay should be treated as 0 (no grace period).
	tracker := mrp.NewAckTracker(-1)
	if tracker == nil {
		t.Fatal("NewAckTracker(-1) returned nil")
	}
	// Verify it behaves as a 0-delay tracker: obligation is immediately due.
	tracker.Owe(100, 0, 1, true, t0)
	due := tracker.Due(t0)
	if len(due) != 1 {
		t.Fatalf("Due at t0 with 0-delay: len=%d, want 1", len(due))
	}
}

// TestLookupAndDischargePresent verifies that LookupAndDischarge returns
// (counter, true) for a present obligation and clears it atomically.
func TestLookupAndDischargePresent(t *testing.T) {
	t.Parallel()

	tracker := mrp.NewAckTracker(0)
	tracker.Owe(42, 0, 7, false, t0)

	counter, ok := tracker.LookupAndDischarge(0, 7)
	if !ok {
		t.Fatal("LookupAndDischarge should return true for present obligation")
	}
	if counter != 42 {
		t.Fatalf("counter=%d, want 42", counter)
	}
	if tracker.Pending() != 0 {
		t.Fatalf("Pending=%d after discharge, want 0", tracker.Pending())
	}
}

// TestLookupAndDischargeAbsent verifies that LookupAndDischarge returns
// (0, false) when no obligation exists for the exchange.
func TestLookupAndDischargeAbsent(t *testing.T) {
	t.Parallel()

	tracker := mrp.NewAckTracker(0)
	counter, ok := tracker.LookupAndDischarge(0, 99)
	if ok {
		t.Fatal("LookupAndDischarge should return false for absent obligation")
	}
	if counter != 0 {
		t.Fatalf("counter=%d, want 0 for absent exchange", counter)
	}
}

// TestNewRetransmitter_NilRNG verifies that NewRetransmitter falls back to a
// default math/rand/v2 PCG generator when rng is nil (exercises line 74-76 of
// retransmit.go). The resulting Retransmitter must be functional.
func TestNewRetransmitter_NilRNG(t *testing.T) {
	t.Parallel()
	// Pass nil to trigger the default-rng branch.
	r := mrp.NewRetransmitter(func([]byte) error { return nil }, (*rand.Rand)(nil))
	if r == nil {
		t.Fatal("NewRetransmitter(nil rng): returned nil")
	}
}
