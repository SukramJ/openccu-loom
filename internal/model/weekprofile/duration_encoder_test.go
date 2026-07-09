// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import "testing"

// TestParseDurationToBaseFactorPicksNaturalBase pins the (base, factor) pair the
// duration encoder emits for representative durations. The encoder must select
// the coarsest natural base for the input unit — e.g. "2min" is (MIN_1, 2), not
// the finer (SEC_5, 24) a smallest-base search would produce. Both encode 120s,
// but the CCU editor surfaces the emitted base/factor, so the natural base is
// the one the reference writes.
//
// Golden values mirror `convert_duration_to_base_factor` in
// schedule_models.py:706 (its docstring examples "45s"->(SEC_5,9) and
// "40min"->(MIN_5,8) are reproduced verbatim below).
func TestParseDurationToBaseFactorPicksNaturalBase(t *testing.T) {
	t.Parallel()

	// TimeBase ids: MS_100=0, SEC_1=1, SEC_5=2, SEC_10=3, MIN_1=4, MIN_5=5,
	// MIN_10=6, HOUR_1=7.
	cases := []struct {
		duration   string
		wantBase   int
		wantFactor int
	}{
		// Natural-base selection: value fits its own unit's base directly.
		{"5s", 1, 5},     // SEC_1 × 5
		{"1s", 1, 1},     // SEC_1 × 1
		{"10s", 1, 10},   // SEC_1 × 10
		{"30s", 1, 30},   // SEC_1 × 30 (factor at the CCU cap)
		{"1min", 4, 1},   // MIN_1 × 1
		{"2min", 4, 2},   // MIN_1 × 2 — the regression: NOT (SEC_5, 24)
		{"10min", 4, 10}, // MIN_1 × 10
		{"1h", 7, 1},     // HOUR_1 × 1
		// Promotion: factor at the natural base would exceed the CCU cap of 30,
		// so the encoder promotes to the next base that divides evenly.
		{"45s", 2, 9},   // SEC_1 factor 45 > 30 → SEC_5 × 9
		{"40min", 5, 8}, // MIN_1 factor 40 > 30 → MIN_5 × 8
		// ms special-case: literal milliseconds collapse to 100ms steps.
		{"500ms", 0, 5}, // MS_100 × 5
		// Unrepresentable / malformed inputs yield the (0, 0) sentinel.
		{"", 0, 0},
		{"abc", 0, 0},
		{"0s", 0, 0},
		{"250ms", 0, 0}, // sub-100ms granularity is not representable
	}

	for _, tc := range cases {
		base, factor := parseDurationToBaseFactorInts(tc.duration)
		if base != tc.wantBase || factor != tc.wantFactor {
			t.Errorf("parseDurationToBaseFactorInts(%q) = (%d, %d), want (%d, %d)",
				tc.duration, base, factor, tc.wantBase, tc.wantFactor)
		}
	}
}

// TestDurationBaseFactorRoundTrip verifies that a duration encodes to a
// (base, factor) pair that decodes back to the same duration string. This
// guards the natural-base encoder against a base/factor that FormatTimeBaseFactor
// would render as a different (though numerically equal) duration.
func TestDurationBaseFactorRoundTrip(t *testing.T) {
	t.Parallel()

	for _, duration := range []string{"5s", "1s", "10s", "30s", "1min", "2min", "10min", "1h", "500ms"} {
		base, factor := parseDurationToBaseFactorInts(duration)
		if factor == 0 {
			t.Fatalf("parseDurationToBaseFactorInts(%q) unexpectedly returned factor 0", duration)
		}
		if got := FormatTimeBaseFactor(base, factor); got != duration {
			t.Errorf("round-trip %q → (%d,%d) → %q; want %q", duration, base, factor, got, duration)
		}
	}
}
