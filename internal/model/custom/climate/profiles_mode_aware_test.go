// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestClimateProfilesIsModeAware pins
// `profiles` semantics (climate.py:530-535 / :776-781): week-program
// slots are included only when the thermostat is in AUTO mode.
// Drives `Climate.DiscoveryTriggers()` listing CONTROL_MODE /
// HEATING_COOLING so the bridge re-renders + republishes the
// discovery whenever the mode flips. Cf.
func TestClimateProfilesIsModeAware(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		SupportsProfile: true,
		SupportsBoost:   true,
		SupportsAuto:    true,
	})

	// HEAT mode: no week-program slots.
	r.climate.OnMode(ModeHeat)
	heat := r.climate.Profiles()
	for _, p := range heat {
		if p == ProfileWeekProgram1 || p == ProfileWeekProgram2 || p == ProfileWeekProgram3 ||
			p == ProfileWeekProgram4 || p == ProfileWeekProgram5 || p == ProfileWeekProgram6 {
			t.Errorf("HEAT mode must NOT include %q (week-program slots are AUTO-gated); got %v", p, heat)
		}
	}

	// AUTO mode: all six week-program slots present (KindIP).
	r.climate.OnMode(ModeAuto)
	auto := r.climate.Profiles()
	saw := map[Profile]bool{}
	for _, p := range auto {
		saw[p] = true
	}
	for _, expect := range []Profile{ProfileWeekProgram1, ProfileWeekProgram6} {
		if !saw[expect] {
			t.Errorf("AUTO mode must include %q; got %v", expect, auto)
		}
	}

	// Discovery-time bootstrap: a fresh thermostat with SupportsAuto but no
	// observed mode is treated as AUTO so the discovery payload includes
	// week_programs.
	r2 := newRig(t, "y", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
		SupportsProfile: true,
		SupportsBoost:   true,
		SupportsAuto:    true,
	})
	bootstrap := r2.climate.Profiles()
	bootSaw := map[Profile]bool{}
	for _, p := range bootstrap {
		bootSaw[p] = true
	}
	if !bootSaw[ProfileWeekProgram1] {
		t.Errorf("Bootstrap (SupportsAuto, no observed mode) must include week-programs; got %v", bootstrap)
	}
}
