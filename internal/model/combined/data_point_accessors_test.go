// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── HSColor P2 ──────────────────────────────────────────────────────────────

// TestHSColorP2Methods verifies Default, Max, Min, Service, Values,
// Multiplier, ParamsetKey, TranslationKey, DataPointNamePostfix,
// HasDataPoints, IsStatusValid, IsStateChange, IsValid.
func TestHSColorP2Methods(t *testing.T) {
	h := NewHSColor("addr", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)

	if h.Default() != nil {
		t.Error("Default() must be nil")
	}
	if _, ok := h.Max(); ok {
		t.Error("Max() must return (0, false)")
	}
	if _, ok := h.Min(); ok {
		t.Error("Min() must return (0, false)")
	}
	if h.Service() {
		t.Error("Service() must be false")
	}
	if h.Values() != nil {
		t.Error("Values() must be nil")
	}
	if h.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", h.Multiplier())
	}
	if got := h.ParamsetKey(); got != "COMBINED" {
		t.Errorf("ParamsetKey()=%q, want COMBINED", got)
	}
	if got := h.TranslationKey(); got != "hs_color" {
		t.Errorf("TranslationKey()=%q, want hs_color", got)
	}
	if got := h.DataPointNamePostfix(); got != "" {
		t.Errorf("DataPointNamePostfix()=%q, want empty", got)
	}
	// No observations yet
	if h.HasDataPoints() {
		t.Error("HasDataPoints() must be false with no observations")
	}
	if h.IsStatusValid() {
		t.Error("IsStatusValid() must be false with no observations (StateUncertain=true)")
	}
	if !h.ModifiedAt().IsZero() {
		t.Error("ModifiedAt() must be zero")
	}
	if !h.RefreshedAt().IsZero() {
		t.Error("RefreshedAt() must be zero")
	}
	if h.IsStateChange() {
		t.Error("IsStateChange() must be false before any observation")
	}
	if h.IsValid() {
		t.Error("IsValid() must be false before any observation")
	}
}

// TestHSColorHasDataPointsAfterBothObserved verifies HasDataPoints and
// IsValid become true once both hue and saturation are observed.
func TestHSColorHasDataPointsAfterBothObserved(t *testing.T) {
	h := NewHSColor("addr", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	h.OnHue(180)
	if h.HasDataPoints() {
		t.Error("HasDataPoints() must be false after only hue")
	}
	h.OnSaturation(0.5)
	if !h.HasDataPoints() {
		t.Error("HasDataPoints() must be true after both observed")
	}
	if !h.IsValid() {
		t.Error("IsValid() must be true after both observed")
	}
}

// ─── Timer P2 ────────────────────────────────────────────────────────────────

// TestTimerP2Methods verifies Default, Max, Min, HasUnit, IsValid,
// Service, Values, Multiplier, ParamsetKey, TranslationKey,
// DataPointNamePostfix, HasDataPoints, IsStatusValid, IsStateChange.
func TestTimerP2Methods(t *testing.T) {
	timer := NewTimer("addr", nil, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)

	if timer.Default() != nil {
		t.Error("Default() must be nil")
	}
	maxVal, ok := timer.Max()
	if !ok || maxVal != float64(timerUpperBoundSeconds) {
		t.Errorf("Max()=(%v,%v), want (%v,true)", maxVal, ok, timerUpperBoundSeconds)
	}
	if _, ok := timer.Min(); ok {
		t.Error("Min() must return (0, false)")
	}
	if !timer.HasUnit() {
		t.Error("HasUnit() must be true when UnitParameter is set")
	}
	if !timer.IsValid() {
		t.Error("IsValid() must be true when ValueParameter is set")
	}
	if timer.Service() {
		t.Error("Service() must be false")
	}
	if timer.Values() != nil {
		t.Error("Values() must be nil")
	}
	if timer.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", timer.Multiplier())
	}
	if got := timer.ParamsetKey(); got != "COMBINED" {
		t.Errorf("ParamsetKey()=%q, want COMBINED", got)
	}
	if got := timer.TranslationKey(); got != "timer" {
		t.Errorf("TranslationKey()=%q, want timer", got)
	}
	if got := timer.DataPointNamePostfix(); got != "" {
		t.Errorf("DataPointNamePostfix()=%q, want empty", got)
	}
	// No observation yet
	if timer.HasDataPoints() {
		t.Error("HasDataPoints() must be false before any OnComponents")
	}
	if timer.IsStatusValid() {
		t.Error("IsStatusValid() must be false before any OnComponents")
	}
	if timer.IsStateChange() {
		t.Error("IsStateChange() must be false before any OnComponents")
	}
	// After observation
	timer.OnComponents(120, TimerUnitSeconds)
	if !timer.HasDataPoints() {
		t.Error("HasDataPoints() must be true after observation")
	}
	if !timer.IsStatusValid() {
		t.Error("IsStatusValid() must be true after observation")
	}
}

// TestTimerHasUnitFalseWhenNoUnitParam verifies HasUnit returns false
// when no unit parameter is configured.
func TestTimerHasUnitFalseWhenNoUnitParam(t *testing.T) {
	timer := NewTimer("addr", nil, hmenum.ParameterOnTime, hmenum.Parameter(""))
	if timer.HasUnit() {
		t.Error("HasUnit() must be false when UnitParameter is empty")
	}
}

// ─── LevelCombined P2 ────────────────────────────────────────────────────────

// TestLevelCombinedP2Methods verifies ParamsetKey, TranslationKey,
// DataPointNamePostfix, HasDataPoints, IsStatusValid, ModifiedAt,
// RefreshedAt.
func TestLevelCombinedP2Methods(t *testing.T) {
	lc := NewLevelCombined("addr", nil,
		hmenum.ParameterLevel, hmenum.ParameterLevel2,
		hmenum.ParameterLevelCombined)

	if got := lc.ParamsetKey(); got != "COMBINED" {
		t.Errorf("ParamsetKey()=%q, want COMBINED", got)
	}
	if got := lc.TranslationKey(); got != "level_combined" {
		t.Errorf("TranslationKey()=%q, want level_combined", got)
	}
	if got := lc.DataPointNamePostfix(); got != "" {
		t.Errorf("DataPointNamePostfix()=%q, want empty", got)
	}
	if lc.HasDataPoints() {
		t.Error("HasDataPoints() must be false before observations")
	}
	if lc.IsStatusValid() {
		t.Error("IsStatusValid() must be false before observations")
	}
	if !lc.ModifiedAt().IsZero() {
		t.Error("ModifiedAt() must be zero")
	}
	if !lc.RefreshedAt().IsZero() {
		t.Error("RefreshedAt() must be zero")
	}
	// After both observations
	lc.OnLevel(0.5)
	lc.OnSlatsLevel(0.3)
	if !lc.HasDataPoints() {
		t.Error("HasDataPoints() must be true after both observations")
	}
	if !lc.IsStatusValid() {
		t.Error("IsStatusValid() must be true after both observations")
	}
}

// TestCombinedDPIsReadableIsWritable verifies that all combined data
// point types report IsReadable() == false and IsWritable() == true,
// _operations = Operations.WRITE
// (combined/data_point.py:110) which sets WRITE but not READ.
func TestCombinedDPIsReadableIsWritable(t *testing.T) {
	h := NewHSColor("addr", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if h.IsReadable() {
		t.Error("HSColor.IsReadable() must be false")
	}
	if !h.IsWritable() {
		t.Error("HSColor.IsWritable() must be true")
	}

	timer := NewTimer("addr", nil, hmenum.ParameterOnTime, hmenum.ParameterOnTimeUnit)
	if timer.IsReadable() {
		t.Error("Timer.IsReadable() must be false")
	}
	if !timer.IsWritable() {
		t.Error("Timer.IsWritable() must be true")
	}

	lc := NewLevelCombined("addr", nil,
		hmenum.ParameterLevel, hmenum.ParameterLevel2, hmenum.ParameterLevelCombined)
	if lc.IsReadable() {
		t.Error("LevelCombined.IsReadable() must be false")
	}
	if !lc.IsWritable() {
		t.Error("LevelCombined.IsWritable() must be true")
	}

	wp := NewWeekProfile("addr", nil, nil, hmenum.ParameterWeekProgramPointer)
	if wp.IsReadable() {
		t.Error("WeekProfile.IsReadable() must be false")
	}
	if !wp.IsWritable() {
		t.Error("WeekProfile.IsWritable() must be true")
	}
}
