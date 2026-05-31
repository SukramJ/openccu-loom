// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// helper: returns a Registry pre-populated with the generated profiles.
func testDefaultRegistry() *Registry {
	r := NewRegistry()
	RegisterGeneratedProfiles(r)
	return r
}

// containsParam reports whether params contains p.
func containsParam(params []hmenum.Parameter, p hmenum.Parameter) bool {
	for _, v := range params {
		if v == p {
			return true
		}
	}
	return false
}

// TestRegistryRequiredParametersIncludesDefaults verifies that DefaultDataPoints
// parameters are present in RequiredParameters.
func TestRegistryRequiredParametersIncludesDefaults(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	for _, params := range DefaultDataPoints {
		for _, p := range params {
			if !containsParam(got, p) {
				t.Errorf("RequiredParameters() missing DefaultDataPoints param %q", p)
			}
		}
	}
}

// TestRegistryRequiredParametersIncludesProfileFields verifies that
// SET_POINT_TEMPERATURE (from IPThermostat profile) is in the list.
func TestRegistryRequiredParametersIncludesProfileFields(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	if !containsParam(got, hmenum.ParameterSetPointTemperature) {
		t.Errorf("RequiredParameters() missing ParameterSetPointTemperature")
	}
}

// TestRegistryRequiredParametersIncludesAdditional verifies that parameters
// from AdditionalDataPoints entries in ProfileConfigs are included.
// IPGarage has AdditionalDataPoints with STATE.
func TestRegistryRequiredParametersIncludesAdditional(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	// IPGarage AdditionalDataPoints has ParameterState.
	if !containsParam(got, hmenum.ParameterState) {
		t.Errorf("RequiredParameters() missing ParameterState from AdditionalDataPoints")
	}
}

// TestRegistryRequiredParametersIncludesExtended verifies that parameters
// from Extended configs (FixedChannelFields, AdditionalDataPoints) are included.
// hbw-lc-rgbww-in6-dr has Extended with PRESS_LONG / PRESS_SHORT.
func TestRegistryRequiredParametersIncludesExtended(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	if !containsParam(got, hmenum.ParameterPressLong) {
		t.Errorf("RequiredParameters() missing ParameterPressLong from Extended.AdditionalDataPoints")
	}
	if !containsParam(got, hmenum.ParameterPressShort) {
		t.Errorf("RequiredParameters() missing ParameterPressShort from Extended.AdditionalDataPoints")
	}
}

// TestRegistryRequiredParametersDeduplicate verifies that parameters appearing
// in multiple sources appear only once in the output.
func TestRegistryRequiredParametersDeduplicate(t *testing.T) {
	t.Parallel()

	// ACTUAL_TEMPERATURE appears in both DefaultDataPoints (channel 0) and in
	// IPThermostat's Fields. It must appear exactly once.
	r := testDefaultRegistry()
	got := r.RequiredParameters()

	count := 0
	for _, p := range got {
		if p == hmenum.ParameterActualTemperature {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ParameterActualTemperature appears %d times, want exactly 1", count)
	}
}

// TestRegistryRequiredParametersStableOrder verifies the output is sorted.
func TestRegistryRequiredParametersStableOrder(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got1 := r.RequiredParameters()
	got2 := r.RequiredParameters()

	if len(got1) != len(got2) {
		t.Fatalf("two calls returned different lengths: %d vs %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Errorf("output not stable at index %d: %q vs %q", i, got1[i], got2[i])
		}
	}
	// Also verify the slice is sorted.
	if !sort.SliceIsSorted(got1, func(i, j int) bool { return got1[i] < got1[j] }) {
		t.Error("RequiredParameters() output is not sorted")
	}
}

// TestProfileRequiredParametersForBWTH verifies that the IPThermostat Profile
// returns SET_POINT_TEMPERATURE (and a few others) in its RequiredParameters.
func TestProfileRequiredParametersForBWTH(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	// Find any profile named IPThermostat.
	var found bool
	var target Profile
	r.mu.RLock()
	for _, p := range r.items {
		if p.Name == hmenum.DeviceProfile("IPThermostat") {
			target = p
			found = true
			break
		}
	}
	r.mu.RUnlock()
	if !found {
		t.Skip("IPThermostat profile not registered")
	}

	got := target.RequiredParameters()
	for _, want := range []hmenum.Parameter{
		hmenum.ParameterSetPointTemperature,
		hmenum.ParameterControlMode,
		hmenum.ParameterBoostMode,
	} {
		if !containsParam(got, want) {
			t.Errorf("IPThermostat.RequiredParameters() missing %q", want)
		}
	}
}

// TestProfileRequiredParametersForButtonLock verifies that the IPButtonLock
// Profile returns GLOBAL_BUTTON_LOCK in its RequiredParameters.
func TestProfileRequiredParametersForButtonLock(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	var found bool
	var target Profile
	r.mu.RLock()
	for _, p := range r.items {
		if p.Name == hmenum.DeviceProfile("IPButtonLock") {
			target = p
			found = true
			break
		}
	}
	r.mu.RUnlock()
	if !found {
		t.Skip("IPButtonLock profile not registered")
	}

	got := target.RequiredParameters()
	if !containsParam(got, hmenum.ParameterGlobalButtonLock) {
		t.Errorf("IPButtonLock.RequiredParameters() missing ParameterGlobalButtonLock")
	}
}

// TestProfileRequiredParametersEmptyForUnregistered verifies that a Profile
// with no Config and no Extended returns an empty (non-nil) slice.
func TestProfileRequiredParametersEmptyForUnregistered(t *testing.T) {
	t.Parallel()

	bare := Profile{
		Name:       hmenum.DeviceProfile("Bare"),
		DeviceType: "SOME-DEVICE",
		Config:     nil,
		Extended:   nil,
	}
	got := bare.RequiredParameters()
	if got == nil {
		t.Error("Profile.RequiredParameters() returned nil, want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 params, got %d: %v", len(got), got)
	}
}

// TestRegistryRequiredParametersEmptyRegistry verifies that an empty
// registry combined with DefaultDataPoints still returns the defaults.
func TestRegistryRequiredParametersEmptyRegistry(t *testing.T) {
	t.Parallel()

	r := NewRegistry() // no profiles registered
	got := r.RequiredParameters()

	// Should still include DefaultDataPoints entries.
	for _, params := range DefaultDataPoints {
		for _, p := range params {
			if !containsParam(got, p) {
				t.Errorf("empty registry: RequiredParameters() missing DefaultDataPoints param %q", p)
			}
		}
	}
}

// TestRegistryRequiredParametersIncludesWaterFlow verifies that
// WATER_FLOW from IPIrrigationValve's AdditionalDataPoints is present.
func TestRegistryRequiredParametersIncludesWaterFlow(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	if !containsParam(got, hmenum.ParameterWaterFlow) {
		t.Error("RequiredParameters() missing ParameterWaterFlow from IPIrrigationValve.AdditionalDataPoints")
	}
}

// TestRegistryRequiredParametersIncludesChannelFields verifies that
// parameters from ChannelFields sub-maps are included.
// IPThermostat has ChannelFields with CONCENTRATION on channel 0.
func TestRegistryRequiredParametersIncludesChannelFields(t *testing.T) {
	t.Parallel()

	r := testDefaultRegistry()
	got := r.RequiredParameters()

	if !containsParam(got, hmenum.ParameterConcentration) {
		t.Error("RequiredParameters() missing ParameterConcentration from IPThermostat.ChannelFields")
	}
}
