// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestPermanentDurationRoundTrips is the property the reserved word exists
// for: a switch point the device holds forever must survive a read, a hand
// through the DTO, and a write back as the SAME pair.
//
// Before it had a spelling, the pair decoded to "" — which
// BuildSimpleRawParamset treats as "leave the device alone", so the value
// happened to survive by not being written. That is a different guarantee: it
// held only while nobody could set a duration on the slot, and it showed the
// operator an empty field on a switch point that never expires.
func TestPermanentDurationRoundTrips(t *testing.T) {
	t.Parallel()
	got := decodeWireDuration(permanentBase, permanentFactor)
	if got != PermanentDuration {
		t.Fatalf("decodeWireDuration(%d, %d) = %q, want %q",
			permanentBase, permanentFactor, got, PermanentDuration)
	}
	base, factor, ok := ParseTimeBaseFactor(got)
	if !ok {
		t.Fatalf("ParseTimeBaseFactor(%q) refused the value decode just produced", got)
	}
	if base != permanentBase || factor != permanentFactor {
		t.Errorf("round trip landed on (%d, %d), want (%d, %d)",
			base, factor, permanentBase, permanentFactor)
	}
}

// TestPermanentDurationIsNotAnOrdinaryDuration is the negative control. The
// word has to stay distinguishable from the values around it, or the guard
// above would pass on a decoder that simply returned a fixed string.
func TestPermanentDurationIsNotAnOrdinaryDuration(t *testing.T) {
	t.Parallel()
	// An ordinary pair still renders as a duration, not as the reserved word.
	if got := decodeWireDuration(7, 2); got == PermanentDuration {
		t.Errorf("decodeWireDuration(7, 2) = %q; only (%d, %d) is permanent",
			got, permanentBase, permanentFactor)
	}
	// A slot carrying no duration still reads as "leave it alone", which is a
	// different statement from "permanent" and must not be folded into it.
	if got := decodeWireDuration(0, 0); got == PermanentDuration {
		t.Errorf("decodeWireDuration(0, 0) = %q, want the zero duration", got)
	}
	// And the word does not parse as a time.
	if _, ok := durationIn100ms(PermanentDuration); ok {
		t.Errorf("durationIn100ms(%q) accepted the reserved word as a duration", PermanentDuration)
	}
}

// TestPermanentDurationIsCaseInsensitive keeps the REST surface forgiving on
// the way in: the string reaches ParseTimeBaseFactor straight from a request
// body, and the rest of that parser is lenient by design.
func TestPermanentDurationIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"permanent", "Permanent", "PERMANENT", "  permanent  "} {
		base, factor, ok := ParseTimeBaseFactor(in)
		if !ok || base != permanentBase || factor != permanentFactor {
			t.Errorf("ParseTimeBaseFactor(%q) = (%d, %d, %v), want (%d, %d, true)",
				in, base, factor, ok, permanentBase, permanentFactor)
		}
	}
}
