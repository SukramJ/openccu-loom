// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

// master_gating_test.go covers the three F11 P1-10 follow-up items:
//
// P1-10.1 — relevantMasterParamsetsByChannel wired into MASTER gating
// P1-10.2 — empty Channels set treated as "any channel" wildcard
// P1-10.3 — prefix-match for ignoreParametersByDevice / unIgnoreParametersByDevice
//
// Each section provides ≥ 3 tests: positive, negative, and edge-case.

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// =============================================================================
// P1-10.1 — MASTER channel-whitelist gating
// =============================================================================

// TestMasterGatingWhitelistedParamNotIgnored verifies that a climate parameter
// whitelisted for HmIP-STH channel 1 is NOT ignored in the MASTER paramset.
func TestMasterGatingWhitelistedParamNotIgnored(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HmIP-STH ch1 climateMasterParameters includes TemperatureOffset.
	if d.IsParameterIgnored("HmIP-STH", "TRANSCEIVER", 1, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureOffset) {
		t.Error("TEMPERATURE_OFFSET must NOT be ignored for HmIP-STH ch=1 in MASTER (whitelisted)")
	}
}

// TestMasterGatingNonWhitelistedParamIsIgnored verifies that a parameter NOT in
// the device's MASTER whitelist IS ignored.
func TestMasterGatingNonWhitelistedParamIsIgnored(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HmIP-STH ch1 climateMasterParameters does NOT include ParameterLevel.
	if !d.IsParameterIgnored("HmIP-STH", "TRANSCEIVER", 1, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("LEVEL must be ignored for HmIP-STH ch=1 in MASTER (not whitelisted)")
	}
}

// TestMasterGatingWrongChannelIsIgnored verifies that a whitelisted parameter on
// a non-whitelisted channel is ignored (HmIP-STH only has ch=1).
func TestMasterGatingWrongChannelIsIgnored(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HmIP-STH has Channels:{1}; ch=2 is not in the set.
	if !d.IsParameterIgnored("HmIP-STH", "TRANSCEIVER", 2, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureOffset) {
		t.Error("TEMPERATURE_OFFSET on ch=2 must be ignored for HmIP-STH (channel not in whitelist)")
	}
}

// TestMasterGatingUnknownModelIsIgnored verifies that a model NOT listed in
// relevantMasterParamsetsByDevice gets the default-skip for MASTER paramset.
// Without this rule HmIP-STE2-PCB / HmIP-SFD would surface ~25 configuration
// entities (ARR_TIMEOUT, CYCLIC_INFO_MSG, COND_TX_*, …) the HA-native
// integration deliberately hides.
func TestMasterGatingUnknownModelIsIgnoredByDefault(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// "HmIP-Unknown-XYZ" is not in relevantMasterParamsetsByDevice and
	// LEVEL is not in any channel-level whitelist either.
	if !d.IsParameterIgnored("HmIP-Unknown-XYZ", "X", 1, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("unknown model MUST be ignored for MASTER by default (default-skip semantics)")
	}
}

// TestMasterGatingChannelByChannel verifies the channel-level whitelist
// (relevantMasterParamsetsByChannel) for ch=0 with GLOBAL_BUTTON_LOCK.
// Channel 0 is in relevantMasterParamsetsByChannel; the parameter should not
// be ignored even for an otherwise unknown model.
func TestMasterGatingChannelByChannelWhitelist(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// relevantMasterParamsetsByChannel[0] contains GlobalButtonLock.
	// For a model not in relevantMasterParamsetsByDevice, the channel-level
	// check should pass first.
	if d.IsParameterIgnored("HmIP-Unknown-XYZ", "X", 0, hmenum.ParamsetKeyMaster, hmenum.ParameterGlobalButtonLock) {
		t.Error("GLOBAL_BUTTON_LOCK on ch=0 must NOT be ignored (in relevantMasterParamsetsByChannel)")
	}
}

// TestMasterGatingUnIgnoreEntryCanReenableMasterParam verifies that a custom
// UnIgnoreEntry re-enables a MASTER parameter that would otherwise be gated out.
func TestMasterGatingUnIgnoreEntryCanReenableMasterParam(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HmIP-STH ch=2 — parameter not in whitelist → normally ignored.
	if !d.IsParameterIgnored("HmIP-STH", "TRANSCEIVER", 2, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Fatal("precondition: LEVEL must be ignored for HmIP-STH ch=2 before un-ignore")
	}
	d.LoadUnIgnore([]UnIgnoreEntry{
		{Parameter: hmenum.ParameterLevel, Model: "HmIP-STH", ParamsetKey: hmenum.ParamsetKeyMaster},
	})
	if d.IsParameterIgnored("HmIP-STH", "TRANSCEIVER", 2, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("UnIgnoreEntry must re-enable LEVEL for HmIP-STH ch=2 in MASTER")
	}
}

// =============================================================================
// P1-10.2 — Empty Channels = wildcard (frozenset({None}) from Python)
// =============================================================================

// TestEmptyChannelsIsAnyChannelWildcard verifies that a model with an empty
// Channels set (e.g. HM-CC-RT-DN) treats all channel numbers as valid.
// Positive: a whitelisted climate parameter on any channel must NOT be ignored.
func TestEmptyChannelsIsAnyChannelWildcardPositive(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HM-CC-RT-DN has Channels:{} (empty = wildcard) and climateMasterParameters.
	for _, ch := range []int{0, 1, 2, 5, 10} {
		ch := ch
		t.Run("ch"+string(rune('0'+ch)), func(t *testing.T) { //nolint:gosec // G115: ch is a small channel number in test list; '0'+ch stays within ASCII digit range
			t.Parallel()
			if d.IsParameterIgnored("HM-CC-RT-DN", "X", ch, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureOffset) {
				t.Errorf("TEMPERATURE_OFFSET must NOT be ignored for HM-CC-RT-DN ch=%d (wildcard channels)", ch)
			}
		})
	}
}

// TestEmptyChannelsWildcardNegativeNonWhitelistedParam verifies that even with
// wildcard channels, a parameter NOT in the Parameters set is still ignored.
func TestEmptyChannelsWildcardNegativeNonWhitelistedParam(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HM-CC-RT-DN has climateMasterParameters; ParameterLevel is not in that set.
	if !d.IsParameterIgnored("HM-CC-RT-DN", "X", 3, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("LEVEL must be ignored for HM-CC-RT-DN even with wildcard channels (not in Parameters)")
	}
}

// TestEmptyChannelsWildcardEdgeChannelMinus1 verifies that the wildcard also
// applies when channelNo is the unknown sentinel (-1).
func TestEmptyChannelsWildcardEdgeChannelMinus1(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// channelNoUnknown (-1) should also match the wildcard (empty Channels).
	if d.IsParameterIgnored("HM-CC-RT-DN", "X", channelNoUnknown, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureMinimum) {
		t.Error("TEMPERATURE_MINIMUM must NOT be ignored for HM-CC-RT-DN ch=-1 (wildcard channels, unknown channel)")
	}
}

// TestEmptyChannelsAllThreeWildcardModels verifies the wildcard behaviour for
// all three Python-origin models that have frozenset({None}) channels.
func TestEmptyChannelsAllThreeWildcardModels(t *testing.T) {
	t.Parallel()
	wildcardModels := []string{"HM-CC-RT-DN", "HM-CC-VG-1", "HM-TC-IT-WM-W-EU"}
	d := NewParameterDecider(nil)
	for _, model := range wildcardModels {
		model := model
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			// Temperature offset is in climateMasterParameters — must be allowed on any ch.
			for _, ch := range []int{0, 1, 4} {
				ch := ch
				if d.IsParameterIgnored(model, "X", ch, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureOffset) {
					t.Errorf("%s ch=%d: TEMPERATURE_OFFSET must NOT be ignored (empty-channels wildcard)", model, ch)
				}
			}
		})
	}
}

// =============================================================================
// P1-10.3 — Prefix match for ignoreParametersByDevice / unIgnoreParametersByDevice
// =============================================================================

// TestIgnoreParametersByDevicePrefixMatch verifies that a model name that is a
// strict suffix extension of a listed entry is still suppressed (right-wildcard).
// Example: "HmIP-PS" is listed for OPERATING_VOLTAGE; "HmIP-PS-2" should also
// be suppressed.
func TestIgnoreParametersByDevicePrefixMatchPositive(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// "HmIP-PS" is in ignoreParametersByDevice["OPERATING_VOLTAGE"].
	// "HmIP-PS-2" starts with "HmIP-PS" → must also be suppressed.
	if !d.IsParameterIgnored("HmIP-PS-2", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE must be ignored for HmIP-PS-2 (prefix match against HmIP-PS)")
	}
}

// TestIgnoreParametersByDevicePrefixMatchNegative verifies that a model whose
// name does NOT match any entry in ignoreParametersByDevice is NOT suppressed.
func TestIgnoreParametersByDevicePrefixMatchNegative(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// "HmIP-STH" is not in ignoreParametersByDevice["OPERATING_VOLTAGE"] and
	// does not start with any of the listed prefixes (HmIP-PS, HmIP-BDT, …).
	if d.IsParameterIgnored("HmIP-STH", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE must NOT be ignored for HmIP-STH (no match in ignoreParametersByDevice)")
	}
}

// TestIgnoreParametersByDevicePrefixMatchEdgeCaseExact verifies that an exact
// match still works after the prefix-match refactor.
func TestIgnoreParametersByDevicePrefixMatchEdgeCaseExact(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// "HmIP-PS" is the exact key → must still be suppressed.
	if !d.IsParameterIgnored("HmIP-PS", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage) {
		t.Error("OPERATING_VOLTAGE must be ignored for HmIP-PS (exact match)")
	}
}

// TestUnIgnoreParametersByDevicePrefixMatchPositive verifies that a suffix-extended
// model name still matches the un-ignore list (right-wildcard / prefix match).
// WEEK_PROGRAM_POINTER is in ignoredParameters; HM-CC-RT-DN is in
// unIgnoreParametersByDevice for it. A variant "HM-CC-RT-DN-Variant" must also
// be un-ignored via prefix match.
func TestUnIgnoreParametersByDevicePrefixMatchPositive(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// "HM-CC-RT-DN-Variant" starts with "HM-CC-RT-DN" → must be un-ignored.
	if d.IsParameterIgnored("HM-CC-RT-DN-Variant", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterWeekProgramPointer) {
		t.Error("WEEK_PROGRAM_POINTER must NOT be ignored for HM-CC-RT-DN-Variant (prefix un-ignore from HM-CC-RT-DN)")
	}
}

// TestUnIgnoreParametersByDevicePrefixMatchNegative verifies that a model with
// no matching un-ignore entry does NOT inherit the un-ignore override.
// WEEK_PROGRAM_POINTER is in ignoredParameters; HM-CC-RT-DN un-ignores it but
// HmIP-STH has no such override — it must remain ignored.
func TestUnIgnoreParametersByDevicePrefixMatchNegative(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HmIP-STH does not start with "HM-CC-RT-DN" or any other un-ignore model
	// for WEEK_PROGRAM_POINTER.
	if !d.IsParameterIgnored("HmIP-STH", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterWeekProgramPointer) {
		t.Error("WEEK_PROGRAM_POINTER must be ignored for HmIP-STH (no un-ignore entry for this model)")
	}
}

// TestUnIgnoreParametersByDevicePrefixMatchEdgeCaseExact verifies exact match
// still works after the prefix-match refactor.
func TestUnIgnoreParametersByDevicePrefixMatchEdgeCaseExact(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// HM-CC-RT-DN (exact key) must still un-ignore WEEK_PROGRAM_POINTER.
	if d.IsParameterIgnored("HM-CC-RT-DN", "X", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterWeekProgramPointer) {
		t.Error("WEEK_PROGRAM_POINTER must NOT be ignored for HM-CC-RT-DN (exact un-ignore entry)")
	}
}

// =============================================================================
// Integration: IsAllowedForChannel (Registry)
// =============================================================================

// TestRegistryIsAllowedForChannelMasterWhitelist exercises the new
// IsAllowedForChannel method end-to-end for MASTER paramset gating.
func TestRegistryIsAllowedForChannelMasterWhitelist(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	cases := []struct {
		model   string
		ch      int
		param   hmenum.Parameter
		allowed bool
		desc    string
	}{
		// Whitelisted device + channel + param → allowed.
		{"HmIP-eTRV", 1, hmenum.ParameterTemperatureOffset, true, "HmIP-eTRV ch=1 TEMPERATURE_OFFSET whitelisted"},
		{"HmIP-eTRV", 1, hmenum.ParameterWeekProgramPointer, true, "HmIP-eTRV ch=1 WEEK_PROGRAM_POINTER whitelisted"},
		// Whitelisted device, wrong channel → ignored.
		{"HmIP-eTRV", 2, hmenum.ParameterTemperatureOffset, false, "HmIP-eTRV ch=2 not in Channels"},
		// Whitelisted device, right channel, non-whitelisted param → ignored.
		{"HmIP-eTRV", 1, hmenum.ParameterLevel, false, "HmIP-eTRV ch=1 LEVEL not in climateMasterParameters"},
		// Empty-channels wildcard model → always allowed on any channel.
		{"HM-CC-VG-1", 0, hmenum.ParameterTemperatureOffset, true, "HM-CC-VG-1 ch=0 wildcard"},
		{"HM-CC-VG-1", 7, hmenum.ParameterTemperatureOffset, true, "HM-CC-VG-1 ch=7 wildcard"},
		// Unknown model → default-skip for MASTER (mirrors
		// `should_skip_parameter` rule) → not allowed unless whitelisted.
		{"HmIP-Unknown", 0, hmenum.ParameterLevel, false, "unknown model ch=0 default-skip MASTER"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			got := reg.IsAllowedForChannel(tc.model, "X", tc.ch, hmenum.ParamsetKeyMaster, tc.param)
			if got != tc.allowed {
				t.Errorf("IsAllowedForChannel(%q, ch=%d, MASTER, %q) = %v, want %v",
					tc.model, tc.ch, tc.param, got, tc.allowed)
			}
		})
	}
}

// TestMasterGatingConcurrentSafe verifies that checkMasterParameterIgnored and
// the decider are safe for concurrent use under -race.
func TestMasterGatingConcurrentSafe(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	var wg sync.WaitGroup
	const goroutines = 40
	wg.Add(goroutines)
	models := []string{"HmIP-STH", "HM-CC-RT-DN", "HmIP-Unknown", "HmIP-eTRV"}
	channels := []int{0, 1, 2, channelNoUnknown}
	params := []hmenum.Parameter{
		hmenum.ParameterTemperatureOffset,
		hmenum.ParameterLevel,
		hmenum.ParameterGlobalButtonLock,
	}
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			_ = d.IsParameterIgnored(
				models[i%len(models)],
				"X",
				channels[i%len(channels)],
				hmenum.ParamsetKeyMaster,
				params[i%len(params)],
			)
		}(i)
	}
	wg.Wait()
}
