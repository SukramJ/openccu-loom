// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRequiredParameterOverridesIgnore verifies that a parameter present in
// IGNORED_PARAMETERS (INHIBIT) is NOT ignored when it is in the required-
// parameter whitelist.
func TestRequiredParameterOverridesIgnore(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	// Precondition: INHIBIT is normally ignored.
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Fatal("precondition failed: INHIBIT should be ignored before whitelist is set")
	}

	// Set whitelist containing INHIBIT.
	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit})

	// Now INHIBIT must NOT be ignored.
	if d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Error("required parameter INHIBIT must not be ignored after SetRequiredParameters")
	}
}

// TestNonRequiredParameterStillIgnored verifies that standard ignore behaviour
// is preserved for parameters not in the whitelist.
func TestNonRequiredParameterStillIgnored(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	// Whitelist only contains LEVEL, not INHIBIT.
	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterLevel})

	// INHIBIT is in IGNORED_PARAMETERS and not in the whitelist → still ignored.
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Error("INHIBIT not in whitelist must still be ignored")
	}
}

// TestRequiredParameterWildcardStillRespected verifies that a wildcard-ignored
// parameter (PARTY_MODE_SUBMIT ends with _SUBMIT → matched by end-pattern) is
// NOT ignored when in the required whitelist, and IS ignored when not in it.
func TestRequiredParameterWildcardStillRespected(t *testing.T) {
	t.Parallel()

	const p = hmenum.ParameterPartyModeSubmit // ends with "_SUBMIT"

	d := NewParameterDecider(nil)

	// Without whitelist: wildcard match → ignored.
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Fatal("precondition: PARTY_MODE_SUBMIT should be wildcard-ignored without whitelist")
	}

	// With whitelist containing the parameter: must NOT be ignored.
	d.SetRequiredParameters([]hmenum.Parameter{p})
	if d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Error("wildcard-ignored parameter in whitelist must not be ignored")
	}

	// Remove from whitelist: ignored again.
	d.SetRequiredParameters(nil)
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Error("after removing whitelist parameter should be ignored again")
	}
}

// TestEmptyRequiredFallsBackToDefaults verifies that setting an empty / nil
// whitelist restores default ignore behaviour.
func TestEmptyRequiredFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	// Set non-empty whitelist first.
	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit})
	if d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Fatal("INHIBIT with whitelist must not be ignored")
	}

	// Clear the whitelist with nil.
	d.SetRequiredParameters(nil)
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Error("INHIBIT after clearing whitelist must be ignored again")
	}

	// Clear with empty slice — same result.
	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit})
	d.SetRequiredParameters([]hmenum.Parameter{})
	if !d.IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Error("INHIBIT after empty-slice whitelist must be ignored again")
	}
}

// TestSetRequiredParametersSafeAfterUse verifies that calling
// SetRequiredParameters concurrently with active IsParameterIgnored calls
// produces no data race (tested with -race flag).
func TestSetRequiredParametersSafeAfterUse(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	const goroutines = 8
	const ops = 200

	var wg sync.WaitGroup

	// Reader goroutines.
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			for j := range ops {
				p := hmenum.Parameter("P" + string(rune('A'+((i+j)%26))))
				_ = d.IsParameterIgnored("HmIP-STH", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, p)
			}
		}()
	}

	// Writer goroutine: alternately set and clear the whitelist.
	wg.Go(func() {
		for j := range goroutines * ops / 4 {
			if j%2 == 0 {
				d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit, hmenum.ParameterLevel})
			} else {
				d.SetRequiredParameters(nil)
			}
		}
	})

	wg.Wait()
}

// TestRegistrySetRequiredParametersForwardsToDecider verifies that
// Registry.SetRequiredParameters is wired through to the decider.
func TestRegistrySetRequiredParametersForwardsToDecider(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	// Precondition: INHIBIT is normally ignored via the registry.
	if !reg.Parameter().IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Fatal("precondition: INHIBIT should be ignored before whitelist is set on registry")
	}

	reg.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit})

	if reg.Parameter().IsParameterIgnored("any-model", "CH", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterInhibit) {
		t.Error("INHIBIT must not be ignored after Registry.SetRequiredParameters")
	}
}

// TestRequiredParameterDoesNotAffectMaster verifies that the VALUES whitelist
// has no effect on the MASTER paramset — MASTER uses its own channel-gating.
// GLOBAL_BUTTON_LOCK is whitelisted for MASTER on channel 0, so with no device
// rule it should NOT be ignored. We verify that adding it to required-params
// makes no difference for MASTER decisions.
func TestRequiredParameterDoesNotAffectMaster(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)

	// For MASTER paramset the required-parameter whitelist should have no effect;
	// the MASTER path uses relevantMasterParamsetsByChannel / ByDevice exclusively.
	// GLOBAL_BUTTON_LOCK is in relevantMasterParamsetsByChannel for channel 0
	// → not ignored for MASTER regardless of whitelist.
	beforeWhitelist := d.IsParameterIgnored("any-model", "CH", 0, hmenum.ParamsetKeyMaster, hmenum.ParameterGlobalButtonLock)

	d.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterGlobalButtonLock})
	afterWhitelist := d.IsParameterIgnored("any-model", "CH", 0, hmenum.ParamsetKeyMaster, hmenum.ParameterGlobalButtonLock)

	if beforeWhitelist != afterWhitelist {
		t.Errorf("required whitelist changed MASTER decision from %v to %v; MASTER must be unaffected",
			beforeWhitelist, afterWhitelist)
	}
}

// TestDeviceIgnoreBeatsRequiredWhitelist verifies that the device-specific
// suppress list (ignoreParametersByDevice) applies even when the parameter is
// on the required whitelist. Mirrors the reference stack, where
// `_required_parameters` only exempts from the static ignore list:
// OPERATING_VOLTAGE is a default DP (required) yet mains-powered models like
// HmIP-BSM must still suppress it — only battery models report a meaningful
// voltage.
func TestDeviceIgnoreBeatsRequiredWhitelist(t *testing.T) {
	t.Parallel()

	d := NewParameterDecider(nil)
	d.SetRequiredParameters([]hmenum.Parameter{
		hmenum.ParameterOperatingVoltage,
		hmenum.ParameterCurrentIllumination,
	})

	// Mains-powered models: device ignore wins over required.
	for _, model := range []string{"HmIP-BSM", "HmIP-BROLL", "HmIP-FSM", "HmIP-BDT"} {
		if !d.IsParameterIgnored(model, "MAINTENANCE", 0, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
			t.Errorf("OPERATING_VOLTAGE on %s must be ignored despite required whitelist", model)
		}
	}
	// Motion sensors: CURRENT_ILLUMINATION suppressed by device ignore.
	for _, model := range []string{"HmIP-SMI", "HmIP-SMO-A", "HmIP-SPI"} {
		if !d.IsParameterIgnored(model, "MOTION_DETECTOR", 1, hmenum.ParamsetKeyValues, hmenum.ParameterCurrentIllumination) {
			t.Errorf("CURRENT_ILLUMINATION on %s must be ignored despite required whitelist", model)
		}
	}

	// Battery models keep OPERATING_VOLTAGE (not on the device ignore list).
	if d.IsParameterIgnored("HmIP-SWDO", "MAINTENANCE", 0, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE on HmIP-SWDO (battery) must not be ignored")
	}

	// Built-in un-ignore wins over the device ignore list: HmIP-PCBS-BAT
	// matches the HmIP-PCBS ignore prefix but unIgnoreParametersByDevice
	// re-promotes OPERATING_VOLTAGE/LOW_BAT for it.
	if d.IsParameterIgnored("HmIP-PCBS-BAT", "MAINTENANCE", 0, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE on HmIP-PCBS-BAT must survive via built-in un-ignore")
	}
}
