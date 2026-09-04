// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Cb-T2-01: StateUncertain() aggregate for Timer, HSColor, LevelCombined.

func TestTimerStateUncertain_BeforeObservation(t *testing.T) {
	t.Parallel()
	timer := combined.NewTimer("addr", nil, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	if !timer.StateUncertain() {
		t.Error("Timer.StateUncertain() must be true before first observation")
	}
}

func TestTimerStateUncertain_AfterObservation(t *testing.T) {
	t.Parallel()
	timer := combined.NewTimer("addr", nil, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	timer.OnComponents(10.0, hmenum.TimerUnitSeconds)
	if timer.StateUncertain() {
		t.Error("Timer.StateUncertain() must be false after observation")
	}
}

func TestHSColorStateUncertain_AlwaysFalse(t *testing.T) {
	t.Parallel()
	c := combined.NewHSColor("addr", nil, hmenum.ParameterHue, hmenum.ParameterSaturation)
	// HSColor has no optimistic tracker; StateUncertain is always false.
	if c.StateUncertain() {
		t.Error("HSColor.StateUncertain() must always be false")
	}
	c.OnHue(180)
	c.OnSaturation(0.5)
	if c.StateUncertain() {
		t.Error("HSColor.StateUncertain() must remain false after observation")
	}
}

func TestLevelCombinedStateUncertain_AlwaysFalse(t *testing.T) {
	t.Parallel()
	lc := combined.NewLevelCombined("addr", hmenum.ParameterLevel, hmenum.ParameterLevel2)
	if lc.StateUncertain() {
		t.Error("LevelCombined.StateUncertain() must always be false")
	}
	lc.OnLevel(0.5)
	lc.OnSlatsLevel(0.3)
	if lc.StateUncertain() {
		t.Error("LevelCombined.StateUncertain() must remain false after observation")
	}
}

// Cb-T2-02: Timer.Default() returns nil before Subscribe, non-nil after
// subscribing to a channel whose value DP has a descriptor default.

func TestTimerDefault_NoSubscribe(t *testing.T) {
	t.Parallel()
	timer := combined.NewTimer("addr", nil, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	if timer.Default() != nil {
		t.Error("Timer.Default() must be nil before Subscribe is called")
	}
}
