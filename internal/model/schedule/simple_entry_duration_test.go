// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schedule_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// baseEntry returns a minimal valid SimpleEntry that can have Duration
// modified for duration-validation tests.
func baseEntry() schedule.SimpleEntry {
	return schedule.SimpleEntry{
		Weekdays: []schedule.Weekday{schedule.WeekdayMonday},
		Time:     "08:00",
	}
}

// TestSimpleEntryValidateDurationFactorExactly30 verifies that a
// factor of exactly 30 is accepted.
func TestSimpleEntryValidateDurationFactorExactly30(t *testing.T) {
	t.Parallel()

	e := baseEntry()
	e.Duration = "30s"
	if err := e.Validate(); err != nil {
		t.Errorf("Validate() with Duration=%q returned error: %v", e.Duration, err)
	}
}

// TestSimpleEntryValidateDurationFactor1 verifies that the minimum
// factor of 1 is accepted for each unit.
func TestSimpleEntryValidateDurationFactor1(t *testing.T) {
	t.Parallel()

	for _, d := range []string{"1ms", "1s", "1min", "1h"} {
		e := baseEntry()
		e.Duration = d
		if err := e.Validate(); err != nil {
			t.Errorf("Validate() with Duration=%q returned error: %v", d, err)
		}
	}
}

// TestSimpleEntryValidateDurationNumeralIsNotTheWireFactor records what these
// tests used to assert and why it was wrong.
//
// They read the numeral in the string as the CCU's DURATION_FACTOR and
// required 1..30, so "31s" and "999h" were rejected. The numeral is not the
// factor: the wire pair is (base, factor) and the reader multiplies the factor
// out in the base's own unit, so the read path emits "50min" for (MIN_10, 5)
// and "500ms" for (MS_100, 5) — both perfectly ordinary slots that the bound
// refused. Whether a duration is representable is decided where the pair is
// searched for, in weekprofile.ParseTimeBaseFactor, which fails the write when
// no base divides the value into a factor of 1..30.
//
// So the domain validator checks the spelling and lets the encoder decide the
// arithmetic. "31h" is a case worth keeping visible: it is representable, as
// the CCU's own "Dauerhaft" pair.
func TestSimpleEntryValidateDurationNumeralIsNotTheWireFactor(t *testing.T) {
	t.Parallel()

	for _, d := range []string{"31ms", "31s", "31min", "31h", "999h", "50min", "500ms"} {
		e := baseEntry()
		e.Duration = d
		if err := e.Validate(); err != nil {
			t.Errorf("Validate() with Duration=%q returned error: %v; the numeral is the "+
				"duration in the unit shown, not the wire factor", d, err)
		}
	}
}

// TestSimpleEntryValidateAcceptsTheReservedDurationWords pins the two
// spellings that are not numerals: the CCU's permanent pair and a real zero
// duration. The daemon's own lock encoder mints both, and the validator used
// to reject them — "permanent" did not match the numeric pattern and "0ms"
// failed the lower bound.
func TestSimpleEntryValidateAcceptsTheReservedDurationWords(t *testing.T) {
	t.Parallel()

	for _, d := range []string{schedule.PermanentDuration, schedule.ZeroDuration} {
		e := baseEntry()
		e.Duration = d
		if err := e.Validate(); err != nil {
			t.Errorf("Validate() with Duration=%q returned error: %v", d, err)
		}
		e = baseEntry()
		e.RampTime = d
		if err := e.Validate(); err != nil {
			t.Errorf("Validate() with RampTime=%q returned error: %v", d, err)
		}
	}
}

// TestSimpleEntryValidateEmptyDurationAccepted verifies that an empty
// Duration field passes validation (duration is optional).
func TestSimpleEntryValidateEmptyDurationAccepted(t *testing.T) {
	t.Parallel()

	e := baseEntry()
	e.Duration = ""
	if err := e.Validate(); err != nil {
		t.Errorf("Validate() with empty Duration returned error: %v", err)
	}
}

// TestSimpleEntryValidateDurationInvalidFormatRejected verifies that a
// malformed duration string (no unit) is rejected.
func TestSimpleEntryValidateDurationInvalidFormatRejected(t *testing.T) {
	t.Parallel()

	e := baseEntry()
	e.Duration = "10"
	if err := e.Validate(); err == nil {
		t.Error("Validate() with Duration=10 (no unit) want error, got nil")
	}
}
