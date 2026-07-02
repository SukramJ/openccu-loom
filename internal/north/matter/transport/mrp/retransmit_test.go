// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp_test

import (
	"math"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// TestBackoffDuration_TransmissionExponent verifies the retransmission
// interval formula across the transmission/jitter matrix:
//
//	interval = base × MARGIN × THRESHOLD^max(0, n-1) × (1 + jitter × JITTER_FACTOR)
//
// Mirrors matter.js MRP.ts:125-146 retransmissionIntervalOf. The
// exponent clamps to zero for transmission 0 AND 1 — chip's
// max(0, n-1) means the very first retransmit still runs at the
// un-grown base interval — then grows by the 1.6 threshold from
// transmission 2 onward (1.6^1), and 1.6^2=2.56 at transmission 3.
func TestBackoffDuration_TransmissionExponent(t *testing.T) {
	t.Parallel()

	const base = 500 * time.Millisecond
	always0 := func() float64 { return 0 }
	always1 := func() float64 { return 1 }

	tests := []struct {
		name         string
		transmission int
		rand01       func() float64
		wantFactor   float64 // THRESHOLD^max(0, n-1)
		wantJitter   float64 // 1 + rand01()*JITTER_FACTOR
	}{
		{"transmission0_noJitter", 0, always0, 1, 1},
		{"transmission0_maxJitter", 0, always1, 1, 1.25},
		{"transmission1_noJitter", 1, always0, 1, 1},
		{"transmission1_maxJitter", 1, always1, 1, 1.25},
		{"transmission2_noJitter", 2, always0, 1.6, 1},
		{"transmission2_maxJitter", 2, always1, 1.6, 1.25},
		{"transmission3_noJitter", 3, always0, 2.56, 1},
		{"transmission3_maxJitter", 3, always1, 2.56, 1.25},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mrp.BackoffDuration(base, tc.transmission, tc.rand01)
			want := time.Duration(float64(base) * mrp.MRPBackoffMargin * tc.wantFactor * tc.wantJitter)
			// Tolerance absorbs float64 rounding differences between the
			// literal wantFactor constants here and math.Pow's internal
			// evaluation in the production formula — negligible at
			// millisecond scale.
			if diff := math.Abs(float64(got - want)); diff > float64(time.Microsecond) {
				t.Errorf("BackoffDuration(%v, %d, ...) = %v, want %v (diff %v)",
					base, tc.transmission, got, want, time.Duration(diff))
			}
		})
	}
}

// TestBackoffDuration_ZeroBase verifies the degenerate zero-base input
// produces a zero interval rather than panicking — defensive coverage
// for a caller that passes an unresolved session's zero-value base
// interval before falling back to the spec default.
func TestBackoffDuration_ZeroBase(t *testing.T) {
	t.Parallel()
	got := mrp.BackoffDuration(0, 0, func() float64 { return 0.5 })
	if got != 0 {
		t.Errorf("BackoffDuration(0, ...) = %v, want 0", got)
	}
}
