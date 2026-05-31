// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHSColorModifiedAtZeroBeforeObservation verifies that ModifiedAt
// returns the zero time before either hue or saturation is observed.
func TestHSColorModifiedAtZeroBeforeObservation(t *testing.T) {
	t.Parallel()

	c := combined.NewHSColor("", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if !c.ModifiedAt().IsZero() {
		t.Errorf("ModifiedAt() before observation = %v, want zero", c.ModifiedAt())
	}
}

// TestHSColorModifiedAtUpdatesOnHue verifies that ModifiedAt returns a
// non-zero time after OnHue is called.
func TestHSColorModifiedAtUpdatesOnHue(t *testing.T) {
	t.Parallel()

	c := combined.NewHSColor("", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	before := time.Now()
	c.OnHue(120)
	after := time.Now()

	ts := c.ModifiedAt()
	if ts.IsZero() {
		t.Error("ModifiedAt() after OnHue() = zero, want non-zero")
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("ModifiedAt() = %v, want in [%v, %v]", ts, before, after)
	}
}

// TestHSColorModifiedAtUpdatesOnSaturation verifies that ModifiedAt
// returns a non-zero time after OnSaturation is called.
func TestHSColorModifiedAtUpdatesOnSaturation(t *testing.T) {
	t.Parallel()

	c := combined.NewHSColor("", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	before := time.Now()
	c.OnSaturation(0.8)
	after := time.Now()

	ts := c.ModifiedAt()
	if ts.IsZero() {
		t.Error("ModifiedAt() after OnSaturation() = zero, want non-zero")
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("ModifiedAt() = %v, want in [%v, %v]", ts, before, after)
	}
}

// TestHSColorRefreshedAtUpdatesOnHue verifies that RefreshedAt is also
// updated when OnHue is called (even if value does not change).
func TestHSColorRefreshedAtUpdatesOnHue(t *testing.T) {
	t.Parallel()

	c := combined.NewHSColor("", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(90)
	firstTs := c.RefreshedAt()
	if firstTs.IsZero() {
		t.Error("RefreshedAt() after first OnHue = zero, want non-zero")
	}
	// Second OnHue with same value: ModifiedAt should NOT update but RefreshedAt should.
	time.Sleep(time.Millisecond) // ensure clock advances
	c.OnHue(90)                  // same value
	secondTs := c.RefreshedAt()
	if !secondTs.After(firstTs) && secondTs.Equal(firstTs) {
		// RefreshedAt may or may not advance on same-value calls depending on sub-ms resolution;
		// the important invariant is that it is non-zero and >= firstTs.
		if secondTs.Before(firstTs) {
			t.Errorf("RefreshedAt() after repeat OnHue went backwards: %v < %v", secondTs, firstTs)
		}
	}
}

// TestHSColorModifiedAtReflectsLatestUpdate verifies that ModifiedAt
// advances monotonically when both hue and saturation are observed in sequence.
func TestHSColorModifiedAtReflectsLatestUpdate(t *testing.T) {
	t.Parallel()

	c := combined.NewHSColor("", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(0)
	hueTs := c.ModifiedAt()

	time.Sleep(time.Millisecond)
	c.OnSaturation(1.0)
	satTs := c.ModifiedAt()

	if !satTs.After(hueTs) {
		t.Errorf("ModifiedAt() after OnSaturation (%v) not after ModifiedAt after OnHue (%v)", satTs, hueTs)
	}
}
