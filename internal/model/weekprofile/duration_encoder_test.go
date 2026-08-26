// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestParseTimeBaseFactorPicksCoarsestBase pins the (base, factor) pair
// the duration encoder emits.
//
// The rule is coarsest base first. Every value that fits a coarse base
// also fits several finer ones — 10s is (SEC_10, 1), (SEC_5, 2) and
// (SEC_1, 10) alike — so without one the two ends of the daemon picked
// different pairs for the same duration. The factor cap forces the
// search to keep walking: 45s has no representation in SEC_1 at all,
// because factor 45 is past what the firmware accepts.
func TestParseTimeBaseFactorPicksCoarsestBase(t *testing.T) {
	t.Parallel()

	// TimeBase ids: MS_100=0, SEC_1=1, SEC_5=2, SEC_10=3, MIN_1=4, MIN_5=5,
	// MIN_10=6, HOUR_1=7.
	cases := []struct {
		duration   string
		wantBase   int
		wantFactor int
		wantOK     bool
	}{
		{"1s", 1, 1, true},    // SEC_1 × 1 — no coarser base divides 1s
		{"5s", 2, 1, true},    // SEC_5 × 1
		{"10s", 3, 1, true},   // SEC_10 × 1
		{"30s", 3, 3, true},   // SEC_10 × 3
		{"1min", 4, 1, true},  // MIN_1 × 1
		{"2min", 4, 2, true},  // MIN_1 × 2
		{"10min", 6, 1, true}, // MIN_10 × 1
		{"1h", 7, 1, true},    // HOUR_1 × 1
		// The factor cap forces a coarser base than the value's own unit.
		{"45s", 2, 9, true},     // SEC_1 would need factor 45
		{"40min", 6, 4, true},   // MIN_1 would need factor 40
		{"500ms", 0, 5, true},   // MS_100 × 5
		{"1200ms", 0, 12, true}, // MS_100 × 12 — 1.2s has no coarser home
		// Values that only a fine base can express keep their precision
		// rather than being snapped to a neighbouring round number.
		{"65s", 2, 13, true},   // SEC_5 × 13, not "1min"
		{"70s", 3, 7, true},    // SEC_10 × 7
		{"65min", 5, 13, true}, // MIN_5 × 13
		{"70min", 6, 7, true},  // MIN_10 × 7
		// The "permanent" pair sits one past the cap. The device holds it
		// and a lock schedule read from the CCU carries it back into a
		// save, so it passes through verbatim.
		{"31h", 7, 31, true},
		// Zero is a duration the CCU holds — a door lock encodes
		// `lock_autorelock_start` as exactly (0, 0) — and it means the
		// same in every base, so every spelling lands on the canonical
		// pair rather than being rejected as "no duration".
		{"0ms", 0, 0, true},
		{"0s", 0, 0, true},
		{"0h", 0, 0, true},
		// Lenient input: it arrives straight from a REST payload.
		{"90", 3, 9, true},    // bare number counts as seconds
		{"2m", 4, 2, true},    // "m" is minutes
		{"1.5s", 0, 15, true}, // fractional, but a whole 100ms step
		// Unrepresentable / malformed input is rejected rather than
		// rounded to the nearest pair the CCU would take.
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"-5s", 0, 0, false},
		{"250ms", 0, 0, false}, // sub-100ms granularity
		{"301s", 0, 0, false},  // no base divides it within the factor cap
		{"32h", 0, 0, false},   // past the cap and not the sentinel
	}

	for _, tc := range cases {
		base, factor, ok := ParseTimeBaseFactor(tc.duration)
		if base != tc.wantBase || factor != tc.wantFactor || ok != tc.wantOK {
			t.Errorf("ParseTimeBaseFactor(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tc.duration, base, factor, ok, tc.wantBase, tc.wantFactor, tc.wantOK)
		}
	}
}

// TestDurationBaseFactorRoundTrip walks every (base, factor) pair the
// CCU can hold and asserts the value survives a decode/encode cycle.
//
// This is the guard that matters most on this codec: the string is what
// the SPA shows and what comes back on the next save, so a decoder that
// renders (SEC_5, 13) by magnitude — "1min" — writes 60s to a device the
// operator set to 65s. Nothing else in the stack would notice.
func TestDurationBaseFactorRoundTrip(t *testing.T) {
	t.Parallel()

	// A zero factor is a real pair, not an absent one: it is how a door
	// lock encodes `lock_autorelock_start`, and the sparse paramset write
	// drops the keys entirely when the codec cannot render it.
	for base := range 8 {
		if got := FormatTimeBaseFactor(base, 0); got != ZeroDuration {
			t.Errorf("FormatTimeBaseFactor(%d, 0) = %q, want %q", base, got, ZeroDuration)
		}
	}
	if b, f, ok := ParseTimeBaseFactor(ZeroDuration); !ok || b != 0 || f != 0 {
		t.Errorf("ParseTimeBaseFactor(%q) = (%d, %d, %v), want (0, 0, true)", ZeroDuration, b, f, ok)
	}

	for base := range 8 {
		for factor := 1; factor <= MaxTimeBaseFactor; factor++ {
			duration := FormatTimeBaseFactor(base, factor)
			if duration == "" {
				t.Fatalf("FormatTimeBaseFactor(%d, %d) rendered nothing", base, factor)
			}
			gotBase, gotFactor, ok := ParseTimeBaseFactor(duration)
			if !ok {
				t.Errorf("(%d,%d) → %q → rejected", base, factor, duration)
				continue
			}
			// The pair may legitimately move to a coarser base, but the
			// duration it expresses must not change.
			if got, want := durationSeconds(gotBase, gotFactor), durationSeconds(base, factor); got != want {
				t.Errorf("(%d,%d) → %q → (%d,%d): %v seconds, want %v",
					base, factor, duration, gotBase, gotFactor, got, want)
			}
		}
	}
}

// TestDurationEncodingSettlesAfterOnePass pins that the codec reaches a
// fixed point immediately: a schedule opened and saved without edits may
// have its (base, factor) pair normalised once, and never moves again.
//
// Without this, a pair that keeps shifting would let an untouched
// schedule drift a little further on every save.
func TestDurationEncodingSettlesAfterOnePass(t *testing.T) {
	t.Parallel()

	for base := range 8 {
		for factor := 1; factor <= MaxTimeBaseFactor; factor++ {
			first := FormatTimeBaseFactor(base, factor)
			b1, f1, ok := ParseTimeBaseFactor(first)
			if !ok {
				t.Fatalf("(%d,%d) → %q → rejected", base, factor, first)
			}
			second := FormatTimeBaseFactor(b1, f1)
			b2, f2, ok := ParseTimeBaseFactor(second)
			if !ok {
				t.Fatalf("(%d,%d) → %q → rejected", b1, f1, second)
			}
			if b2 != b1 || f2 != f1 {
				t.Errorf("(%d,%d): settled on (%d,%d) → %q → (%d,%d)",
					base, factor, b1, f1, second, b2, f2)
			}
			if third := FormatTimeBaseFactor(b2, f2); third != second {
				t.Errorf("(%d,%d): %q re-renders as %q", base, factor, second, third)
			}
		}
	}
}

// durationSeconds expresses a (base, factor) pair in seconds so two
// pairs can be compared by the duration they mean rather than by shape.
func durationSeconds(base, factor int) float64 {
	for _, row := range timeBaseTable {
		if row.id == base {
			return float64(factor) * float64(row.in100ms) / 10
		}
	}
	return -1
}
