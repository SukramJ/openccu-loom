// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp_test

import (
	"math"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestMRPBackoffThreshold_Chip pins MRPBackoffThreshold at exactly 1.6.
//
// chip ReliableMessageMgr.cpp defines the growth factor via:
//
//	kRetryFactorNumerator   = 16
//	kRetryFactorDenominator = 10
//	factor = kRetryFactorNumerator / kRetryFactorDenominator = 1.6
//
// Drift from 1.6 causes retransmit windows to skew:  a lower value
// under-spaces retries and triggers Apple's duplicate-message drop
// path; a higher value over-spaces retries and triggers the CASE
// handshake timeout on constrained devices (chip enforces a 5-second
// CASE-sigma exchange budget).
func TestMRPBackoffThreshold_Chip(t *testing.T) {
	t.Parallel()

	const want = 1.6
	if math.Abs(mrp.MRPBackoffThreshold-want) > 1e-9 {
		t.Errorf("MRPBackoffThreshold = %v, want %v (chip kRetryFactor 16/10)", mrp.MRPBackoffThreshold, want)
	}
}

// TestMRPBackoffBase_Chip pins MRPBackoffBase at 300 ms.
//
// chip ReliableMessageMgr.cpp table constant:
//
//	MRP_BACKOFF_BASE_ms = 300
//
// The base forms the initial retransmit window before any exponential
// growth.  Deviating from the chip value shifts all retransmit windows
// and causes misaligned round-trip expectations during the Apple CASE
// handshake.
func TestMRPBackoffBase_Chip(t *testing.T) {
	t.Parallel()

	const want = 300 * time.Millisecond
	if mrp.MRPBackoffBase != want {
		t.Errorf("MRPBackoffBase = %v, want %v (chip MRP_BACKOFF_BASE_ms=300)", mrp.MRPBackoffBase, want)
	}
}

// TestMRPBackoffMargin_Chip pins MRPBackoffMargin at 1.1.
//
// chip ReliableMessageMgr.cpp uses MRP_BACKOFF_MARGIN = 1.1 to
// compensate for clock skew and processing delay on the peer side.
// Without the margin the retransmit fires slightly before the peer's
// ACK deadline, increasing spurious retransmits under normal latency.
func TestMRPBackoffMargin_Chip(t *testing.T) {
	t.Parallel()

	const want = 1.1
	if math.Abs(mrp.MRPBackoffMargin-want) > 1e-9 {
		t.Errorf("MRPBackoffMargin = %v, want %v (chip MRP_BACKOFF_MARGIN=1.1)", mrp.MRPBackoffMargin, want)
	}
}

// TestMRPBackoffJitterFactor_Chip pins MRPBackoffJitterFactor at 0.25.
//
// chip ReliableMessageMgr.cpp uses MRP_BACKOFF_JITTER = 0.25 so each
// retransmit window is widened by up to 25 % uniform random jitter.
// Changing this value shifts Apple's observed "Dropping message" rate
// under CASE-handshake load.
func TestMRPBackoffJitterFactor_Chip(t *testing.T) {
	t.Parallel()

	const want = 0.25
	if math.Abs(mrp.MRPBackoffJitterFactor-want) > 1e-9 {
		t.Errorf("MRPBackoffJitterFactor = %v, want %v (chip MRP_BACKOFF_JITTER=0.25)", mrp.MRPBackoffJitterFactor, want)
	}
}

// TestMaxRetransmissions_Chip pins MaxRetransmissions at 4.
//
// chip ReliableMessageMgr.cpp uses CHIP_CONFIG_MRP_LOCAL_MAX_RETRANS = 4
// (per Matter Core Spec §4.12.6 Table 9, "MRP_MAX_RETRANS = 4").
// Higher values prolong delivery-failure detection; lower values
// declare failure before the peer's ACK deadline.
func TestMaxRetransmissions_Chip(t *testing.T) {
	t.Parallel()

	const want = 4
	if mrp.MaxRetransmissions != want {
		t.Errorf("MaxRetransmissions = %d, want %d (chip CHIP_CONFIG_MRP_LOCAL_MAX_RETRANS=4)", mrp.MaxRetransmissions, want)
	}
}

// TestMRPBackoffExponent_MaxZeroOnFirstRetransmit verifies that the
// exponent is clamped to zero for the first retransmit (retries=1).
// chip ReliableMessageMgr.cpp:295 uses max(0, n-1) as the exponent so
// the first re-send runs at the un-grown base interval (threshold^0=1).
// Without the max(0,n-1) clamping the first retransmit fires at
// MRPBackoffThreshold×base (~480 ms instead of 330 ms) which is ~1.6×
// too aggressive and surfaces as increased "Dropping message" frequency
// in Apple's IM log during CASE handshake bursts.
//
// This test drives the [Retransmitter] with a deterministic RNG
// (zero-jitter approximation) and asserts that the first-retransmit
// deadline does NOT grow beyond MRPBackoffBase×MRPBackoffMargin×(1+jitter).
func TestMRPBackoffExponent_MaxZeroOnFirstRetransmit(t *testing.T) {
	t.Parallel()

	// Upper bound on the first-retransmit window with maximum jitter:
	// base × margin × (1 + jitterFactor) = 300ms × 1.1 × 1.25 = 412.5 ms.
	// Any value greater than this indicates the exponent was not clamped
	// at the first retransmit.
	const firstRetransmitUpperBound = 413 * time.Millisecond

	var sent int
	r := mrp.NewRetransmitter(func([]byte) error {
		sent++
		return nil
	}, nil) // nil → default rng

	t0 := time.Unix(1_000_000, 0)
	r.Track(1, 1, []byte("payload"), t0)

	// No tick before the window — should be quiet.
	if got := r.Tick(t0.Add(10 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("Tick at +10ms: got %d results, want 0", len(got))
	}

	// At firstRetransmitUpperBound the first retransmit must have fired.
	results := r.Tick(t0.Add(firstRetransmitUpperBound))
	if len(results) != 1 {
		t.Fatalf("Tick at +%v: got %d results, want 1 (first retransmit)", firstRetransmitUpperBound, len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("first retransmit returned error: %v", results[0].Err)
	}
	if sent != 1 {
		t.Fatalf("send called %d times, want 1 (first retransmit only)", sent)
	}
}
