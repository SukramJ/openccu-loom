// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// TestSimpleEntryValidateDurationFactorAbove30Rejected verifies that a
// factor of 31 is rejected for every time unit.
func TestSimpleEntryValidateDurationFactorAbove30Rejected(t *testing.T) {
	t.Parallel()

	for _, d := range []string{"31ms", "31s", "31min", "31h"} {
		e := baseEntry()
		e.Duration = d
		if err := e.Validate(); err == nil {
			t.Errorf("Validate() with Duration=%q want error, got nil", d)
		}
	}
}

// TestSimpleEntryValidateDurationFactor999Rejected verifies that very
// large factors (999) are also rejected.
func TestSimpleEntryValidateDurationFactor999Rejected(t *testing.T) {
	t.Parallel()

	e := baseEntry()
	e.Duration = "999h"
	if err := e.Validate(); err == nil {
		t.Error("Validate() with Duration=999h want error, got nil")
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
